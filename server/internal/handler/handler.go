package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/analytics"
	"github.com/kailonyang/liexiu/server/internal/auth"
	"github.com/kailonyang/liexiu/server/internal/cloudruntime"
	"github.com/kailonyang/liexiu/server/internal/daemonws"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/integrations/ghsnapshot"
	obsmetrics "github.com/kailonyang/liexiu/server/internal/metrics"
	"github.com/kailonyang/liexiu/server/internal/middleware"
	"github.com/kailonyang/liexiu/server/internal/realtime"
	"github.com/kailonyang/liexiu/server/internal/service"
	"github.com/kailonyang/liexiu/server/internal/service/localinstance"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	"github.com/kailonyang/liexiu/server/internal/storage"
	"github.com/kailonyang/liexiu/server/internal/util"
	"github.com/kailonyang/liexiu/server/internal/util/secretbox"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/featureflag"
	"github.com/kailonyang/liexiu/server/pkg/llm"
)

// randomID returns a random 16-byte hex string used as a request ID for
// in-memory stores (model list, local skills, CLI update, etc.).
func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type txStarter interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

type dbExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Config struct {
	OwnerBootstrapSecret string
	// AutoLogin enables LieXiu's localhost-only personal development mode.
	// The router forces it off in production even when the environment asks
	// for it. Authentication and the canonical owner identity remain intact;
	// only the interactive login/bootstrap ceremony is removed.
	AutoLogin bool
	// VCSIntegrationEnabled gates the self-hosted Git provider integration
	// (Forgejo / Gitea / GitLab) at the deployment level, independent of whether
	// LIEXIU_VCS_SECRET_KEY is set. It is the product boundary: the feature is
	// intended for self-hosted LieXiu only (where LieXiu and the Git instance
	// can share a network), and is left off on the managed cloud — connect,
	// rotate, and webhook handlers reject when it is false, and /api/config
	// omits it so the UI hides the whole section rather than showing a
	// "missing key" message a cloud user cannot act on. Populated from
	// LIEXIU_VCS_INTEGRATION_ENABLED; the self-host compose defaults it on.
	VCSIntegrationEnabled bool
	// PublicURL is the absolute base URL the API is reachable at from the
	// public internet, with no trailing slash (e.g. "https://liexiu.ai").
	// Used to build durable file/avatar and VCS webhook URLs — never for auth,
	// routing, or workspace resolution. Empty when unset.
	// Reading the public host from request headers (Host / X-Forwarded-Host)
	// is intentionally avoided so a misconfigured reverse proxy cannot trick
	// the server into minting webhook URLs pointing at an attacker-controlled
	// host.
	PublicURL string
	// TrustedProxies are CIDRs whose source IP we trust to set
	// X-Forwarded-For / X-Real-IP. Empty means "trust nothing": the rate
	// limiter uses r.RemoteAddr exclusively. Populated via the
	// LIEXIU_TRUSTED_PROXIES env var (comma-separated CIDRs, e.g.
	// "10.0.0.0/8,127.0.0.1/32"). This is specifically to keep the per-IP
	// webhook limiter from being bypassed by a spoofed XFF on deployments
	// without a header-stripping reverse proxy in front.
	TrustedProxies []netip.Prefix
	// CloudRuntimeFleetURL enables the SaaS-only remote Fleet adapter when set.
	// Empty keeps self-hosted deployments explicit: cloud runtime endpoints
	// return 503 instead of attempting to dial a hard-coded private service.
	CloudRuntimeFleetURL     string
	CloudRuntimeFleetTimeout time.Duration
	AttachmentDownloadMode   string
	AttachmentDownloadURLTTL time.Duration
	// AttachmentFrameAncestors are trusted browser origins allowed to embed
	// attachment preview responses. In production this should mirror the
	// frontend/CORS origin allowlist so split app/api self-hosted deployments
	// can frame API-hosted PDFs without allowing arbitrary third-party frames.
	AttachmentFrameAncestors []string
	// LLM* configure the basic LLM API layer (MUL-4238). They back the
	// server-internal LLM helpers in pkg/llm (e.g. chat title generation).
	// The generic OpenAI-compatible passthrough endpoints were removed in
	// MUL-4309; LLM access is internal-only now. When both LLMAPIKey and
	// LLMBaseURL are empty the layer is disabled and callers fall back
	// silently (see maybeGenerateChatTitleAsync).
	//   - LLMAPIKey       -> LIEXIU_LLM_API_KEY
	//   - LLMBaseURL       -> LIEXIU_LLM_BASE_URL (OpenAI or any compatible gateway)
	//   - LLMDefaultModel  -> LIEXIU_LLM_DEFAULT_MODEL (used when a request omits `model`)
	LLMAPIKey       string
	LLMBaseURL      string
	LLMDefaultModel string
	// ServerVersion is the build version of the running API binary (the same
	// value main.go stamps via -X main.version and reports on /metrics).
	// Surfaced through /api/config so self-hosted operators can confirm which
	// server build is deployed. Empty in dev builds.
	ServerVersion string
}

