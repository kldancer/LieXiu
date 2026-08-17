package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kailonyang/liexiu/server/internal/analytics"
	"github.com/kailonyang/liexiu/server/internal/attribution"
	"github.com/kailonyang/liexiu/server/internal/events"
	obsmetrics "github.com/kailonyang/liexiu/server/internal/metrics"
	"github.com/kailonyang/liexiu/server/internal/realtime"
	"github.com/kailonyang/liexiu/server/internal/util"
	db "github.com/kailonyang/liexiu/server/pkg/db/generated"
	"github.com/kailonyang/liexiu/server/pkg/featureflag"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
	"github.com/kailonyang/liexiu/server/pkg/redact"
	"github.com/kailonyang/liexiu/server/pkg/skillbundle"
	"github.com/kailonyang/liexiu/server/pkg/taskfailure"
)

type TaskService struct {
	Queries   *db.Queries
	TxStarter TxStarter
	Hub       *realtime.Hub
	Bus       *events.Bus
	Analytics analytics.Client
	Metrics   *obsmetrics.BusinessMetrics
	Wakeup    TaskWakeupNotifier
	// FeatureFlags is the server-side toggle router. Nil is valid and returns
	// each call site's default.
	FeatureFlags *featureflag.Service
	// EmptyClaim caches "this runtime has no queued task" so the daemon
	// poll path can skip a Postgres scan on the steady-state empty case.
	// Optional — a nil cache disables the fast path and every claim
	// goes through the DB. Wired in router.go from the shared Redis
	// client.
	EmptyClaim            *EmptyClaimCache
	analyticsContextMu    sync.Mutex
	analyticsContextCache map[string]analytics.TaskContext
	analyticsContextOrder []string
}

type TaskWakeupNotifier interface {
	NotifyTaskAvailable(runtimeID, taskID string)
}

// triggerSummaryMaxLen caps the snapshot length so the row stays cheap to
// transmit (it ends up in every task list response). 200 is enough for a
// recognisable preview of a one-paragraph comment.
const triggerSummaryMaxLen = 200

// truncateForSummary returns s shortened to maxRunes, with a trailing
// `…` when truncated. Operates on runes (not bytes) so multibyte characters
// — Chinese / emoji — count as one each. Strips surrounding whitespace
// first so a leading newline doesn't waste budget.
func truncateForSummary(s string, maxRunes int) string {
	// strings.Builder + Grow avoids the O(N²) realloc cycle of `+=` in
	// a loop. Grow uses byte length, which is an upper bound for the
	// rune-equivalent output (replacing \n/\r/\t with space is byte-equal
	// for ASCII whitespace), so we never reallocate.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	rs := []rune(strings.TrimSpace(b.String()))
	if len(rs) <= maxRunes {
		return string(rs)
	}
	return string(rs[:maxRunes]) + "…"
}

// maxSynthesizedFallbackCommentRunes bounds the completion-fallback comment that
// CompleteTask synthesizes from a task's final output when the agent left no
// comment of its own during the run. A real final assistant message is at most
// a few thousand words; anything larger is a runaway raw-stream dump — every
// streamed text delta concatenated together plus a literal `tool call` line per
// tool_use event — which some runtimes/providers emit as the task's Output on
// long, tool-heavy runs. Such a dump (observed at 190–264 KB) must never be
// posted, even partially, to the issue thread (GH #5455).
const maxSynthesizedFallbackCommentRunes = 8000

const oversizedFallbackCommentNotice = "This task completed, but its output was too large to post safely. The raw output was not posted. Review the task in this issue's Execution log."

// truncateFallbackCommentBody bounds a synthesized completion-fallback comment
// body. Unlike truncateForSummary (which flattens newlines for a one-line row
// snapshot), it preserves genuine final messages below the cap verbatim. Output
// above the cap is untrusted: the reported failure mode puts process narration
// and tool traces at the head, so retaining any excerpt can expose execution
// details and still discard the final answer. Replace the entire body with a
// fixed notice instead. Callers pass the already-redacted body.
func truncateFallbackCommentBody(body string, maxRunes int) string {
	if utf8.RuneCountInString(body) <= maxRunes {
		return body
	}
	return oversizedFallbackCommentNotice
}

const (
	taskAnalyticsContextCacheMax = 4096
	// claimResponseRecoveryWindow must exceed daemon client.Timeout for
	// /tasks/claim (30s) plus /tasks/{id}/start (30s) plus scheduling slack.
	// Longer pre-start work is protected by prepareLeaseDuration instead of
	// stretching this global crash-recovery window.
	claimResponseRecoveryWindow = 90 * time.Second
	prepareLeaseDuration        = 45 * time.Second
)

// buildCommentTriggerSummary fetches the comment content and truncates
// it for storage on the task row. Returns an invalid pgtype.Text when
// the comment is missing (deleted / wrong workspace / etc) so the column
// stays NULL — front-end falls back to a structural label in that case.
//
// workspaceID scopes the fetch to the task's own workspace: the summary is
// later returned in claim / task-history responses, so a foreign comment UUID
// reaching an enqueue/merge path must NOT leak another workspace's text even in
// truncated form (MUL-4252).
func (s *TaskService) buildCommentTriggerSummary(ctx context.Context, workspaceID, commentID pgtype.UUID) pgtype.Text {
	if !commentID.Valid {
		return pgtype.Text{}
	}
	comment, err := s.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID:          commentID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return pgtype.Text{}
	}
	summary := truncateForSummary(comment.Content, triggerSummaryMaxLen)
	if summary == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: summary, Valid: true}
}

// ResolveOriginatorFromTriggerComment is the exported wrapper used by the
// comment-merge path (MUL-4195) to compute the top-of-chain human originator
// for a newly-arrived comment, so a merge can be gated on the originator being
// unchanged. workspaceID scopes the comment lookup to the task's workspace
// (MUL-4252). See resolveOriginatorFromTriggerComment for the chain rules.
func (s *TaskService) ResolveOriginatorFromTriggerComment(ctx context.Context, workspaceID, commentID pgtype.UUID) pgtype.UUID {
	return s.resolveOriginatorFromTriggerComment(ctx, workspaceID, commentID)
}

// AttributionForMergedComment resolves the FULL attribution snapshot for a comment
// being coalesced into an already-queued task (MUL-4302). A merge re-attributes the
// run to the newly-arrived comment's human, so the whole snapshot — source, evidence,
// delegation lineage, and both person columns — must move together as one
// attribution.Result; re-stamping only the person columns would leave a run showing
// B accountable while still pointing at A's stale source / evidence / level. isMention
// picks the agent-authored label (delegation for a mention / thread-parent, otherwise
// comment_source), matching the fresh-enqueue routing.
//
// The merge re-opens the same fail-closed decision the original enqueue faced: a merge
// swaps the effective trigger, responsible human, and evidence to the NEW comment, so
// "the enqueue already checked" does not carry over. It runs the comment through
// applyAttributionFallback — the identical fail-closed gate the fresh-enqueue path uses
// — and returns ErrAttributionFailClosed when the new comment cannot be attributed
// precisely and the workspace forbids the owner_fallback degrade. The caller must then
// REFUSE the merge and keep the original (precisely-attributed) task snapshot rather
// than re-stamp a queued run to a degraded owner_fallback (Elon must-fix).
func (s *TaskService) AttributionForMergedComment(ctx context.Context, workspaceID, commentID pgtype.UUID, isMention bool, agent db.Agent) (attribution.Result, error) {
	agentAuthoredSource := attribution.SourceCommentSource
	if isMention {
		agentAuthoredSource = attribution.SourceDelegation
	}
	attr := s.attributionFromTriggerComment(ctx, workspaceID, commentID, agentAuthoredSource)
	return s.applyAttributionFallback(ctx, attr, agent)
}

// BuildCommentTriggerSummary is the exported wrapper used by the comment-merge
// path (MUL-4195) to refresh a coalesced task's trigger_summary to the newest
// trigger comment's snapshot. workspaceID scopes the lookup (MUL-4252).
func (s *TaskService) BuildCommentTriggerSummary(ctx context.Context, workspaceID, commentID pgtype.UUID) pgtype.Text {
	return s.buildCommentTriggerSummary(ctx, workspaceID, commentID)
}

func NewTaskService(q *db.Queries, tx TxStarter, hub *realtime.Hub, bus *events.Bus, wakeups ...TaskWakeupNotifier) *TaskService {
	var wakeup TaskWakeupNotifier
	if len(wakeups) > 0 {
		wakeup = wakeups[0]
	}
	return &TaskService{Queries: q, TxStarter: tx, Hub: hub, Bus: bus, Wakeup: wakeup}
}

var trivialDoneMarkers = []string{
	"done",
	"готово",
	"готова",
	"сделано",
	"完成",
	"完了",
}

func isTrivialDoneOutput(output string) bool {
	normalized := strings.TrimSpace(strings.ToLower(output))
	normalized = strings.Trim(normalized, ".!！。… ")
	for _, marker := range trivialDoneMarkers {
		if normalized == marker {
			return true
		}
	}
	return false
}

func (s *TaskService) captureTaskQueued(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskEnqueued(source, runtimeMode)
	}
}

// resolveOriginatorFromTriggerComment returns the top-of-chain HUMAN user
// id for a comment that triggered an Enqueue* path. The chain rules
// (MUL-3869):
//
//   - trigger comment authored by a member → originator = author_id (that
//     member IS the top-of-chain human).
//   - trigger comment authored by an agent → read the parent task via
//     comment.source_task_id and inherit its originator_user_id. This is
//     the load-bearing case for agent fan-out: agent A @-mentions agent B,
//     comment author is A, but we MUST surface the human who originally
//     told A to run, not lose the originator at the first agent hop.
//   - missing comment / unknown source task / NULL parent originator →
//     invalid pgtype.UUID. BuildTaskOverlay treats that as "no overlay"
//     (gate 1).
//
// A nil receiver / nil Queries falls through to invalid so unit-test
// setups that don't wire a DB stay safe. workspaceID scopes the comment lookup
// to the task's workspace so a foreign comment UUID cannot resolve an
// originator from another tenant (MUL-4252).
func (s *TaskService) resolveOriginatorFromTriggerComment(ctx context.Context, workspaceID, commentID pgtype.UUID) pgtype.UUID {
	// The originator VALUE is independent of the agent-authored source label, so
	// any label works here; comment_source is passed only as a placeholder.
	return s.attributionFromTriggerComment(ctx, workspaceID, commentID, attribution.SourceCommentSource).UserID
}

// attributionFromTriggerComment resolves the full attribution (accountable
// human + provenance label + delegation lineage + evidence) for a
// comment-triggered run. It performs the DB reads and hands the gathered facts
// to the pure attribution.ClassifyComment rules so the classification stays
// side-effect-free and unit-tested. The returned UserID is byte-identical to
// the pre-MUL-4302 originator resolution, so authorization behavior remains
// unchanged. workspaceID scopes the comment lookup to the task's workspace.
//
// agentAuthoredSource selects the label for an agent-authored trigger comment:
// attribution.SourceCommentSource for the issue-assignee-reacting path, or
// attribution.SourceDelegation for an agent-authored delegation.
func (s *TaskService) attributionFromTriggerComment(ctx context.Context, workspaceID, commentID pgtype.UUID, agentAuthoredSource attribution.Source) attribution.Result {
	if s == nil || s.Queries == nil || !commentID.Valid {
		return attribution.Result{Source: attribution.SourceUnattributed}
	}
	comment, err := s.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID:          commentID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return attribution.Result{Source: attribution.SourceUnattributed}
	}
	return s.attributionFromComment(ctx, comment, agentAuthoredSource)
}

// attributionFromComment classifies a run from an already-loaded trigger comment,
// so a caller that already has the row (e.g. to inspect author_type) does not
// re-read it. Kept byte-identical to the inline logic attributionFromTriggerComment
// used before, so authorization behavior is unchanged.
func (s *TaskService) attributionFromComment(ctx context.Context, comment db.Comment, agentAuthoredSource attribution.Source) attribution.Result {
	facts := attribution.CommentFacts{
		CommentID:  comment.ID,
		AuthorType: comment.AuthorType,
		AuthorID:   comment.AuthorID,
	}
	// For an agent-authored comment, walk comment.source_task_id → parent task →
	// parent.originator_user_id (set by every agent comment-write path since
	// migration 120). A NULL/missing source task leaves ParentOriginator
	// invalid, which ClassifyComment maps to unattributed.
	if comment.AuthorType == "agent" && comment.SourceTaskID.Valid {
		facts.SourceTaskID = comment.SourceTaskID
		if parent, err := s.Queries.GetAgentTask(ctx, comment.SourceTaskID); err == nil {
			facts.ParentOriginator = parent.OriginatorUserID
			facts.ParentAccountable = parent.AccountableUserID
		}
	}
	return attribution.ClassifyComment(facts, agentAuthoredSource)
}

// resolveOriginatorForIssueTask returns the top-of-chain human for issue-backed
// dispatches. Comment-triggered runs keep the existing comment-chain semantics;
// direct issue assignment/creation falls back to the issue's member creator.
// Agent-created issues that carry an explicit task-origin link — quick_create
// (daemon quick-create flow) or agent_create (an agent's ordinary `issue
// create`, MUL-4305) — inherit that origin task's originator, since origin_id
// points at the agent_task_queue row that created the issue. Other
// agent/system origins deliberately remain unattributed.
func (s *TaskService) resolveOriginatorForIssueTask(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID) pgtype.UUID {
	return s.attributionForIssueTask(ctx, issue, triggerCommentID, attribution.SourceCommentSource, pgtype.UUID{}).UserID
}

