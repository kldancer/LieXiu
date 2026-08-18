package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/kailonyang/liexiu/server/internal/auth"
	"github.com/kailonyang/liexiu/server/internal/service/localinstance"
)

const minOwnerBootstrapSecretBytes = 32
const maxLocalBootstrapBodyBytes = 32 << 10

type LocalBootstrapStatusResponse struct {
	Enabled           bool `json:"enabled"`
	Initialized       bool `json:"initialized"`
	RequiresSelection bool `json:"requires_selection"`
}

type LocalBootstrapRequest struct {
	Secret        string `json:"secret"`
	OwnerName     string `json:"owner_name"`
	OwnerEmail    string `json:"owner_email"`
	WorkspaceName string `json:"workspace_name"`
	WorkspaceSlug string `json:"workspace_slug"`
	WorkspaceID   string `json:"workspace_id"`
}

type LocalBootstrapResponse struct {
	Token       string            `json:"token"`
	User        UserResponse      `json:"user"`
	Workspace   WorkspaceResponse `json:"workspace"`
	Provisioned bool              `json:"provisioned"`
}

type LocalSessionResponse struct {
	User        UserResponse      `json:"user"`
	Workspace   WorkspaceResponse `json:"workspace"`
	Provisioned bool              `json:"provisioned"`
}

var personalBootstrapInput = localinstance.BootstrapInput{
	OwnerName:     "LieXiu Owner",
	OwnerEmail:    "owner@liexiu.local",
	WorkspaceName: "LieXiu",
	WorkspaceSlug: "liexiu",
	IssuePrefix:   "LX",
}

func (h *Handler) GetLocalBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	response := LocalBootstrapStatusResponse{Enabled: h.ownerBootstrapEnabled()}
	if h.LocalInstance == nil {
		writeError(w, http.StatusServiceUnavailable, "local bootstrap is unavailable")
		return
	}
	status, err := h.LocalInstance.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read local bootstrap status")
		return
	}
	response.Initialized = status.Initialized
	response.RequiresSelection = status.RequiresSelection
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) BootstrapLocalOwner(w http.ResponseWriter, r *http.Request) {
	if !h.ownerBootstrapEnabled() || h.LocalInstance == nil {
		writeError(w, http.StatusServiceUnavailable, "local bootstrap is not configured")
		return
	}

	var req LocalBootstrapRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLocalBootstrapBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !secureSecretEqual(req.Secret, h.cfg.OwnerBootstrapSecret) {
		writeError(w, http.StatusUnauthorized, "invalid bootstrap credential")
		return
	}

	input, ok := localBootstrapInput(w, req)
	if !ok {
		return
	}
	result, err := h.LocalInstance.Bootstrap(r.Context(), input)
	if err != nil {
		switch {
		case errors.Is(err, localinstance.ErrInvalidInput):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, localinstance.ErrSelectionRequired),
			errors.Is(err, localinstance.ErrInvalidSelection),
			errors.Is(err, localinstance.ErrIncompleteStore),
			errors.Is(err, localinstance.ErrCorruptBinding):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to bootstrap local owner")
		}
		return
	}

	token, err := h.setLocalSessionCookies(w, result)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, LocalBootstrapResponse{
		Token:       token,
		User:        h.userToResponse(result.Owner),
		Workspace:   h.workspaceToResponse(result.Workspace),
		Provisioned: result.Provisioned,
	})
}

// StartLocalSession removes the login ceremony for a single-user development
// instance without removing authentication. It is deliberately limited to a
// loopback backend connection, a localhost browser origin, and a router
// configuration that is disabled in production. Existing stores are selected
// only when exactly one owner membership exists; ambiguous stores fail closed.
func (h *Handler) StartLocalSession(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.AutoLogin || h.LocalInstance == nil {
		writeError(w, http.StatusNotFound, "personal mode is unavailable")
		return
	}
	if !isLocalBrowserRequest(r) {
		writeError(w, http.StatusForbidden, "personal mode is limited to localhost")
		return
	}

	result, err := h.LocalInstance.BootstrapPersonal(r.Context(), personalBootstrapInput)
	if err != nil {
		switch {
		case errors.Is(err, localinstance.ErrSelectionRequired),
			errors.Is(err, localinstance.ErrInvalidSelection),
			errors.Is(err, localinstance.ErrIncompleteStore),
			errors.Is(err, localinstance.ErrCorruptBinding):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to initialize personal session")
		}
		return
	}
	if _, err := h.setLocalSessionCookies(w, result); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	writeJSON(w, http.StatusOK, LocalSessionResponse{
		User:        h.userToResponse(result.Owner),
		Workspace:   h.workspaceToResponse(result.Workspace),
		Provisioned: result.Provisioned,
	})
}