type cloudRuntimeProxy interface {
	Enabled() bool
	Do(ctx context.Context, req cloudruntime.Request) (*cloudruntime.Response, error)
}

type RuntimeProfileRefreshNotifier interface {
	NotifyRuntimeProfilesChanged(workspaceID, profileID string)
}

type WorkspaceSetRefreshNotifier interface {
	NotifyWorkspacesChanged(userID string)
}

// DaemonPendingWorkNotifier pushes a runtime-scoped "heartbeat now" hint to the
// daemon so a queued heartbeat-carried request (model discovery) is picked up
// immediately instead of on the daemon's next scheduled tick (MUL-5444).
// Satisfied by both *daemonws.Hub (single-node) and *daemonws.RelayNotifier
// (multi-node, fans out through Redis).
type DaemonPendingWorkNotifier interface {
	NotifyPendingWork(runtimeID, kind string)
}

type Handler struct {
	Queries                *db.Queries
	DB                     dbExecutor
	TxStarter              txStarter
	Hub                    *realtime.Hub
	DaemonHub              *daemonws.Hub
	DaemonProfileRefresh   RuntimeProfileRefreshNotifier
	DaemonWorkspaceRefresh WorkspaceSetRefreshNotifier
	Bus                    *events.Bus
	TaskService            *service.TaskService
	Orchestration          *orchestration.Service
	LocalInstance          *localinstance.Repository
	IssueService           *service.IssueService
	UpdateStore            UpdateStore
	ModelListStore         ModelListStore
	LocalSkillListStore    LocalSkillListStore
	LocalSkillImportStore  LocalSkillImportStore
	FeatureFlags           *featureflag.Service
	LivenessStore          LivenessStore
	HeartbeatScheduler     HeartbeatScheduler
	Storage                storage.Storage
	CFSigner               *auth.CloudFrontSigner
	Analytics              analytics.Client
	// DaemonPendingWork pushes "heartbeat now" hints for queued
	// heartbeat-carried requests (MUL-5444). Optional: when nil,
	// requestDaemonPendingWork falls back to the local DaemonHub, which is the
	// correct delivery scope for a single-node deployment.
	DaemonPendingWork DaemonPendingWorkNotifier
	// ModelCatalogCache serves the last known good model list for a runtime so
	// the picker can render without waiting for a daemon round trip
	// (stale-while-revalidate, MUL-5444). Nil-safe: every call site treats a nil
	// cache as a permanent miss and falls back to the full discovery flow.
	ModelCatalogCache ModelCatalogCache
	// Metrics is the shared business-metrics collector built by main.go.
	// May be nil in tests / self-hosted with the metrics listener disabled;
	// every Record* method is nil-safe and obsmetrics.RecordEvent treats a
	// nil Metrics as local observation disabled.
	Metrics                      *obsmetrics.BusinessMetrics
	PATCache                     *auth.PATCache
	DaemonTokenCache             *auth.DaemonTokenCache
	MembershipCache              *auth.MembershipCache
	WebhookRateLimiter           WebhookRateLimiter
	WebhookIPRateLimiter         WebhookRateLimiter
	WebhookAbsoluteIPRateLimiter WebhookRateLimiter
	CloudRuntime                 cloudRuntimeProxy
	// Obsolete external integration state was removed in Wave 1C.6.
	// LLM is the basic LLM API layer (MUL-4238): a thin wrapper over the
	// OpenAI Go SDK backing server-internal one-shot LLM helpers such as chat
	// title generation. The generic passthrough endpoints were removed in
	// MUL-4309, so it is internal-only now. Always non-nil (New builds it from
	// Config); when unconfigured its Enabled() reports false and callers fall
	// back silently.
	LLM *llm.Client
	// VCSSecretBox encrypts/decrypts per-workspace Git provider access tokens and
	// webhook secrets at rest (Forgejo / Gitea / GitLab). Nil when
	// LIEXIU_VCS_SECRET_KEY is unset; the connect/webhook handlers return 503
	// in that case so a misconfigured self-host deployment surfaces a clear
	// error rather than silently storing plaintext. Wired in
	// cmd/server/router.go after New.
	VCSSecretBox *secretbox.Box
	// PRRefresh drives the GitHub API snapshot pipeline for PR cards (MUL-5265):
	// webhook / page-visit / TTL triggers → authenticated GraphQL fetch →
	// head-SHA-guarded atomic snapshot write. Always non-nil, but inert (every
	// trigger is a no-op) when GITHUB_APP_ID / GITHUB_APP_PRIVATE_KEY are unset,
	// so the feature degrades cleanly on deployments without a private key.
	// Wired in cmd/server/router.go after New.
	PRRefresh *ghsnapshot.Manager
	cfg       Config
}