// attributionForIssueTask resolves the full attribution for an issue-backed
// enqueue. Comment-triggered runs keep the comment-chain semantics; direct
// assignment/creation falls back to the issue's member creator; agent-created
// quick-create issues inherit the origin task's human as a delegation. The
// accountable-human value is byte-identical to resolveOriginatorForIssueTask,
// which now delegates here — so there is a single source of truth and
// authorization is unaffected. agentAuthoredSource labels the agent-authored
// trigger comment case (see attributionFromTriggerComment).
func (s *TaskService) attributionForIssueTask(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID, agentAuthoredSource attribution.Source, actorUserID pgtype.UUID) attribution.Result {
	// A direct member action is the accountable human AND originator, ahead of any
	// trigger comment, origin, or rule (MUL-4302 §4/§5). This covers assign/promote,
	// a manual rerun — the last of which may INHERIT
	// a triggerCommentID for the daemon's prompt context, but must still attribute to
	// the member who clicked rerun, not the original comment's human. So the actor is
	// checked before the trigger-comment / origin branches.
	if actorUserID.Valid {
		return attribution.ClassifyDirect(attribution.DirectFacts{IssueID: issue.ID, ActorUserID: actorUserID})
	}
	if triggerCommentID.Valid {
		if s == nil || s.Queries == nil {
			return attribution.Result{Source: attribution.SourceUnattributed}
		}
		// workspace-scoped so a foreign comment UUID cannot resolve a human from
		// another tenant (MUL-4252).
		comment, err := s.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
			ID:          triggerCommentID,
			WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			return attribution.Result{Source: attribution.SourceUnattributed}
		}
		// A member/agent trigger comment resolves the human (direct_human / delegation
		// / comment_source). A SYSTEM-authored comment — today the Stage-completion
		// child-done comment (issue_child_done.go), which wakes the parent assignee
		// and threads no actor — carries no human and is not part of any delegation
		// chain. Classifying it would degrade straight to owner_fallback (the agent's
		// own owner), which is wrong for a Stage cascade: the woken run should be
		// accountable to whoever caused the PARENT issue to exist. So for a system
		// comment we skip the comment branch and fall through to the parent issue's
		// own provenance below — the same creator / agent_create-origin chain a
		// direct enqueue resolves — reaching owner_fallback
		// only if that provenance itself has no human (MUL-4302; raised by Bohan on
		// the stage-cascade fallback).
		if comment.AuthorType != "system" {
			return s.attributionFromComment(ctx, comment, agentAuthoredSource)
		}
	}
	facts := attribution.DirectFacts{
		IssueID:     issue.ID,
		CreatorType: issue.CreatorType,
		CreatorID:   issue.CreatorID,
	}
	// Member-created issues resolve without a DB read. Only origin-linked
	// agent-created issues (quick_create, agent_create) need to load the origin
	// task to inherit its human, and only when the DB is wired (nil Queries keeps
	// unit-test setups safe and yields unattributed). Both origin types stamp
	// origin_id with the agent_task_queue row that created the issue, so the
	// top-of-chain human is that task's originator_user_id (MUL-4305).
	if !(issue.CreatorType == "member" && issue.CreatorID.Valid) &&
		s != nil && s.Queries != nil && issue.OriginType.Valid && issue.OriginID.Valid &&
		(issue.OriginType.String == "quick_create" || issue.OriginType.String == "agent_create") {
		facts.OriginType = issue.OriginType.String
		facts.OriginTaskID = issue.OriginID
		if task, err := s.Queries.GetAgentTask(ctx, issue.OriginID); err == nil {
			facts.OriginOriginator = task.OriginatorUserID
			facts.OriginAccountable = task.AccountableUserID
		}
	}
	return attribution.ClassifyDirect(facts)
}

// ErrAttributionFailClosed signals that a run resolved to no precise accountable
// human and the enqueue is REFUSED rather than started. It covers three cases, all
// of which mean "we cannot guarantee an accountable human for this run" (MUL-4302
// §1/§3.5): the workspace opted into fail-closed; the workspace policy could not be
// read (so we cannot confirm fallback is allowed — fail closed, don't run); or
// owner_fallback has no agent owner to fall back to. Enqueue paths surface it so the
// run never starts.
var ErrAttributionFailClosed = errors.New("attribution: no precise accountable human and enqueue refused (fail-closed policy, policy read failed, or no agent owner)")

// ErrDuplicatePendingTask means a fresh enqueue lost the race to a concurrent
// one: a queued/dispatched task for the same (issue, agent) already exists, so
// the pending-task unique index rejected the insert (#5914). This is a benign
// outcome — a sibling run already covers this target
// — not a server fault. Enqueue paths return it so callers can report a
// success-shaped coalesced outcome / structured 409 instead of surfacing the
// raw Postgres constraint as a 500. It is returned BARE (the raw driver text,
// including the index name, is logged once at debug and never wrapped in) so no
// upper-layer log or response can leak the constraint name (#5914, Elon review).
var ErrDuplicatePendingTask = errors.New("a pending task for this issue and agent already exists")

// isDuplicatePendingTaskErr reports whether err is the pending-task unique-index
// violation (a concurrent enqueue won the race). Accept both names while v1 and
// v2 can coexist during a rolling deploy.
func isDuplicatePendingTaskErr(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	switch pgErr.ConstraintName {
	case "idx_one_pending_task_per_issue_agent", "idx_one_pending_task_per_issue_agent_v2":
		return true
	default:
		return false
	}
}

// applyAttributionFallback applies the workspace's degraded-attribution policy to a
// resolved attribution whose source came back unattributed (no precise human). A
// PRECISE attribution passes through untouched (no policy read at all). For an
// unattributed run the accountable-never-null guarantee is enforced fail-closed —
// we never silently enqueue a task that could run with a NULL accountable_user_id:
//
//   - policy read fails (or no workspace) → REFUSE. We cannot confirm the workspace
//     permits fallback, so we do not run an unattributable task on a transient DB
//     hiccup. (Only the rare unattributed path pays this; precise runs never read.)
//   - fail-closed workspace → REFUSE.
//   - otherwise → owner_fallback (accountable = agent owner, audit-only, originator
//     untouched). If there is no valid agent owner, owner_fallback stays
//     unattributed → REFUSE rather than enqueue a NULL-accountable task.
//
// Keeping this at the enqueue boundary (not inside the pure classifiers) means
// owner_fallback needs the agent owner, which every enqueue path has in hand.
func (s *TaskService) applyAttributionFallback(ctx context.Context, attr attribution.Result, agent db.Agent) (attribution.Result, error) {
	if attr.Source != attribution.SourceUnattributed {
		return attr, nil
	}
	if s == nil || s.Queries == nil || !agent.WorkspaceID.Valid {
		return attr, fmt.Errorf("%w: workspace policy unavailable", ErrAttributionFailClosed)
	}
	failClosed, err := s.Queries.GetWorkspaceAttributionFailClosed(ctx, agent.WorkspaceID)
	if err != nil {
		// Cannot confirm the workspace allows fallback → fail closed rather than
		// silently run an unattributable task.
		return attr, fmt.Errorf("%w: policy read failed: %v", ErrAttributionFailClosed, err)
	}
	if failClosed {
		return attr, ErrAttributionFailClosed
	}
	fallback := attribution.OwnerFallback(attr, agent.OwnerID)
	if fallback.Source == attribution.SourceUnattributed {
		// owner_fallback could not resolve an accountable human (no valid agent
		// owner): refuse rather than enqueue a NULL-accountable task.
		return attr, fmt.Errorf("%w: no agent owner to attribute", ErrAttributionFailClosed)
	}
	return fallback, nil
}

// attributionCreateParams maps a resolved attribution onto the CreateAgentTask
// provenance columns. originator_source is always stamped (never NULL for a new
// row); delegation lineage and evidence are stamped only when present.
func attributionCreateParams(attr attribution.Result) (source pgtype.Text, delegatedFrom pgtype.UUID, evidenceKind pgtype.Text, evidenceRef pgtype.UUID) {
	source = pgtype.Text{String: attr.Source.String(), Valid: true}
	delegatedFrom = attr.DelegatedFromTaskID
	evidenceKind = pgtype.Text{String: string(attr.EvidenceKind), Valid: attr.EvidenceKind != ""}
	evidenceRef = attr.EvidenceRefID
	return
}

// OriginatorForIssueTask exposes resolveOriginatorForIssueTask to callers
// outside the service package so callers judge the top-of-chain human with the exact same
// resolution the enqueue path persists on the task row. Without a shared entry
// point the gate saw an empty originator for agent-triggered assigns and denied
// private leaders that the write path would have attributed correctly
// (MUL-4305).
func (s *TaskService) OriginatorForIssueTask(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID) pgtype.UUID {
	return s.resolveOriginatorForIssueTask(ctx, issue, triggerCommentID)
}

func (s *TaskService) captureTaskDispatched(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskDispatched(util.UUIDToString(task.ID), source, runtimeMode, taskQueueWaitSeconds(task))
	}
}

func (s *TaskService) AnalyticsContextForTask(ctx context.Context, task db.AgentTaskQueue) analytics.TaskContext {
	return s.taskAnalyticsContext(ctx, task)
}

func (s *TaskService) captureTaskStarted(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, provider := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskStarted(source, runtimeMode, provider)
	}
}

func (s *TaskService) captureTaskCompleted(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskTerminal(util.UUIDToString(task.ID), source, runtimeMode, task.Status, taskRunSeconds(task), taskTotalSeconds(task), task.Attempt)
	}
}

func (s *TaskService) captureTaskFailed(ctx context.Context, task db.AgentTaskQueue) {
	failureReason := taskFailureReason(task)
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskTerminal(util.UUIDToString(task.ID), source, runtimeMode, task.Status, taskRunSeconds(task), taskTotalSeconds(task), task.Attempt)
		s.Metrics.RecordTaskFailed(source, runtimeMode, failureReason)
	}
}

func (s *TaskService) captureTaskCancelled(ctx context.Context, task db.AgentTaskQueue) {
	if s.Metrics != nil {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskTerminal(util.UUIDToString(task.ID), source, runtimeMode, task.Status, taskRunSeconds(task), taskTotalSeconds(task), task.Attempt)
	}
	// Revoke any mat_ task tokens minted for this task. Cancellation is
	// a terminal transition, so the running agent process no longer
	// needs to call back; eagerly deleting the token closes the
	// window where a compromised process could keep authenticating
	// against the API until the 24h expiry. Failure is non-fatal — the
	// expiry / FK cascade are the durable guards. MUL-2600.
	if err := s.Queries.DeleteTaskTokensByTask(ctx, task.ID); err != nil {
		slog.Warn("cancel task: failed to revoke task tokens",
			"task_id", util.UUIDToString(task.ID), "error", err)
	}
}

// costUSDTicks is the provider's own price for this usage in 1e-10 USD, or 0
// when it reported none — the metrics layer prefers it over its rate table.
func (s *TaskService) CaptureTaskUsage(ctx context.Context, task db.AgentTaskQueue, provider, model string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, costUSDTicks int64) {
	if s.Metrics == nil {
		return
	}
	source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
	s.Metrics.RecordLLMUsage(source, runtimeMode, provider, model, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, costUSDTicks)
}

func (s *TaskService) CaptureQueuedExpiredTasks(ctx context.Context, tasks []db.AgentTaskQueue) {
	if s.Metrics == nil {
		return
	}
	for _, task := range tasks {
		source, runtimeMode, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskQueuedExpired(source, runtimeMode)
	}
}

func (s *TaskService) CaptureLeaseExpiredTasks(ctx context.Context, tasks []db.AgentTaskQueue) {
	if s.Metrics == nil {
		return
	}
	for _, task := range tasks {
		source, _, _ := s.taskMetricsContext(ctx, task)
		s.Metrics.RecordTaskLeaseExpired(source)
	}
}

// EnqueueTaskForIssue creates a queued task for an agent-assigned issue.
// No context snapshot is stored — the agent fetches all data it needs at
// runtime via the liexiu CLI.
func (s *TaskService) EnqueueTaskForIssue(ctx context.Context, issue db.Issue, triggerCommentID ...pgtype.UUID) (db.AgentTaskQueue, error) {
	var commentID pgtype.UUID
	if len(triggerCommentID) > 0 {
		commentID = triggerCommentID[0]
	}
	return s.enqueueIssueTask(ctx, issue, commentID, false, "", pgtype.UUID{}, pgtype.UUID{})
}

// EnqueueTaskForIssueWithHandoff is the assign/promote variant that carries a
// handoff note into the run's opening context (MUL-3375). The note rides a
// dedicated task column; the daemon renders it via the assignment-handoff
// branch. Empty note behaves exactly like EnqueueTaskForIssue. actorUserID is the
// member who performed the assign/promote and becomes the accountable human for
// the run (MUL-4302 §4); invalid when the caller has no member actor.
func (s *TaskService) EnqueueTaskForIssueWithHandoff(ctx context.Context, issue db.Issue, handoffNote string, actorUserID pgtype.UUID) (db.AgentTaskQueue, error) {
	return s.enqueueIssueTask(ctx, issue, pgtype.UUID{}, false, handoffNote, actorUserID, pgtype.UUID{})
}

// enqueueIssueTask is the shared implementation behind EnqueueTaskForIssue
// and the manual rerun path. forceFreshSession=true marks the task so the
// daemon claim handler skips the (agent_id, issue_id) resume lookup — the
// user already judged the prior output bad, a fresh agent session is the
// expected behavior.
// ResolveIssueReviewSHA returns the head SHA of the commit currently under
// review for an issue (the head_sha of its most-relevant linked PR), or the
// empty string when the issue has no linked PR. Callers thread this into both
// the reviewer-loop dedup check and the enqueue path so a pending review task
// pinned to an old head does not satisfy a request after HEAD advanced
// (TEN-356). Empty string is the safe default: it makes dedup fall back to the
// pre-TEN-356 (issue_id, agent_id) key and leaves the task's context NULL.
//
// The lookup fails soft — any DB error (including "no linked PR") returns "" so
// a transient github-table hiccup can never over-dedup a review out of
// existence; the worst case is the pre-TEN-356 coalescing behavior.
func (s *TaskService) ResolveIssueReviewSHA(ctx context.Context, issueID pgtype.UUID) string {
	if !issueID.Valid {
		return ""
	}
	sha, err := s.Queries.GetIssueReviewHeadSha(ctx, issueID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("resolve issue review sha failed",
				"issue_id", util.UUIDToString(issueID), "error", err)
		}
		return ""
	}
	return sha
}

// headShaText wraps a resolved review SHA into the pgtype.Text the dedup/enqueue
// queries expect. Empty SHA marshals to an invalid (NULL) Text so the queries
// take their fall-back branch.
func headShaText(sha string) pgtype.Text {
	return pgtype.Text{String: sha, Valid: sha != ""}
}

// ResolveIssueReviewSHAParam is ResolveIssueReviewSHA wrapped as the pgtype.Text
// the dedup queries take, so both service- and handler-package call sites can
// key dedup on the reviewed head with a single call (TEN-356).
func (s *TaskService) ResolveIssueReviewSHAParam(ctx context.Context, issueID pgtype.UUID) pgtype.Text {
	return headShaText(s.ResolveIssueReviewSHA(ctx, issueID))
}

func (s *TaskService) enqueueIssueTask(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID, forceFreshSession bool, handoffNote string, actorUserID pgtype.UUID, rerunOfTaskID pgtype.UUID) (db.AgentTaskQueue, error) {
	return s.enqueueIssueTaskWithCommentPlan(ctx, issue, triggerCommentID, nil, forceFreshSession, handoffNote, actorUserID, rerunOfTaskID)
}

