//go:build realacceptance

package main

// This file is intentionally opt-in twice: the build tag keeps it out of the
// normal test graph, while init prevents TestMain from opening a database
// unless every explicit real-acceptance consent switch is present.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/analytics"
	"github.com/kailonyang/liexiu/server/internal/events"
	"github.com/kailonyang/liexiu/server/internal/realtime"
	"github.com/kailonyang/liexiu/server/internal/service/orchestration"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/redact"
)

const (
	realAcceptanceDBPrefixA  = "liexiu_realacceptance_"
	realAcceptanceDBPrefixB  = "liexiu_wave4b6_acceptance_"
	realAcceptanceCapability = "orchestration-context-v1"
)

func init() {
	// TestMain reads DATABASE_URL before tests run. Clearing it here is the
	// fail-closed boundary for all ordinary invocations of this tagged file.
	if _, err := readRealAcceptanceConfig(); err != nil {
		_ = os.Setenv("DATABASE_URL", "")
		_ = os.Unsetenv("LIEXIU_REAL_ACCEPTANCE_EXTERNAL_FIXTURE")
		if realAcceptanceEnabled() {
			_ = os.Setenv("LIEXIU_REAL_ACCEPTANCE_CONFIG_ERROR", err.Error())
		} else {
			_ = os.Unsetenv("LIEXIU_REAL_ACCEPTANCE_CONFIG_ERROR")
		}
		return
	}
	_ = os.Unsetenv("LIEXIU_REAL_ACCEPTANCE_CONFIG_ERROR")
	_ = os.Setenv("DATABASE_URL", os.Getenv("LIEXIU_REAL_ACCEPTANCE_DATABASE_URL"))
	_ = os.Setenv("LIEXIU_REAL_ACCEPTANCE_EXTERNAL_FIXTURE", "1")
}

func realAcceptanceEnabled() bool {
	for _, name := range []string{"LIEXIU_RUN_REAL_ACCEPTANCE", "LIEXIU_ALLOW_RUNTIME_SUBPROCESSES", "LIEXIU_ALLOW_EXTERNAL_QUOTA", "LIEXIU_ALLOW_REAL_ACCEPTANCE_CLEANUP"} {
		if os.Getenv(name) != "1" {
			return false
		}
	}
	return true
}

func acceptedAcceptanceDatabase(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Path == "" {
		return false
	}
	if strings.Count(u.Path, "/") != 1 || !strings.HasPrefix(u.Path, "/") {
		return false
	}
	db := strings.TrimPrefix(u.Path, "/")
	return db != "" && (strings.HasPrefix(db, realAcceptanceDBPrefixA) || strings.HasPrefix(db, realAcceptanceDBPrefixB))
}

type realAcceptanceConfig struct {
	binary, providerA, providerB, pathEnvA, pathEnvB, executableA, executableB, modelA, modelB string
	timeout, roleTimeout                                                                       time.Duration
	maxTokens, maxCost                                                                         int64
	credentialEnvA, credentialEnvB                                                             []string
}

type realRuntimeReceipt struct {
	Provider                  string `json:"provider"`
	Model                     string `json:"model"`
	DaemonVersion             string `json:"daemon_version"`
	CLIVersion                string `json:"cli_version"`
	RequiredCapabilityPresent bool   `json:"required_capability_present"`
	OwnershipVerified         bool   `json:"ownership_verified"`
	Visibility                string `json:"visibility"`
}