func New(queries *db.Queries, txStarter txStarter, hub *realtime.Hub, bus *events.Bus, store storage.Storage, cfSigner *auth.CloudFrontSigner, analyticsClient analytics.Client, cfg Config, daemonHubs ...*daemonws.Hub) *Handler {
	var executor dbExecutor
	if candidate, ok := txStarter.(dbExecutor); ok {
		executor = candidate
	}

	if analyticsClient == nil {
		analyticsClient = analytics.NoopClient{}
	}
	if mode, ok := normalizeAttachmentDownloadMode(cfg.AttachmentDownloadMode); ok {
		cfg.AttachmentDownloadMode = string(mode)
	} else {
		slog.Warn("invalid ATTACHMENT_DOWNLOAD_MODE, using auto", "value", cfg.AttachmentDownloadMode)
		cfg.AttachmentDownloadMode = string(attachmentDownloadModeAuto)
	}
	if cfg.AttachmentDownloadURLTTL <= 0 {
		cfg.AttachmentDownloadURLTTL = defaultAttachmentDownloadURLTTL
	}

	var daemonHub *daemonws.Hub
	if len(daemonHubs) > 0 {
		daemonHub = daemonHubs[0]
	}
	var daemonProfileRefresh RuntimeProfileRefreshNotifier
	var daemonWorkspaceRefresh WorkspaceSetRefreshNotifier
	if daemonHub != nil {
		daemonProfileRefresh = daemonHub
		daemonWorkspaceRefresh = daemonHub
	}

	llmClient := llm.New(llm.Config{
		APIKey:       cfg.LLMAPIKey,
		BaseURL:      cfg.LLMBaseURL,
		DefaultModel: cfg.LLMDefaultModel,
	})

	taskSvc := service.NewTaskService(queries, txStarter, hub, bus, daemonHub)
	taskSvc.Analytics = analyticsClient
	h := &Handler{
		Queries:                      queries,
		DB:                           executor,
		TxStarter:                    txStarter,
		LocalInstance:                localinstance.NewRepository(queries, txStarter),
		Hub:                          hub,
		DaemonHub:                    daemonHub,
		DaemonProfileRefresh:         daemonProfileRefresh,
		DaemonWorkspaceRefresh:       daemonWorkspaceRefresh,
		Bus:                          bus,
		TaskService:                  taskSvc,
		IssueService:                 service.NewIssueService(queries, txStarter, bus, analyticsClient, taskSvc),
		UpdateStore:                  NewInMemoryUpdateStore(),
		ModelListStore:               NewInMemoryModelListStore(),
		ModelCatalogCache:            NewInMemoryModelCatalogCache(),
		LocalSkillListStore:          NewInMemoryLocalSkillListStore(),
		LocalSkillImportStore:        NewInMemoryLocalSkillImportStore(),
		LivenessStore:                NewNoopLivenessStore(),
		HeartbeatScheduler:           NewPassthroughHeartbeatScheduler(queries),
		Storage:                      store,
		CFSigner:                     cfSigner,
		Analytics:                    analyticsClient,
		WebhookRateLimiter:           NewMemoryWebhookRateLimiter(DefaultWebhookRateLimit()),
		WebhookIPRateLimiter:         NewMemoryWebhookIPRateLimiter(DefaultWebhookIPRateLimit()),
		WebhookAbsoluteIPRateLimiter: NewMemoryWebhookAbsoluteIPRateLimiter(DefaultWebhookAbsoluteIPRateLimit()),
		CloudRuntime: cloudruntime.NewClient(cloudruntime.Config{
			BaseURL: cfg.CloudRuntimeFleetURL,
			Timeout: cfg.CloudRuntimeFleetTimeout,
		}),
		LLM: llmClient,
		cfg: cfg,
	}
	// GitHub API snapshot pipeline for PR cards (MUL-5265). Built
	// unconditionally but inert (every trigger no-ops) when the App private key
	// is unconfigured, so the feature degrades cleanly. main.go calls
	// h.PRRefresh.Start(ctx) to launch its worker pool + TTL sweeper.
	ghClient, err := ghsnapshot.NewClientFromEnv()
	if err != nil {
		// Malformed key is operator-actionable; the pipeline stays disabled.
		slog.Warn("github: PR snapshot pipeline disabled (invalid App private key)", "err", err)
	}
	h.PRRefresh = ghsnapshot.NewManager(ghClient, queries, txStarter, h.broadcastPRSnapshotApplied)

	return h
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	// Marshal the body up front so we can advertise an accurate Content-Length
	// header. Streaming straight into the ResponseWriter after WriteHeader forces
	// net/http into chunked transfer encoding, which omits Content-Length; buffering
	// first lets clients (and proxies) see the exact body size.
	body, err := json.Marshal(v)
	if err != nil {
		// Fall back to a minimal, self-describing error payload rather than leaving
		// the client with a half-written response.
		body = []byte(`{"error":"failed to encode response"}`)
		status = http.StatusInternalServerError
	}
	// Match the trailing newline that json.Encoder.Encode historically appended.
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeMeasuredJSON behaves like writeJSON but returns the encoded body size so
// callers can record payload bytes in slow-endpoint diagnostics. It measures the
// uncompressed JSON length and is unrelated to transport compression.
func writeMeasuredJSON(w http.ResponseWriter, status int, v any) (int, error) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode response")
		return 0, err
	}
	body = append(body, '\n')
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		return len(body), err
	}
	return len(body), nil
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeErrorCode is writeError plus a stable machine-readable code, so a UI
// can translate the failure instead of toasting the English sentence at a
// user whose console is in another language. The sentence stays as the
// fallback for anything that has not been given a translation yet.
func writeErrorCode(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// Thin wrappers around util functions.
//
// parseUUID is intentionally the panicking variant: any handler call site
// reachable here is expected to feed a UUID that is either (a) a sqlc round-trip
// of a DB-sourced value, or (b) a raw request input that has already been
// validated upstream. A panic here means an unguarded user-input string slipped
// in — that is a real bug we want surfaced loudly (chi's middleware.Recoverer
// converts it to a 500) instead of silently corrupting data via a zero UUID.
//
// For unvalidated user input at request boundaries, use parseUUIDOrBadRequest
// (writes 400) — never feed raw chi.URLParam / request-body strings into
// parseUUID directly when the call writes to the database.
func parseUUID(s string) pgtype.UUID                { return util.MustParseUUID(s) }
func uuidToString(u pgtype.UUID) string             { return util.UUIDToString(u) }
func textToPtr(t pgtype.Text) *string               { return util.TextToPtr(t) }
func ptrToText(s *string) pgtype.Text               { return util.PtrToText(s) }
func strToText(s string) pgtype.Text                { return util.StrToText(s) }
func timestampToString(t pgtype.Timestamptz) string { return util.TimestampToString(t) }
func timestampToPtr(t pgtype.Timestamptz) *string   { return util.TimestampToPtr(t) }
func dateToPtr(d pgtype.Date) *string               { return util.DateToPtr(d) }
func uuidToPtr(u pgtype.UUID) *string               { return util.UUIDToPtr(u) }

// uuidsToStrings maps a UUID array column to string ids, skipping NULL/invalid
// entries. Returns nil (not an empty slice) when there is nothing to emit so
// `omitempty` JSON fields drop out cleanly (MUL-4195).
func uuidsToStrings(us []pgtype.UUID) []string {
	if len(us) == 0 {
		return nil
	}
	out := make([]string, 0, len(us))
	for _, u := range us {
		if u.Valid {
			out = append(out, uuidToString(u))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// uuidStringsOrEmpty preserves the distinction between a modern, authoritative
// empty UUID-array value (`[]`) and a field omitted by a legacy server. Delivery
// receipts use this so clients never mistake zero delivered comments for an
// unknown receipt and fall back to the enqueue-time plan.
func uuidStringsOrEmpty(us []pgtype.UUID) []string {
	out := uuidsToStrings(us)
	if out == nil {
		return []string{}
	}
	return out
}

func int8ToPtr(v pgtype.Int8) *int64 { return util.Int8ToPtr(v) }
func int4ToPtr(v pgtype.Int4) *int32 { return util.Int4ToPtr(v) }
func ptrToInt4(v *int32) pgtype.Int4 { return util.PtrToInt4(v) }

// parseUUIDOrBadRequest validates a UUID string sourced from user input
// (URL params, request body, headers). On invalid input it writes a 400
// response and returns ok=false; callers must return immediately.
//
// Use this anywhere a malformed UUID would otherwise reach a write query
// (DELETE / UPDATE) — the silent zero-UUID behavior of the old ParseUUID
// caused real silent-data-loss bugs (#1661).
func parseUUIDOrBadRequest(w http.ResponseWriter, s, fieldName string) (pgtype.UUID, bool) {
	u, err := util.ParseUUID(s)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+fieldName)
		return pgtype.UUID{}, false
	}
	return u, true
}

func parseUUIDSliceOrBadRequest(w http.ResponseWriter, ids []string, fieldName string) ([]pgtype.UUID, bool) {
	uuids := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		u, err := util.ParseUUID(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid "+fieldName)
			return nil, false
		}
		uuids[i] = u
	}
	return uuids, true
}

// publish sends a domain event through the event bus.
func (h *Handler) publish(eventType, workspaceID, actorType, actorID string, payload any) {
	h.Bus.Publish(events.Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		ActorType:   actorType,
		ActorID:     actorID,
		Payload:     payload,
	})
}