func (s *TaskService) enqueueIssueTaskWithCommentPlan(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID, coalescedCommentIDs []pgtype.UUID, forceFreshSession bool, handoffNote string, actorUserID pgtype.UUID, rerunOfTaskID pgtype.UUID) (db.AgentTaskQueue, error) {
	if !issue.AssigneeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "issue has no assignee")
		return db.AgentTaskQueue{}, fmt.Errorf("issue has no assignee")
	}

	agent, err := s.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agent.ID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", "agent has no runtime")
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	// The issue assignee reacting to an agent-authored comment is a
	// comment_source attribution (a special case of delegation); a member
	// comment or direct member assignment is direct_human. attr.UserID is the
	// same value the pre-MUL-4302 resolver produced, so overlay/authorization
	// are unchanged; the extra fields are audit provenance.
	attr := s.attributionForIssueTask(ctx, issue, triggerCommentID, attribution.SourceCommentSource, actorUserID)
	// No precise human resolved → owner_fallback (accountable = agent owner), or
	// refuse the enqueue if the workspace is fail-closed (MUL-4302 §3.5).
	attr, err = s.applyAttributionFallback(ctx, attr, agent)
	if err != nil {
		slog.Warn("task enqueue refused: attribution fail-closed", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(issue.AssigneeID))
		return db.AgentTaskQueue{}, err
	}
	originatorUserID := attr.UserID
	attrSource, attrDelegatedFrom, attrEvidenceKind, attrEvidenceRef := attributionCreateParams(attr)
	createParams := db.CreateAgentTaskParams{
		AgentID:              issue.AssigneeID,
		RuntimeID:            agent.RuntimeID,
		IssueID:              issue.ID,
		Priority:             priorityToInt(issue.Priority),
		TriggerCommentID:     triggerCommentID,
		CoalescedCommentIds:  coalescedCommentIDs,
		TriggerSummary:       s.buildCommentTriggerSummary(ctx, issue.WorkspaceID, triggerCommentID),
		ForceFreshSession:    pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
		HandoffNote:          pgtype.Text{String: handoffNote, Valid: handoffNote != ""},
		OriginatorUserID:     originatorUserID,
		AccountableUserID:    attr.AccountableUserID,
		RerunOfTaskID:        rerunOfTaskID,
		OriginatorSource:     attrSource,
		DelegatedFromTaskID:  attrDelegatedFrom,
		TriggerEvidenceKind:  attrEvidenceKind,
		TriggerEvidenceRefID: attrEvidenceRef,
		// Stamp the reviewed head so dedup can distinguish this run's target
		// from a later request against a new HEAD (TEN-356).
		HeadSha: headShaText(s.ResolveIssueReviewSHA(ctx, issue.ID)),
	}
	task, err := s.Queries.CreateAgentTask(ctx, createParams)
	if err != nil {
		slog.Error("task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create task: %w", err)
	}

	slog.Info("task enqueued",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(issue.ID),
		"agent_id", util.UUIDToString(issue.AssigneeID),
		"force_fresh_session", forceFreshSession,
	)
	// Order matters: broadcast first, notify daemon second. notifyTaskAvailable
	// kicks an in-process channel that the daemon picks up over HTTP and
	// claims; the claim path then emits its own task:dispatch. Doing the
	// queued broadcast afterwards risks the dispatch event reaching clients
	// before the queued one (rare but unsafe-by-construction). Publishing
	// in the desired observe-order makes correctness independent of timing.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

func (s *TaskService) enqueueMentionTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, forceFreshSession bool, handoffNote string, actorUserID pgtype.UUID, rerunOfTaskID pgtype.UUID) (db.AgentTaskQueue, error) {
	return s.enqueueMentionTaskWithCommentPlan(ctx, issue, agentID, triggerCommentID, nil, forceFreshSession, handoffNote, actorUserID, rerunOfTaskID)
}

func (s *TaskService) enqueueMentionTaskWithCommentPlan(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, coalescedCommentIDs []pgtype.UUID, forceFreshSession bool, handoffNote string, actorUserID pgtype.UUID, rerunOfTaskID pgtype.UUID) (db.AgentTaskQueue, error) {
	agent, err := s.Queries.GetAgent(ctx, agentID)
	if err != nil {
		slog.Error("mention task enqueue failed: agent not found", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("load agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		slog.Debug("mention task enqueue skipped: agent is archived", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent is archived")
	}
	if !agent.RuntimeID.Valid {
		slog.Error("mention task enqueue failed: agent has no runtime", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentTaskQueue{}, fmt.Errorf("agent has no runtime")
	}

	// An agent-authored delegation from a comment is a delegation (the parent task's human is
	// copied); a member mention is direct_human. attr.UserID matches the
	// pre-MUL-4302 value, so authorization is unchanged.
	attr := s.attributionForIssueTask(ctx, issue, triggerCommentID, attribution.SourceDelegation, actorUserID)
	// No precise human resolved → owner_fallback (accountable = agent owner), or
	// refuse the enqueue if the workspace is fail-closed (MUL-4302 §3.5).
	attr, err = s.applyAttributionFallback(ctx, attr, agent)
	if err != nil {
		slog.Warn("mention task enqueue refused: attribution fail-closed", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
		return db.AgentTaskQueue{}, err
	}
	originatorUserID := attr.UserID
	attrSource, attrDelegatedFrom, attrEvidenceKind, attrEvidenceRef := attributionCreateParams(attr)
	task, err := s.Queries.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:              agentID,
		RuntimeID:            agent.RuntimeID,
		IssueID:              issue.ID,
		Priority:             priorityToInt(issue.Priority),
		TriggerCommentID:     triggerCommentID,
		CoalescedCommentIds:  coalescedCommentIDs,
		TriggerSummary:       s.buildCommentTriggerSummary(ctx, issue.WorkspaceID, triggerCommentID),
		ForceFreshSession:    pgtype.Bool{Bool: forceFreshSession, Valid: forceFreshSession},
		HandoffNote:          pgtype.Text{String: handoffNote, Valid: handoffNote != ""},
		OriginatorUserID:     originatorUserID,
		AccountableUserID:    attr.AccountableUserID,
		RerunOfTaskID:        rerunOfTaskID,
		OriginatorSource:     attrSource,
		DelegatedFromTaskID:  attrDelegatedFrom,
		TriggerEvidenceKind:  attrEvidenceKind,
		TriggerEvidenceRefID: attrEvidenceRef,
		// Stamp the reviewed head so dedup can distinguish this run's target
		// from a later request against a new HEAD (TEN-356).
		HeadSha: headShaText(s.ResolveIssueReviewSHA(ctx, issue.ID)),
	})
	if err != nil {
		// A concurrent enqueue for the same (issue, agent) won the race and the
		// unique index rejected this insert. That is benign — a sibling run
		// already covers this target — so log it at debug and return a typed
		// sentinel the caller maps to a coalesced outcome / 409 rather than a
		// 500 that leaks the raw constraint name (#5914).
		if isDuplicatePendingTaskErr(err) {
			slog.Debug("mention task enqueue coalesced: pending task already exists", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
			return db.AgentTaskQueue{}, ErrDuplicatePendingTask
		}
		slog.Error("mention task enqueue failed", "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID), "error", err)
		return db.AgentTaskQueue{}, fmt.Errorf("create task: %w", err)
	}

	slog.Info("mention task enqueued", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(issue.ID), "agent_id", util.UUIDToString(agentID))
	// See EnqueueTaskForIssue for ordering rationale.
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
	return task, nil
}

// CancelTasksForIssue cancels every active task on the issue, reconciles each
// affected agent's status, and broadcasts task:cancelled events so frontends
// clear their live cards.
//
// Callers are explicit issue-lifecycle cleanup paths only — DeleteIssue and
// BatchDeleteIssues, where the owning issue row is going away so its tasks
// must not be left orphaned. A plain status flip, `cancelled` included, no
// longer routes here (MUL-4465): cancelling an issue is not an implicit "stop
// all runs" switch. Do not re-add a status-driven caller.
//
// Before #1587 this path was "cancel rows and return", which left each affected
// agent stuck at status="working" indefinitely, requiring a manual
// `liexiu agent update <id> --status idle` to unwedge. It now reconciles agent
// status and broadcasts task:cancelled, matching CancelTask and RerunIssue.
func (s *TaskService) CancelTasksForIssue(ctx context.Context, issueID pgtype.UUID) error {
	cancelled, err := s.Queries.CancelAgentTasksByIssue(ctx, issueID)
	if err != nil {
		return err
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	// Reconcile once per distinct agent instead of once per cancelled row:
	// cancelling an issue often stops several tasks owned by the same agent,
	// and each reconcile is a DB write plus a status broadcast. Matches
	// CancelTasksForAgent's single-reconcile shape (D#3319).
	for _, agentID := range distinctAgentIDs(cancelled) {
		s.ReconcileAgentStatus(ctx, agentID)
	}
	s.notifyTasksFinished(cancelled)
	return nil
}

// distinctAgentIDs returns each agent id appearing in the cancelled rows once,
// preserving first-seen order. Bulk cancellations frequently stop several tasks
// owned by the same agent; reconciling per distinct agent (rather than per row)
// collapses the redundant RefreshAgentStatusFromTasks writes and status
// broadcasts down to one per agent without changing the final agent status.
func distinctAgentIDs(cancelled []db.AgentTaskQueue) []pgtype.UUID {
	seen := make(map[pgtype.UUID]struct{}, len(cancelled))
	ids := make([]pgtype.UUID, 0, len(cancelled))
	for _, t := range cancelled {
		if _, dup := seen[t.AgentID]; dup {
			continue
		}
		seen[t.AgentID] = struct{}{}
		ids = append(ids, t.AgentID)
	}
	return ids
}

// CancelTasksForAgent cancels every active task belonging to an agent
// (queued + dispatched + running), reconciles the agent's status, and
// broadcasts task:cancelled events. Used by the agent-level "Cancel all
// tasks" action — same shape as CancelTasksForIssue but scoped on agent_id.
//
// Returns the cancelled rows so callers can report counts / log them.
func (s *TaskService) CancelTasksForAgent(ctx context.Context, agentID pgtype.UUID) ([]db.AgentTaskQueue, error) {
	cancelled, err := s.Queries.CancelAgentTasksByAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	// Reconcile once after the loop — agent transitions from
	// working→available based on remaining task counts, no need to call
	// per row (the rows we just cancelled all belong to the same agent).
	s.ReconcileAgentStatus(ctx, agentID)
	s.notifyTasksFinished(cancelled)
	return cancelled, nil
}

// CancelTasksByTriggerComment cancels active tasks whose planned comment batch
// contains the given edited/deleted comment. The historical method name is
// retained for call-site stability. It must run before deletion clears the
// trigger FK; the returned rows let the handler re-route every surviving input.
func (s *TaskService) CancelTasksByTriggerComment(ctx context.Context, commentID pgtype.UUID) ([]db.AgentTaskQueue, error) {
	cancelled, err := s.Queries.CancelAgentTasksByTriggerComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}
	// Reconcile once per distinct agent instead of once per cancelled row: an
	// edited/deleted trigger comment can cancel several tasks owned by the same
	// agent, and each reconcile is a DB write plus a status broadcast (D#3319).
	for _, agentID := range distinctAgentIDs(cancelled) {
		s.ReconcileAgentStatus(ctx, agentID)
	}
	s.notifyTasksFinished(cancelled)
	return cancelled, nil
}

// BroadcastCancelledTasks reconciles each affected agent's status and emits
// task:cancelled for every row. Callers must invoke this AFTER committing the
// cancellation so subscribers don't observe a "cancelled" event for a row
// that the tx might still roll back.
//
// workspaceID comes from the caller instead of being resolved per task, because
// the transaction these callers have just committed can delete the row the
// resolution would read. Each caller already knows the workspace being torn
// down, so the lookup is not needed and cannot fail.
func (s *TaskService) BroadcastCancelledTasks(ctx context.Context, workspaceID string, cancelled []db.AgentTaskQueue) {
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.publishTaskEvent(protocol.EventTaskCancelled, workspaceID, t)
	}
	s.notifyTasksFinished(cancelled)
}

// BroadcastTaskQueued emits a post-commit queue invalidation for clients.
func (s *TaskService) BroadcastTaskQueued(ctx context.Context, task db.AgentTaskQueue) {
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
}

func (s *TaskService) CaptureCancelledTasks(ctx context.Context, cancelled []db.AgentTaskQueue) {
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
	}
}

// CancelTask cancels a single task by ID. It broadcasts a task:cancelled event
// so frontends can update immediately.
func (s *TaskService) CancelTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	task, err := s.cancelTask(ctx, taskID, "", "")
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// CancelTaskWithReason cancels a task the SERVER decided to stop, persisting an
// actionable reason onto the row and preserving the ordinary task side effects.
func (s *TaskService) CancelTaskWithReason(ctx context.Context, taskID pgtype.UUID, errorMessage, failureReason string) (*db.AgentTaskQueue, error) {
	task, err := s.cancelTask(ctx, taskID, errorMessage, failureReason)
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// cancelTask performs ordinary task cancellation. Session locks, transcript
// mutation, and deferred finalization intentionally do not belong
// to this shared task path.
func (s *TaskService) cancelTask(ctx context.Context, taskID pgtype.UUID, errorMessage, failureReason string) (db.AgentTaskQueue, error) {
	var task db.AgentTaskQueue
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		var err error
		if errorMessage != "" || failureReason != "" {
			task, err = qtx.CancelAgentTaskWithReason(ctx, db.CancelAgentTaskWithReasonParams{
				ID:            taskID,
				Error:         pgtype.Text{String: errorMessage, Valid: errorMessage != ""},
				FailureReason: pgtype.Text{String: failureReason, Valid: failureReason != ""},
			})
		} else {
			task, err = qtx.CancelAgentTask(ctx, taskID)
		}
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := s.Queries.GetAgentTask(ctx, taskID)
		if err != nil {
			return db.AgentTaskQueue{}, fmt.Errorf("cancel task: %w", err)
		}
		return existing, nil
	}
	if err != nil {
		return db.AgentTaskQueue{}, fmt.Errorf("cancel task: %w", err)
	}

	slog.Info("task cancelled", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskCancelled(ctx, task)
	s.ReconcileAgentStatus(ctx, task.AgentID)
	s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, task)
	s.NotifyTaskFinished(task)

	return task, nil
}

// RebroadcastCancelledTask re-announces an already-cancelled task after a
// post-terminal delivery landed on its row (the cancel-ack's branch name or
// preserved-worktree error). The original task:cancelled broadcast fired at
// cancel time, BEFORE the daemon's ack — clients may have refetched a row
// without the delivery and will not refetch again on their own. Consumers
// treat task:cancelled as idempotent cache invalidation, so a replay is safe.
func (s *TaskService) RebroadcastCancelledTask(ctx context.Context, taskID pgtype.UUID) {
	task, err := s.Queries.GetAgentTask(ctx, taskID)
	if err != nil {
		slog.Warn("rebroadcast cancelled task: load failed",
			"task_id", util.UUIDToString(taskID), "error", err)
		return
	}
	if task.Status != "cancelled" {
		// A complete/fail callback already announced its own terminal event
		// carrying the row's final fields; nothing stale to refresh.
		return
	}
	s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, task)
}

