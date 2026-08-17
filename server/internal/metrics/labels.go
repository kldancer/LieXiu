package metrics

import (
	"regexp"
	"strings"

	"github.com/kailonyang/liexiu/server/pkg/taskfailure"
)

const (
	labelSource         = "source"
	labelRuntimeMode    = "runtime_mode"
	labelProvider       = "provider"
	labelTerminalStatus = "terminal_status"
	labelFailureReason  = "failure_reason"
	labelTokenType      = "token_type"
	labelModel          = "model"
	labelModelAlias     = "model_alias"

	// PR3 labels (funnel / community / commercial).
	labelSignupSource = "signup_source"
	labelPlatform     = "platform"
	labelPath         = "path"
	labelRecoverable  = "recoverable"
	labelKind         = "kind"
	labelStatus       = "status"
	labelEventKind    = "event_kind"
	labelAction       = "action"
	labelResult       = "result"
	labelOp           = "op"
)

var businessMetricLabels = map[string][]string{
	"liexiu_agent_task_enqueued_total":     {labelSource, labelRuntimeMode},
	"liexiu_agent_task_dispatched_total":   {labelSource, labelRuntimeMode},
	"liexiu_agent_task_started_total":      {labelSource, labelRuntimeMode, labelProvider},
	"liexiu_agent_task_terminal_total":     {labelSource, labelRuntimeMode, labelTerminalStatus},
	"liexiu_agent_task_failed_total":       {labelSource, labelRuntimeMode, labelFailureReason},
	"liexiu_agent_task_queue_wait_seconds": {labelSource, labelRuntimeMode},
	"liexiu_agent_task_run_seconds":        {labelSource, labelRuntimeMode, labelTerminalStatus},
	"liexiu_agent_task_total_seconds":      {labelSource, labelRuntimeMode, labelTerminalStatus},
	"liexiu_agent_task_in_progress":        {labelSource, labelRuntimeMode},
	"liexiu_agent_task_iteration_count":    {labelSource, labelTerminalStatus},
	"liexiu_llm_tokens_total":              {labelProvider, labelModel, labelTokenType, labelRuntimeMode, labelSource},
	"liexiu_llm_cost_usd_total":            {labelProvider, labelModel, labelTokenType, labelRuntimeMode, labelSource},
	"liexiu_llm_unpriced_tokens_total":     {labelProvider, labelModelAlias, labelTokenType},
	"liexiu_llm_request_total":             {labelProvider, labelModel, labelRuntimeMode},
	"liexiu_task_queued_expired_total":     {labelSource, labelRuntimeMode},
	"liexiu_task_lease_expired_total":      {labelSource},

	// PR3 funnel / community / commercial.
	"liexiu_signup_total":                             {labelSignupSource},
	"liexiu_workspace_created_total":                  {labelSource},
	"liexiu_team_invite_sent_total":                   {},
	"liexiu_team_invite_accepted_total":               {},
	"liexiu_onboarding_started_total":                 {labelPlatform},
	"liexiu_onboarding_questionnaire_submitted_total": {},
	"liexiu_onboarding_source_submitted_total":        {},
	"liexiu_onboarding_completed_total":               {labelPath},
	"liexiu_cloud_waitlist_joined_total":              {},
	"liexiu_issue_created_total":                      {labelSource, labelPlatform},
	"liexiu_agent_created_total":                      {labelRuntimeMode, labelSource},
	"liexiu_issue_executed_total":                     {labelSource},
	"liexiu_runtime_registered_total":                 {labelRuntimeMode, labelProvider},
	"liexiu_runtime_ready_total":                      {labelRuntimeMode, labelProvider},
	"liexiu_runtime_ready_seconds":                    {labelRuntimeMode, labelProvider},
	"liexiu_runtime_failed_total":                     {labelRuntimeMode, labelProvider, labelFailureReason, labelRecoverable},
	"liexiu_runtime_offline_total":                    {labelRuntimeMode, labelProvider},
	"liexiu_daemon_ws_message_received_total":         {labelKind},
	"liexiu_github_event_received_total":              {labelEventKind, labelAction},
	"liexiu_github_pr_review_total":                   {labelResult},
	"liexiu_cloudruntime_request_total":               {labelOp, labelStatus},
	"liexiu_cloudruntime_request_duration_seconds":    {labelOp},
}