func (h *Handler) notifyDaemonWorkspacesChanged(userIDs ...string) {
	if h.DaemonWorkspaceRefresh == nil {
		return
	}
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		h.DaemonWorkspaceRefresh.NotifyWorkspacesChanged(userID)
	}
}

// publishTask is publish() plus a TaskID hint so the realtime layer can route
// the event to the per-task scope rather than the whole workspace.
func (h *Handler) publishTask(eventType, workspaceID, actorType, actorID, taskID string, payload any) {
	h.Bus.Publish(events.Event{
		Type:        eventType,
		WorkspaceID: workspaceID,
		ActorType:   actorType,
		ActorID:     actorID,
		TaskID:      taskID,
		Payload:     payload,
	})
}

func isNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// isCheckViolation reports whether err is a PostgreSQL CHECK constraint
// violation (SQLSTATE 23514). Used to translate column-level CHECK failures
// into a 4xx instead of a generic 500.
func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

func requestUserID(r *http.Request) string {
	return r.Header.Get("X-User-ID")
}

// resolveActor determines whether the request is from an agent or a human member.
//
// First-class signal: X-Actor-Source set to "task_token" means the request
// authenticated via an `mat_` task-scoped token. The auth middleware sets
// that header (and stripped any client-supplied value first), so it is
// authoritative — the bound (agent_id, task_id) cannot be forged or
// stripped by the agent process. This is the path MUL-2600 relies on to
// reject agent-process traffic on owner-only endpoints.
//
// Fallback signal (legacy CLI / member-token paths): the request MUST
// carry both X-Agent-ID and a valid X-Task-ID, and the task must belong
// to the claimed agent. Otherwise we fall back to "member".
//
// X-Agent-ID alone is not trusted: any workspace member can guess or observe
// an agent's UUID, and a member-supplied X-Agent-ID would otherwise let that
// member impersonate the agent and bypass the private-agent gate (#2359
// review). The daemon always pairs the two headers, so requiring both has
// no effect on legitimate agent callers but closes the impersonation path.
//
// Returns ("agent", agentID) on success, ("member", userID) otherwise.
func (h *Handler) resolveActor(r *http.Request, userID, workspaceID string) (actorType, actorID string) {
	if r.Header.Get("X-Actor-Source") == "task_token" {
		// Server-set header — auth middleware also forced X-Agent-ID
		// from the token row. Trust it directly without re-querying.
		return "agent", r.Header.Get("X-Agent-ID")
	}
	agentID := r.Header.Get("X-Agent-ID")
	if agentID == "" {
		return "member", userID
	}
	taskID := r.Header.Get("X-Task-ID")
	if taskID == "" {
		slog.Debug("resolveActor: X-Agent-ID present but X-Task-ID missing, refusing to trust agent identity", "agent_id", agentID)
		return "member", userID
	}

	agentUUID, err := util.ParseUUID(agentID)
	if err != nil {
		slog.Debug("resolveActor: X-Agent-ID is not a valid UUID, falling back to member", "agent_id", agentID)
		return "member", userID
	}
	// Validate the agent exists in the target workspace.
	agent, err := h.Queries.GetAgent(r.Context(), agentUUID)
	if err != nil || uuidToString(agent.WorkspaceID) != workspaceID {
		slog.Debug("resolveActor: X-Agent-ID rejected, agent not found or workspace mismatch", "agent_id", agentID, "workspace_id", workspaceID)
		return "member", userID
	}

	taskUUID, err := util.ParseUUID(taskID)
	if err != nil {
		slog.Debug("resolveActor: X-Task-ID is not a valid UUID, falling back to member", "task_id", taskID)
		return "member", userID
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil || uuidToString(task.AgentID) != agentID {
		slog.Debug("resolveActor: X-Task-ID rejected, task not found or agent mismatch", "agent_id", agentID, "task_id", taskID)
		return "member", userID
	}

	return "agent", agentID
}

func requireUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := requestUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "user not authenticated")
		return "", false
	}
	return userID, true
}