// ClaimTask atomically claims the next queued task for an agent,
// respecting max_concurrent_tasks.
func (s *TaskService) ClaimTask(ctx context.Context, agentID pgtype.UUID) (*db.AgentTaskQueue, error) {
	start := time.Now()
	outcome := "unknown"
	var getAgentMs, countRunningMs, claimAgentMs, updateStatusMs, dispatchMs int64
	var claimed *db.AgentTaskQueue
	defer func() {
		s.maybeLogClaimSlow(agentID, outcome, start, getAgentMs, countRunningMs, claimAgentMs, updateStatusMs, dispatchMs)
	}()

	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t0 := time.Now()
		agent, err := qtx.GetAgentForClaimUpdate(ctx, agentID)
		getAgentMs = time.Since(t0).Milliseconds()
		if err != nil {
			outcome = "error_get_agent"
			return fmt.Errorf("agent not found: %w", err)
		}

		t0 = time.Now()
		running, err := qtx.CountRunningTasks(ctx, agentID)
		countRunningMs = time.Since(t0).Milliseconds()
		if err != nil {
			outcome = "error_count_running"
			return fmt.Errorf("count running tasks: %w", err)
		}
		if running >= int64(agent.MaxConcurrentTasks) {
			slog.Debug("task claim: no capacity", "agent_id", util.UUIDToString(agentID), "running", running, "max", agent.MaxConcurrentTasks)
			outcome = "no_capacity"
			return nil
		}

		t0 = time.Now()
		task, err := qtx.ClaimAgentTask(ctx, db.ClaimAgentTaskParams{
			AgentID:          agentID,
			PrepareLeaseSecs: prepareLeaseDuration.Seconds(),
		})
		claimAgentMs = time.Since(t0).Milliseconds()
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Debug("task claim: no tasks available", "agent_id", util.UUIDToString(agentID))
				outcome = "no_tasks"
				return nil
			}
			outcome = "error_claim"
			return fmt.Errorf("claim task: %w", err)
		}

		claimedTask := task
		claimed = &claimedTask
		return nil
	})
	if err != nil {
		if outcome == "unknown" {
			outcome = "error_transaction"
		}
		return nil, err
	}
	if claimed == nil {
		return nil, nil
	}

	slog.Info("task claimed", "task_id", util.UUIDToString(claimed.ID), "agent_id", util.UUIDToString(agentID))
	s.captureTaskDispatched(ctx, *claimed)

	// Refresh agent status from active tasks. This avoids a stale unconditional
	// working write racing after a just-cancelled claim.
	t0 := time.Now()
	s.ReconcileAgentStatus(ctx, agentID)
	updateStatusMs = time.Since(t0).Milliseconds()

	// Broadcast task:dispatch. ResolveTaskWorkspaceID inside this path can
	// re-query the issue, so it can also be a real contributor to claim latency.
	t0 = time.Now()
	s.broadcastTaskDispatch(ctx, *claimed)
	dispatchMs = time.Since(t0).Milliseconds()

	outcome = "claimed"
	return claimed, nil
}

// ClaimTaskForRuntime claims the next runnable task for a runtime while
// still respecting each agent's max_concurrent_tasks limit.
//
// Empty-claim fast path: when EmptyClaim is configured and a recent
// check verified the runtime had no queued tasks, returns immediately
// without touching Postgres. The cache is invalidated synchronously on
// every enqueue (notifyTaskAvailable), so a queued task becomes
// claimable on the next call rather than waiting for the TTL.
func (s *TaskService) ClaimTaskForRuntime(ctx context.Context, runtimeID pgtype.UUID) (*db.AgentTaskQueue, error) {
	start := time.Now()
	var (
		outcome          = "no_task"
		listMs, loopMs   int64
		listCount, tried int
		claimedFlag      bool
	)
	defer func() {
		totalMs := time.Since(start).Milliseconds()
		if totalMs < 300 {
			return
		}
		slog.Info("claim_for_runtime slow",
			"runtime_id", util.UUIDToString(runtimeID),
			"outcome", outcome,
			"total_ms", totalMs,
			"list_pending_ms", listMs,
			"list_pending_count", listCount,
			"agents_tried", tried,
			"claim_loop_ms", loopMs,
			"claimed", claimedFlag,
		)
	}()

	runtimeKey := util.UUIDToString(runtimeID)
	if err := s.PromoteDueDeferredTasksForRuntime(ctx, runtimeID); err != nil {
		outcome = "error_promote_deferred"
		return nil, err
	}

	// Check this before EmptyClaim: a lost claim response moves the task out of
	// `queued`, so the empty-queued cache cannot represent recoverability.
	stale, err := s.Queries.ReclaimStaleDispatchedTaskForRuntime(ctx, db.ReclaimStaleDispatchedTaskForRuntimeParams{
		RuntimeID:         runtimeID,
		ClaimRecoverySecs: claimResponseRecoveryWindow.Seconds(),
		PrepareLeaseSecs:  prepareLeaseDuration.Seconds(),
	})
	if err == nil {
		outcome = "reclaimed_dispatched"
		claimedFlag = true
		slog.Info("stale dispatched task reclaimed",
			"task_id", util.UUIDToString(stale.ID),
			"runtime_id", runtimeKey,
			"agent_id", util.UUIDToString(stale.AgentID),
		)
		return &stale, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		outcome = "error_reclaim_dispatched"
		return nil, fmt.Errorf("reclaim stale dispatched task: %w", err)
	}

	if s.EmptyClaim.IsEmpty(ctx, runtimeKey) {
		outcome = "empty_cache_hit"
		return nil, nil
	}

	// Sample the invalidation version BEFORE the SELECT. If a
	// concurrent enqueue Bumps between this read and the post-SELECT
	// MarkEmpty, the next IsEmpty will see the empty key tagged with
	// a stale version and reject it — closing the race that would
	// otherwise stall the just-queued task until the empty key's TTL
	// expired.
	preSelectVersion := s.EmptyClaim.CurrentVersion(ctx, runtimeKey)

	t0 := time.Now()
	tasks, err := s.Queries.ListQueuedClaimCandidatesByRuntime(ctx, runtimeID)
	listMs = time.Since(t0).Milliseconds()
	listCount = len(tasks)
	if err != nil {
		outcome = "error_list"
		return nil, fmt.Errorf("list queued claim candidates: %w", err)
	}

	if len(tasks) == 0 {
		s.EmptyClaim.MarkEmpty(ctx, runtimeKey, preSelectVersion)
		outcome = "empty_db"
		return nil, nil
	}

	loopStart := time.Now()
	triedAgents := map[string]struct{}{}
	var claimed *db.AgentTaskQueue
	for _, candidate := range tasks {
		agentKey := util.UUIDToString(candidate.AgentID)
		if _, seen := triedAgents[agentKey]; seen {
			continue
		}
		triedAgents[agentKey] = struct{}{}
		tried++

		task, err := s.ClaimTask(ctx, candidate.AgentID)
		if err != nil {
			loopMs = time.Since(loopStart).Milliseconds()
			outcome = "error_claim"
			return nil, err
		}
		if task != nil && task.RuntimeID == runtimeID {
			claimed = task
			break
		}
	}
	loopMs = time.Since(loopStart).Milliseconds()
	if claimed != nil {
		claimedFlag = true
		outcome = "claimed"
	}

	return claimed, nil
}