func (h *Handler) setLocalSessionCookies(w http.ResponseWriter, result localinstance.Result) (string, error) {
	token, err := h.issueJWT(result.Owner)
	if err != nil {
		return "", err
	}
	if err := auth.SetAuthCookies(w, token); err != nil {
		return "", err
	}
	if h.CFSigner != nil {
		for _, cookie := range h.CFSigner.SignedCookies(time.Now().Add(auth.AuthTokenTTL())) {
			http.SetCookie(w, cookie)
		}
	}
	return token, nil
}

func isLoopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.Trim(strings.TrimSpace(remoteAddr), "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalBrowserRequest(r *http.Request) bool {
	if r == nil || !isLoopbackRemote(r.RemoteAddr) {
		return false
	}
	referer, err := url.Parse(strings.TrimSpace(r.Referer()))
	if err != nil || referer.Hostname() == "" {
		return false
	}
	host := strings.Trim(strings.TrimSpace(referer.Hostname()), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// GetCanonicalWorkspace resolves the singleton workspace for the authenticated
// local owner. It never accepts a workspace selector and deliberately returns
// 404 for non-owners so the binding cannot be used as an identity oracle.
func (h *Handler) GetCanonicalWorkspace(w http.ResponseWriter, r *http.Request) {
	if h.LocalInstance == nil {
		writeError(w, http.StatusServiceUnavailable, "canonical workspace is unavailable")
		return
	}
	result, err := h.LocalInstance.Current(r.Context())
	if err != nil {
		switch {
		case errors.Is(err, localinstance.ErrNotInitialized):
			writeError(w, http.StatusNotFound, "canonical workspace not found")
		case errors.Is(err, localinstance.ErrCorruptBinding):
			writeError(w, http.StatusConflict, "canonical workspace binding is invalid")
		default:
			writeError(w, http.StatusInternalServerError, "failed to read canonical workspace")
		}
		return
	}
	if requestUserID(r) != uuidToString(result.Owner.ID) {
		writeError(w, http.StatusNotFound, "canonical workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, h.workspaceToResponse(result.Workspace))
}

func (h *Handler) ownerBootstrapEnabled() bool {
	return len(strings.TrimSpace(h.cfg.OwnerBootstrapSecret)) >= minOwnerBootstrapSecretBytes
}

func secureSecretEqual(got, want string) bool {
	gotHash := sha256.Sum256([]byte(got))
	wantHash := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotHash[:], wantHash[:]) == 1
}

func localBootstrapInput(w http.ResponseWriter, req LocalBootstrapRequest) (localinstance.BootstrapInput, bool) {
	ownerName := strings.TrimSpace(req.OwnerName)
	ownerEmail := strings.ToLower(strings.TrimSpace(req.OwnerEmail))
	workspaceName := strings.TrimSpace(req.WorkspaceName)
	workspaceSlug := strings.ToLower(strings.TrimSpace(req.WorkspaceSlug))
	workspaceID := strings.TrimSpace(req.WorkspaceID)

	if ownerEmail != "" {
		parsed, err := mail.ParseAddress(ownerEmail)
		if err != nil || !strings.EqualFold(parsed.Address, ownerEmail) {
			writeError(w, http.StatusBadRequest, "owner email is invalid")
			return localinstance.BootstrapInput{}, false
		}
	}
	if workspaceSlug != "" {
		if !workspaceSlugPattern.MatchString(workspaceSlug) || isReservedSlug(workspaceSlug) {
			writeError(w, http.StatusBadRequest, "workspace slug is invalid")
			return localinstance.BootstrapInput{}, false
		}
	}

	issuePrefix := ""
	if workspaceSlug != "" {
		issuePrefix = defaultIssuePrefixFromSlug(workspaceSlug)
	}
	return localinstance.BootstrapInput{
		OwnerName:     ownerName,
		OwnerEmail:    ownerEmail,
		WorkspaceName: workspaceName,
		WorkspaceSlug: workspaceSlug,
		IssuePrefix:   issuePrefix,
		WorkspaceID:   workspaceID,
	}, true
}