// resolveWorkspaceID returns the workspace UUID for this request. Delegates
// to middleware.ResolveWorkspaceIDFromRequest so middleware-protected routes
// and middleware-less routes (e.g. /api/upload-file) share identical
// resolution behavior — including slug → UUID translation via the DB.
//
// Returns "" when no workspace identifier was provided or a slug was provided
// but doesn't match any workspace.
func (h *Handler) resolveWorkspaceID(r *http.Request) string {
	return middleware.ResolveWorkspaceIDFromRequest(r, h.Queries)
}

// ctxMember returns the workspace member from context (set by workspace middleware).
func ctxMember(ctx context.Context) (db.Member, bool) {
	return middleware.MemberFromContext(ctx)
}

// ctxWorkspaceID returns the workspace ID from context (set by workspace middleware).
func ctxWorkspaceID(ctx context.Context) string {
	return middleware.WorkspaceIDFromContext(ctx)
}

// workspaceIDFromURL returns the workspace ID from context (preferred) or chi URL param (fallback).
func workspaceIDFromURL(r *http.Request, param string) string {
	if id := middleware.WorkspaceIDFromContext(r.Context()); id != "" {
		return id
	}
	return chi.URLParam(r, param)
}

// workspaceMember returns the member from middleware context, or falls back to a DB
// lookup when the handler is called directly (e.g. in tests).
func (h *Handler) workspaceMember(w http.ResponseWriter, r *http.Request, workspaceID string) (db.Member, bool) {
	if m, ok := ctxMember(r.Context()); ok {
		return m, true
	}
	return h.requireWorkspaceMember(w, r, workspaceID, "workspace not found")
}