// FinalizeTaskClaim atomically persists the task-scoped token and, for a
// comment-backed task, the exact comment ids embedded in the response. The
// handler must call this only after the full payload has been built and before
// writing any response bytes. A failure rolls both writes back so the claim can
// be safely returned to the queue.
func (s *TaskService) FinalizeTaskClaim(
	ctx context.Context,
	task db.AgentTaskQueue,
	token db.CreateTaskTokenParams,
	deliveredCommentIDs []pgtype.UUID,
	recordCommentReceipt bool,
) ([]pgtype.UUID, error) {
	receipt := task.DeliveredCommentIds
	err := s.runInTx(ctx, func(qtx *db.Queries) error {
		if _, err := qtx.CreateTaskToken(ctx, token); err != nil {
			return fmt.Errorf("create task token: %w", err)
		}
		if !recordCommentReceipt {
			return nil
		}
		persisted, err := qtx.SetTaskDeliveredCommentIDs(ctx, db.SetTaskDeliveredCommentIDsParams{
			DeliveredCommentIds:      deliveredCommentIDs,
			TaskID:                   task.ID,
			RuntimeID:                task.RuntimeID,
			DispatchedAt:             task.DispatchedAt,
			ExpectedTriggerCommentID: task.TriggerCommentID,
		})
		if err != nil {
			return fmt.Errorf("set delivered comment ids: %w", err)
		}
		receipt = persisted
		return nil
	})
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

// RequeueTaskAfterClaimFailure immediately releases an exact dispatched claim
// whose payload finalization failed before the HTTP response was written. The
// SQL CAS includes dispatched_at so a late handler cannot roll back a newer
// reclaim. This is not a fresh enqueue: do not duplicate queued analytics.
func (s *TaskService) RequeueTaskAfterClaimFailure(ctx context.Context, task db.AgentTaskQueue) (*db.AgentTaskQueue, error) {
	requeued, err := s.Queries.RequeueAgentTaskAfterClaimFailure(ctx, db.RequeueAgentTaskAfterClaimFailureParams{
		TaskID:       task.ID,
		RuntimeID:    task.RuntimeID,
		DispatchedAt: task.DispatchedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("requeue task after claim failure: %w", err)
	}
	s.ReconcileAgentStatus(ctx, requeued.AgentID)
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, requeued)
	s.notifyTaskAvailable(requeued)
	slog.Info("task requeued after claim finalization failure",
		"task_id", util.UUIDToString(requeued.ID),
		"runtime_id", util.UUIDToString(requeued.RuntimeID),
	)
	return &requeued, nil
}

// ClaimTasksForRuntimes is the machine-level (MUL-4257) batch counterpart of
// ClaimTaskForRuntime: it claims up to maxTasks tasks across every runtime in
// runtimeIDs in a single call, so a daemon can poll for all of its runtimes
// with one HTTP request and a constant number of DB queries instead of one
// request (and one promote/reclaim/list cycle) per runtime.
//
// It preserves the exact per-runtime semantics, just set-ified:
//  1. promote due deferred tasks across the set (one UPDATE);
//  2. reclaim up to maxTasks stale-dispatched tasks across the set (one UPDATE)
//     — done before the empty-cache check because a lost claim response moves
//     the task out of `queued`, which the empty-queued cache cannot represent;
//  3. short-circuit runtimes whose empty-claim verdict is cached, sampling the
//     invalidation version for the rest BEFORE the candidate SELECT;
//  4. list queued candidates across the non-empty set (one SELECT);
//  5. mark still-empty runtimes so their next idle poll skips Postgres;
//  6. claim per distinct agent via ClaimTask (unchanged — preserves the
//     per-(issue, agent) serialization, the agent concurrency cap, and every
//     dispatch side effect) until maxTasks is reached.
//
// The returned slice contains both reclaimed and freshly-claimed tasks, each
// already carrying its runtime_id so the daemon routes it to the matching
// runtime locally.
func (s *TaskService) ClaimTasksForRuntimes(ctx context.Context, runtimeIDs []pgtype.UUID, maxTasks int) ([]db.AgentTaskQueue, error) {
	if len(runtimeIDs) == 0 || maxTasks <= 0 {
		return nil, nil
	}

	// De-dup runtime IDs defensively so MarkEmpty/version bookkeeping stays
	// unambiguous even if a daemon ever sends a duplicate.
	seen := make(map[string]struct{}, len(runtimeIDs))
	uniqueIDs := make([]pgtype.UUID, 0, len(runtimeIDs))
	runtimeInSet := make(map[string]struct{}, len(runtimeIDs))
	for _, rid := range runtimeIDs {
		key := util.UUIDToString(rid)
		runtimeInSet[key] = struct{}{}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		uniqueIDs = append(uniqueIDs, rid)
	}

	claimed := make([]db.AgentTaskQueue, 0, maxTasks)

	// 1. Promote due deferred tasks across the whole set (promote-first, like
	// the singular path). Replay the per-row side effects the singular service
	// method PromoteDueDeferredTasksForRuntime performs — crucially
	// EmptyClaim.Bump (via NotifyTaskEnqueued → notifyTaskAvailable) so a
	// just-promoted deferred task invalidates its runtime's cached empty
	// verdict BEFORE the empty-cache filter in step 3; otherwise a stale
	// MarkEmpty from a prior idle poll would short-circuit the runtime and the
	// promoted task would sit unclaimed until the empty key's TTL. Also emits
	// the deferred→queued UI event and the enqueue analytics sample.
	promoted, err := s.Queries.PromoteDueDeferredTasksForRuntimes(ctx, uniqueIDs)
	if err != nil {
		return nil, fmt.Errorf("promote deferred tasks: %w", err)
	}
	for _, task := range promoted {
		slog.Info("deferred fallback task promoted (batch)",
			"task_id", util.UUIDToString(task.ID),
			"runtime_id", util.UUIDToString(task.RuntimeID),
			"agent_id", util.UUIDToString(task.AgentID),
		)
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
		s.NotifyTaskEnqueued(ctx, task)
	}

	// 2. Reclaim lost-response dispatched tasks across the set, up to maxTasks.
	reclaimed, err := s.Queries.ReclaimStaleDispatchedTasksForRuntimes(ctx, db.ReclaimStaleDispatchedTasksForRuntimesParams{
		RuntimeIds:        uniqueIDs,
		ClaimRecoverySecs: claimResponseRecoveryWindow.Seconds(),
		PrepareLeaseSecs:  prepareLeaseDuration.Seconds(),
		MaxTasks:          int32(maxTasks),
	})
	if err != nil {
		return nil, fmt.Errorf("reclaim stale dispatched tasks: %w", err)
	}
	for i := range reclaimed {
		claimed = append(claimed, reclaimed[i])
		slog.Info("stale dispatched task reclaimed (batch)",
			"task_id", util.UUIDToString(reclaimed[i].ID),
			"runtime_id", util.UUIDToString(reclaimed[i].RuntimeID),
			"agent_id", util.UUIDToString(reclaimed[i].AgentID),
		)
	}
	if len(claimed) >= maxTasks {
		return claimed[:maxTasks], nil
	}

	// 3. Empty-cache short-circuit + version sampling for the remaining runtimes.
	nonEmpty := make([]pgtype.UUID, 0, len(uniqueIDs))
	versions := make(map[string]int64, len(uniqueIDs))
	for _, rid := range uniqueIDs {
		key := util.UUIDToString(rid)
		if s.EmptyClaim.IsEmpty(ctx, key) {
			continue
		}
		versions[key] = s.EmptyClaim.CurrentVersion(ctx, key)
		nonEmpty = append(nonEmpty, rid)
	}
	if len(nonEmpty) == 0 {
		return claimed, nil
	}

	// 4. One candidate SELECT across the non-empty set.
	candidates, err := s.Queries.ListQueuedClaimCandidatesByRuntimes(ctx, nonEmpty)
	if err != nil {
		// Steps 2/6 commit reclaimed/claimed tasks in their own transactions,
		// so `claimed` may already hold tasks dispatched server-side. Dropping
		// them with a 500 makes the daemon HTTP-fall-back and claim a SECOND
		// batch into the same free slots (the first batch then waits for stale
		// reclaim) — the same double-claim this PR set out to remove
		// (MUL-4257). Prefer partial success: hand back what committed so the
		// handler finalizes and returns it; the errored candidates stay queued
		// for the next poll.
		if len(claimed) > 0 {
			slog.Error("batch claim: candidate query failed after partial success; returning claimed tasks to avoid loss",
				"error", err, "claimed", len(claimed))
			return claimed, nil
		}
		return nil, fmt.Errorf("list queued claim candidates: %w", err)
	}

	// 5. Mark runtimes with zero candidates empty so their next idle poll skips
	// Postgres. Runtimes that had at least one candidate are intentionally not
	// marked (positive results always re-check the DB, matching the singular
	// path).
	withCandidates := make(map[string]struct{}, len(candidates))
	for i := range candidates {
		withCandidates[util.UUIDToString(candidates[i].RuntimeID)] = struct{}{}
	}
	for _, rid := range nonEmpty {
		key := util.UUIDToString(rid)
		if _, ok := withCandidates[key]; !ok {
			s.EmptyClaim.MarkEmpty(ctx, key, versions[key])
		}
	}

	// 6. Claim per distinct agent (unchanged path → same per-(issue, agent)
	// serialization, capacity cap, and dispatch side effects) until maxTasks is
	// reached.
	triedAgents := make(map[string]struct{}, len(candidates))
	for i := range candidates {
		if len(claimed) >= maxTasks {
			break
		}
		agentKey := util.UUIDToString(candidates[i].AgentID)
		if _, tried := triedAgents[agentKey]; tried {
			continue
		}
		triedAgents[agentKey] = struct{}{}

		task, err := s.ClaimTask(ctx, candidates[i].AgentID)
		if err != nil {
			// Each ClaimTask commits in its own transaction, so earlier
			// iterations (and step-2 reclaims) are already dispatched
			// server-side. Returning nil here would drop them and force the
			// daemon to double-claim via HTTP fallback (MUL-4257). Return the
			// partial batch instead; the failed agent's task stays queued.
			if len(claimed) > 0 {
				slog.Error("batch claim: claim task failed after partial success; returning claimed tasks to avoid loss",
					"error", err, "claimed", len(claimed))
				return claimed, nil
			}
			return nil, fmt.Errorf("claim task: %w", err)
		}
		if task == nil {
			continue
		}
		// ClaimAgentTask selects by agent only; guard that the claimed task
		// belongs to a runtime this daemon hosts. An agent with a
		// higher-priority queued task on ANOTHER daemon's runtime could
		// otherwise be dispatched here and dropped — matching the singular
		// path's runtime_id guard. Such a stray dispatch is recovered by the
		// reclaim path on the owning daemon's next poll.
		if _, ok := runtimeInSet[util.UUIDToString(task.RuntimeID)]; !ok {
			continue
		}
		claimed = append(claimed, *task)
	}

	return claimed, nil
}

func (s *TaskService) PromoteDueDeferredTasksForRuntime(ctx context.Context, runtimeID pgtype.UUID) error {
	tasks, err := s.Queries.PromoteDueDeferredTasksForRuntime(ctx, runtimeID)
	if err != nil {
		return fmt.Errorf("promote due deferred tasks: %w", err)
	}
	for _, task := range tasks {
		slog.Info("deferred fallback task promoted",
			"task_id", util.UUIDToString(task.ID),
			"runtime_id", util.UUIDToString(runtimeID),
			"agent_id", util.UUIDToString(task.AgentID),
		)
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
		s.NotifyTaskEnqueued(ctx, task)
	}
	return nil
}

// maybeLogClaimSlow emits one structured log per ClaimTask call when its total
// latency exceeds 300ms, so the prod tail can be diagnosed without flooding
// logs at normal poll rates. Called via defer so it captures the full path
// including post-claim updateAgentStatus / broadcastTaskDispatch (both of
// which can hit the DB) and any error exit.
func (s *TaskService) maybeLogClaimSlow(agentID pgtype.UUID, outcome string, start time.Time, getAgentMs, countRunningMs, claimAgentMs, updateStatusMs, dispatchMs int64) {
	totalMs := time.Since(start).Milliseconds()
	if totalMs < 300 {
		return
	}
	slog.Info("claim_task slow",
		"agent_id", util.UUIDToString(agentID),
		"outcome", outcome,
		"total_ms", totalMs,
		"get_agent_ms", getAgentMs,
		"count_running_ms", countRunningMs,
		"claim_agent_ms", claimAgentMs,
		"update_status_ms", updateStatusMs,
		"dispatch_ms", dispatchMs,
	)
}

// StartTask transitions a dispatched task to running.
// Issue status is NOT changed here — the agent manages it via the CLI.
func (s *TaskService) StartTask(ctx context.Context, taskID pgtype.UUID) (*db.AgentTaskQueue, error) {
	task, err := s.Queries.StartAgentTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("start task: %w", err)
	}
	s.cancelDeferredEscalationsForTask(ctx, task.ID)

	slog.Info("task started", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskStarted(ctx, task)
	// A local-directory waiter was reconciled out of the persisted working
	// status while parked. Restore working as soon as it enters running; the
	// normal dispatched -> running path is already working, so this is
	// intentionally idempotent there.
	s.ReconcileAgentStatus(ctx, task.AgentID)
	// Tell every connected workspace WS client that this task transitioned
	// (dispatched | waiting_local_directory) → running. Without this, the
	// workspace-wide `agentTaskSnapshot` query only refreshes on the 30s
	// staleTime, so any UI that distinguishes "queued" from "running" (e.g.
	// the issue-card agent activity indicator) lags by up to half a minute
	// on the transition users care about most.
	s.broadcastTaskEvent(ctx, protocol.EventTaskRunning, task)
	return &task, nil
}

func (s *TaskService) cancelDeferredEscalationsForTask(ctx context.Context, taskID pgtype.UUID) {
	cancelled, err := s.Queries.CancelDeferredEscalationsForTask(ctx, taskID)
	if err != nil {
		slog.Warn("cancel deferred escalations for task failed", "task_id", util.UUIDToString(taskID), "error", err)
		return
	}
	for _, task := range cancelled {
		slog.Info("deferred fallback task cancelled",
			"task_id", util.UUIDToString(task.ID),
			"primary_task_id", util.UUIDToString(taskID),
			"reason", "primary_acknowledged",
		)
	}
}

func (s *TaskService) CancelDeferredEscalationsForIssueAgent(ctx context.Context, issueID, agentID pgtype.UUID) {
	cancelled, err := s.Queries.CancelDeferredEscalationsForIssueAgent(ctx, db.CancelDeferredEscalationsForIssueAgentParams{
		IssueID: issueID,
		AgentID: agentID,
	})
	if err != nil {
		slog.Warn("cancel deferred escalations for issue agent failed",
			"issue_id", util.UUIDToString(issueID),
			"agent_id", util.UUIDToString(agentID),
			"error", err)
		return
	}
	for _, task := range cancelled {
		slog.Info("deferred fallback task cancelled",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(issueID),
			"agent_id", util.UUIDToString(agentID),
			"reason", "agent_comment_acknowledged",
		)
	}
}

// ExtendTaskPrepareLease keeps a claimed-but-not-started task protected while
// the daemon resolves cached inputs and prepares the execution environment.
func (s *TaskService) ExtendTaskPrepareLease(ctx context.Context, taskID, runtimeID pgtype.UUID) (*db.AgentTaskQueue, error) {
	task, err := s.Queries.ExtendAgentTaskPrepareLease(ctx, db.ExtendAgentTaskPrepareLeaseParams{
		ID:        taskID,
		RuntimeID: runtimeID,
		LeaseSecs: prepareLeaseDuration.Seconds(),
	})
	if err != nil {
		return nil, fmt.Errorf("extend task prepare lease: %w", err)
	}
	return &task, nil
}

// MarkTaskWaitingLocalDirectory parks a dispatched task in the
// waiting_local_directory state while the daemon waits for another in-flight
// task to release the project_resource path lock. reason carries a short
// human-readable hint (typically the contested path) that the UI surfaces
// next to the status. Returns the updated row so the daemon can confirm the
// transition and so the broadcast carries the up-to-date snapshot.
func (s *TaskService) MarkTaskWaitingLocalDirectory(ctx context.Context, taskID pgtype.UUID, reason string) (*db.AgentTaskQueue, error) {
	reason = strings.TrimSpace(reason)
	task, err := s.Queries.MarkAgentTaskWaitingLocalDirectory(ctx, db.MarkAgentTaskWaitingLocalDirectoryParams{
		ID:               taskID,
		WaitReason:       pgtype.Text{String: reason, Valid: reason != ""},
		PrepareLeaseSecs: prepareLeaseDuration.Seconds(),
	})
	if err != nil {
		return nil, fmt.Errorf("mark task waiting_local_directory: %w", err)
	}

	slog.Info("task waiting_local_directory",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(task.IssueID),
		"reason", reason,
	)
	// waiting_local_directory is owned/queued work, not executing work. The
	// claim path marked the agent working while the row was dispatched, so
	// reconcile immediately when it parks instead of leaving that persisted
	// status stale until a terminal transition.
	s.ReconcileAgentStatus(ctx, task.AgentID)
	s.broadcastTaskEvent(ctx, protocol.EventTaskWaitingLocalDirectory, task)
	return &task, nil
}

// CompleteTask marks a task as completed.
// Issue status is NOT changed here — the agent manages it via the CLI.
func (s *TaskService) CompleteTask(ctx context.Context, taskID pgtype.UUID, result []byte, sessionID, workDir, branchName string, sessionRolloutMissing bool, retiredSessionID string) (*db.AgentTaskQueue, error) {
	var task db.AgentTaskQueue
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t, err := qtx.CompleteAgentTask(ctx, db.CompleteAgentTaskParams{
			ID:                    taskID,
			Result:                result,
			SessionID:             pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:               pgtype.Text{String: workDir, Valid: workDir != ""},
			BranchName:            pgtype.Text{String: branchName, Valid: branchName != ""},
			SessionRolloutMissing: sessionRolloutMissing,
			RetiredSessionID:      pgtype.Text{String: retiredSessionID, Valid: retiredSessionID != ""},
		})
		if err != nil {
			return err
		}
		task = t

		return nil
	}); err != nil {
		// When parallel agents race, a task may already be completed,
		// cancelled, or failed by the time this call runs. The UPDATE
		// … WHERE status = 'running' returns no rows in that case.
		// Treat it as an idempotent success — same pattern as CancelTask.
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Info("complete task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				return &existing, nil
			}
			slog.Warn("complete task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("complete task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("complete task: %w", err)
	}

	slog.Info("task completed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID))
	s.captureTaskCompleted(ctx, task)

	// Invariant: every completed issue task must have at least one agent
	// comment on the issue, so the user always sees something when a run
	// ends. If the agent posted a comment during execution (result, progress
	// ping, or CLI reply), HasAgentCommentedSince returns true and we skip.
	// Otherwise, synthesize one from the final output. For comment-triggered
	// tasks, TriggerCommentID threads the fallback under the original comment;
	// for assignment-triggered tasks it is NULL and the fallback is top-level.
	if task.IssueID.Valid {
		agentCommented, _ := s.Queries.HasAgentCommentedSince(ctx, db.HasAgentCommentedSinceParams{
			IssueID:  task.IssueID,
			AuthorID: task.AgentID,
			Since:    task.StartedAt,
		})
		if !agentCommented {
			var payload protocol.TaskCompletedPayload
			if err := json.Unmarshal(result, &payload); err == nil {
				if payload.Output != "" {
					// Match the CLI's --content / --description behavior: agents that
					// emit literal `\n` 4-char sequences (Python/JSON-style) get them
					// decoded into real newlines before the comment hits the DB. See
					// util.UnescapeBackslashEscapes for the exact contract.
					body := util.UnescapeBackslashEscapes(payload.Output)
					if task.TriggerCommentID.Valid && isTrivialDoneOutput(body) {
						slog.Warn("suppressing trivial comment-trigger fallback output",
							"task_id", util.UUIDToString(task.ID),
							"issue_id", util.UUIDToString(task.IssueID),
							"agent_id", util.UUIDToString(task.AgentID),
						)
					} else {
						// Redact first, then bound: a runaway raw-stream Output (GH #5455)
						// must never reach the issue thread, even as a clipped excerpt.
						content := truncateFallbackCommentBody(redact.Text(body), maxSynthesizedFallbackCommentRunes)
						s.createAgentComment(ctx, task.IssueID, task.AgentID, content, "comment", task.TriggerCommentID, task.ID)
					}
				}
			}
		}
	}

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// Broadcast
	s.broadcastTaskEvent(ctx, protocol.EventTaskCompleted, task)

	return &task, nil
}

// FailTask marks a task as failed.
// Issue status is NOT changed here — the agent manages it via the CLI.
//
// sessionID/workDir are optional and are retained on the task row when the
// daemon established a real session or worktree before failing.
//
// failureReason is a coarse classifier consumed by the auto-retry path.
// Pass "" when unknown — the server runs the raw error text through
// taskfailure.Classify so the persisted failure_reason still lands in
// the canonical refined taxonomy rather than the legacy "agent_error"
// coarse bucket. Daemon callers that already produced a refined reason
// (via classifyPoisonedError, the timeout / runtime classifier, etc.)
// will have their value preserved untouched.
func (s *TaskService) FailTask(ctx context.Context, taskID pgtype.UUID, errMsg, sessionID, workDir, branchName, failureReason string, sessionRolloutMissing bool, retiredSessionID string) (*db.AgentTaskQueue, error) {
	// MUL-2946: synthesise a refined reason from the error text whenever the
	// caller didn't supply one. This is the last write-path guard against
	// "agent_error" coarse rows ending up in agent_task_queue.failure_reason
	// — every other path either provides a classified reason directly
	// (sweepers writing 'queued_expired' / 'runtime_offline' / 'timeout'
	// / 'runtime_recovery' via SQL) or runs the daemon's classifyPoisonedError
	// + taskfailure.Classify chain.
	if failureReason == "" {
		failureReason = taskfailure.Classify(errMsg).String()
	}
	// MUL-5370: daemons upgrade on their own cadence, so a fix that depends on
	// a new daemon-side label only reaches hosts that happened to update. An
	// older daemon reports a *non-empty* catchall for a failed skill-bundle
	// download, which the branch above deliberately leaves alone — without this
	// the retry and the actionable copy would both skip every un-upgraded host.
	// Runs after the empty-reason branch so a legacy reason synthesised there
	// is normalised too, and before the retry pre-compute below so the upgraded
	// reason is what decides retry eligibility.
	failureReason = taskfailure.NormalizeDaemonReason(failureReason, errMsg).String()

	// Pre-compute the auto-retry so the retry child can be created inside the
	// SAME transaction as the fail (MUL-4351). Historical runtime overlay metadata
	// is copied from the parent.
	var (
		wantRetry          bool
		retryOverlay       []byte
		retryConnectedApps []byte
		retryFireAt        pgtype.Timestamptz
		retryMaxAttempts   pgtype.Int4
	)
	if retryableReasons[failureReason] {
		if parent, perr := s.Queries.GetAgentTask(ctx, taskID); perr != nil {
			slog.Warn("fail task auto-retry: load parent failed",
				"task_id", util.UUIDToString(taskID), "error", perr)
		} else if retryEligible(failureReason, parent) {
			wantRetry = true
			// Persist the reason-aware effective budget into the child so the
			// retry chain self-describes (e.g. provider_network → max_attempts=3),
			// rather than leaking a contradictory attempt=N/max_attempts=2 row.
			retryMaxAttempts = pgtype.Int4{Int32: retryAttemptCeiling(failureReason, parent.MaxAttempts), Valid: true}
			// Defer this attempt when the reason's schedule calls for a backoff
			// (provider_network's final attempt waits ~5s); a zero delay leaves
			// fire_at NULL so the child is created immediately-claimable.
			if delay := retryDelayForAttempt(failureReason, parent.Attempt); delay > 0 {
				retryFireAt = pgtype.Timestamptz{Time: time.Now().Add(delay), Valid: true}
			}
			retryOverlay = parent.RuntimeMcpOverlay
			retryConnectedApps = parent.RuntimeConnectedApps
		}
	}

	var task db.AgentTaskQueue
	var retried *db.AgentTaskQueue
	if err := s.runInTx(ctx, func(qtx *db.Queries) error {
		t, err := qtx.FailAgentTask(ctx, db.FailAgentTaskParams{
			ID:            taskID,
			Error:         pgtype.Text{String: errMsg, Valid: true},
			FailureReason: pgtype.Text{String: failureReason, Valid: failureReason != ""},
			SessionID:     pgtype.Text{String: sessionID, Valid: sessionID != ""},
			WorkDir:       pgtype.Text{String: workDir, Valid: workDir != ""},
			// A failed run can still have produced a branch: worktree mode
			// commits whatever the agent left before tearing the worktree down,
			// precisely so partial work survives. Dropping the name here would
			// leave that commit with no pointer to it.
			BranchName:            pgtype.Text{String: branchName, Valid: branchName != ""},
			SessionRolloutMissing: sessionRolloutMissing,
			RetiredSessionID:      pgtype.Text{String: retiredSessionID, Valid: retiredSessionID != ""},
		})
		if err != nil {
			return err
		}
		task = t

		// Create the retry child atomically with the fail. CreateRetryTask reads
		// the just-failed parent row in the same transaction; broadcast/notify
		// happen after commit.
		if wantRetry {
			child, cerr := qtx.CreateRetryTask(ctx, db.CreateRetryTaskParams{
				ID:                   taskID,
				FireAt:               retryFireAt,
				MaxAttempts:          retryMaxAttempts,
				RuntimeMcpOverlay:    retryOverlay,
				RuntimeConnectedApps: retryConnectedApps,
			})
			if cerr != nil {
				return fmt.Errorf("create retry task: %w", cerr)
			}
			retried = &child
		}

		return nil
	}); err != nil {
		if existing, lookupErr := s.Queries.GetAgentTask(ctx, taskID); lookupErr == nil {
			if errors.Is(err, pgx.ErrNoRows) {
				slog.Info("fail task: already finalized",
					"task_id", util.UUIDToString(taskID),
					"current_status", existing.Status,
					"agent_id", util.UUIDToString(existing.AgentID),
				)
				return &existing, nil
			}
			slog.Warn("fail task failed",
				"task_id", util.UUIDToString(taskID),
				"current_status", existing.Status,
				"issue_id", util.UUIDToString(existing.IssueID),
				"agent_id", util.UUIDToString(existing.AgentID),
				"error", err,
			)
		} else {
			slog.Warn("fail task failed: task not found",
				"task_id", util.UUIDToString(taskID),
				"lookup_error", lookupErr,
			)
		}
		return nil, fmt.Errorf("fail task: %w", err)
	}

	slog.Warn("task failed", "task_id", util.UUIDToString(task.ID), "issue_id", util.UUIDToString(task.IssueID), "error", errMsg, "failure_reason", failureReason)
	s.captureTaskFailed(ctx, task)

	// The auto-retry child (if any) was created inside the transaction above.
	// Surface it now: broadcast
	// queued first, then notify the daemon — see EnqueueTaskForIssue for the
	// ordering rationale. A deferred child (backoff armed via fire_at) is NOT
	// queued yet: PromoteDueDeferredTasksForRuntime emits its queued event and
	// daemon wakeup when fire_at arrives, so announcing it here would be wrong.
	if retried != nil {
		slog.Info("task auto-retry enqueued",
			"parent_task_id", util.UUIDToString(task.ID),
			"child_task_id", util.UUIDToString(retried.ID),
			"reason", failureReason,
			"attempt", retried.Attempt,
			"max_attempts", retried.MaxAttempts,
			"status", retried.Status,
		)
		if retried.Status == "queued" {
			s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, *retried)
			s.NotifyTaskEnqueued(ctx, *retried)
		}
	}

	// Skip the per-failure system comment when we'll immediately retry —
	// the new task will surface its own status to the user, and we don't
	// want to spam the issue with "task timed out" messages on every
	// daemon hiccup.
	if errMsg != "" && task.IssueID.Valid && retried == nil {
		s.createAgentComment(ctx, task.IssueID, task.AgentID, redact.Text(errMsg), "system", task.TriggerCommentID, task.ID)
	}

	// Reconcile agent status
	s.ReconcileAgentStatus(ctx, task.AgentID)

	// A retry-pending attempt stays silent because its child reports the eventual
	// terminal outcome.
	s.broadcastTaskFailedEvent(ctx, task, errMsg, failureReason, retried != nil)

	return &task, nil
}

