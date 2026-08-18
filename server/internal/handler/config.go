package handler

import (
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/kailonyang/liexiu/server/internal/featureflags"
)

type AppConfig struct {
	CdnDomain string `json:"cdn_domain"`
	// AutoLogin is safe public capability metadata. It never includes an
	// identity or credential and is true only for localhost personal mode.
	AutoLogin bool `json:"auto_login,omitempty"`
	// CdnSigned tells clients that the CDN domain above serves PRIVATE
	// content through time-bounded signed URLs (CloudFront signing is
	// enabled). When true, a raw storage URL on the CDN domain is NOT
	// publicly fetchable — renderers must not pick it as a native
	// <img>/<video> source and should fall back to the per-attachment
	// API endpoint or a freshly signed download_url instead (MUL-3254).
	// Omitted when false so older clients see the previous shape.
	CdnSigned bool `json:"cdn_signed,omitempty"`
	// Public daemon setup config consumed by the web app at runtime so
	// self-hosted instances can show `liexiu setup self-host` commands
	// with the operator's own domains instead of LieXiu Cloud defaults.
	DaemonServerURL string `json:"daemon_server_url,omitempty"`
	DaemonAppURL    string `json:"daemon_app_url,omitempty"`

	// VCSIntegrationAvailable mirrors the LIEXIU_VCS_INTEGRATION_ENABLED
	// deployment switch so the Settings UI can hide the whole self-hosted Git
	// provider section on deployments where it is off (the managed cloud),
	// instead of rendering it and surfacing an operator-only "missing
	// LIEXIU_VCS_SECRET_KEY" hint a cloud user cannot resolve. Omitted when
	// false so the managed-cloud response keeps its previous shape; the UI
	// defaults absent to false (hidden).
	VCSIntegrationAvailable bool `json:"vcs_integration_available,omitempty"`

	// FeatureFlags exposes only frontend-safe boolean decisions. Do not dump
	// raw rules here: /api/config is public and may be called anonymously.
	FeatureFlags map[string]bool `json:"feature_flags,omitempty"`

	// ServerVersion is the running API build version, so self-hosted
	// operators can confirm what's deployed and include it in bug reports.
	// Only emitted on self-hosted deployments — omitted on the managed cloud,
	// which is continuously deployed so its users can't act on the version —
	// and empty for dev builds that aren't stamped via -X main.version.
	ServerVersion string `json:"server_version,omitempty"`
}

// GetConfig is mounted on the public (unauthenticated) route group. Only add
// deployment metadata that is safe to expose to anonymous callers.
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	config := AppConfig{}
	if h.Storage != nil {
		config.CdnDomain = h.Storage.CdnDomain()
	}
	config.CdnSigned = h.CFSigner != nil
	config.AutoLogin = h.cfg.AutoLogin && isLocalBrowserRequest(r)
	config.DaemonServerURL, config.DaemonAppURL = daemonSetupURLsFromEnv()
	config.VCSIntegrationAvailable = h.cfg.VCSIntegrationEnabled
	config.FeatureFlags = featureflags.EvaluateFrontendPublicFlags(r.Context(), h.FeatureFlags)
	// Only surface the build version on self-hosted deployments. The managed
	// cloud is continuously deployed and its users can't choose the build, so
	// the Help popover's version row would just be noise there (MUL-4108).
	if !isOfficialCloudDeployment() {
		config.ServerVersion = h.cfg.ServerVersion
	}

	writeJSON(w, http.StatusOK, config)
}

func daemonSetupURLsFromEnv() (string, string) {
	serverURL := normalizePublicURL(os.Getenv("LIEXIU_PUBLIC_URL"))
	appURL := resolveFrontendAppURL()
	if appURL == "" {
		return "", ""
	}

	if serverURL == "" {
		serverURL = appURL
	}
	if isOfficialCloudDaemonConfig(appURL) {
		return "", ""
	}
	return serverURL, appURL
}

// resolveFrontendAppURL returns the operator-configured frontend origin
// (LIEXIU_APP_URL, falling back to FRONTEND_ORIGIN), normalized. Shared by
// the daemon-setup URLs and the managed-cloud detection so both read the same
// signal.
func resolveFrontendAppURL() string {
	appURL := normalizePublicURL(os.Getenv("LIEXIU_APP_URL"))
	if appURL == "" {
		appURL = normalizePublicURL(os.Getenv("FRONTEND_ORIGIN"))
	}
	return appURL
}

func normalizePublicURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// isOfficialCloudDaemonConfig reports whether this deployment is the official
// LieXiu Cloud, identified by its frontend host alone (liexiu.ai). The
// daemon setup for the managed cloud is always
// `liexiu setup` (which hardcodes api.liexiu.ai), so the per-deployment URLs
// must be omitted from /api/config even when LIEXIU_PUBLIC_URL is unset or
// misconfigured. Previously this also required serverURL==api.liexiu.ai, so a
// cloud deployment that forgot LIEXIU_PUBLIC_URL fell through and emitted a
// `setup self-host --server-url https://liexiu.ai` command — pointing the
// daemon's backend at the frontend (no /health, no WebSocket proxy).
func isOfficialCloudDaemonConfig(appURL string) bool {
	return urlHostEquals(appURL, "liexiu.ai")
}

// isOfficialCloudDeployment reports whether this server is the official LieXiu
// Cloud, reusing the same frontend-host signal as the daemon setup (liexiu.ai).
// Managed-cloud-only behavior — such as suppressing the Help popover's
// server-version row, which only matters to self-hosted operators — is gated on
// this.
func isOfficialCloudDeployment() bool {
	return isOfficialCloudDaemonConfig(resolveFrontendAppURL())
}

func urlHostEquals(raw, want string) bool {
	host := canonicalURLHost(raw)
	if host == "" {
		return false
	}
	want = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(want)), ".")
	return host == want
}

func canonicalURLHost(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" && !strings.Contains(raw, "://") {
		u, err = url.Parse("https://" + raw)
		if err != nil {
			return ""
		}
		host = u.Hostname()
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}