func roleAllowed(role string, roles ...string) bool {
	for _, candidate := range roles {
		if role == candidate {
			return true
		}
	}
	return false
}

func countOwners(members []db.Member) int {
	owners := 0
	for _, member := range members {
		if member.Role == "owner" {
			owners++
		}
	}
	return owners
}

func (h *Handler) getWorkspaceMember(ctx context.Context, userID, workspaceID string) (db.Member, error) {
	userUUID, err := util.ParseUUID(userID)
	if err != nil {
		return db.Member{}, err
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return db.Member{}, err
	}
	return h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      userUUID,
		WorkspaceID: wsUUID,
	})
}

func (h *Handler) requireWorkspaceMember(w http.ResponseWriter, r *http.Request, workspaceID, notFoundMsg string) (db.Member, bool) {
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return db.Member{}, false
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return db.Member{}, false
	}

	member, err := h.getWorkspaceMember(r.Context(), userID, workspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, notFoundMsg)
		return db.Member{}, false
	}

	return member, true
}

func (h *Handler) requireWorkspaceRole(w http.ResponseWriter, r *http.Request, workspaceID, notFoundMsg string, roles ...string) (db.Member, bool) {
	member, ok := h.requireWorkspaceMember(w, r, workspaceID, notFoundMsg)
	if !ok {
		return db.Member{}, false
	}
	if !roleAllowed(member.Role, roles...) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return db.Member{}, false
	}
	return member, true
}