// retryableReasons enumerates failure reasons that the auto-retry path is
// allowed to act on. Agent-side errors (compile failures, model rejections,
// etc.) are intentionally excluded — those are real problems that the user
// should see, not infrastructure flakiness.
//
// The one agent_error.* exception is provider_network: a mid-stream provider
// disconnect (e.g. Claude Code's "API Error: Connection closed mid-response")
// is transient infrastructure flakiness, not an agent decision, so the
// platform retries it directly (MUL-4910). It is resume-safe (not in
// resumeUnsafeFailureReason), so the retry child inherits the session and
// continues the truncated conversation rather than restarting from scratch.
// skill_bundle_unavailable is retryable for the same reason: the agent process
// never started, so there is nothing to be idempotent about, and every bundle
// that did download is already cached on disk — a retry resumes from there
// instead of re-fetching the whole set (MUL-5370).
var retryableReasons = map[string]bool{
	"runtime_offline":           true,
	"runtime_recovery":          true,
	"timeout":                   true,
	"codex_semantic_inactivity": true,
	string(taskfailure.ReasonAgentProviderNetwork):   true,
	string(taskfailure.ReasonSkillBundleUnavailable): true,
}

// Transient provider stream cuts (provider_network) get a bespoke three-tier
// schedule (MUL-4910): first run + immediate retry + one retry deferred ~5s.
// A blip that survives the immediate retry gets a short cooldown before the
// final attempt instead of firing back-to-back. Every other retryable reason
// keeps the task's generic max_attempts ceiling and retries immediately.
const (
	providerNetworkMaxAttempts    = 3
	providerNetworkFinalRetryWait = 5 * time.Second
)

// retryAttemptCeiling reports how many attempts the auto-retry path allows for
// a failure reason. It only ever WIDENS the task's generic max_attempts, and
// only for reasons with a bespoke schedule; everything else keeps the column's
// value (default 2 = first run + one retry).
//
// max_attempts <= 1 explicitly disables auto-retry (055_task_lease_and_retry.up
// .sql: "1 disables retry"), so it is never overridden — a disabled task must
// not be revived by a raised ceiling. Callers persist this value into the retry
// child (CreateRetryTask's max_attempts) so the row stays self-consistent:
// provider_network's chain records attempt=3, max_attempts=3, not a
// contradictory attempt=3, max_attempts=2 (MUL-4910).
func retryAttemptCeiling(reason string, taskMaxAttempts int32) int32 {
	if taskMaxAttempts <= 1 {
		return taskMaxAttempts
	}
	if reason == string(taskfailure.ReasonAgentProviderNetwork) && taskMaxAttempts < providerNetworkMaxAttempts {
		return providerNetworkMaxAttempts
	}
	return taskMaxAttempts
}

// retryDelayForAttempt reports how long to defer the NEXT attempt after a
// failure at failedAttempt. Only provider_network's final attempt is deferred
// (~5s); every other retry — including provider_network's first — is immediate
// (zero delay → the child is created 'queued', claimable at once). Callers pass
// the returned delay to CreateRetryTask via fire_at.
func retryDelayForAttempt(reason string, failedAttempt int32) time.Duration {
	if reason == string(taskfailure.ReasonAgentProviderNetwork) &&
		failedAttempt >= providerNetworkMaxAttempts-1 {
		return providerNetworkFinalRetryWait
	}
	return 0
}

func resumeUnsafeFailureReason(reason string) bool {
	switch reason {
	// Failures that poison the agent CONVERSATION (not the workdir): resuming
	// the same session would immediately replay the stuck/oversized state.
	// Keep in sync with the GetLastTaskSession resume blacklist.
	// (CreateRetryTask's fresh-session CASE WHEN only needs the
	// subset of these that is also auto-retryable, currently
	// codex_semantic_inactivity.)
	// codex_resume_oversized is the strongest member of this set: a codex
	// rollout only ever grows, so a thread whose resume response already
	// overflowed the reader will overflow on every future attempt too.
	case "iteration_limit", "agent_fallback_message", "api_invalid_request", "codex_semantic_inactivity", "agent_error.context_overflow", "codex_resume_oversized":
		return true
	default:
		return false
	}
}

// ResumeUnsafeFailure reports whether a failed task's agent session must NOT be
// resumed on a retry. It combines the failure_reason poison set
// (resumeUnsafeFailureReason) with the SAME defense-in-depth on raw error text
// that the GetLastTaskSession resume query applies: an
// Anthropic 400 invalid_request_error means the conversation history itself is
// unprocessable even when failure_reason was mis- or un-classified (legacy
// 'agent_error' rows written before MUL-1921, or deploy-window rows). Callers
// that only have a failure_reason (e.g. at fail time) may pass an empty
// errorText.
//
// This is the shared source of truth for the manual-retry claim path, which
// reads the exact source task instead of GetLastTaskSession and would otherwise
// bypass the error-text guard.
func ResumeUnsafeFailure(failureReason, errorText string) bool {
	if resumeUnsafeFailureReason(failureReason) {
		return true
	}
	lower := strings.ToLower(errorText)
	if strings.Contains(lower, "400") && strings.Contains(lower, "invalid_request_error") {
		return true
	}
	// Provider credential-resolution failures are deterministic on resume: the
	// missing api_key / auth_token / auth header is baked into the session's
	// provider state, so a rerun must start fresh instead of replaying the same
	// auth error on the recorded (agent, issue) session. taskfailure.Classify
	// deliberately leaves this error as agent_error.unknown, so this
	// reason-independent text guard is the load-bearing protection for both new
	// and already persisted rows. Keep it in sync with GetLastTaskSession.
	//
	// The phrase itself lives in taskfailure.AuthMethodUnresolved, shared with
	// the daemon's in-turn fresh-session retry gate so the two layers cannot
	// disagree about which errors mean "this session can never be resumed".
	if taskfailure.AuthMethodUnresolved(errorText) {
		return true
	}
	// Same defense-in-depth for the provider-agnostic empty-message shape:
	// a daemon too old to carry classifyPoisonedError's new branch reports
	// agent_error.unknown, and without this the manual-retry path would
	// happily resume the transcript the provider just refused (GH #6066).
	return taskfailure.UnresumableHistory(errorText)
}

// retryEligible reports whether a failed task qualifies for an automatic retry
// attempt: an infrastructure-shaped failure_reason, remaining attempt budget,
// and linked to an ordinary issue. Shared by
// FailTask's in-transaction retry and the orphan sweeper's MaybeRetryFailedTask
// so both agree on which failures re-run.
func retryEligible(failureReason string, t db.AgentTaskQueue) bool {
	return retryableReasons[failureReason] &&
		t.Attempt < retryAttemptCeiling(failureReason, t.MaxAttempts) &&
		!t.OrchestrationRunID.Valid &&
		t.IssueID.Valid
}