type realRouteReceipt struct {
	Node     string `json:"node"`
	Purpose  string `json:"purpose"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Lineage  string `json:"lineage_sha256"`
}

type realMissionExecutionReceipt struct {
	Routes                   []realRouteReceipt `json:"routes"`
	RuntimeBReused           bool               `json:"runtime_b_reused"`
	ReviewersSeparated       bool               `json:"reviewers_separated"`
	FinalArtifactKind        string             `json:"final_artifact_kind"`
	FinalArtifactHashPresent bool               `json:"final_artifact_hash_present"`
	FinalStatus              string             `json:"final_status"`
	FinalProgressPercent     int                `json:"final_progress_percent"`
	Usage                    realUsageReceipt   `json:"usage"`
	Budget                   realBudgetReceipt  `json:"budget"`
}

type realUsageReceipt struct {
	TaskCount              int64 `json:"task_count"`
	UsageReportedTaskCount int64 `json:"usage_reported_task_count"`
	TotalTokens            int64 `json:"total_tokens"`
	ReportedCostUSDTicks   int64 `json:"reported_cost_usd_ticks"`
	UncostedTokens         int64 `json:"uncosted_tokens"`
}

type realBudgetReceipt struct {
	Status               string `json:"status"`
	ConsumedTokens       int64  `json:"consumed_tokens"`
	ReservedTokens       int64  `json:"reserved_tokens"`
	ConsumedCostUSDTicks int64  `json:"consumed_cost_usd_ticks"`
	ReservedCostUSDTicks int64  `json:"reserved_cost_usd_ticks"`
}

func loadRealAcceptanceConfig(t *testing.T) realAcceptanceConfig {
	t.Helper()
	c, err := readRealAcceptanceConfig()
	if err != nil {
		t.Fatalf("invalid real acceptance configuration: %v", err)
	}
	return c
}

func readRealAcceptanceConfig() (realAcceptanceConfig, error) {
	if !realAcceptanceEnabled() {
		return realAcceptanceConfig{}, fmt.Errorf("all four explicit consent switches are required")
	}
	if !acceptedAcceptanceDatabase(os.Getenv("LIEXIU_REAL_ACCEPTANCE_DATABASE_URL")) {
		return realAcceptanceConfig{}, fmt.Errorf("a dedicated acceptance-prefixed PostgreSQL database is required")
	}
	c := realAcceptanceConfig{
		binary: strings.TrimSpace(os.Getenv("LIEXIU_REAL_DAEMON_BINARY")), providerA: strings.TrimSpace(os.Getenv("LIEXIU_REAL_PROVIDER_A")), providerB: strings.TrimSpace(os.Getenv("LIEXIU_REAL_PROVIDER_B")),
		pathEnvA: strings.TrimSpace(os.Getenv("LIEXIU_REAL_PROVIDER_A_PATH_ENV")), pathEnvB: strings.TrimSpace(os.Getenv("LIEXIU_REAL_PROVIDER_B_PATH_ENV")),
		executableA: strings.TrimSpace(os.Getenv("LIEXIU_REAL_PROVIDER_A_EXECUTABLE")), executableB: strings.TrimSpace(os.Getenv("LIEXIU_REAL_PROVIDER_B_EXECUTABLE")),
		modelA: strings.TrimSpace(os.Getenv("LIEXIU_REAL_PROVIDER_A_MODEL")), modelB: strings.TrimSpace(os.Getenv("LIEXIU_REAL_PROVIDER_B_MODEL")),
	}
	var err error
	if c.credentialEnvA, err = parseRealAcceptanceCredentialEnvs("LIEXIU_REAL_PROVIDER_A_CREDENTIAL_ENVS", os.Getenv("LIEXIU_REAL_PROVIDER_A_CREDENTIAL_ENVS")); err != nil {
		return realAcceptanceConfig{}, err
	}
	if c.credentialEnvB, err = parseRealAcceptanceCredentialEnvs("LIEXIU_REAL_PROVIDER_B_CREDENTIAL_ENVS", os.Getenv("LIEXIU_REAL_PROVIDER_B_CREDENTIAL_ENVS")); err != nil {
		return realAcceptanceConfig{}, err
	}
	if c.maxTokens, err = strconv.ParseInt(os.Getenv("LIEXIU_REAL_MAX_TOKENS"), 10, 64); err != nil {
		return realAcceptanceConfig{}, fmt.Errorf("LIEXIU_REAL_MAX_TOKENS must be an integer")
	}
	if c.maxCost, err = strconv.ParseInt(os.Getenv("LIEXIU_REAL_MAX_COST_USD_TICKS"), 10, 64); err != nil {
		return realAcceptanceConfig{}, fmt.Errorf("LIEXIU_REAL_MAX_COST_USD_TICKS must be an integer")
	}
	roleTimeoutSeconds, err := strconv.ParseInt(os.Getenv("LIEXIU_REAL_ROLE_TIMEOUT_SECONDS"), 10, 64)
	if err != nil {
		return realAcceptanceConfig{}, fmt.Errorf("LIEXIU_REAL_ROLE_TIMEOUT_SECONDS must be an integer")
	}
	c.timeout, err = time.ParseDuration(os.Getenv("LIEXIU_REAL_ACCEPTANCE_TIMEOUT"))
	if err != nil {
		return realAcceptanceConfig{}, fmt.Errorf("LIEXIU_REAL_ACCEPTANCE_TIMEOUT must be a Go duration")
	}
	c.roleTimeout = time.Duration(roleTimeoutSeconds) * time.Second
	if c.timeout <= 0 || c.roleTimeout <= 0 || c.maxTokens < 3 || c.maxCost < 3 {
		return realAcceptanceConfig{}, fmt.Errorf("timeouts must be positive and Mission token/cost budgets must each be at least 3")
	}
	if c.providerA == c.providerB {
		return realAcceptanceConfig{}, fmt.Errorf("provider A and B must differ")
	}
	pathEnvPattern := regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	if !pathEnvPattern.MatchString(c.pathEnvA) || !pathEnvPattern.MatchString(c.pathEnvB) || c.pathEnvA == c.pathEnvB {
		return realAcceptanceConfig{}, fmt.Errorf("provider path env names must be distinct POSIX environment names")
	}
	if !realProviderPathEnvMatches(c.providerA, c.pathEnvA) || !realProviderPathEnvMatches(c.providerB, c.pathEnvB) {
		return realAcceptanceConfig{}, fmt.Errorf("each provider path env must match its provider identity")
	}
	for name, path := range map[string]string{"LIEXIU_REAL_DAEMON_BINARY": c.binary, "LIEXIU_REAL_PROVIDER_A_EXECUTABLE": c.executableA, "LIEXIU_REAL_PROVIDER_B_EXECUTABLE": c.executableB} {
		if !filepath.IsAbs(path) {
			return realAcceptanceConfig{}, fmt.Errorf("%s must be an absolute path", name)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return realAcceptanceConfig{}, fmt.Errorf("%s must name a regular executable", name)
		}
	}
	for name, value := range map[string]string{"LIEXIU_REAL_PROVIDER_A": c.providerA, "LIEXIU_REAL_PROVIDER_B": c.providerB, "LIEXIU_REAL_PROVIDER_A_PATH_ENV": c.pathEnvA, "LIEXIU_REAL_PROVIDER_B_PATH_ENV": c.pathEnvB, "LIEXIU_REAL_PROVIDER_A_MODEL": c.modelA, "LIEXIU_REAL_PROVIDER_B_MODEL": c.modelB} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
			return realAcceptanceConfig{}, fmt.Errorf("%s is required", name)
		}
	}
	return c, nil
}

func realProviderPathEnvMatches(provider, pathEnv string) bool {
	var normalized strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(provider)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			normalized.WriteRune(r)
		} else {
			normalized.WriteByte('_')
		}
	}
	want := normalized.String() + "_PATH"
	return pathEnv == "LIEXIU_"+want
}

func parseRealAcceptanceCredentialEnvs(configName, raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	pattern := regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	seen := make(map[string]struct{})
	values := make([]string, 0, 2)
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if !pattern.MatchString(name) || !isRealAcceptanceCredentialEnv(name) || isProtectedRealAcceptanceEnv(name) {
			return nil, fmt.Errorf("%s contains an invalid provider credential environment name", configName)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		value, ok := os.LookupEnv(name)
		if !ok || value == "" || strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("%s requires each named provider credential environment value to be present and single-line", configName)
		}
		seen[name] = struct{}{}
		values = append(values, name)
		if len(values) > 8 {
			return nil, fmt.Errorf("%s may name at most eight provider credential environments", configName)
		}
	}
	return values, nil
}

func isRealAcceptanceCredentialEnv(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "AUTH", "PASSWORD", "CREDENTIAL"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func isProtectedRealAcceptanceEnv(name string) bool {
	if strings.HasPrefix(name, "LIEXIU_REAL_") {
		return true
	}
	switch name {
	case "DATABASE_URL", "PGPASSWORD", "PGPASSFILE", "PGSERVICE", "PGSERVICEFILE", "PGHOST", "PGHOSTADDR", "PGPORT", "PGDATABASE", "PGUSER",
		"POSTGRES_PASSWORD", "POSTGRES_USER", "POSTGRES_DB", "REDIS_URL", "JWT_SECRET",
		"LIEXIU_TOKEN", "LIEXIU_OWNER_BOOTSTRAP_SECRET", "LIEXIU_VCS_SECRET_KEY", "LIEXIU_LLM_API_KEY":
		return true
	default:
		return false
	}
}

func isRealAcceptanceProviderPathEnv(name string) bool {
	return strings.HasPrefix(name, "LIEXIU_") && strings.HasSuffix(name, "_PATH")
}

// TestMultiRuntimeRealAcceptance drives production orchestration around two
// real foreground daemons. It is never a default test and is intentionally not
// runnable with a normal DATABASE_URL.
func TestMultiRuntimeRealAcceptance(t *testing.T) {
	c := loadRealAcceptanceConfig(t)
	if testPool == nil {
		t.Skip("integration TestMain did not start")
	}
	if strings.TrimSpace(normalizeServerVersion(version)) == "" {
		t.Fatal("real acceptance requires a non-empty server version receipt")
	}
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(signalCtx, c.timeout)
	defer cancel()

	hub := realtime.NewHub()
	go hub.Run()
	bus := events.New()
	registerListeners(bus, hub)
	router, h := NewRouterWithOptions(testPool, hub, bus, analytics.NoopClient{}, nil, RouterOptions{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	fixture := createRealAcceptanceFixture(t, ctx, c, server.URL)
	profilesRoot := t.TempDir()
	var procA, procB *realDaemonProcess
	t.Cleanup(func() {
		stopRealDaemon(procB)
		stopRealDaemon(procA)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanupRealAcceptanceFixture(t, cleanupCtx, fixture.workspaceID, fixture.userID)
	})

	writeRealProfile(t, profilesRoot, fixture.profileA, server.URL, fixture.workspaceID, fixture.workspacesA, fixture.token)
	writeRealProfile(t, profilesRoot, fixture.profileB, server.URL, fixture.workspaceID, fixture.workspacesB, fixture.token)
	procA = startRealDaemon(t, ctx, c.binary, profilesRoot, fixture.profileA, fixture.daemonA, c.providerA, c.pathEnvA, c.executableA, c.credentialEnvA, c.roleTimeout, fixture.token)
	procB = startRealDaemon(t, ctx, c.binary, profilesRoot, fixture.profileB, fixture.daemonB, c.providerB, c.pathEnvB, c.executableB, c.credentialEnvB, c.roleTimeout, fixture.token)

	runtimeA := waitRegisteredRuntime(t, ctx, fixture.workspaceID, fixture.daemonA, c.providerA, procA)
	runtimeB := waitRegisteredRuntime(t, ctx, fixture.workspaceID, fixture.daemonB, c.providerB, procB)
	runtimeReceiptA := assertRuntimeReceipt(t, ctx, runtimeA, fixture.userID, c.providerA, c.modelA)
	runtimeReceiptB := assertRuntimeReceipt(t, ctx, runtimeB, fixture.userID, c.providerB, c.modelB)
	fixture.createAgents(t, ctx, runtimeA, runtimeB, c)
	repository := orchestration.NewRepository(db.New(testPool), testPool)
	bindings := fixture.createRoleProfiles(t, ctx, repository, runtimeA, runtimeB, c)
	mission, planningRun := createRealMission(t, ctx, h.Orchestration, fixture, bindings, c)
	planning := reconcileRealRun(t, ctx, repository, fixture.workspaceID, planningRun)
	if planning.PlanProposalArtifact == nil {
		t.Fatalf("planner daemon completed without a PlanProposal artifact: run_status=%s failure_kind=%q failure_message=%q",
			planning.Run.Status, planning.Run.FailureKind.String,
			redactRealAcceptance(planning.Run.FailureMessage.String,
				realAcceptanceLogSecrets(fixture.token, append(append([]string(nil), c.credentialEnvA...), c.credentialEnvB...))...))
	}
	plannerRoute := assertRealRoute(t, ctx, planningRun, runtimeA, c.providerA, c.modelA, false)
	assertCanonicalRealProposal(t, planning.PlanProposalArtifact.Metadata, mission, c)
	var planningRevision int64
	if err := testPool.QueryRow(ctx, `SELECT revision FROM mission WHERE issue_id=$1`, mission).Scan(&planningRevision); err != nil {
		t.Fatal(err)
	}
	approved, err := h.Orchestration.ApprovePlanProposal(ctx, orchestration.SubmitPlanProposalCommand{WorkspaceID: fixture.workspaceID, MissionID: mission, ProposalArtifactID: planning.PlanProposalArtifact.ID, CommandID: parseRealUUID(uuid.NewString()), CorrelationID: parseRealUUID(uuid.NewString()), ActorID: fixture.userID, ExpectedRevision: planningRevision})
	if err != nil {
		t.Fatalf("approve real planner proposal: %v", err)
	}
	if _, err := h.Orchestration.StartMission(ctx, orchestration.StartMissionCommand{WorkspaceID: fixture.workspaceID, MissionID: mission, CommandID: parseRealUUID(uuid.NewString()), CorrelationID: parseRealUUID(uuid.NewString()), ActorID: fixture.userID, ExpectedRevision: approved.Mission.Revision, RolePolicyBindings: bindings[1:]}); err != nil {
		t.Fatalf("start real mission: %v", err)
	}
	rolePolicyHashes := assertRolePolicySnapshots(t, ctx, fixture.workspaceID, mission, c)

	// Every state transition is production ReconcileRun followed by
	// AdvanceMission. Terminal output is read from the daemon-owned task row;
	// this harness never writes task.result or records an artifact/verdict.
	executionReceipt := driveRealMission(t, ctx, repository, h.Orchestration, fixture, mission, runtimeA, runtimeB, procB, c)
	executionReceipt.Routes = append([]realRouteReceipt{plannerRoute}, executionReceipt.Routes...)
	assertRealRouteShape(t, executionReceipt.Routes, c)
	receipt := struct {
		SchemaVersion        int                         `json:"schema_version"`
		ServerVersion        string                      `json:"server_version"`
		RuntimeA             realRuntimeReceipt          `json:"runtime_a"`
		RuntimeB             realRuntimeReceipt          `json:"runtime_b"`
		RolePolicyHashes     map[string]string           `json:"role_policy_hashes"`
		MaxTokens            int64                       `json:"max_tokens"`
		MaxCostUSDTicks      int64                       `json:"max_cost_usd_ticks"`
		RoleTimeoutSeconds   int64                       `json:"role_timeout_seconds"`
		CredentialEnvNamesA  []string                    `json:"credential_env_names_a"`
		CredentialEnvNamesB  []string                    `json:"credential_env_names_b"`
		ProviderHomeIsolated bool                        `json:"provider_home_isolated"`
		Execution            realMissionExecutionReceipt `json:"execution"`
	}{
		SchemaVersion: 1, ServerVersion: normalizeServerVersion(version), RuntimeA: runtimeReceiptA, RuntimeB: runtimeReceiptB,
		RolePolicyHashes: rolePolicyHashes, MaxTokens: c.maxTokens, MaxCostUSDTicks: c.maxCost, RoleTimeoutSeconds: int64(c.roleTimeout.Seconds()),
		CredentialEnvNamesA: append([]string(nil), c.credentialEnvA...), CredentialEnvNamesB: append([]string(nil), c.credentialEnvB...), ProviderHomeIsolated: true, Execution: executionReceipt,
	}
	rawReceipt, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal real acceptance receipt: %v", err)
	}
	secretNames := append(append([]string(nil), c.credentialEnvA...), c.credentialEnvB...)
	t.Logf("REAL_ACCEPTANCE_RECEIPT %s", redactRealAcceptance(string(rawReceipt), realAcceptanceLogSecrets(fixture.token, secretNames)...))
}

type realAcceptanceFixture struct {
	workspaceID, userID                  pgtype.UUID
	daemonA, daemonB, profileA, profileB string
	workspacesA, workspacesB             string
	token                                string
	agentA, agentB, reviewerA, reviewerB pgtype.UUID
}

func createRealAcceptanceFixture(t *testing.T, ctx context.Context, c realAcceptanceConfig, serverURL string) realAcceptanceFixture {
	t.Helper()
	suffix := uuid.NewString()
	short := strings.ReplaceAll(suffix[:12], "-", "")
	f := realAcceptanceFixture{
		daemonA: "realacceptance-a-" + suffix, daemonB: "realacceptance-b-" + suffix,
		profileA: "realacceptance-a-" + short, profileB: "realacceptance-b-" + short,
		workspacesA: filepath.Join(t.TempDir(), "a"), workspacesB: filepath.Join(t.TempDir(), "b"),
	}
	email := "realacceptance-" + suffix + "@liexiu.invalid"
	if err := testPool.QueryRow(ctx, `INSERT INTO "user"(name,email) VALUES($1,$2) RETURNING id`, "Real Acceptance "+short, email).Scan(&f.userID); err != nil {
		t.Fatal(err)
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanupRealAcceptanceFixture(t, cleanupCtx, f.workspaceID, f.userID)
	}()
	token, err := generateTestJWT(uuid.UUID(f.userID.Bytes).String(), email, "Real Acceptance "+short)
	if err != nil {
		t.Fatalf("generate isolated real acceptance token: %v", err)
	}
	f.token = token
	if err := testPool.QueryRow(ctx, `INSERT INTO workspace(name,slug,description,issue_prefix) VALUES($1,$2,$3,'RAC') RETURNING id`, "real acceptance", "realacceptance-"+suffix, "isolated real acceptance fixture").Scan(&f.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO member(workspace_id,user_id,role) VALUES($1,$2,'owner')`, f.workspaceID, f.userID); err != nil {
		t.Fatal(err)
	}
	complete = true
	_ = c
	_ = serverURL
	return f
}