// isWorkspaceEntity checks whether a user_id belongs to the given workspace,
// as either a member or an agent depending on userType.
func (h *Handler) isWorkspaceEntity(ctx context.Context, userType, userID, workspaceID string) bool {
	switch userType {
	case "member":
		_, err := h.getWorkspaceMember(ctx, userID, workspaceID)
		return err == nil
	case "agent":
		userUUID, err := util.ParseUUID(userID)
		if err != nil {
			return false
		}
		wsUUID, err := util.ParseUUID(workspaceID)
		if err != nil {
			return false
		}
		_, err = h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          userUUID,
			WorkspaceID: wsUUID,
		})
		return err == nil
	default:
		return false
	}
}

func (h *Handler) loadIssueForUser(w http.ResponseWriter, r *http.Request, issueID string) (db.Issue, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return db.Issue{}, false
	}

	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return db.Issue{}, false
	}

	// Try identifier format first (e.g., "JIA-42"). resolveIssueByIdentifier
	// silently returns false for non-identifier strings, falling through to
	// the UUID path below.
	if issue, ok := h.resolveIssueByIdentifier(r.Context(), issueID, workspaceID); ok {
		return issue, true
	}

	issueUUID, err := util.ParseUUID(issueID)
	if err != nil {
		// Not a valid UUID and didn't match identifier format → 404 (consistent
		// with previous silent-zero behavior, which would also have produced 404).
		writeError(w, http.StatusNotFound, "issue not found")
		return db.Issue{}, false
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return db.Issue{}, false
	}
	issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID:          issueUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return db.Issue{}, false
	}
	return issue, true
}

