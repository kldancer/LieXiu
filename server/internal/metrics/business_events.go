package metrics

import (
	"github.com/kailonyang/liexiu/server/internal/analytics"
	"github.com/prometheus/client_golang/prometheus"
)

// Local funnel and execution counters. Events are dispatched only to bounded
// Prometheus collectors and are never transmitted to an external service.

// runtimeReadyBuckets covers cold-start runtime readiness from <1s to ~5min.
// Most provider boots land in 5–60s; the long tail catches stuck pulls.
var runtimeReadyBuckets = []float64{1, 2.5, 5, 10, 30, 60, 120, 300, 600}

// cloudRuntimeRequestBuckets covers outbound Fleet/Gateway calls from sub-100ms
// (status pings) to ~30s (provision). Aligns with cloudruntime.defaultTimeout.
var cloudRuntimeRequestBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30}

// prMergeSecondsBuckets covers PR-open → PR-merged latency from minutes to weeks.
var prMergeSecondsBuckets = []float64{
	300, 900, 1800,
	3600, 2 * 3600, 6 * 3600, 12 * 3600,
	24 * 3600, 2 * 24 * 3600, 7 * 24 * 3600, 30 * 24 * 3600,
}

// businessEventMetrics holds the PR3 collectors. Kept in a separate struct
// so business.go (PR2 task lifecycle / LLM) stays focused; both are exposed
// through the same BusinessMetrics receiver and the same Collectors() slice.
type businessEventMetrics struct {
	signup                          *prometheus.CounterVec
	workspaceCreated                *prometheus.CounterVec
	teamInviteSent                  *prometheus.CounterVec
	teamInviteAccepted              *prometheus.CounterVec
	onboardingStarted               *prometheus.CounterVec
	onboardingQuestionnaireSubmit   *prometheus.CounterVec
	onboardingSourceSubmit          *prometheus.CounterVec
	onboardingCompleted             *prometheus.CounterVec
	cloudWaitlistJoined             *prometheus.CounterVec
	issueCreated                    *prometheus.CounterVec
	agentCreated                    *prometheus.CounterVec
	issueExecuted                   *prometheus.CounterVec
	runtimeRegistered               *prometheus.CounterVec
	runtimeReady                    *prometheus.CounterVec
	runtimeReadySeconds             *prometheus.HistogramVec
	runtimeFailed                   *prometheus.CounterVec
	runtimeOffline                  *prometheus.CounterVec
	daemonWSMessageReceived         *prometheus.CounterVec
	githubEventReceived             *prometheus.CounterVec
	githubPRReview                  *prometheus.CounterVec
	githubPRMergeSeconds            prometheus.Histogram
	cloudRuntimeRequest             *prometheus.CounterVec
	cloudRuntimeRequestDurationSecs *prometheus.HistogramVec
}

func newBusinessEventMetrics() *businessEventMetrics {
	return &businessEventMetrics{
		signup: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_signup_total",
			Help: "Total user signups (account creations).",
		}, metricLabels("liexiu_signup_total")),
		workspaceCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_workspace_created_total",
			Help: "Total workspaces created.",
		}, metricLabels("liexiu_workspace_created_total")),
		teamInviteSent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_team_invite_sent_total",
			Help: "Total workspace invitations sent.",
		}, metricLabels("liexiu_team_invite_sent_total")),
		teamInviteAccepted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_team_invite_accepted_total",
			Help: "Total workspace invitations accepted.",
		}, metricLabels("liexiu_team_invite_accepted_total")),
		onboardingStarted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_onboarding_started_total",
			Help: "Total onboarding flows started.",
		}, metricLabels("liexiu_onboarding_started_total")),
		onboardingQuestionnaireSubmit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_onboarding_questionnaire_submitted_total",
			Help: "Total onboarding questionnaires submitted.",
		}, metricLabels("liexiu_onboarding_questionnaire_submitted_total")),
		onboardingSourceSubmit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_onboarding_source_submitted_total",
			Help: "Total acquisition-source answers or declines recorded (workspace backfill prompt).",
		}, metricLabels("liexiu_onboarding_source_submitted_total")),
		onboardingCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_onboarding_completed_total",
			Help: "Total onboarding flows completed.",
		}, metricLabels("liexiu_onboarding_completed_total")),
		cloudWaitlistJoined: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_cloud_waitlist_joined_total",
			Help: "Total users that joined the cloud waitlist.",
		}, metricLabels("liexiu_cloud_waitlist_joined_total")),
		issueCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_issue_created_total",
			Help: "Total issues created (any source).",
		}, metricLabels("liexiu_issue_created_total")),
		agentCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_agent_created_total",
			Help: "Total agents created.",
		}, metricLabels("liexiu_agent_created_total")),
		issueExecuted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_issue_executed_total",
			Help: "First task completion per issue (per-issue exactly-once activation keystone).",
		}, metricLabels("liexiu_issue_executed_total")),
		runtimeRegistered: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_runtime_registered_total",
			Help: "Total first-time runtime registrations.",
		}, metricLabels("liexiu_runtime_registered_total")),
		runtimeReady: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_runtime_ready_total",
			Help: "Total runtimes that reached ready state.",
		}, metricLabels("liexiu_runtime_ready_total")),
		runtimeReadySeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "liexiu_runtime_ready_seconds",
			Help:    "Time from runtime registration to ready (seconds).",
			Buckets: runtimeReadyBuckets,
		}, metricLabels("liexiu_runtime_ready_seconds")),
		runtimeFailed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_runtime_failed_total",
			Help: "Total runtime failures by canonical reason.",
		}, metricLabels("liexiu_runtime_failed_total")),
		runtimeOffline: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_runtime_offline_total",
			Help: "Total runtime offline transitions.",
		}, metricLabels("liexiu_runtime_offline_total")),
		daemonWSMessageReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_daemon_ws_message_received_total",
			Help: "Total daemon WebSocket inbound messages by handler kind.",
		}, metricLabels("liexiu_daemon_ws_message_received_total")),
		githubEventReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_github_event_received_total",
			Help: "Total GitHub webhook events received by event kind and action.",
		}, metricLabels("liexiu_github_event_received_total")),
		githubPRReview: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_github_pr_review_total",
			Help: "Total GitHub pull request reviews observed by result.",
		}, metricLabels("liexiu_github_pr_review_total")),
		githubPRMergeSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "liexiu_github_pr_merge_seconds",
			Help:    "Time from PR opened to merged (seconds).",
			Buckets: prMergeSecondsBuckets,
		}),
		cloudRuntimeRequest: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "liexiu_cloudruntime_request_total",
			Help: "Total outbound cloud runtime requests by op and status bucket.",
		}, metricLabels("liexiu_cloudruntime_request_total")),
		cloudRuntimeRequestDurationSecs: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "liexiu_cloudruntime_request_duration_seconds",
			Help:    "Outbound cloud runtime request duration (seconds).",
			Buckets: cloudRuntimeRequestBuckets,
		}, metricLabels("liexiu_cloudruntime_request_duration_seconds")),
	}
}