func writeRealProfile(t *testing.T, root, profile, serverURL string, workspace pgtype.UUID, workspaces, token string) {
	t.Helper()
	// Daemon startup is a human-local command and must not carry
	// LIEXIU_TASK_CONFIG_ROOT (a strong daemon-task identity signal). Put the
	// profile below this Runtime's isolated HOME using the normal CLI layout.
	dir := filepath.Join(realAcceptanceProviderEnvRoot(root, profile), "home", ".liexiu", "profiles", profile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// The token is written only to a 0600 task-local config. It is never an argv
	// value and this helper deliberately does not log the JSON or its path.
	b, err := json.Marshal(map[string]any{"server_url": serverURL, "workspace_id": uuid.UUID(workspace.Bytes).String(), "token": token, "device_name": "real-acceptance-" + profile, "runtime_name": "real-acceptance-" + profile, "workspaces_root": workspaces})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}

type realDaemonProcess struct {
	cmd                                                                  *exec.Cmd
	log                                                                  *boundedRealLog
	ctx                                                                  context.Context
	binary, root, profile, daemonID, provider, executableEnv, executable string
	credentialEnvNames                                                   []string
	agentTimeout                                                         time.Duration
	token                                                                string
	wait                                                                 *realDaemonWaitState
}
type realDaemonWaitState struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}
type boundedRealLog struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	secrets []string
}

func (w *boundedRealLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	const max = 64 << 10
	if len(p) > max {
		p = p[len(p)-max:]
	}
	w.buf.Write(p)
	if w.buf.Len() > max {
		b := w.buf.Bytes()
		w.buf.Reset()
		_, _ = w.buf.Write(b[len(b)-max:])
	}
	return len(p), nil
}
func (w *boundedRealLog) tail() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return redactRealAcceptance(w.buf.String(), w.secrets...)
}
func redactRealAcceptance(s string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	s = redact.Text(s)
	s = regexp.MustCompile(`\b(?:mat|mdt|mul|mcn)_[0-9A-Fa-f]{40}\b`).ReplaceAllString(s, "[REDACTED_TOKEN]")
	s = regexp.MustCompile(`https?://[^\s]+`).ReplaceAllString(s, "[REDACTED_URL]")
	return s
}