// MaybeRetryFailedTask spawns a fresh queued attempt for a recently-failed
// task when the failure was infrastructure-shaped (daemon crash, runtime
// went offline, dispatch/run timeout) and the task hasn't exhausted its
// max_attempts budget. The child task inherits ordinary issue execution
// lineage and runtime metadata. Returns
// the new task, or nil when no retry was created.
func (s *TaskService) MaybeRetryFailedTask(ctx context.Context, parent db.AgentTaskQueue) (*db.AgentTaskQueue, error) {
	if parent.Status != "failed" {
		return nil, nil
	}
	reason := ""
	if parent.FailureReason.Valid {
		reason = parent.FailureReason.String
	}
	if !retryableReasons[reason] {
		return nil, nil
	}
	// Use the reason-aware ceiling, not the raw max_attempts column, so an
	// orphaned provider_network task recovered on its 2nd attempt is still
	// allowed its deferred 3rd attempt (retryAttemptCeiling raises the ceiling
	// to 3). Kept in sync with retryEligible below, which applies the same
	// ceiling to the primary FailTask path.
	if parent.Attempt >= retryAttemptCeiling(reason, parent.MaxAttempts) {
		slog.Info("task auto-retry skipped: budget exhausted",
			"task_id", util.UUIDToString(parent.ID),
			"attempt", parent.Attempt,
			"max_attempts", parent.MaxAttempts,
			"ceiling", retryAttemptCeiling(reason, parent.MaxAttempts),
		)
		return nil, nil
	}
	// A task with no issue link has nowhere to report its retry; retryEligible
	// keeps this sweeper path in sync with FailTask's in-tx retry.
	if !retryEligible(reason, parent) {
		return nil, nil
	}

	// Mirror FailTask's in-tx backoff + effective-budget persistence: defer the
	// final provider_network attempt ~5s via fire_at (zero delay leaves fire_at
	// NULL for an immediate child), and write the reason-aware ceiling into the
	// child's max_attempts so the retry chain stays self-consistent.
	var retryFireAt pgtype.Timestamptz
	if delay := retryDelayForAttempt(reason, parent.Attempt); delay > 0 {
		retryFireAt = pgtype.Timestamptz{Time: time.Now().Add(delay), Valid: true}
	}
	child, err := s.Queries.CreateRetryTask(ctx, db.CreateRetryTaskParams{
		ID:                   parent.ID,
		FireAt:               retryFireAt,
		MaxAttempts:          pgtype.Int4{Int32: retryAttemptCeiling(reason, parent.MaxAttempts), Valid: true},
		RuntimeMcpOverlay:    parent.RuntimeMcpOverlay,
		RuntimeConnectedApps: parent.RuntimeConnectedApps,
	})
	if err != nil {
		slog.Warn("task auto-retry failed",
			"parent_task_id", util.UUIDToString(parent.ID),
			"reason", reason,
			"error", err,
		)
		return nil, err
	}
	slog.Info("task auto-retry enqueued",
		"parent_task_id", util.UUIDToString(parent.ID),
		"child_task_id", util.UUIDToString(child.ID),
		"reason", reason,
		"attempt", child.Attempt,
		"max_attempts", child.MaxAttempts,
		"status", child.Status,
	)
	// A queued child transitions ∅ → queued (same as EnqueueTaskFor*): broadcast
	// queued first, then notify the daemon — see EnqueueTaskForIssue for ordering
	// rationale. A deferred child (backoff armed) stays inert until
	// PromoteDueDeferredTasksForRuntime fires its queued event + wakeup.
	if child.Status == "queued" {
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, child)
		s.NotifyTaskEnqueued(ctx, child)
	}
	return &child, nil
}

// RerunIssue creates a fresh queued task for an agent on the issue. Used by
// the manual rerun endpoint.
//
// Target agent resolution:
//   - sourceTaskID Valid: rerun the agent that ran that task. This is what the
//     execution log retry button uses so a per-row retry survives a subsequent
//     assignee change and re-fires the agent whose row was clicked. The
//     source task's trigger_comment_id is also inherited (when the caller
//     didn't pass one) so a per-row rerun of a comment- or mention-triggered
//     task stays comment-triggered — the daemon's buildCommentPrompt path
//     keys on TriggerCommentID, and losing it would degrade the rerun into
//     a generic issue run that no longer carries the original comment.
//   - sourceTaskID empty: fall back to the issue's current agent assignee. This preserves the CLI / API contract for callers
//     that have an issue ID but no specific task to target.
//
// A retry ALWAYS reuses the source task's workdir when it still exists on
// disk (MUL-4869): a transient failure — network, provider 5xx/rate-limit,
// runtime_offline, timeout, or an auth/quota/config error the user has since
// fixed — should not throw away the work already done. Only the agent SESSION
// is conditionally resumed, and that decision is made later by the daemon claim
// handler from the SOURCE task (via rerun_of_task_id), NOT baked into this row.
// enqueueRerunTask pins force_fresh_session=true so an old claim handler during
// a rolling deploy degrades to a clean start rather than resuming a different
// execution; the new claim handler ignores the flag for reruns and resumes the
// session only when the source failure did not poison the conversation (see
// service.ResumeUnsafeFailure) and the source ran on the same runtime. When the
// dir is objectively unreusable (GC'd, absent on the claiming runtime, or never
// recorded) the daemon falls back to a fresh workdir. Auto-retry of an orphaned
// mid-flight failure (HandleFailedTasks → MaybeRetryFailedTask →
// CreateRetryTask) takes its own path, so MUL-1128's mid-flight resume contract
// is preserved.
//
// ErrRerunInvokeNotAllowed signals that RerunIssue refused to rerun because the
// current operator may not invoke the resolved target agent. The handler maps it
// to a structured 403 (no task was cancelled or created).
var ErrRerunInvokeNotAllowed = errors.New("rerun: operator not allowed to invoke target agent")
var ErrOrchestrationRerunRequiresCommand = errors.New("rerun: orchestration execution must be retried through a Mission command")

// Only tasks belonging to the target agent on this issue are cancelled.
// Tasks owned by other agents on the same issue (e.g. a parallel
// @-mention agent) are left alone — rerun must not collateral-cancel
// them.
//
// canInvoke re-validates that the current operator may invoke the RESOLVED
// target agent, keyed on the historical agent for a task_id rerun and on the
// current assignee/leader otherwise (MUL-4525). It runs AFTER the target is
// resolved but BEFORE any prior task is cancelled or a new one is created, so a
// caller who can see the issue but cannot invoke its private agent cannot use
// rerun as a back door — and a blocked rerun mutates nothing. Pass nil only
// from trusted internal callers (tests, backfill) that have already gated.
func (s *TaskService) RerunIssue(ctx context.Context, issueID pgtype.UUID, sourceTaskID pgtype.UUID, triggerCommentID pgtype.UUID, actorUserID pgtype.UUID, canInvoke func(agent db.Agent) bool) (*db.AgentTaskQueue, error) {
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("load issue: %w", err)
	}

	// Determine the target agent for the rerun.
	var (
		agentID             pgtype.UUID
		coalescedCommentIDs []pgtype.UUID
	)
	if sourceTaskID.Valid {
		sourceTask, err := s.Queries.GetAgentTask(ctx, sourceTaskID)
		if err != nil {
			return nil, fmt.Errorf("load source task: %w", err)
		}
		if !sourceTask.IssueID.Valid || util.UUIDToString(sourceTask.IssueID) != util.UUIDToString(issueID) {
			return nil, fmt.Errorf("source task does not belong to this issue")
		}
		if sourceTask.OrchestrationRunID.Valid {
			return nil, ErrOrchestrationRerunRequiresCommand
		}
		agentID = sourceTask.AgentID
		// Historical routed rows are intentionally rerun as ordinary agent tasks;
		// retired routing metadata is not propagated.
		// Inherit trigger provenance so a per-row rerun of a comment- or
		// mention-triggered task stays a comment-triggered task. Without
		// this the daemon's buildCommentPrompt path is skipped (it keys on
		// TriggerCommentID) and the rerun degrades into a generic issue
		// run that has lost the original comment context. Only override
		// when the caller didn't pass one explicitly.
		if !triggerCommentID.Valid {
			coalescedCommentIDs = append([]pgtype.UUID{}, sourceTask.CoalescedCommentIds...)
			if sourceTask.TriggerCommentID.Valid {
				triggerCommentID = sourceTask.TriggerCommentID
			} else if len(coalescedCommentIDs) > 0 {
				triggerCommentID, coalescedCommentIDs, err = s.promoteNewestSurvivingComment(ctx, coalescedCommentIDs)
				if err != nil {
					return nil, fmt.Errorf("repair source comment plan: %w", err)
				}
			}
		}
	} else {
		switch {
		case issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid:
			agentID = issue.AssigneeID
		default:
			return nil, fmt.Errorf("issue is not assigned to an agent")
		}
	}

	// Re-validate invoke permission on the RESOLVED target before mutating
	// anything (MUL-4525). For a task_id rerun this gates the historical agent,
	// so a since-reassigned issue can't be used to re-fire a private agent the
	// operator may only view. A block fails closed: no prior task is cancelled,
	// no new task is created.
	if canInvoke != nil {
		targetAgent, err := s.Queries.GetAgent(ctx, agentID)
		if err != nil {
			return nil, fmt.Errorf("load target agent: %w", err)
		}
		if !canInvoke(targetAgent) {
			return nil, ErrRerunInvokeNotAllowed
		}
	}

	// Cancel only the target agent's active/queued tasks on this issue.
	cancelled, err := s.Queries.CancelAgentTasksByIssueAndAgent(ctx, db.CancelAgentTasksByIssueAndAgentParams{
		IssueID: issueID,
		AgentID: agentID,
	})
	if err != nil {
		slog.Warn("rerun: cancel prior tasks failed",
			"issue_id", util.UUIDToString(issueID),
			"agent_id", util.UUIDToString(agentID),
			"error", err,
		)
	}
	for _, t := range cancelled {
		s.captureTaskCancelled(ctx, t)
		s.ReconcileAgentStatus(ctx, t.AgentID)
		s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, t)
	}

	// A manual rerun is a NEW direct_human trigger attributed to the rerunning
	// member, not the original run's human (MUL-4302 §5); actorUserID carries them.
	// sourceTaskID is the rerun lineage: it rides the CreateAgentTask insert
	// (rerun_of_task_id) so the queued event / daemon claim never sees a NULL
	// lineage, and it stays distinct from system-retry's retry_of_task_id (§5).
	task, err := s.enqueueRerunTask(ctx, issue, agentID, triggerCommentID, coalescedCommentIDs, actorUserID, sourceTaskID)
	if err != nil {
		return nil, err
	}
	slog.Info("issue rerun enqueued",
		"task_id", util.UUIDToString(task.ID),
		"issue_id", util.UUIDToString(issueID),
		"agent_id", util.UUIDToString(agentID),
		"source_task_id", util.UUIDToString(sourceTaskID),
		"cancelled_prior", len(cancelled),
	)
	return &task, nil
}

// promoteNewestSurvivingComment repairs a manual rerun whose original trigger
// was deleted (the FK clears trigger_comment_id while the UUID-array plan
// survives). Promoting before enqueue lets the normal enqueue path recompute
// originator and user-scoped connected-app capabilities from the real comment,
// rather than carrying the deleted trigger's stale security context.
func (s *TaskService) promoteNewestSurvivingComment(ctx context.Context, ids []pgtype.UUID) (pgtype.UUID, []pgtype.UUID, error) {
	type survivingComment struct {
		id        pgtype.UUID
		createdAt time.Time
	}
	survivors := make([]survivingComment, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !id.Valid {
			continue
		}
		key := util.UUIDToString(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		comment, err := s.Queries.GetComment(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return pgtype.UUID{}, nil, err
		}
		survivors = append(survivors, survivingComment{id: comment.ID, createdAt: comment.CreatedAt.Time})
	}
	if len(survivors) == 0 {
		return pgtype.UUID{}, nil, nil
	}
	newest := 0
	for i := 1; i < len(survivors); i++ {
		if survivors[i].createdAt.After(survivors[newest].createdAt) ||
			(survivors[i].createdAt.Equal(survivors[newest].createdAt) &&
				util.UUIDToString(survivors[i].id) > util.UUIDToString(survivors[newest].id)) {
			newest = i
		}
	}
	remaining := make([]pgtype.UUID, 0, len(survivors)-1)
	for i, comment := range survivors {
		if i != newest {
			remaining = append(remaining, comment.id)
		}
	}
	return survivors[newest].id, remaining, nil
}

// enqueueRerunTask enqueues a fresh task for the given agent on the issue.
// When the target agent is the issue's single-agent assignee we use the
// assignee-driven path (enqueueIssueTask) so the issue-assignee bookkeeping
// stays in sync; otherwise (prior assignee that has since been reassigned or a
// mention agent) we use the mention path.
//
// force_fresh_session is pinned to true on every rerun row on purpose. It is
// the rollback-safe legacy signal: an OLD claim handler (mid rolling deploy)
// gates the whole resume lookup on !force_fresh_session, so it starts clean
// instead of resuming via the (agent, issue) most-recent query — which could
// pick a different execution than the one the user clicked. The NEW claim
// handler ignores this flag for reruns and instead reads the exact source task
// (rerun_of_task_id) to reuse its workdir and, when the failure did not poison
// the conversation, resume its session (MUL-4869).
func (s *TaskService) enqueueRerunTask(ctx context.Context, issue db.Issue, agentID pgtype.UUID, triggerCommentID pgtype.UUID, coalescedCommentIDs []pgtype.UUID, actorUserID pgtype.UUID, rerunOfTaskID pgtype.UUID) (db.AgentTaskQueue, error) {
	if issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid &&
		util.UUIDToString(issue.AssigneeID) == util.UUIDToString(agentID) {
		return s.enqueueIssueTaskWithCommentPlan(ctx, issue, triggerCommentID, coalescedCommentIDs, true, "", actorUserID, rerunOfTaskID)
	}
	return s.enqueueMentionTaskWithCommentPlan(ctx, issue, agentID, triggerCommentID, coalescedCommentIDs, true, "", actorUserID, rerunOfTaskID)
}

// HandleFailedTasks runs the post-failure side effects for a batch of
// freshly-failed tasks: optional auto-retry, task:failed event broadcast,
// agent status reconciliation, and (when an issue has no remaining active
// task and isn't being retried) resetting the issue back to todo so the
// daemon can pick it up again.
//
// All callers that surface a task as failed — sweepers, FailTask,
// recover-orphans — funnel through here so the same UI-consistency
// guarantees apply on every code path.
func (s *TaskService) HandleFailedTasks(ctx context.Context, tasks []db.AgentTaskQueue) int {
	if len(tasks) == 0 {
		return 0
	}

	affectedAgents := make(map[string]pgtype.UUID)
	processedIssues := make(map[string]bool)
	retriedIssues := make(map[string]bool)
	retried := 0

	for _, t := range tasks {
		// Auto-retry first so the issue stays in_progress rather than
		// flapping todo → in_progress within a tick.
		retryPending := false
		if child, _ := s.MaybeRetryFailedTask(ctx, t); child != nil {
			retryPending = true
			retried++
			if t.IssueID.Valid {
				retriedIssues[util.UUIDToString(t.IssueID)] = true
			}
		}

		failureReason := "agent_error"
		if t.FailureReason.Valid && t.FailureReason.String != "" {
			failureReason = t.FailureReason.String
		}
		s.captureTaskFailed(ctx, t)

		workspaceID := ""
		// The Issue backing a TaskNode is a compatibility projection. Its status
		// is written by Orchestrator in the same transaction as Run/TaskNode state;
		// the execution plane must not race that authority by resetting it to todo.
		if t.IssueID.Valid && !t.OrchestrationRunID.Valid {
			if issue, err := s.Queries.GetIssue(ctx, t.IssueID); err == nil {
				workspaceID = util.UUIDToString(issue.WorkspaceID)
				// Reset stuck in_progress issues only when no other active
				// task exists for the issue and no retry was just enqueued.
				issueKey := util.UUIDToString(t.IssueID)
				if issue.Status == "in_progress" && !processedIssues[issueKey] && !retriedIssues[issueKey] {
					processedIssues[issueKey] = true
					hasActive, checkErr := s.Queries.HasActiveTaskForIssue(ctx, t.IssueID)
					if checkErr != nil {
						slog.Warn("handle failed tasks: active check failed",
							"issue_id", issueKey,
							"error", checkErr,
						)
					} else if !hasActive {
						updatedIssue, updateErr := s.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
							ID:          t.IssueID,
							Status:      "todo",
							WorkspaceID: issue.WorkspaceID,
						})
						if updateErr != nil {
							slog.Warn("handle failed tasks: reset stuck issue failed",
								"issue_id", issueKey,
								"error", updateErr,
							)
						} else {
							// This direct reset bypasses the HTTP UpdateIssue
							// handler that normally emits issue:updated, so emit
							// it here too. Without it the board / status-filter
							// caches keep showing the issue as in_progress until
							// the next write touches it (#4648 / MUL-3782).
							s.broadcastIssueUpdated(updatedIssue, issue.Status)
						}
					}
				}
			}
		}
		if workspaceID == "" {
			workspaceID = s.ResolveTaskWorkspaceID(ctx, t)
		}

		s.publishTaskFailedEvent(workspaceID, t, t.Error.String, failureReason, retryPending)

		affectedAgents[util.UUIDToString(t.AgentID)] = t.AgentID
	}

	for _, agentID := range affectedAgents {
		s.ReconcileAgentStatus(ctx, agentID)
	}
	s.notifyTasksFinished(tasks)
	return retried
}