func (e *businessEventMetrics) collectors() []prometheus.Collector {
	if e == nil {
		return nil
	}
	return []prometheus.Collector{
		e.signup,
		e.workspaceCreated,
		e.teamInviteSent,
		e.teamInviteAccepted,
		e.onboardingStarted,
		e.onboardingQuestionnaireSubmit,
		e.onboardingSourceSubmit,
		e.onboardingCompleted,
		e.cloudWaitlistJoined,
		e.issueCreated,
		e.agentCreated,
		e.issueExecuted,
		e.runtimeRegistered,
		e.runtimeReady,
		e.runtimeReadySeconds,
		e.runtimeFailed,
		e.runtimeOffline,
		e.daemonWSMessageReceived,
		e.githubEventReceived,
		e.githubPRReview,
		e.githubPRMergeSeconds,
		e.cloudRuntimeRequest,
		e.cloudRuntimeRequestDurationSecs,
	}
}

// RecordEvent increments the matching local Prometheus counter. The client
// parameter is retained temporarily so Wave 1C domain deletion does not force a
// cross-cutting constructor rewrite; it is deliberately ignored.
func RecordEvent(_ analytics.Client, m *BusinessMetrics, ev analytics.Event) {
	if m != nil {
		m.IncForEvent(ev)
	}
}

