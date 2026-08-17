package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/kailonyang/liexiu/server/internal/analytics"
	"github.com/kailonyang/liexiu/server/internal/auth"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/realtime"
)

func TestLocalBootstrapIdentityDrivesMissionAndDaemonEntrypoints(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var existingBindings int
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM local_instance`).Scan(&existingBindings); err != nil {
		t.Fatal(err)
	}
	if existingBindings != 0 {
		t.Skip("local_instance is already bound; refusing to replace an existing local owner")
	}

	const bootstrapSecret = "integration-only-bootstrap-secret-32-bytes"
	t.Setenv("LIEXIU_OWNER_BOOTSTRAP_SECRET", bootstrapSecret)

	hub := realtime.NewHub()
	go hub.Run()
	bus := events.New()
	registerListeners(bus, hub)
	server := httptest.NewServer(NewRouter(testPool, hub, bus, analytics.NoopClient{}, nil))
	t.Cleanup(server.Close)

	bootstrapResponse := postJSON(t, server.URL+"/api/bootstrap", map[string]any{
		"secret":       bootstrapSecret,
		"owner_email":  integrationTestEmail,
		"workspace_id": testWorkspaceID,
	}, nil)
	if bootstrapResponse.StatusCode != http.StatusOK {
		defer bootstrapResponse.Body.Close()
		t.Fatalf("bootstrap status=%d, want 200: %s", bootstrapResponse.StatusCode, readResponseBody(bootstrapResponse))
	}
	var bootstrapped struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(bootstrapResponse.Body).Decode(&bootstrapped); err != nil {
		bootstrapResponse.Body.Close()
		t.Fatal(err)
	}
	bootstrapResponse.Body.Close()
	if bootstrapped.Token == "" {
		t.Fatal("bootstrap response did not contain a JWT compatibility token")
	}

	var authCookie, csrfCookie *http.Cookie
	for _, cookie := range bootstrapResponse.Cookies() {
		switch cookie.Name {
		case auth.AuthCookieName:
			authCookie = cookie
		case auth.CSRFCookieName:
			csrfCookie = cookie
		}
	}
	if authCookie == nil || csrfCookie == nil {
		t.Fatalf("bootstrap response missing auth/CSRF cookies: %v", bootstrapResponse.Cookies())
	}

	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `
			DELETE FROM local_instance
			WHERE owner_user_id=$1 AND canonical_workspace_id=$2
		`, testUserID, testWorkspaceID); err != nil {
			t.Errorf("cleanup local instance binding: %v", err)
		}
	})

	missionResponse := postJSON(t, server.URL+"/api/issues/quick-create", map[string]any{
		"command_id": uuid.NewString(),
		"prompt":     "Prove the bootstrapped local owner can create a Mission",
	}, func(request *http.Request) {
		request.AddCookie(authCookie)
		request.AddCookie(csrfCookie)
		request.Header.Set("X-CSRF-Token", csrfCookie.Value)
		request.Header.Set("X-Workspace-ID", testWorkspaceID)
	})
	if missionResponse.StatusCode != http.StatusCreated {
		defer missionResponse.Body.Close()
		t.Fatalf("quick-create with bootstrap cookies status=%d, want 201: %s", missionResponse.StatusCode, readResponseBody(missionResponse))
	}
	var mission struct {
		MissionID string `json:"mission_id"`
	}
	if err := json.NewDecoder(missionResponse.Body).Decode(&mission); err != nil {
		missionResponse.Body.Close()
		t.Fatal(err)
	}
	missionResponse.Body.Close()
	if mission.MissionID == "" {
		t.Fatal("quick-create response did not contain mission_id")
	}
	missionID := missionTestUUID(t, mission.MissionID)
	workspaceID := missionTestUUID(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMissionHTTPFixture(t, missionID, workspaceID) })

	daemonID := "bootstrap-integration-" + uuid.NewString()
	daemonResponse := postJSON(t, server.URL+"/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    daemonID,
		"device_name":  "bootstrap-integration-device",
		"runtimes": []map[string]any{
			{"name": "bootstrap-codex", "type": "codex", "version": "integration", "status": "online"},
		},
	}, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+bootstrapped.Token)
	})
	if daemonResponse.StatusCode != http.StatusOK {
		defer daemonResponse.Body.Close()
		t.Fatalf("daemon register with bootstrap JWT status=%d, want 200: %s", daemonResponse.StatusCode, readResponseBody(daemonResponse))
	}
	var daemon struct {
		Runtimes []struct {
			ID string `json:"id"`
		} `json:"runtimes"`
	}
	if err := json.NewDecoder(daemonResponse.Body).Decode(&daemon); err != nil {
		daemonResponse.Body.Close()
		t.Fatal(err)
	}
	daemonResponse.Body.Close()
	if len(daemon.Runtimes) != 1 || daemon.Runtimes[0].ID == "" {
		t.Fatalf("daemon registration did not return one runtime: %#v", daemon)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE workspace_id=$1 AND daemon_id=$2`, testWorkspaceID, daemonID); err != nil {
			t.Errorf("cleanup bootstrap daemon runtime: %v", err)
		}
	})
}

func postJSON(t *testing.T, url string, body any, configure func(*http.Request)) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if configure != nil {
		configure(request)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readResponseBody(response *http.Response) string {
	var payload any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return fmt.Sprintf("<unreadable response: %v>", err)
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}
