package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kailonyang/liexiu/server/internal/auth"
	"github.com/kailonyang/liexiu/server/internal/service/localinstance"
)

const localBootstrapTestSecret = "local-bootstrap-test-secret-0123456789"

func prepareLocalBootstrapHandlerTest(t *testing.T) {
	t.Helper()
	if testPool == nil || testHandler == nil {
		t.Skip("handler database fixture is unavailable")
	}
	var tableReady bool
	if err := testPool.QueryRow(context.Background(), `SELECT to_regclass('local_instance') IS NOT NULL`).Scan(&tableReady); err != nil {
		t.Fatalf("check local_instance schema: %v", err)
	}
	if !tableReady {
		t.Skip("local_instance migration is not applied")
	}
	var bound bool
	if err := testPool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM local_instance WHERE singleton_key = TRUE)`).Scan(&bound); err != nil {
		t.Fatalf("check local instance binding: %v", err)
	}
	if bound {
		t.Skip("local_instance is already bound by another test or environment")
	}

	previousRepository := testHandler.LocalInstance
	previousSecret := testHandler.cfg.OwnerBootstrapSecret
	previousAutoLogin := testHandler.cfg.AutoLogin
	testHandler.LocalInstance = localinstance.NewRepository(testHandler.Queries, testPool)
	testHandler.cfg.OwnerBootstrapSecret = localBootstrapTestSecret
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM local_instance WHERE owner_user_id = $1`, testUserID)
		testHandler.LocalInstance = previousRepository
		testHandler.cfg.OwnerBootstrapSecret = previousSecret
		testHandler.cfg.AutoLogin = previousAutoLogin
	})
}

func TestStartLocalSessionIsDisabledByDefault(t *testing.T) {
	prepareLocalBootstrapHandlerTest(t)
	testHandler.cfg.AutoLogin = false

	req := newRequest(http.MethodPost, "/api/auth/local-session", nil)
	req.RemoteAddr = "127.0.0.1:44001"
	req.Header.Set("Referer", "http://localhost:3000/login")
	response := httptest.NewRecorder()
	testHandler.StartLocalSession(response, req)

	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled personal mode status = %d, want 404: %s", response.Code, response.Body.String())
	}
	assertNoLocalInstanceRow(t)
}

func TestStartLocalSessionRejectsNonLoopback(t *testing.T) {
	prepareLocalBootstrapHandlerTest(t)
	testHandler.cfg.AutoLogin = true

	req := newRequest(http.MethodPost, "/api/auth/local-session", nil)
	req.RemoteAddr = "192.0.2.10:44001"
	req.Header.Set("Referer", "http://localhost:3000/login")
	response := httptest.NewRecorder()
	testHandler.StartLocalSession(response, req)

	if response.Code != http.StatusForbidden {
		t.Fatalf("remote personal mode status = %d, want 403: %s", response.Code, response.Body.String())
	}
	assertNoLocalInstanceRow(t)
}