// IncForEvent dispatches an analytics.Event to the matching Prometheus counter.
// Unknown event names are silently ignored — the lint test in
// business_pairing_test.go is responsible for catching missing dispatch entries.
func (m *BusinessMetrics) IncForEvent(ev analytics.Event) {
	if m == nil || m.events == nil {
		return
	}
	switch ev.Name {
	case analytics.EventSignup:
		m.events.signup.WithLabelValues(NormalizeSignupSource(stringProp(ev.Properties, "signup_source"))).Inc()
	case analytics.EventWorkspaceCreated:
		m.events.workspaceCreated.WithLabelValues(NormalizeTaskSource(stringProp(ev.Properties, "source"))).Inc()
	case analytics.EventTeamInviteSent:
		m.events.teamInviteSent.WithLabelValues().Inc()
	case analytics.EventTeamInviteAccepted:
		m.events.teamInviteAccepted.WithLabelValues().Inc()
	case analytics.EventOnboardingStarted:
		m.events.onboardingStarted.WithLabelValues(NormalizePlatform(stringProp(ev.Properties, "platform"))).Inc()
	case analytics.EventOnboardingQuestionnaireSubmit:
		m.events.onboardingQuestionnaireSubmit.WithLabelValues().Inc()
	case analytics.EventOnboardingSourceSubmit:
		m.events.onboardingSourceSubmit.WithLabelValues().Inc()
	case analytics.EventOnboardingCompleted:
		m.events.onboardingCompleted.WithLabelValues(NormalizeOnboardingPath(stringProp(ev.Properties, "completion_path"))).Inc()
	case analytics.EventCloudWaitlistJoined:
		m.events.cloudWaitlistJoined.WithLabelValues().Inc()
	case analytics.EventIssueCreated:
		m.events.issueCreated.WithLabelValues(
			NormalizeTaskSource(stringProp(ev.Properties, "source")),
			NormalizePlatform(stringProp(ev.Properties, "platform")),
		).Inc()
	case analytics.EventAgentCreated:
		m.events.agentCreated.WithLabelValues(
			NormalizeRuntimeMode(stringProp(ev.Properties, "runtime_mode")),
			NormalizeTaskSource(stringProp(ev.Properties, "source")),
		).Inc()
	case analytics.EventIssueExecuted:
		m.events.issueExecuted.WithLabelValues(NormalizeTaskSource(stringProp(ev.Properties, "source"))).Inc()
	case analytics.EventRuntimeRegistered:
		m.events.runtimeRegistered.WithLabelValues(
			NormalizeRuntimeMode(stringProp(ev.Properties, "runtime_mode")),
			NormalizeRuntimeProvider(stringProp(ev.Properties, "provider")),
		).Inc()
	case analytics.EventRuntimeReady:
		runtimeMode := NormalizeRuntimeMode(stringProp(ev.Properties, "runtime_mode"))
		provider := NormalizeRuntimeProvider(stringProp(ev.Properties, "provider"))
		m.events.runtimeReady.WithLabelValues(runtimeMode, provider).Inc()
		if d := int64Prop(ev.Properties, "ready_duration_ms"); d > 0 {
			m.events.runtimeReadySeconds.WithLabelValues(runtimeMode, provider).Observe(float64(d) / 1000.0)
		}
	case analytics.EventRuntimeFailed:
		m.events.runtimeFailed.WithLabelValues(
			NormalizeRuntimeMode(stringProp(ev.Properties, "runtime_mode")),
			NormalizeRuntimeProvider(stringProp(ev.Properties, "provider")),
			NormalizeFailureReason(stringProp(ev.Properties, "failure_reason")),
			boolLabel(boolProp(ev.Properties, "recoverable")),
		).Inc()
	case analytics.EventRuntimeOffline:
		m.events.runtimeOffline.WithLabelValues(
			NormalizeRuntimeMode(stringProp(ev.Properties, "runtime_mode")),
			NormalizeRuntimeProvider(stringProp(ev.Properties, "provider")),
		).Inc()
	default:
		// agent_task_* lifecycle telemetry is recorded straight to Prometheus
		// via the typed BusinessMetrics.RecordTask* methods (they take
		// queue/run/total seconds that an analytics.Event does not carry), so
		// there is no analytics.Event to dispatch here. Anything else reaching
		// this default is a missing case and the lint test will fail CI.
	}
}

// ---- Typed Record* helpers (no generic analytics.Event source) ------------

// RecordGithubEventReceived counts a GitHub webhook event by event kind / action.
func (m *BusinessMetrics) RecordGithubEventReceived(eventKind, action string) {
	if m == nil || m.events == nil {
		return
	}
	m.events.githubEventReceived.WithLabelValues(
		NormalizeGithubEventKind(eventKind),
		NormalizeGithubAction(action),
	).Inc()
}

// RecordGithubPRReview counts a PR review observation by result.
func (m *BusinessMetrics) RecordGithubPRReview(result string) {
	if m == nil || m.events == nil {
		return
	}
	m.events.githubPRReview.WithLabelValues(NormalizeGithubPRReviewResult(result)).Inc()
}

// ObserveGithubPRMergeSeconds records open→merge latency in seconds.
// Negative or zero values are ignored.
func (m *BusinessMetrics) ObserveGithubPRMergeSeconds(seconds float64) {
	if m == nil || m.events == nil || seconds <= 0 {
		return
	}
	m.events.githubPRMergeSeconds.Observe(seconds)
}

// RecordCloudRuntimeRequest counts an outbound Fleet/Gateway call by op +
// status bucket and observes its duration.
func (m *BusinessMetrics) RecordCloudRuntimeRequest(op, status string, durationSeconds float64) {
	if m == nil || m.events == nil {
		return
	}
	op = NormalizeCloudRuntimeOp(op)
	status = NormalizeCloudRuntimeStatus(status)
	m.events.cloudRuntimeRequest.WithLabelValues(op, status).Inc()
	if durationSeconds >= 0 {
		m.events.cloudRuntimeRequestDurationSecs.WithLabelValues(op).Observe(durationSeconds)
	}
}

// RecordDaemonWSMessageReceived counts an inbound daemon WS message by handler kind.
func (m *BusinessMetrics) RecordDaemonWSMessageReceived(kind string) {
	if m == nil || m.events == nil {
		return
	}
	m.events.daemonWSMessageReceived.WithLabelValues(NormalizeDaemonWSKind(kind)).Inc()
}

// ---- property accessors ---------------------------------------------------

func stringProp(props map[string]any, key string) string {
	if props == nil {
		return ""
	}
	v, ok := props[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func int64Prop(props map[string]any, key string) int64 {
	if props == nil {
		return 0
	}
	v, ok := props[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func boolProp(props map[string]any, key string) bool {
	if props == nil {
		return false
	}
	v, ok := props[key]
	if !ok || v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func boolLabel(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