var forbiddenMetricLabels = map[string]struct{}{
	"workspace_id": {},
	// installation_id is the same class as the rest: one series per channel
	// installation, growing with tenants rather than with the deployment. It
	// is also the natural thing to reach for in any channel metric — every
	// adapter call site already carries one — which is what makes leaving it
	// off this list a matter of time rather than of luck.
	"installation_id": {},
	"user_id":         {},
	"agent_id":        {},
	"task_id":         {},
	"issue_id":        {},
	"runtime_id":      {},
	"session_id":      {},
	"ip":              {},
}

var (
	knownSources = map[string]string{
		"issue":        "issue",
		"quick_create": "quick_create",
		"manual":       "manual",
		"api":          "api",
		"other":        "other",
	}
	knownRuntimeModes = map[string]string{
		"local":   "local",
		"cloud":   "cloud",
		"unknown": "unknown",
	}
	knownRuntimeProviders = map[string]string{
		"antigravity":   "antigravity",
		"claude":        "claude",
		"codebuddy":     "codebuddy",
		"codex":         "codex",
		"copilot":       "copilot",
		"cursor":        "cursor",
		"dsh":           "dsh",
		"gemini":        "gemini",
		"grok":          "grok",
		"hermes":        "hermes",
		"kiro":          "kiro",
		"kimi":          "kimi",
		"reasonix":      "reasonix",
		"liexiu_agent": "liexiu_agent",
		"openclaw":      "openclaw",
		"opencode":      "opencode",
		"deveco":        "deveco",
		"pi":            "pi",
		"qoder":         "qoder",
		"qoderclicn":    "qoderclicn",
		"qwen":          "qwen",
		"traecli":       "traecli",
		"other":         "other",
	}
	knownTerminalStatuses = map[string]string{
		"completed": "completed",
		"failed":    "failed",
		"cancelled": "cancelled",
		"blocked":   "blocked",
		"other":     "other",
	}
	knownTokenTypes = map[string]string{
		"input":       "input",
		"output":      "output",
		"cache_read":  "cache_read",
		"cache_write": "cache_write",
	}
	knownFailureReasons = map[string]string{}
	modelAliasUnsafeRe  = regexp.MustCompile(`[^a-z0-9._:/+-]+`)
)

func init() {
	for _, reason := range taskfailure.AllReasons() {
		knownFailureReasons[reason.String()] = reason.String()
	}
}

func validateBusinessMetricLabels() {
	for metric, labels := range businessMetricLabels {
		for _, label := range labels {
			if _, forbidden := forbiddenMetricLabels[label]; forbidden {
				panic("forbidden high-cardinality label " + label + " on " + metric)
			}
		}
	}
}

func metricLabels(metric string) []string {
	labels, ok := businessMetricLabels[metric]
	if !ok {
		panic("missing business metric label definition for " + metric)
	}
	return labels
}

func NormalizeTaskSource(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownSources[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeRuntimeMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownRuntimeModes[value]; ok {
		return normalized
	}
	return "unknown"
}

func NormalizeRuntimeProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownRuntimeProviders[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeTerminalStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownTerminalStatuses[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeFailureReason(value string) string {
	value = strings.TrimSpace(value)
	if normalized, ok := knownFailureReasons[value]; ok {
		return normalized
	}
	return taskfailure.Classify(value).String()
}

func NormalizeTokenType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownTokenTypes[value]; ok {
		return normalized
	}
	return "input"
}

func NormalizeModelAlias(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	value = modelAliasUnsafeRe.ReplaceAllString(value, "_")
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