func TestStartLocalSessionSetsCookiesForBoundOwner(t *testing.T) {
	prepareLocalBootstrapHandlerTest(t)
	testHandler.cfg.AutoLogin = true
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO local_instance (singleton_key, owner_user_id, canonical_workspace_id, bootstrap_version)
		VALUES (TRUE, $1, $2, 1)
	`, testUserID, testWorkspaceID); err != nil {
		t.Fatalf("seed local instance: %v", err)
	}

	req := newRequest(http.MethodPost, "/api/auth/local-session", nil)
	req.RemoteAddr = "[::1]:44001"
	req.Header.Set("Referer", "http://localhost:3000/login")
	response := httptest.NewRecorder()
	testHandler.StartLocalSession(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("personal session status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body LocalSessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode personal session: %v", err)
	}
	if body.User.ID != testUserID || body.Workspace.ID != testWorkspaceID || body.Provisioned {
		t.Fatalf("unexpected personal session: %+v", body)
	}
	if strings.Contains(response.Body.String(), "token") {
		t.Fatalf("personal session response must not expose the JWT: %s", response.Body.String())
	}

	var authCookie, csrfCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		switch cookie.Name {
		case auth.AuthCookieName:
			authCookie = cookie
		case auth.CSRFCookieName:
			csrfCookie = cookie
		}
	}
	if authCookie == nil || !authCookie.HttpOnly || csrfCookie == nil || csrfCookie.HttpOnly {
		t.Fatalf("personal session cookies are incomplete: %v", response.Result().Cookies())
	}
}

func TestIsLoopbackRemote(t *testing.T) {
	for _, test := range []struct {
		addr string
		want bool
	}{
		{addr: "127.0.0.1:8080", want: true},
		{addr: "[::1]:8080", want: true},
		{addr: "127.0.0.1", want: true},
		{addr: "192.0.2.1:8080", want: false},
		{addr: "localhost:8080", want: false},
		{addr: "", want: false},
	} {
		if got := isLoopbackRemote(test.addr); got != test.want {
			t.Errorf("isLoopbackRemote(%q) = %v, want %v", test.addr, got, test.want)
		}
	}
}

func TestIsLocalBrowserRequestRejectsLANOriginBehindLoopbackProxy(t *testing.T) {
	for _, test := range []struct {
		name    string
		remote  string
		referer string
		want    bool
	}{
		{name: "localhost through local proxy", remote: "127.0.0.1:8080", referer: "http://localhost:3000/login", want: true},
		{name: "loopback IP through local proxy", remote: "[::1]:8080", referer: "http://127.0.0.1:3000/", want: true},
		{name: "LAN browser behind local proxy", remote: "127.0.0.1:8080", referer: "http://10.10.60.205:3000/", want: false},
		{name: "missing browser origin", remote: "127.0.0.1:8080", want: false},
		{name: "remote backend connection", remote: "192.0.2.1:8080", referer: "http://localhost:3000/", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/auth/local-session", nil)
			req.RemoteAddr = test.remote
			if test.referer != "" {
				req.Header.Set("Referer", test.referer)
			}
			if got := isLocalBrowserRequest(req); got != test.want {
				t.Fatalf("isLocalBrowserRequest() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBootstrapLocalOwnerRejectsWeakSecretWithoutWriting(t *testing.T) {
	prepareLocalBootstrapHandlerTest(t)
	testHandler.cfg.OwnerBootstrapSecret = "too-short"

	req := newRequest(http.MethodPost, "/api/bootstrap", LocalBootstrapRequest{Secret: "too-short"})
	response := httptest.NewRecorder()
	testHandler.BootstrapLocalOwner(response, req)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("weak secret status = %d, want %d: %s", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatalf("weak secret unexpectedly set cookies: %v", response.Result().Cookies())
	}
	assertNoLocalInstanceRow(t)
}

func TestBootstrapLocalOwnerRejectsWrongSecretWithoutWriting(t *testing.T) {
	prepareLocalBootstrapHandlerTest(t)

	req := newRequest(http.MethodPost, "/api/bootstrap", LocalBootstrapRequest{Secret: strings.Repeat("x", len(localBootstrapTestSecret))})
	response := httptest.NewRecorder()
	testHandler.BootstrapLocalOwner(response, req)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret status = %d, want %d: %s", response.Code, http.StatusUnauthorized, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatalf("wrong secret unexpectedly set cookies: %v", response.Result().Cookies())
	}
	assertNoLocalInstanceRow(t)
}

func TestGetLocalBootstrapStatusDoesNotLeakIdentityOrSecret(t *testing.T) {
	prepareLocalBootstrapHandlerTest(t)

	req := newRequest(http.MethodGet, "/api/bootstrap/status", nil)
	response := httptest.NewRecorder()
	testHandler.GetLocalBootstrapStatus(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body LocalBootstrapStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if !body.Enabled || body.Initialized {
		t.Fatalf("unexpected status response: %+v", body)
	}
	for _, forbidden := range []string{localBootstrapTestSecret, handlerTestEmail, testUserID, testWorkspaceID} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("status response leaked %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestBootstrapLocalOwnerSetsAuthAndCSRFCookies(t *testing.T) {
	prepareLocalBootstrapHandlerTest(t)

	req := newRequest(http.MethodPost, "/api/bootstrap", LocalBootstrapRequest{
		Secret:        localBootstrapTestSecret,
		OwnerName:     handlerTestName,
		OwnerEmail:    handlerTestEmail,
		WorkspaceID:   testWorkspaceID,
		WorkspaceName: "ignored for existing workspace",
		WorkspaceSlug: "ignored-for-existing",
	})
	response := httptest.NewRecorder()
	testHandler.BootstrapLocalOwner(response, req)

	if response.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var body LocalBootstrapResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if body.Token == "" || body.User.ID != testUserID || body.Workspace.ID != testWorkspaceID || body.Provisioned {
		t.Fatalf("unexpected bootstrap response: %+v", body)
	}

	cookies := response.Result().Cookies()
	var authCookie, csrfCookie *http.Cookie
	for _, cookie := range cookies {
		switch cookie.Name {
		case auth.AuthCookieName:
			authCookie = cookie
		case auth.CSRFCookieName:
			csrfCookie = cookie
		}
	}
	if authCookie == nil || !authCookie.HttpOnly || authCookie.Value == "" {
		t.Fatalf("missing HttpOnly auth cookie: %v", cookies)
	}
	if csrfCookie == nil || csrfCookie.HttpOnly || csrfCookie.Value == "" {
		t.Fatalf("missing readable CSRF cookie: %v", cookies)
	}
}

func TestGetCanonicalWorkspaceAllowsOnlyBoundOwner(t *testing.T) {
	prepareLocalBootstrapHandlerTest(t)

	bootstrap := newRequest(http.MethodPost, "/api/bootstrap", LocalBootstrapRequest{
		Secret:        localBootstrapTestSecret,
		OwnerEmail:    handlerTestEmail,
		WorkspaceID:   testWorkspaceID,
		OwnerName:     handlerTestName,
		WorkspaceName: "ignored",
		WorkspaceSlug: "ignored",
	})
	bootstrapResponse := httptest.NewRecorder()
	testHandler.BootstrapLocalOwner(bootstrapResponse, bootstrap)
	if bootstrapResponse.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d: %s", bootstrapResponse.Code, bootstrapResponse.Body.String())
	}

	ownerRequest := newRequest(http.MethodGet, "/api/workspaces/canonical", nil)
	ownerResponse := httptest.NewRecorder()
	testHandler.GetCanonicalWorkspace(ownerResponse, ownerRequest)
	if ownerResponse.Code != http.StatusOK {
		t.Fatalf("canonical owner status = %d: %s", ownerResponse.Code, ownerResponse.Body.String())
	}
	var workspace WorkspaceResponse
	if err := json.Unmarshal(ownerResponse.Body.Bytes(), &workspace); err != nil {
		t.Fatal(err)
	}
	if workspace.ID != testWorkspaceID {
		t.Fatalf("canonical workspace id = %q, want %q", workspace.ID, testWorkspaceID)
	}

	nonOwnerRequest := newRequest(http.MethodGet, "/api/workspaces/canonical", nil)
	nonOwnerRequest.Header.Set("X-User-ID", "00000000-0000-0000-0000-000000000001")
	nonOwnerResponse := httptest.NewRecorder()
	testHandler.GetCanonicalWorkspace(nonOwnerResponse, nonOwnerRequest)
	if nonOwnerResponse.Code != http.StatusNotFound {
		t.Fatalf("canonical non-owner status = %d, want 404: %s", nonOwnerResponse.Code, nonOwnerResponse.Body.String())
	}
}

func TestBootstrapLocalOwnerMapsInvalidSelectionToConflict(t *testing.T) {
	prepareLocalBootstrapHandlerTest(t)

	req := newRequest(http.MethodPost, "/api/bootstrap", LocalBootstrapRequest{
		Secret:      localBootstrapTestSecret,
		OwnerEmail:  "not-an-owner@liexiu.test",
		WorkspaceID: testWorkspaceID,
	})
	response := httptest.NewRecorder()
	testHandler.BootstrapLocalOwner(response, req)

	if response.Code != http.StatusConflict {
		t.Fatalf("invalid selection status = %d, want %d: %s", response.Code, http.StatusConflict, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatalf("invalid selection unexpectedly set cookies: %v", response.Result().Cookies())
	}
	assertNoLocalInstanceRow(t)
}

func assertNoLocalInstanceRow(t *testing.T) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM local_instance WHERE singleton_key = TRUE`).Scan(&count); err != nil {
		t.Fatalf("count local_instance rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("unexpected local_instance rows: %d", count)
	}
}