// resolveIssueByIdentifier tries to look up an issue by "PREFIX-NUMBER" format.
//
// The prefix must match the workspace's own issue prefix, the same rule
// `lookupIssueByIdentifier` applies to VCS webhooks. Without it every prefix
// resolved to the same issue number, so `FOO-134` and `TRS-134` were
// interchangeable — which makes the identifier URL `/{ws}/issues/{key}`
// unusable as a canonical link.
func (h *Handler) resolveIssueByIdentifier(ctx context.Context, id, workspaceID string) (db.Issue, bool) {
	parts := splitIdentifier(id)
	if parts == nil {
		return db.Issue{}, false
	}
	if workspaceID == "" {
		return db.Issue{}, false
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return db.Issue{}, false
	}
	// Case-insensitive: a hand-typed `trs-134` should open `TRS-134`.
	prefix := h.getIssuePrefix(ctx, wsUUID)
	if prefix == "" || !strings.EqualFold(parts.prefix, prefix) {
		return db.Issue{}, false
	}
	issue, err := h.Queries.GetIssueByNumber(ctx, db.GetIssueByNumberParams{
		WorkspaceID: wsUUID,
		Number:      parts.number,
	})
	if err != nil {
		return db.Issue{}, false
	}
	return issue, true
}

type identifierParts struct {
	prefix string
	number int32
}

func splitIdentifier(id string) *identifierParts {
	idx := -1
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '-' {
			idx = i
			break
		}
	}
	if idx <= 0 || idx >= len(id)-1 {
		return nil
	}
	numStr := id[idx+1:]
	num := 0
	for _, c := range numStr {
		if c < '0' || c > '9' {
			return nil
		}
		num = num*10 + int(c-'0')
		// Guard the int32 conversion below: a UUID whose last group happens to
		// be all digits ("…-421234567890") reaches here and would otherwise be
		// truncated into a plausible-looking issue number.
		if num > math.MaxInt32 {
			return nil
		}
	}
	if num <= 0 {
		return nil
	}
	return &identifierParts{prefix: id[:idx], number: int32(num)}
}

// issuePrefixForWorkspace resolves a workspace row's effective issue prefix:
// the configured value, or one generated from the workspace name when the
// stored prefix is empty (e.g. workspaces created before the prefix was
// introduced). Split out from getIssuePrefix so callers that already hold the
// row — such as the GitHub close-intent scan, which must not re-read it — can
// reuse the rule.
//
// The empty-prefix fallback stays on the FROZEN name-based derivation, not the
// slug-based one new workspaces get (MUL-6050): identifiers are computed at
// read time, so switching this path would rewrite the identifier of every
// issue in those legacy workspaces. New workspaces always persist a prefix at
// creation, so they never reach this branch.
func issuePrefixForWorkspace(ws db.Workspace) string {
	if ws.IssuePrefix != "" {
		return ws.IssuePrefix
	}
	return legacyIssuePrefixFromName(ws.Name)
}

// getIssuePrefix fetches the effective issue_prefix for a workspace, and
// returns "" when the workspace row cannot be loaded.
func (h *Handler) getIssuePrefix(ctx context.Context, workspaceID pgtype.UUID) string {
	ws, err := h.Queries.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return ""
	}
	return issuePrefixForWorkspace(ws)
}

func (h *Handler) loadAgentForUser(w http.ResponseWriter, r *http.Request, agentID string) (db.Agent, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return db.Agent{}, false
	}

	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return db.Agent{}, false
	}

	agentUUID, ok := parseUUIDOrBadRequest(w, agentID, "agent id")
	if !ok {
		return db.Agent{}, false
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return db.Agent{}, false
	}

	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return db.Agent{}, false
	}
	return agent, true
}