func startRealDaemon(t *testing.T, ctx context.Context, binary, root, profile, daemonID, provider, executableEnv, executable string, credentialEnvNames []string, agentTimeout time.Duration, token string) *realDaemonProcess {
	t.Helper()
	// Lifecycle is owned by stopRealDaemon. Using CommandContext here would
	// SIGKILL only the daemon leader when the Mission timeout fires, racing the
	// daemon's graceful shutdown and its provider process-tree controller.
	cmd := exec.Command(binary, "--profile", profile, "daemon", "start", "--foreground", "--daemon-id", daemonID, "--no-auto-update", "--no-auto-reload", "--poll-interval", "250ms", "--heartbeat-interval", "1s", "--max-concurrent-tasks", "1", "--agent-timeout", agentTimeout.String())
	log := &boundedRealLog{secrets: realAcceptanceLogSecrets(token, credentialEnvNames)}
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = realAcceptanceDaemonEnv(t, root, profile, provider, executableEnv, executable, credentialEnvNames)
	cmd.Env = append(cmd.Env, "LIEXIU_DAEMON_ID="+daemonID)
	cmd.Dir = realAcceptanceDaemonCWD(t, root, profile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon %s: %v\n%s", profile, err, log.tail())
	}
	wait := &realDaemonWaitState{done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		wait.mu.Lock()
		wait.err = err
		wait.mu.Unlock()
		close(wait.done)
	}()
	return &realDaemonProcess{cmd: cmd, log: log, ctx: ctx, binary: binary, root: root, profile: profile, daemonID: daemonID, provider: provider, executableEnv: executableEnv, executable: executable, credentialEnvNames: append([]string(nil), credentialEnvNames...), agentTimeout: agentTimeout, token: token, wait: wait}
}

func realAcceptanceDaemonEnv(t *testing.T, root, profile, provider, executableEnv, executable string, credentialEnvNames []string) []string {
	t.Helper()
	envRoot := realAcceptanceProviderEnvRoot(root, profile)
	home := filepath.Join(envRoot, "home")
	xdgConfig := filepath.Join(envRoot, "xdg-config")
	xdgData := filepath.Join(envRoot, "xdg-data")
	xdgCache := filepath.Join(envRoot, "xdg-cache")
	xdgState := filepath.Join(envRoot, "xdg-state")
	for _, dir := range []string{home, xdgConfig, xdgData, xdgCache, xdgState} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create isolated provider environment: %v", err)
		}
	}
	env := withoutRealAcceptanceSecrets(os.Environ(), executableEnv, credentialEnvNames)
	env = append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+xdgConfig,
		"XDG_DATA_HOME="+xdgData,
		"XDG_CACHE_HOME="+xdgCache,
		"XDG_STATE_HOME="+xdgState,
		executableEnv+"="+executable,
	)
	if strings.EqualFold(strings.TrimSpace(provider), "opencode") {
		// The acceptance profile requires empty tool/path permissions. Do not
		// inherit a host OpenCode permission/config override; install one
		// harness-owned, secret-free deny policy in the isolated provider home.
		env = append(env, `OPENCODE_CONFIG_CONTENT={"permission":{"*":"deny"}}`)
	}
	if strings.EqualFold(strings.TrimSpace(provider), "codex") {
		sharedCodexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
		if sharedCodexHome == "" {
			userHome, err := os.UserHomeDir()
			if err != nil {
				t.Fatalf("resolve shared Codex home: %v", err)
			}
			sharedCodexHome = filepath.Join(userHome, ".codex")
		}
		if !filepath.IsAbs(sharedCodexHome) {
			t.Fatal("shared CODEX_HOME must be absolute for real acceptance")
		}
		if info, err := os.Stat(filepath.Join(sharedCodexHome, "auth.json")); err != nil || !info.Mode().IsRegular() {
			t.Fatal("real acceptance Codex runtime requires a regular shared CODEX_HOME/auth.json")
		}
		env = append(env, "CODEX_HOME="+sharedCodexHome)
	}
	return env
}

func realAcceptanceProviderEnvRoot(root, profile string) string {
	return filepath.Join(root, "provider-env", profile)
}

func realAcceptanceDaemonCWD(t *testing.T, root, profile string) string {
	t.Helper()
	cwd := filepath.Join(realAcceptanceProviderEnvRoot(root, profile), "cwd")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatalf("create isolated daemon working directory: %v", err)
	}
	return cwd
}