// runInTx executes fn inside a single DB transaction. If TxStarter is nil
// (e.g. some tests construct TaskService directly), fn runs against the
// regular Queries handle without transactional guarantees.
func (s *TaskService) runInTx(ctx context.Context, fn func(*db.Queries) error) error {
	if s.TxStarter == nil {
		return fn(s.Queries)
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReportProgress broadcasts a progress update via the event bus.
func (s *TaskService) ReportProgress(ctx context.Context, taskID string, workspaceID string, summary string, step, total int) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventTaskProgress,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		ActorID:     "",
		TaskID:      taskID,
		Payload: protocol.TaskProgressPayload{
			TaskID:  taskID,
			Summary: summary,
			Step:    step,
			Total:   total,
		},
	})
}

// ReconcileAgentStatus refreshes agent status from the current working task
// set. The query returns no row when the status is already correct, which
// avoids rewriting updated_at and broadcasting a zero-information event.
func (s *TaskService) ReconcileAgentStatus(ctx context.Context, agentID pgtype.UUID) {
	agent, err := s.Queries.RefreshAgentStatusFromTasks(ctx, agentID)
	if err != nil {
		return
	}
	slog.Debug("agent status reconciled", "agent_id", util.UUIDToString(agentID), "status", agent.Status)
	s.publishAgentStatus(agent)
}

func (s *TaskService) updateAgentStatus(ctx context.Context, agentID pgtype.UUID, status string) {
	agent, err := s.Queries.UpdateAgentStatus(ctx, db.UpdateAgentStatusParams{
		ID:     agentID,
		Status: status,
	})
	if err != nil {
		return
	}
	s.publishAgentStatus(agent)
}

func (s *TaskService) publishAgentStatus(agent db.Agent) {
	s.Bus.Publish(events.Event{
		Type:        protocol.EventAgentStatus,
		WorkspaceID: util.UUIDToString(agent.WorkspaceID),
		ActorType:   "system",
		ActorID:     "",
		Payload:     map[string]any{"agent": agentToMap(agent)},
	})
}

// LoadAgentSkills loads an agent's skills with their files for task execution.
func (s *TaskService) LoadAgentSkills(ctx context.Context, agentID pgtype.UUID) []AgentSkillData {
	skills, err := s.Queries.ListAgentSkills(ctx, agentID)
	if err != nil || len(skills) == 0 {
		return nil
	}

	result := make([]AgentSkillData, 0, len(skills))
	for _, sk := range skills {
		data := AgentSkillData{
			ID:          util.UUIDToString(sk.ID),
			Name:        sk.Name,
			Description: sk.Description,
			Content:     sk.Content,
		}
		files, _ := s.Queries.ListSkillFiles(ctx, sk.ID)
		for _, f := range files {
			data.Files = append(data.Files, AgentSkillFileData{Path: f.Path, Content: f.Content})
		}
		result = append(result, data)
	}
	return result
}

// LoadAgentSkillBundles returns every skill visible to an agent, including
// built-ins, with stable bundle hashes and lightweight refs for slim claims.
func (s *TaskService) LoadAgentSkillBundles(ctx context.Context, agentID pgtype.UUID) ([]AgentSkillData, []AgentSkillRefData) {
	skills := s.LoadAgentSkills(ctx, agentID)
	skills = append(skills, s.BuiltinSkills()...)
	return BuildAgentSkillBundles(skills)
}

func BuildAgentSkillBundles(skills []AgentSkillData) ([]AgentSkillData, []AgentSkillRefData) {
	bundles := make([]AgentSkillData, 0, len(skills))
	refs := make([]AgentSkillRefData, 0, len(skills))
	for _, skill := range skills {
		source := skill.Source
		id := skill.ID
		if source == "" {
			if id == "" {
				source = skillbundle.SourceBuiltin
			} else {
				source = skillbundle.SourceWorkspace
			}
		}
		if id == "" && source == skillbundle.SourceBuiltin {
			id = "builtin:" + skill.Name
		}
		skill.Source = source
		skill.ID = id

		files := make([]skillbundle.File, 0, len(skill.Files))
		for _, file := range skill.Files {
			files = append(files, skillbundle.File{Path: file.Path, Content: file.Content})
		}
		manifest := skillbundle.BuildManifest(skillbundle.Skill{
			ID:          skill.ID,
			Source:      skill.Source,
			Name:        skill.Name,
			Description: skill.Description,
			Content:     skill.Content,
			Files:       files,
		})
		skill.Hash = manifest.Hash
		skill.SizeBytes = manifest.SizeBytes
		fileRefsByPath := make(map[string]skillbundle.FileRef, len(manifest.Files))
		for _, file := range manifest.Files {
			fileRefsByPath[file.Path] = file
		}
		for i := range skill.Files {
			if ref, ok := fileRefsByPath[skill.Files[i].Path]; ok {
				skill.Files[i].SHA256 = ref.SHA256
				skill.Files[i].SizeBytes = ref.SizeBytes
			}
		}
		bundles = append(bundles, skill)

		refFiles := make([]AgentSkillFileRefData, 0, len(manifest.Files))
		for _, file := range manifest.Files {
			refFiles = append(refFiles, AgentSkillFileRefData{
				Path:      file.Path,
				SHA256:    file.SHA256,
				SizeBytes: file.SizeBytes,
			})
		}
		refs = append(refs, AgentSkillRefData{
			ID:          skill.ID,
			Source:      skill.Source,
			Name:        skill.Name,
			Description: skill.Description,
			Hash:        manifest.Hash,
			SizeBytes:   manifest.SizeBytes,
			FileCount:   manifest.FileCount,
			Files:       refFiles,
		})
	}
	return bundles, refs
}

// AgentSkillData represents a skill for task execution responses.
type AgentSkillData struct {
	ID          string               `json:"id"`
	Source      string               `json:"source,omitempty"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Hash        string               `json:"hash,omitempty"`
	SizeBytes   int64                `json:"size_bytes,omitempty"`
	Content     string               `json:"content"`
	Files       []AgentSkillFileData `json:"files,omitempty"`
}

// AgentSkillFileData represents a supporting file within a skill.
type AgentSkillFileData struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	SHA256    string `json:"sha256,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

type AgentSkillRefData struct {
	ID          string                  `json:"id"`
	Source      string                  `json:"source"`
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Hash        string                  `json:"hash"`
	SizeBytes   int64                   `json:"size_bytes"`
	FileCount   int                     `json:"file_count"`
	Files       []AgentSkillFileRefData `json:"files,omitempty"`
}

type AgentSkillFileRefData struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

func priorityToInt(p string) int32 {
	switch p {
	case "urgent":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// NotifyTaskEnqueued is the cross-package shim for callers outside
// TaskService that insert a row into agent_task_queue directly. Invalidates
// the empty-claim
// cache and kicks the daemon WS so the new task is claimed without
// waiting for the next poll.
func (s *TaskService) NotifyTaskEnqueued(ctx context.Context, task db.AgentTaskQueue) {
	s.captureTaskQueued(ctx, task)
	s.notifyTaskAvailable(task)
}

// NotifyTaskFinished invalidates a runtime's empty-claim verdict and emits a
// best-effort daemon wakeup after a task reaches a terminal state. The task ID
// is deliberately omitted from the wakeup payload: the completed task itself
// is not available; the hint only means that a queued successor may have
// become claimable because an agent-capacity or serialization barrier cleared.
func (s *TaskService) NotifyTaskFinished(task db.AgentTaskQueue) {
	s.notifyRuntimeMayHaveWork(task.RuntimeID, "")
}

// notifyTasksFinished is the batch form used by bulk terminal transitions.
// Coalesce by runtime so cancelling many tasks on one machine produces one
// cache bump and one websocket hint rather than a burst of identical work.
func (s *TaskService) notifyTasksFinished(tasks []db.AgentTaskQueue) {
	seen := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if !task.RuntimeID.Valid {
			continue
		}
		runtimeKey := util.UUIDToString(task.RuntimeID)
		if _, ok := seen[runtimeKey]; ok {
			continue
		}
		seen[runtimeKey] = struct{}{}
		s.notifyRuntimeMayHaveWork(task.RuntimeID, "")
	}
}

// notifyTaskAvailable runs after a task has been inserted: bumps the
// runtime's invalidation version so any in-flight claim that is about
// to write an "empty" verdict will have it rejected on read, then
// kicks the daemon WS so the daemon claims without waiting for its
// next poll. Order matters — Bump must happen before the wakeup,
// otherwise the wakeup-driven claim could read the still-current
// empty verdict and return null.
func (s *TaskService) notifyTaskAvailable(task db.AgentTaskQueue) {
	s.notifyRuntimeMayHaveWork(task.RuntimeID, util.UUIDToString(task.ID))
}

// notifyRuntimeMayHaveWork is the shared bump-before-wakeup primitive for both
// fresh enqueues and terminal transitions that can unblock queued work.
func (s *TaskService) notifyRuntimeMayHaveWork(runtimeID pgtype.UUID, taskID string) {
	if !runtimeID.Valid {
		return
	}
	runtimeKey := util.UUIDToString(runtimeID)
	// Use a background context: the cache bump / wakeup must outlive
	// the request that created the task, otherwise an early client
	// disconnect could leave the empty verdict in place and stall the
	// just-queued task until the TTL expires. The cache itself bounds
	// every Redis call with a short timeout so a wedged Redis cannot
	// block enqueue.
	s.EmptyClaim.Bump(context.Background(), runtimeKey)
	if s.Wakeup == nil {
		return
	}
	s.Wakeup.NotifyTaskAvailable(runtimeKey, taskID)
}

func (s *TaskService) getIssuePrefix(workspaceID pgtype.UUID) string {
	ws, err := s.Queries.GetWorkspace(context.Background(), workspaceID)
	if err != nil {
		return ""
	}
	return ws.IssuePrefix
}

func (s *TaskService) createAgentComment(ctx context.Context, issueID, agentID pgtype.UUID, content, commentType string, parentID, sourceTaskID pgtype.UUID) {
	if content == "" {
		return
	}
	// Look up issue to get workspace ID for mention expansion and broadcasting.
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return
	}
	// Resolve the thread root for thread-level side effects without overwriting
	// parentID. The stored parent_id must remain the exact comment being replied
	// to; recursive thread reads recover the root when needed.
	var rootComment *db.Comment
	if parentID.Valid {
		if root, err := s.Queries.GetThreadRoot(ctx, db.GetThreadRootParams{
			CommentID:   parentID,
			WorkspaceID: issue.WorkspaceID,
		}); err == nil {
			rootComment = &root
		}
	}
	comment, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:      issueID,
		WorkspaceID:  issue.WorkspaceID,
		AuthorType:   "agent",
		AuthorID:     agentID,
		Content:      content,
		Type:         commentType,
		ParentID:     parentID,
		SourceTaskID: sourceTaskID,
	})
	if err != nil {
		return
	}
	s.CancelDeferredEscalationsForIssueAgent(ctx, issueID, agentID)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload: map[string]any{
			"comment": map[string]any{
				"id":             util.UUIDToString(comment.ID),
				"issue_id":       util.UUIDToString(comment.IssueID),
				"author_type":    comment.AuthorType,
				"author_id":      util.UUIDToString(comment.AuthorID),
				"content":        comment.Content,
				"type":           comment.Type,
				"parent_id":      util.UUIDToPtr(comment.ParentID),
				"source_task_id": util.UUIDToPtr(comment.SourceTaskID),
				"created_at":     comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			},
			"issue_title":  issue.Title,
			"issue_status": issue.Status,
		},
	})
	s.AutoUnresolveThreadOnReply(ctx, rootComment, util.UUIDToString(issue.WorkspaceID), "agent", util.UUIDToString(agentID))
}

// AutoUnresolveThreadOnReply clears resolved_at on the thread root when a
// reply lands in a resolved thread, and broadcasts comment:unresolved. Shared
// between the user-facing Handler.CreateComment path and the agent-facing
// TaskService.createAgentComment path so the resolved-then-replied state can
// never desync (one of the bugs Emacs flagged on PR #2300). Errors are logged
// — the reply itself already committed, the desync is recoverable on next read.
func (s *TaskService) AutoUnresolveThreadOnReply(ctx context.Context, parent *db.Comment, workspaceID, actorType, actorID string) {
	if parent == nil || !parent.ResolvedAt.Valid {
		return
	}
	updated, err := s.Queries.UnresolveComment(ctx, parent.ID)
	if err != nil {
		slog.Warn("auto-unresolve on reply failed", "error", err, "comment_id", util.UUIDToString(parent.ID))
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentUnresolved,
		WorkspaceID: workspaceID,
		ActorType:   actorType,
		ActorID:     actorID,
		Payload: map[string]any{
			"comment": map[string]any{
				"id":               util.UUIDToString(updated.ID),
				"issue_id":         util.UUIDToString(updated.IssueID),
				"author_type":      updated.AuthorType,
				"author_id":        util.UUIDToString(updated.AuthorID),
				"content":          updated.Content,
				"type":             updated.Type,
				"parent_id":        util.UUIDToPtr(updated.ParentID),
				"created_at":       util.TimestampToString(updated.CreatedAt),
				"updated_at":       util.TimestampToString(updated.UpdatedAt),
				"resolved_at":      util.TimestampToPtr(updated.ResolvedAt),
				"resolved_by_type": util.TextToPtr(updated.ResolvedByType),
				"resolved_by_id":   util.UUIDToPtr(updated.ResolvedByID),
			},
		},
	})
}