func withoutRealAcceptanceSecrets(env []string, executableEnv string, credentialEnvNames []string) []string {
	allowedCredentials := make(map[string]struct{}, len(credentialEnvNames))
	for _, name := range credentialEnvNames {
		allowedCredentials[name] = struct{}{}
	}
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		_, credentialAllowed := allowedCredentials[name]
		// A real-acceptance daemon is a new human-owned daemon process, not a
		// child AgentTask. Strip every ambient LieXiu control/identity value and
		// inject only the exact daemon settings owned by this harness below.
		if strings.HasPrefix(name, "LIEXIU_") {
			continue
		}
		if isProtectedRealAcceptanceEnv(name) || isRealAcceptanceProviderPathEnv(name) || isRealAcceptanceProviderHomeEnv(name) || isRealAcceptanceProviderControlEnv(name, credentialAllowed) || (isRealAcceptanceCredentialEnv(name) && !credentialAllowed) {
			continue
		}
		if name == executableEnv {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func isRealAcceptanceProviderHomeEnv(name string) bool {
	switch name {
	case "HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "CODEX_HOME":
		return true
	default:
		return false
	}
}

func isRealAcceptanceProviderControlEnv(name string, credentialAllowed bool) bool {
	if !strings.HasPrefix(name, "OPENCODE_") {
		return false
	}
	// OPENCODE_AUTH_CONTENT is the sole provider-specific credential channel
	// used by this acceptance profile. It survives only when the matching
	// Runtime allowlist names it; every host config/permission/path override is
	// removed before the harness injects its own deny policy.
	return name != "OPENCODE_AUTH_CONTENT" || !credentialAllowed
}

func realAcceptanceLogSecrets(token string, credentialEnvNames []string) []string {
	secrets := []string{token}
	for _, name := range credentialEnvNames {
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		secrets = append(secrets, value)
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) == nil {
			collectRealAcceptanceSecretStrings(decoded, &secrets)
		}
	}
	return secrets
}

func collectRealAcceptanceSecretStrings(value any, secrets *[]string) {
	switch typed := value.(type) {
	case string:
		if len(typed) >= 8 {
			*secrets = append(*secrets, typed)
		}
	case []any:
		for _, entry := range typed {
			collectRealAcceptanceSecretStrings(entry, secrets)
		}
	case map[string]any:
		for _, entry := range typed {
			collectRealAcceptanceSecretStrings(entry, secrets)
		}
	}
}

func restartRealDaemon(t *testing.T, proc *realDaemonProcess) {
	t.Helper()
	stopRealDaemon(proc)
	next := startRealDaemon(t, proc.ctx, proc.binary, proc.root, proc.profile, proc.daemonID, proc.provider, proc.executableEnv, proc.executable, proc.credentialEnvNames, proc.agentTimeout, proc.token)
	proc.cmd = next.cmd
	proc.log = next.log
	proc.ctx = next.ctx
	proc.binary = next.binary
	proc.root = next.root
	proc.profile = next.profile
	proc.daemonID = next.daemonID
	proc.provider = next.provider
	proc.executableEnv = next.executableEnv
	proc.executable = next.executable
	proc.credentialEnvNames = next.credentialEnvNames
	proc.agentTimeout = next.agentTimeout
	proc.token = next.token
	proc.wait = next.wait
}

func stopRealDaemon(proc *realDaemonProcess) {
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil || proc.wait == nil {
		return
	}
	select {
	case <-proc.wait.done:
		return
	default:
		_ = proc.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-proc.wait.done:
	case <-time.After(10 * time.Second):
		_ = proc.cmd.Process.Kill()
		<-proc.wait.done
	}
	if err := proc.waitError(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		// The caller decides whether a stopped daemon is expected; only a bounded
		// redacted tail is retained for diagnosis.
		_ = proc.log.tail()
	}
}

func (proc *realDaemonProcess) waitError() error {
	if proc == nil || proc.wait == nil {
		return nil
	}
	proc.wait.mu.Lock()
	defer proc.wait.mu.Unlock()
	return proc.wait.err
}

func waitRegisteredRuntime(t *testing.T, ctx context.Context, workspace pgtype.UUID, daemonID, provider string, proc *realDaemonProcess) pgtype.UUID {
	t.Helper()
	var id string
	for {
		select {
		case <-proc.wait.done:
			t.Fatalf("daemon %s/%s exited before Runtime registration: %v\n%s", daemonID, provider, proc.waitError(), proc.log.tail())
		default:
		}
		err := testPool.QueryRow(ctx, `SELECT id::text FROM agent_runtime WHERE workspace_id=$1 AND daemon_id=$2 AND provider=$3 AND status='online' ORDER BY last_seen_at DESC LIMIT 1`, workspace, daemonID, provider).Scan(&id)
		if err == nil {
			return parseRealUUID(id)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("runtime %s/%s did not register: %v", daemonID, provider, err)
		}
		if ctx.Err() != nil {
			t.Fatalf("runtime %s/%s did not register before timeout\n%s", daemonID, provider, proc.log.tail())
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func parseRealUUID(s string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(s), Valid: true} }

func (f *realAcceptanceFixture) createAgents(t *testing.T, ctx context.Context, runtimeA, runtimeB pgtype.UUID, c realAcceptanceConfig) {
	for i, v := range []struct {
		name    string
		runtime pgtype.UUID
	}{{"A producer", runtimeA}, {"B producer", runtimeB}, {"A reviewer", runtimeA}, {"B reviewer", runtimeB}} {
		var id pgtype.UUID
		model := c.modelA
		if v.runtime == runtimeB {
			model = c.modelB
		}
		if err := testPool.QueryRow(ctx, `INSERT INTO agent(workspace_id,name,description,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks,owner_id,model) VALUES($1,$2,'real acceptance','local','{}',$3,'workspace',1,$4,$5) RETURNING id`, f.workspaceID, v.name, v.runtime, f.userID, model).Scan(&id); err != nil {
			t.Fatal(err)
		}
		switch i {
		case 0:
			f.agentA = id
		case 1:
			f.agentB = id
		case 2:
			f.reviewerA = id
		case 3:
			f.reviewerB = id
		}
	}
}

func (f realAcceptanceFixture) createRoleProfiles(t *testing.T, ctx context.Context, repo *orchestration.Repository, runtimeA, runtimeB pgtype.UUID, c realAcceptanceConfig) []orchestration.RolePolicyBinding {
	t.Helper()
	bindings := make([]orchestration.RolePolicyBinding, 0, 4)
	for _, duty := range []orchestration.Duty{orchestration.DutyPlanner, orchestration.DutyExecutor, orchestration.DutyReviewer, orchestration.DutyIntegrator} {
		allowed := []string{uuid.UUID(runtimeA.Bytes).String()}
		preferred := allowed
		providers := []string{c.providerA}
		models := []string{c.modelA}
		if duty == orchestration.DutyExecutor || duty == orchestration.DutyReviewer {
			allowed = []string{uuid.UUID(runtimeB.Bytes).String(), uuid.UUID(runtimeA.Bytes).String()}
			preferred = []string{uuid.UUID(runtimeB.Bytes).String()}
			providers = []string{c.providerB, c.providerA}
			models = []string{c.modelB, c.modelA}
		}
		maxTokens, maxCost := c.maxTokens, c.maxCost
		instructions := "Use only the requested terminal protocol. Do not call any tool or CLI, read files, inspect the issue, or access the network; answer solely from the frozen input, frozen role instructions, and result contract. Capability: " + realAcceptanceCapability
		if duty == orchestration.DutyPlanner {
			instructions += ` Return canonical JSON with exactly nodes A, B, C in that order. A is executor/commit with no dependency and budget_estimate tokens=1,cost_usd_ticks=1. B is executor/commit depending only on A with the same estimate. C is integrator/final_delivery depending only on B with the same estimate. Preserve the exact frozen input and limits.`
		}
		if duty == orchestration.DutyReviewer {
			instructions += ` Approve a schema-valid acceptance artifact of the required kind. Return decision approved with no requested changes; do not invent extra delivery requirements.`
		}
		if duty == orchestration.DutyExecutor || duty == orchestration.DutyIntegrator {
			instructions += ` Return the required Artifact JSON only. Set artifact.uri to the non-empty provider-neutral value urn:liexiu:realacceptance:<node_key>:<required-kind>, deriving node_key and the required kind from the frozen input and result contract. Set content_hash and summary to non-empty deterministic strings and metadata to an object; never emit an empty URI or additional fields.`
		}
		profile, err := repo.CreateRoleProfileVersion(ctx, orchestration.CreateRoleProfileVersionParams{
			WorkspaceID: f.workspaceID, CommandID: parseRealUUID(uuid.NewString()), ActorID: f.userID,
			ProfileKey: "realacceptance-" + duty.String(), Duty: duty, Name: "Real acceptance " + duty.String(),
			Config: orchestration.RoleProfileConfig{
				Instructions:         instructions,
				RequiredCapabilities: []string{realAcceptanceCapability},
				Runtime:              orchestration.RoleRuntimePreferences{AllowedRuntimeIDs: allowed, PreferredRuntimeIDs: preferred, Providers: providers, Models: models},
				Tools:                orchestration.RoleToolPermissions{AllowedTools: []string{}, AllowedPaths: []string{}},
				Budget:               orchestration.RoleBudgetLimits{MaxTokens: &maxTokens, MaxCostUSDTicks: &maxCost, MaxReworkCycles: 1, MaxTechnicalRetries: 1}, TimeoutSeconds: int(c.roleTimeout.Seconds()), MaxConcurrency: 1,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		var agent pgtype.UUID
		if duty == orchestration.DutyPlanner || duty == orchestration.DutyIntegrator {
			agent = f.agentA
		}
		bindings = append(bindings, orchestration.RolePolicyBinding{Duty: duty, ProfileKey: profile.Profile.ProfileKey, Version: profile.Profile.Version, AgentID: agent})
	}
	return bindings
}
func createRealMission(t *testing.T, ctx context.Context, s *orchestration.Service, f realAcceptanceFixture, bindings []orchestration.RolePolicyBinding, c realAcceptanceConfig) (pgtype.UUID, pgtype.UUID) {
	limits := realPlanLimits(c)
	created, err := s.CreateMission(ctx, orchestration.CreateMissionCommand{WorkspaceID: f.workspaceID, CommandID: parseRealUUID(uuid.NewString()), CorrelationID: parseRealUUID(uuid.NewString()), ActorID: f.userID, Title: "real acceptance A-B-C", Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	input := realPlanInput()
	planning, err := s.RequestPlan(ctx, orchestration.RequestPlanCommand{WorkspaceID: f.workspaceID, MissionID: created.Mission.IssueID, CommandID: parseRealUUID(uuid.NewString()), CorrelationID: parseRealUUID(uuid.NewString()), ActorID: f.userID, ExpectedRevision: created.Mission.Revision, RolePolicyBinding: bindings[0], Input: input})
	if err != nil {
		t.Fatalf("request real planner run: %v", err)
	}
	return created.Mission.IssueID, planning.Run.ID
}

func realPlanLimits(c realAcceptanceConfig) orchestration.PlanLimits {
	return orchestration.PlanLimits{MaxParallelRuns: 2, MaxTaskAttempts: 2, MaxReworkCycles: 1, Budget: &orchestration.BudgetPolicy{MaxTokens: &c.maxTokens, MaxCostUSDTicks: &c.maxCost, Gate: orchestration.BudgetGateFailClosed}}
}
func realPlanInput() orchestration.PlanProposalInput {
	return orchestration.PlanProposalInput{Objective: "Return a canonical exact linear plan A→B→C: A is an executor commit with no dependencies; B is an executor commit depending on A; C is an integrator final_delivery depending on B. Every node must use budget_estimate tokens=1 and cost_usd_ticks=1.", DeliveryCriteria: []string{"exactly three nodes A, B, C", "A executor commit has no dependencies", "B executor commit depends only on A", "C integrator final_delivery depends only on B", "each node has positive budget_estimate tokens=1 and cost_usd_ticks=1"}}
}

func assertCanonicalRealProposal(t *testing.T, metadata []byte, mission pgtype.UUID, c realAcceptanceConfig) {
	t.Helper()
	proposal, err := orchestration.DecodePlanProposal(metadata)
	if err != nil {
		t.Fatalf("decode planner proposal: %v", err)
	}
	if proposal.SchemaVersion != orchestration.PlanProposalSchemaVersion || proposal.MissionID != uuid.UUID(mission.Bytes).String() || proposal.ProposalKey == "" {
		t.Fatalf("planner proposal identity is not canonical: %#v", proposal)
	}
	if !reflect.DeepEqual(proposal.Input, realPlanInput()) || !reflect.DeepEqual(proposal.Limits, realPlanLimits(c)) {
		t.Fatalf("planner proposal input/limits drifted: %#v", proposal)
	}
	want := []struct {
		key       string
		duty      orchestration.Duty
		artifacts []orchestration.ArtifactKind
		dependsOn []string
	}{
		{key: "A", duty: orchestration.DutyExecutor, artifacts: []orchestration.ArtifactKind{orchestration.ArtifactKindCommit}, dependsOn: []string{}},
		{key: "B", duty: orchestration.DutyExecutor, artifacts: []orchestration.ArtifactKind{orchestration.ArtifactKindCommit}, dependsOn: []string{"A"}},
		{key: "C", duty: orchestration.DutyIntegrator, artifacts: []orchestration.ArtifactKind{orchestration.ArtifactKindFinalDelivery}, dependsOn: []string{"B"}},
	}
	if len(proposal.Nodes) != len(want) {
		t.Fatalf("planner proposal nodes are not the required exact A→B→C contract: %#v", proposal.Nodes)
	}
	var estimatedTokens, estimatedCost int64
	for i, expected := range want {
		node := proposal.Nodes[i]
		if node.Key != expected.key || node.Duty != expected.duty || !reflect.DeepEqual(node.ArtifactKinds, expected.artifacts) || !reflect.DeepEqual(node.DependsOn, expected.dependsOn) || strings.TrimSpace(node.Title) == "" || strings.TrimSpace(node.Description) == "" || len(node.AcceptanceCriteria) == 0 || node.BudgetEstimate.Tokens <= 0 || node.BudgetEstimate.CostUSDTicks <= 0 {
			t.Fatalf("planner proposal node[%d] is not the required A→B→C semantic contract: %#v", i, node)
		}
		estimatedTokens += node.BudgetEstimate.Tokens
		estimatedCost += node.BudgetEstimate.CostUSDTicks
	}
	if estimatedTokens > c.maxTokens || estimatedCost > c.maxCost {
		t.Fatalf("planner proposal estimates exceed the frozen Mission budget: tokens=%d cost_ticks=%d", estimatedTokens, estimatedCost)
	}
}

func cleanupRealAcceptanceFixture(t *testing.T, ctx context.Context, workspaceID, userID pgtype.UUID) {
	t.Helper()
	if workspaceID.Valid {
		for _, statement := range []string{
			`DELETE FROM task_token WHERE workspace_id=$1`,
			`DELETE FROM agent_task_queue WHERE agent_id IN (SELECT id FROM agent WHERE workspace_id=$1)`,
			`DELETE FROM orchestration_human_gate WHERE workspace_id=$1`, `DELETE FROM mission_role_policy_snapshot WHERE workspace_id=$1`, `DELETE FROM role_profile WHERE workspace_id=$1`,
			`DELETE FROM review_verdict WHERE workspace_id=$1`, `DELETE FROM artifact WHERE workspace_id=$1`, `DELETE FROM orchestration_run WHERE workspace_id=$1`,
			`DELETE FROM orchestration_assignment WHERE workspace_id=$1`, `DELETE FROM orchestration_activity WHERE workspace_id=$1`, `DELETE FROM task_node WHERE workspace_id=$1`,
			`DELETE FROM mission WHERE workspace_id=$1`, `DELETE FROM issue WHERE workspace_id=$1`, `DELETE FROM agent WHERE workspace_id=$1`,
			`DELETE FROM agent_runtime WHERE workspace_id=$1`, `DELETE FROM member WHERE workspace_id=$1`, `DELETE FROM workspace WHERE id=$1`,
		} {
			if _, err := testPool.Exec(ctx, statement, workspaceID); err != nil {
				t.Errorf("cleanup failed for fixture workspace statement %q: %v", statement, err)
			}
		}
	}
	if userID.Valid {
		if _, err := testPool.Exec(ctx, `DELETE FROM "user" WHERE id=$1`, userID); err != nil {
			t.Errorf("cleanup failed for fixture user: %v", err)
		}
	}
}
func reconcileRealRun(t *testing.T, ctx context.Context, repo *orchestration.Repository, workspace, runID pgtype.UUID) orchestration.ReconcileRunResult {
	t.Helper()
	for {
		var status string
		err := testPool.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE orchestration_run_id=$1 ORDER BY created_at DESC LIMIT 1`, runID).Scan(&status)
		if err == nil && (status == "completed" || status == "failed" || status == "cancelled") {
			result, reconcileErr := repo.ReconcileRun(ctx, orchestration.ReconcileRunParams{WorkspaceID: workspace, RunID: runID, ObservedAt: time.Now().UTC()})
			if reconcileErr != nil {
				t.Fatalf("reconcile real run: %v", reconcileErr)
			}
			return result
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("read real run %s task status: %v", uuid.UUID(runID.Bytes), err)
		}
		if ctx.Err() != nil {
			t.Fatalf("real run %s did not reach terminal state", uuid.UUID(runID.Bytes))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func driveRealMission(t *testing.T, ctx context.Context, repo *orchestration.Repository, s *orchestration.Service, f realAcceptanceFixture, mission, runtimeA, runtimeB pgtype.UUID, procB *realDaemonProcess, c realAcceptanceConfig) realMissionExecutionReceipt {
	// The routing assertions intentionally use the persisted Assignment and
	// Runtime rows, never names or PATH discovery.
	routes := make([]realRouteReceipt, 0, 6)
	aWork := advanceOneRealRun(t, ctx, s, f.workspaceID, mission, "A", "execute")
	routes = append(routes, assertRealRoute(t, ctx, aWork.ID, runtimeB, c.providerB, c.modelB, false))
	aWorkResult := reconcileRealRun(t, ctx, repo, f.workspaceID, aWork.ID)
	aArtifact := requireRealArtifact(t, aWorkResult, mission, aWork.ID, orchestration.ArtifactKindCommit)
	aReview := advanceOneRealRun(t, ctx, s, f.workspaceID, mission, "A", "review")
	routes = append(routes, assertRealRoute(t, ctx, aReview.ID, runtimeB, c.providerB, c.modelB, true))
	assertDifferentAssignmentAgents(t, ctx, aWork.ID, aReview.ID)
	requireApprovedRealReview(t, reconcileRealRun(t, ctx, repo, f.workspaceID, aReview.ID), mission, aReview.ID, aArtifact.ID)

	stopRealDaemon(procB)
	if err := procB.waitError(); err != nil {
		t.Fatalf("daemon B did not stop cleanly: %v\n%s", err, procB.log.tail())
	}
	waitRuntimeOffline(t, ctx, f.workspaceID, runtimeB)
	bWork := advanceOneRealRun(t, ctx, s, f.workspaceID, mission, "B", "execute")
	routes = append(routes, assertRealRoute(t, ctx, bWork.ID, runtimeA, c.providerA, c.modelA, false))
	bWorkResult := reconcileRealRun(t, ctx, repo, f.workspaceID, bWork.ID)
	bArtifact := requireRealArtifact(t, bWorkResult, mission, bWork.ID, orchestration.ArtifactKindCommit)
	bReview := advanceOneRealRun(t, ctx, s, f.workspaceID, mission, "B", "review")
	routes = append(routes, assertRealRoute(t, ctx, bReview.ID, runtimeA, c.providerA, c.modelA, true))
	assertDifferentAssignmentAgents(t, ctx, bWork.ID, bReview.ID)
	requireApprovedRealReview(t, reconcileRealRun(t, ctx, repo, f.workspaceID, bReview.ID), mission, bReview.ID, bArtifact.ID)

	restartRealDaemon(t, procB)
	runtimeBAgain := waitRegisteredRuntime(t, ctx, f.workspaceID, procB.daemonID, c.providerB, procB)
	if runtimeBAgain != runtimeB {
		t.Fatalf("B restart rotated runtime UUID: before=%s after=%s", uuid.UUID(runtimeB.Bytes), uuid.UUID(runtimeBAgain.Bytes))
	}
	assertRuntimeReceipt(t, ctx, runtimeBAgain, f.userID, c.providerB, c.modelB)
	cWork := advanceOneRealRun(t, ctx, s, f.workspaceID, mission, "C", "integrate")
	routes = append(routes, assertRealRoute(t, ctx, cWork.ID, runtimeA, c.providerA, c.modelA, false))
	cWorkResult := reconcileRealRun(t, ctx, repo, f.workspaceID, cWork.ID)
	finalArtifact := requireRealArtifact(t, cWorkResult, mission, cWork.ID, orchestration.ArtifactKindFinalDelivery)
	if !finalArtifact.ContentHash.Valid || strings.TrimSpace(finalArtifact.ContentHash.String) == "" {
		t.Fatal("final delivery artifact is missing its content hash")
	}
	cReview := advanceOneRealRun(t, ctx, s, f.workspaceID, mission, "C", "review")
	routes = append(routes, assertRealRoute(t, ctx, cReview.ID, runtimeB, c.providerB, c.modelB, true))
	assertDifferentAssignmentAgents(t, ctx, cWork.ID, cReview.ID)
	requireApprovedRealReview(t, reconcileRealRun(t, ctx, repo, f.workspaceID, cReview.ID), mission, cReview.ID, finalArtifact.ID)
	if _, err := s.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: f.workspaceID, MissionID: mission, CorrelationID: parseRealUUID(uuid.NewString())}); err != nil {
		t.Fatalf("terminal advance: %v", err)
	}
	projection, err := s.GetMissionProjection(ctx, f.workspaceID, mission)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Mission.Status != orchestration.MissionStatusCompleted || projection.Mission.Progress.Percent != 100 || len(projection.HumanGates) != 0 {
		t.Fatalf("final projection violates acceptance: %#v", projection.Mission)
	}
	if projection.Mission.Progress.Completed != 3 {
		t.Fatalf("final projection completed nodes=%d, want 3", projection.Mission.Progress.Completed)
	}
	usage := readRealUsageReceipt(t, ctx, mission)
	// Some authenticated providers (including account-backed Codex) report
	// tokens without an authoritative USD price. That is an explicit receipt
	// state, not zero-cost evidence: the production budget path reserves the
	// frozen estimate when cost is absent, and this receipt exposes the exact
	// uncosted token count for audit instead of pretending it was priced.
	if usage.TaskCount != 7 || usage.UsageReportedTaskCount <= 0 || usage.UsageReportedTaskCount > usage.TaskCount || usage.TotalTokens <= 0 || usage.TotalTokens > c.maxTokens || usage.ReportedCostUSDTicks < 0 || usage.ReportedCostUSDTicks > c.maxCost || usage.UncostedTokens < 0 || usage.UncostedTokens > usage.TotalTokens {
		t.Fatalf("real acceptance usage violates receipt contract: tasks=%d usage_reported_tasks=%d tokens=%d reported_cost_ticks=%d", usage.TaskCount, usage.UsageReportedTaskCount, usage.TotalTokens, usage.ReportedCostUSDTicks)
	}
	budget := projection.Mission.Budget
	if budget.ReservedTokens != 0 || budget.ReservedCostUSDTicks != 0 || budget.ConsumedTokens <= 0 || budget.ConsumedTokens > c.maxTokens || budget.ConsumedCostUSDTicks < 0 || budget.ConsumedCostUSDTicks > c.maxCost {
		t.Fatalf("final Mission budget projection violates receipt contract: status=%s consumed_tokens=%d reserved_tokens=%d consumed_cost_ticks=%d reserved_cost_ticks=%d", budget.Status, budget.ConsumedTokens, budget.ReservedTokens, budget.ConsumedCostUSDTicks, budget.ReservedCostUSDTicks)
	}
	return realMissionExecutionReceipt{
		Routes: routes, RuntimeBReused: true, ReviewersSeparated: true,
		FinalArtifactKind: finalArtifact.Kind, FinalArtifactHashPresent: true,
		FinalStatus: projection.Mission.Status.String(), FinalProgressPercent: projection.Mission.Progress.Percent, Usage: usage,
		Budget: realBudgetReceipt{Status: budget.Status, ConsumedTokens: budget.ConsumedTokens, ReservedTokens: budget.ReservedTokens, ConsumedCostUSDTicks: budget.ConsumedCostUSDTicks, ReservedCostUSDTicks: budget.ReservedCostUSDTicks},
	}
}

func assertRealRouteShape(t *testing.T, routes []realRouteReceipt, c realAcceptanceConfig) {
	t.Helper()
	want := []realRouteReceipt{
		{Node: "mission", Purpose: "plan", Provider: c.providerA, Model: c.modelA},
		{Node: "A", Purpose: "execute", Provider: c.providerB, Model: c.modelB},
		{Node: "A", Purpose: "review", Provider: c.providerB, Model: c.modelB},
		{Node: "B", Purpose: "execute", Provider: c.providerA, Model: c.modelA},
		{Node: "B", Purpose: "review", Provider: c.providerA, Model: c.modelA},
		{Node: "C", Purpose: "integrate", Provider: c.providerA, Model: c.modelA},
		{Node: "C", Purpose: "review", Provider: c.providerB, Model: c.modelB},
	}
	if len(routes) != len(want) {
		t.Fatalf("real acceptance route count=%d, want %d", len(routes), len(want))
	}
	for i := range want {
		if routes[i].Node != want[i].Node || routes[i].Purpose != want[i].Purpose || routes[i].Provider != want[i].Provider || routes[i].Model != want[i].Model || len(routes[i].Lineage) != sha256.Size*2 {
			t.Fatalf("real acceptance route[%d] violates the seven-segment contract: %#v", i, routes[i])
		}
	}
}

func readRealUsageReceipt(t *testing.T, ctx context.Context, mission pgtype.UUID) realUsageReceipt {
	t.Helper()
	var receipt realUsageReceipt
	err := testPool.QueryRow(ctx, `
		SELECT
			COUNT(DISTINCT q.id)::bigint,
			COUNT(DISTINCT u.task_id)::bigint,
			COALESCE(SUM(COALESCE(u.input_tokens,0)+COALESCE(u.output_tokens,0)+COALESCE(u.cache_read_tokens,0)+COALESCE(u.cache_write_tokens,0)),0)::bigint,
			COALESCE(SUM(u.cost_usd_ticks) FILTER (WHERE u.cost_usd_ticks IS NOT NULL),0)::bigint,
			COALESCE(SUM(CASE WHEN u.cost_usd_ticks IS NULL THEN COALESCE(u.input_tokens,0)+COALESCE(u.output_tokens,0)+COALESCE(u.cache_read_tokens,0)+COALESCE(u.cache_write_tokens,0) ELSE 0 END),0)::bigint
		FROM orchestration_run r
		JOIN agent_task_queue q ON q.orchestration_run_id=r.id
		LEFT JOIN task_usage u ON u.task_id=q.id
		WHERE r.mission_id=$1`, mission).Scan(&receipt.TaskCount, &receipt.UsageReportedTaskCount, &receipt.TotalTokens, &receipt.ReportedCostUSDTicks, &receipt.UncostedTokens)
	if err != nil {
		t.Fatalf("read real acceptance usage receipt: %v", err)
	}
	return receipt
}

func advanceOneRealRun(t *testing.T, ctx context.Context, s *orchestration.Service, workspace, mission pgtype.UUID, node, purpose string) db.OrchestrationRun {
	t.Helper()
	result, err := s.AdvanceMission(ctx, orchestration.AdvanceMissionCommand{WorkspaceID: workspace, MissionID: mission, CorrelationID: parseRealUUID(uuid.NewString())})
	if err != nil {
		t.Fatalf("advance %s/%s: %v", node, purpose, err)
	}
	if len(result.CreatedRuns) != 1 {
		states := make([]string, 0, len(result.TaskNodes))
		for _, taskNode := range result.TaskNodes {
			states = append(states, taskNode.NodeKey+":"+taskNode.Status)
		}
		usage := readRealUsageReceipt(t, ctx, mission)
		t.Fatalf("advance %s/%s created %d runs: mission_status=%s budget_gate=%s task_states=%v usage_tasks=%d usage_reported_tasks=%d usage_tokens=%d reported_cost_ticks=%d",
			node, purpose, len(result.CreatedRuns), result.Mission.Status, result.Mission.BudgetGateStatus, states, usage.TaskCount, usage.UsageReportedTaskCount, usage.TotalTokens, usage.ReportedCostUSDTicks)
	}
	run := result.CreatedRuns[0]
	var gotNode, gotPurpose string
	if err := testPool.QueryRow(ctx, `SELECT n.node_key,r.purpose FROM orchestration_run r JOIN task_node n ON n.issue_id=r.task_node_id WHERE r.id=$1`, run.ID).Scan(&gotNode, &gotPurpose); err != nil {
		t.Fatal(err)
	}
	if gotNode != node || gotPurpose != purpose {
		t.Fatalf("expected %s/%s, got %s/%s", node, purpose, gotNode, gotPurpose)
	}
	return run
}
func assertRealRoute(t *testing.T, ctx context.Context, runID, runtime pgtype.UUID, provider, model string, review bool) realRouteReceipt {
	var gotRuntime, gotProvider, gotModel string
	var node, purpose, assignmentID string
	if err := testPool.QueryRow(ctx, `SELECT COALESCE(n.node_key,'mission'),r.purpose,a.id::text,a.runtime_id::text,rt.provider,ag.model FROM orchestration_run r JOIN orchestration_assignment a ON a.id=r.assignment_id JOIN agent_runtime rt ON rt.id=a.runtime_id JOIN agent ag ON ag.id=a.agent_id LEFT JOIN task_node n ON n.issue_id=r.task_node_id WHERE r.id=$1`, runID).Scan(&node, &purpose, &assignmentID, &gotRuntime, &gotProvider, &gotModel); err != nil {
		t.Fatal(err)
	}
	if gotRuntime != uuid.UUID(runtime.Bytes).String() || gotProvider != provider || gotModel != model {
		t.Fatalf("route mismatch review=%t runtime=%s provider=%s model=%s", review, gotRuntime, gotProvider, gotModel)
	}
	lineage := sha256.Sum256([]byte(uuid.UUID(runID.Bytes).String() + "\x00" + assignmentID + "\x00" + gotRuntime + "\x00" + node + "\x00" + purpose))
	return realRouteReceipt{Node: node, Purpose: purpose, Provider: gotProvider, Model: gotModel, Lineage: fmt.Sprintf("%x", lineage)}
}
func assertDifferentAssignmentAgents(t *testing.T, ctx context.Context, producer, reviewer pgtype.UUID) {
	var a, b string
	if err := testPool.QueryRow(ctx, `SELECT pa.agent_id::text,ra.agent_id::text FROM orchestration_run p JOIN orchestration_run r ON r.mission_id=p.mission_id AND r.task_node_id=p.task_node_id JOIN orchestration_assignment pa ON pa.id=p.assignment_id JOIN orchestration_assignment ra ON ra.id=r.assignment_id WHERE p.id=$1 AND r.id=$2`, producer, reviewer).Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("reviewer reused producer agent %s", a)
	}
}
func assertRuntimeReceipt(t *testing.T, ctx context.Context, runtime, owner pgtype.UUID, provider, model string) realRuntimeReceipt {
	var ownerID string
	var visibility string
	var metadata []byte
	if err := testPool.QueryRow(ctx, `SELECT owner_id::text,visibility,metadata FROM agent_runtime WHERE id=$1`, runtime).Scan(&ownerID, &visibility, &metadata); err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Version      string   `json:"version"`
		CLIVersion   string   `json:"cli_version"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(metadata, &receipt); err != nil {
		t.Fatalf("decode runtime receipt for %s: %v", uuid.UUID(runtime.Bytes), err)
	}
	if ownerID != uuid.UUID(owner.Bytes).String() || visibility != "private" || strings.TrimSpace(receipt.Version) == "" || strings.TrimSpace(receipt.CLIVersion) == "" || !slicesContainReal(receipt.Capabilities, realAcceptanceCapability) {
		t.Fatalf("runtime receipt incomplete for %s: visibility=%q version_present=%t cli_version_present=%t capability_present=%t", uuid.UUID(runtime.Bytes), visibility, receipt.Version != "", receipt.CLIVersion != "", slicesContainReal(receipt.Capabilities, realAcceptanceCapability))
	}
	return realRuntimeReceipt{Provider: provider, Model: model, DaemonVersion: receipt.Version, CLIVersion: receipt.CLIVersion, RequiredCapabilityPresent: true, OwnershipVerified: true, Visibility: visibility}
}
func slicesContainReal(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func assertRolePolicySnapshots(t *testing.T, ctx context.Context, workspace, mission pgtype.UUID, c realAcceptanceConfig) map[string]string {
	rows, err := testPool.Query(ctx, `SELECT duty,content_hash,config FROM mission_role_policy_snapshot WHERE workspace_id=$1 AND mission_id=$2`, workspace, mission)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	hashes := map[string]string{}
	for rows.Next() {
		var duty, contentHash string
		var raw []byte
		if err := rows.Scan(&duty, &contentHash, &raw); err != nil {
			t.Fatal(err)
		}
		var config orchestration.RoleProfileConfig
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatalf("decode %s role policy snapshot: %v", duty, err)
		}
		if contentHash == "" || config.Budget.MaxTokens == nil || *config.Budget.MaxTokens != c.maxTokens || config.Budget.MaxCostUSDTicks == nil || *config.Budget.MaxCostUSDTicks != c.maxCost || config.TimeoutSeconds != int(c.roleTimeout.Seconds()) || !slicesContainReal(config.RequiredCapabilities, realAcceptanceCapability) || len(config.Tools.AllowedTools) != 0 || len(config.Tools.AllowedPaths) != 0 {
			t.Fatalf("%s role policy snapshot does not freeze the acceptance contract", duty)
		}
		seen[duty] = true
		hashes[duty] = contentHash
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, duty := range []orchestration.Duty{orchestration.DutyPlanner, orchestration.DutyExecutor, orchestration.DutyReviewer, orchestration.DutyIntegrator} {
		if !seen[duty.String()] {
			t.Fatalf("missing %s role policy snapshot", duty)
		}
	}
	return hashes
}
func requireRealArtifact(t *testing.T, result orchestration.ReconcileRunResult, mission, runID pgtype.UUID, kind orchestration.ArtifactKind) db.Artifact {
	t.Helper()
	lineageOK := result.Artifact != nil && result.Artifact.MissionID == mission && result.Artifact.RunID == runID && result.Artifact.TaskNodeID == result.Run.TaskNodeID
	kindOK := result.Artifact != nil && result.Artifact.Kind == string(kind)
	if !lineageOK || !kindOK {
		t.Fatalf("run did not produce expected artifact lineage: expected_kind=%s run_status=%s failure_kind=%q failure_message=%q artifact_present=%t artifact_kind_ok=%t lineage_ok=%t",
			kind, result.Run.Status, result.Run.FailureKind.String, redactRealAcceptance(result.Run.FailureMessage.String), result.Artifact != nil, kindOK, lineageOK)
	}
	return *result.Artifact
}
func requireApprovedRealReview(t *testing.T, result orchestration.ReconcileRunResult, mission, runID, artifactID pgtype.UUID) db.ReviewVerdict {
	t.Helper()
	verdict := result.ReviewVerdict
	lineageOK := verdict != nil && verdict.MissionID == mission && verdict.ReviewRunID == runID && verdict.ArtifactID == artifactID && verdict.TaskNodeID == result.Run.TaskNodeID
	decisionOK := verdict != nil && verdict.Decision == string(orchestration.ReviewDecisionApproved)
	if !lineageOK || !decisionOK {
		t.Fatalf("review did not produce approved verdict lineage: run_status=%s failure_kind=%q failure_message=%q verdict_present=%t decision_ok=%t lineage_ok=%t",
			result.Run.Status, result.Run.FailureKind.String, redactRealAcceptance(result.Run.FailureMessage.String), verdict != nil, decisionOK, lineageOK)
	}
	return *verdict
}
func waitRuntimeOffline(t *testing.T, ctx context.Context, workspace, runtime pgtype.UUID) {
	for {
		var status string
		err := testPool.QueryRow(ctx, `SELECT status FROM agent_runtime WHERE workspace_id=$1 AND id=$2`, workspace, runtime).Scan(&status)
		if err == nil && status == "offline" {
			return
		}
		if err != nil {
			t.Fatalf("read runtime %s while waiting offline: %v", uuid.UUID(runtime.Bytes), err)
		}
		if ctx.Err() != nil {
			t.Fatalf("runtime %s did not become offline", uuid.UUID(runtime.Bytes))
		}
		time.Sleep(250 * time.Millisecond)
	}
}
