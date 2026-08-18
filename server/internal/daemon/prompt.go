package daemon

import (
	"fmt"
	"strings"

	"github.com/kailonyang/liexiu/server/internal/daemon/execenv"
	"github.com/kailonyang/liexiu/server/pkg/protocol"
)

func sessionContinuityNoticeFor(Task) string { return execenv.SessionContinuityNoticeIssue }

// backendResumeContinuityNotice returns the notice the BACKEND should inject if
// it lands on a fresh thread, or "" when the prompt already carries one.
//
// Only one notice may reach a turn. Two paths can produce it — the daemon,
// which appends it to the prompt whenever it already knows the resume is gone,
// and the backend, which is the only one that can see a live resume RPC being
// rejected mid-run. Before MUL-5722 both fired on the codex overflow retry, so
// the same paragraph was paid for twice in one turn and maintained as two
// hand-written strings. Deriving the backend's copy from the daemon's, and
// suppressing it exactly when the prompt already said it, makes a duplicate
// structurally impossible rather than merely unlikely.
func backendResumeContinuityNotice(task Task) string {
	if task.PriorSessionResumeUnavailable {
		return ""
	}
	return sessionContinuityNoticeFor(task)
}

// Turn-mode markers consumed by the runtime brief's mode router
// (execenv.writeWorkflowIssue). The brief is byte-identical on every run and
// therefore cannot say what triggered this turn; these lines do, and they are
// emitted unconditionally from the same branches BuildPrompt uses to pick a
// path, so the two can never disagree.
//
// Reply mode = respond to the triggering comment, do not touch issue status.
// Ownership mode = an assignment/status change started this run; own the
// status arc. Applying the wrong one silently changes issue status.
const (
	turnModeReply     = "**Turn mode: Reply.** Follow the Reply-mode block in your runtime workflow file for this turn; the Ownership-mode status steps do not apply.\n\n"
	turnModeOwnership = "**Turn mode: Ownership.** Follow the Ownership-mode block in your runtime workflow file for this turn; the Reply-mode rules do not apply.\n\n"
)

// perTurnContextBlocks renders the run-scoped context blocks that used to live
// in the runtime brief (CLAUDE.md / AGENTS.md).
//
// Every value here changes from one run to the next on the same issue — the
// initiator differs whenever another person comments, the continuity notice is
// true of one run and false of the next, and the connected-app set is resolved
// per run from the runtime MCP overlay. Claude Code loads the brief into
// messages[0], ahead of the entire conversation, so rendering these there threw
// away the prompt cache for the whole history on every resume. Appending them
// to the per-turn user message puts them after the cached prefix instead, where
// changing them costs only this turn's own tokens (MUL-5377).
//
// Returns "" when none of the blocks apply.
func perTurnContextBlocks(task Task) string {
	var b strings.Builder
	b.WriteString(buildActiveSiblingRunsBlock(task.IssueID, task.ActiveSiblingRuns))
	if task.PriorSessionResumeUnavailable {
		b.WriteString(sessionContinuityNoticeFor(task))
	}
	b.WriteString(execenv.BuildTaskInitiatorBlock(task.InitiatorType, task.InitiatorName, task.InitiatorEmail))
	b.WriteString(execenv.BuildConnectedAppsBlock(task.ConnectedApps))
	return b.String()
}

func buildActiveSiblingRunsBlock(currentIssueID string, runs []ActiveSiblingRunData) string {
	// Sibling issue work is useful context only for another issue task. Tasks
	// without a current target issue have no claim history to inspect.
	if currentIssueID == "" || len(runs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Active sibling runs\n\n")
	b.WriteString("This agent has other in-flight issue tasks. Before starting overlapping code or PR work, check this issue's comment history for a claim or handoff")
	fmt.Fprintf(&b, " (`liexiu issue comment list %s --roots-only --summary --compact --output json`)", currentIssueID)
	b.WriteString(" and inspect relevant siblings with the `run-messages` commands below — coordinate with existing work instead of opening a second PR. For writes that only record ownership or status of work already underway, use `--no-start` on `liexiu issue assign`/`update`/`status`.\n\n")
	for _, run := range runs {
		issueLabel := run.IssueIdentifier
		if issueLabel == "" {
			issueLabel = run.IssueID
		}
		fmt.Fprintf(&b, "- %s — task `%s`, status `%s`", issueLabel, run.TaskID, run.Status)
		if run.StartedAt != "" {
			fmt.Fprintf(&b, ", started %s", run.StartedAt)
		} else if run.CreatedAt != "" {
			fmt.Fprintf(&b, ", created %s", run.CreatedAt)
		}
		title := strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(run.IssueTitle))
		if title != "" {
			fmt.Fprintf(&b, ": %s", title)
		}
		fmt.Fprintf(&b, "; inspect: `liexiu issue run-messages %s`\n", run.TaskID)
	}
	b.WriteString("\n")
	return b.String()
}

// BuildPrompt constructs the task prompt for an agent CLI.
// Keep this minimal — detailed instructions live in CLAUDE.md / AGENTS.md
// injected by execenv.InjectRuntimeConfig. The provider string is threaded
// through to comment-triggered tasks' per-turn reply template; that template
// is provider-agnostic AND host-agnostic now (every OS → write a UTF-8 file,
// post with `--content-file`) because the shell-layer corruption it guards
// against is not specific to any one provider or host (MUL-2904, #4182).
func BuildPrompt(task Task, provider string) string {
	if task.OrchestrationRun != nil {
		return buildOrchestrationPrompt(*task.OrchestrationRun)
	}
	body := buildPromptBody(task, provider)
	// Run-scoped context is appended, never prepended: everything ahead of it
	// is stable across runs of a resumed session, and appending keeps it after
	// the cached prefix (MUL-5377).
	if blocks := perTurnContextBlocks(task); blocks != "" {
		if !strings.HasSuffix(body, "\n\n") {
			body += "\n"
		}
		body += blocks
	}
	return body
}

// buildOrchestrationPrompt is deliberately provider-neutral. The daemon only
// transports the frozen run input and contract; it does not interpret mission
// state or select a provider-specific workflow.
func buildOrchestrationPrompt(run protocol.OrchestrationRunContextV1) string {
	var b strings.Builder
	b.WriteString("Execute the orchestration task using the frozen input below. Treat it as authoritative and do not invent or widen requirements.\n\n")
	fmt.Fprintf(&b, "Orchestration run: %s\nPurpose: %s\n\nFrozen input (JSON):\n%s\n\n", run.RunID, run.Purpose, strings.TrimSpace(string(run.Input)))
	if instructions := strings.TrimSpace(run.RoleInstructions); instructions != "" {
		fmt.Fprintf(&b, "Frozen role instructions:\n%s\n\n", instructions)
	}
	b.WriteString("Your final stdout must contain exactly one JSON value, with no Markdown fences, prose, commentary, or additional output. The JSON must satisfy this result contract:\n")
	switch run.ResultContract.Kind {
	case protocol.OrchestrationResultKindPlanProposal:
		b.WriteString("Return the canonical PlanProposal JSON object. The only allowed top-level fields are schema_version, mission_id, proposal_key, input, limits, and nodes. Copy mission_id exactly from Frozen input; set schema_version to Frozen input.proposal_schema_version; copy the complete Frozen input.input object verbatim into the top-level input field; and copy the complete Frozen input.limits object verbatim into the top-level limits field. Do not promote nested limit fields such as max_parallel_runs, max_task_attempts, max_rework_cycles, max_tokens, max_cost_usd_ticks, or gate to the top level. Use this exact shape: {\"schema_version\":1,\"mission_id\":\"...\",\"proposal_key\":\"...\",\"input\":{...},\"limits\":{...},\"nodes\":[...]}. Each nodes item must contain only key, title, description, duty, acceptance_criteria, artifact_kinds, depends_on, and budget_estimate. duty must be exactly the lowercase JSON string \"executor\" or \"integrator\"; never use planner, reviewer, execution, integration, title case, or any other value. artifact_kinds must be a JSON array using only the exact lowercase kinds required by the frozen objective, such as \"commit\" and \"final_delivery\". budget_estimate must contain exactly tokens and cost_usd_ticks, both positive integers that obey the Frozen input limits and any more specific frozen objective. When limits.budget.max_tokens or limits.budget.max_cost_usd_ticks is enabled, zero or a missing matching estimate is invalid. Do not wrap the proposal in another object, add fields, or omit fields.\n")
	case protocol.OrchestrationResultKindArtifact:
		b.WriteString("Return exactly: {\"schema_version\":1,\"artifact\":{\"kind\":\"...\",\"uri\":\"...\",\"content_hash\":\"...\",\"summary\":\"...\",\"metadata\":{}}}. artifact.kind must be one of: ")
		b.WriteString(strings.Join(run.ResultContract.AllowedArtifactKinds, ", "))
		b.WriteString(". artifact.uri must be a non-empty string after trimming whitespace and at most 4096 bytes. If the frozen input does not provide an external artifact location, use a stable provider-neutral URI derived from its node_key, for example urn:liexiu:artifact:<kind>:<node_key>; never return an empty URI. artifact.content_hash and artifact.summary must each be strings of at most 4096 bytes, and artifact.metadata must be a JSON object with at most 128 fields. Do not add fields outside this exact shape.\n")
	case protocol.OrchestrationResultKindReviewVerdict:
		b.WriteString("Return exactly: {\"schema_version\":1,\"decision\":\"approved|changes_requested|rejected\",\"evidence\":{},\"requested_changes\":[]}.\n")
	default:
		b.WriteString("The result contract is unsupported; return one JSON object describing the validation error.\n")
	}
	return b.String()
}

func buildPromptBody(task Task, provider string) string {
	if task.TriggerCommentID != "" {
		return buildCommentPrompt(task, provider)
	}
	if task.QuickCreatePrompt != "" {
		return buildQuickCreatePrompt(task)
	}
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a LieXiu workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	b.WriteString(turnModeOwnership)
	// Assignment handoff (MUL-3375): a free-text instruction the person who
	// assigned/promoted this issue left for you. Frame it as a handoff, not a
	// comment to reply to — there is no comment thread to answer here.
	if task.HandoffNote != "" {
		b.WriteString("You were handed this issue with a handoff note. Treat it as the assigner's scoping instruction for this run; follow it before doing anything broader, and do not reply to it as if it were a comment:\n\n")
		fmt.Fprintf(&b, "> %s\n\n", task.HandoffNote)
	}
	fmt.Fprintf(&b, "Start by running `liexiu issue get %s --output json` to understand your task, then complete it.\n", task.IssueID)
	fmt.Fprintf(&b, "For comment history, follow the rule in your runtime workflow file (assignment-triggered tasks treat the read as mandatory). Scan the threads first with `liexiu issue comment list %s --roots-only --summary --compact --output json`, then expand only what matters with `--thread <thread-id> --tail 30`. For `--since` incremental polling, pagination, and folding, see `liexiu issue comment list --help`.\n", task.IssueID)
	return b.String()
}

// buildQuickCreatePrompt constructs a prompt for quick-create tasks. The
// user typed a single natural-language sentence in the create-issue modal;
// the agent's job is to translate it into one `liexiu issue create` CLI
// invocation, using its judgment to decide whether fetching referenced URLs
// would produce a better issue. No issue exists yet, so the agent must NOT
// call `liexiu issue get` or attempt to comment — there's nothing to read
// or reply to.
func buildQuickCreatePrompt(task Task) string {
	var b strings.Builder
	b.WriteString("You are running as a quick-create assistant for a LieXiu workspace.\n\n")
	b.WriteString("A user captured the following input via the quick-create modal. There is NO existing issue. Your job is to create a well-formed issue from this input with a single `liexiu issue create` command.\n\n")
	fmt.Fprintf(&b, "User input:\n> %s\n\n", task.QuickCreatePrompt)

	b.WriteString("Field rules:\n\n")

	// title
	b.WriteString("- **title**: required. A concise but semantically rich summary. If the input references external resources (PRs, issues, URLs), use your judgment on whether fetching the resource would produce a meaningfully better title — e.g. \"review PR #123\" → \"Review PR #123: Refactor auth module to OAuth2\". Strip filler words but preserve key semantic information.\n\n")

	// description — the core optimization
	b.WriteString("- **description**: The description is the executing agent's primary context. Aim for high fidelity — they should grasp the user's intent as if they had read the raw input themselves. Use a two-section structure:\n\n")
	b.WriteString("  1. **User request** — Faithfully restate what the user wants in their own words. Preserve specific names, identifiers, file paths, code snippets, and technical terms verbatim. Strip non-spec material before writing it (this is removal, not paraphrasing): verbal routing wrappers about creating the issue or routing it (e.g. \"create an issue\", \"分配给 X\", \"让 @X 处理\") and pure conversational fillers (e.g. \"对吧？\"). When in doubt, keep it.\n\n")
	b.WriteString("     CC exception: `liexiu issue create` has no `--subscriber` flag, and the platform auto-subscribes members whose `[@Name](mention://member/<uuid>)` link appears in the description. When the user wrote \"cc @Y\", strip the verbal \"cc\" wrapper from the User request body and append a final `CC: <mention link(s)>` line to the description so the cc routing still fires.\n\n")
	b.WriteString("  2. **Context** — include ONLY when the input cited external resources AND you successfully fetched them AND they produced verifiable facts worth recording. Summarize facts only (e.g. \"PR #45 changes auth to JWT\"), not interpretation or unsolicited reference implementations. If you have nothing factual to add, omit the section entirely — never use it as an apology log for resources you could not fetch.\n\n")
	b.WriteString("  Hard rules: never invent requirements, implementation details, or acceptance criteria the user did not express; never reduce multi-sentence input to a single vague sentence; never echo the title.\n\n")
	b.WriteString("  Passing the description: a short, single-line body with no code, quotes, backticks, `$()`, or other special characters may go inline via `--description \"...\"`. Anything multi-line, or containing code snippets / file paths / quotes / backticks / `$()` / special characters, or otherwise long — which quick-create descriptions usually are — MUST be written to `./description.md` and passed with `--description-file ./description.md`; passing rich text inline lets the shell rewrite or truncate it (MUL-2904). That file MUST live inside your current working directory (e.g. `./description.md`) — never `/tmp` or any machine-shared path, where a different run may have left a stale file that would silently become this issue's description. If the file write fails for any reason, stop and fix it; never run `--description-file` against a file whose write did not succeed.\n\n")

	// priority
	if task.QuickCreatePriority != "" {
		fmt.Fprintf(&b, "- **priority**: required for this run. Pass `--priority %s`; the quick-create selection is authoritative.\n\n", task.QuickCreatePriority)
	} else {
		b.WriteString("- **priority**: one of `urgent`, `high`, `medium`, `low`, or omit. Map P0/P1 → urgent/high; \"asap\" → urgent. If unspecified, omit.\n\n")
	}

	// assignee
	b.WriteString("- **assignee**:\n")
	b.WriteString("    - When the user names someone (\"assign to X\" / \"@X\"), call `liexiu workspace member list --output json` and `liexiu agent list --output json` and find the matching entity by display name. On a clean unambiguous match, prefer `--assignee-id <uuid>` using the `user_id` (member) or `id` (agent) from that JSON — UUID matching is exact and robust to name collisions in workspaces with overlapping names. `--assignee <name>` (fuzzy) is acceptable as a fallback when names are unambiguous. On no match or ambiguous match, do NOT pass either flag — instead append a final line to the description: `Unrecognized assignee: X`.\n")
	b.WriteString("    - Treat bare @-routing as an assignee directive even when the user did not write the English word \"assign\". Strip the leading `@`/`＠` before matching display names. Do not keep that routing wrapper or `@Name` in the description unless it is a true CC-style notification rather than ownership.\n")
	agentID := ""
	agentName := ""
	if task.Agent != nil {
		agentID = task.Agent.ID
		agentName = task.Agent.Name
	}
	switch {
	case agentID != "":
		fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to YOURSELF: pass `--assignee-id %q` (your agent UUID). The picker agent is the expected owner because the user opened quick-create with you selected — never leave the issue unassigned. Use the UUID flag, not `--assignee <name>`, so the assignment is unambiguous even when other agents share part of your name.\n\n", agentID)
	case agentName != "":
		fmt.Fprintf(&b, "    - When the user did NOT name an assignee, default to YOURSELF: pass `--assignee %q`. The picker agent is the expected owner because the user opened quick-create with you selected — never leave the issue unassigned.\n\n", agentName)
	default:
		b.WriteString("    - When the user did NOT name an assignee, default to YOURSELF (the picker agent): pass `--assignee-id <your agent UUID>` (preferred) or `--assignee <your agent name>`. Never leave the issue unassigned.\n\n")
	}

	if task.QuickCreateDueDate != "" {
		fmt.Fprintf(&b, "- **due-date**: required for this run. Pass `--due-date %s`; the quick-create selection is authoritative.\n\n", task.QuickCreateDueDate)
	}

	// project — pinned by the modal when the user picked one, otherwise
	// omitted so the platform routes to the workspace default. Always pass
	// the UUID (never a name) so the issue lands in the right project even
	// when several share a title.
	if task.ProjectID != "" {
		if task.ProjectTitle != "" {
			fmt.Fprintf(&b, "- **project**: required for this run. Pass `--project %q` so the new issue lands in project %q (the user picked it in the quick-create modal). Do not infer a different project from the prompt text — the modal selection is authoritative.\n", task.ProjectID, task.ProjectTitle)
		} else {
			fmt.Fprintf(&b, "- **project**: required for this run. Pass `--project %q` so the new issue lands in the project the user picked in the quick-create modal. Do not infer a different project from the prompt text — the modal selection is authoritative.\n", task.ProjectID)
		}
	} else {
		b.WriteString("- **project**: omit. The platform will route the issue to the workspace default.\n")
	}
	// parent — pinned by the modal when the user opened it from "Add sub
	// issue" on an existing issue. Pass the UUID (never the identifier) so
	// the create lands the sub-issue under the right parent even when the
	// workspace prefix changes; the identifier is included in the prose
	// purely as human-readable context for the agent.
	if task.ParentIssueID != "" {
		if task.ParentIssueIdentifier != "" {
			fmt.Fprintf(&b, "- **parent**: required for this run. Pass `--parent %q` so the new issue is filed as a sub-issue of %s (the user opened quick-create from that issue's \"Add sub issue\" entry). Do not infer a different parent from the prompt text — the modal entry point is authoritative.\n", task.ParentIssueID, task.ParentIssueIdentifier)
		} else {
			fmt.Fprintf(&b, "- **parent**: required for this run. Pass `--parent %q` so the new issue is filed as a sub-issue of the parent the user picked in the quick-create modal. Do not infer a different parent from the prompt text — the modal entry point is authoritative.\n", task.ParentIssueID)
		}
	}
	b.WriteString("- **status**: omit (defaults to `todo`).\n")
	b.WriteString("- **attachments**: `--attachment` takes LOCAL file paths, never URLs. Image URLs in the user input are already markdown — keep them inline. Files you produced: see `## Output`.\n\n")

	// output format
	b.WriteString("Output format:\n")
	b.WriteString("- Run exactly one `liexiu issue create --output json` invocation. Do not retry for any reason — even on non-zero exit. The issue may already exist; another attempt would create a duplicate.\n")
	b.WriteString("- Parse the JSON response to read the created issue's `identifier` (preferred) or `id` (fallback). Do not scrape human output and do not assume any workspace issue prefix such as `MUL-`; workspaces can use custom prefixes.\n")
	b.WriteString("- After success, print exactly one line: `Created <identifier-or-id>: <title>` and exit. No commentary, no follow-up tool calls.\n")
	b.WriteString("- Do NOT call `liexiu issue get` or `liexiu issue comment add` — there is no issue to query or comment on.\n")
	b.WriteString("- On CLI error or JSON parse error, exit with the error as the only output. The platform records the failed task outcome automatically.\n")
	return b.String()
}

// buildCommentPrompt constructs a prompt for comment-triggered tasks.
// The triggering comment content is embedded directly so the agent cannot
// miss it, even when stale output files exist in a reused workdir.
// The reply instructions (including the current TriggerCommentID as --parent)
// are re-emitted on every turn so resumed sessions cannot carry forward a
// previous turn's --parent UUID.
func buildCommentPrompt(task Task, provider string) string {
	var b strings.Builder
	b.WriteString("You are running as a local coding agent for a LieXiu workspace.\n\n")
	fmt.Fprintf(&b, "Your assigned issue ID is: %s\n\n", task.IssueID)
	// Mode marker for the brief's router. Emitted unconditionally from the same
	// branch that selects this code path, so the brief and the prompt can never
	// disagree about which mode this turn is in. It must NOT be gated on
	// TriggerCommentContent: an empty comment body (or an older server that
	// doesn't send one) would otherwise leave the turn unlabelled, and the
	// agent would fall through to Ownership mode and change the issue status.
	b.WriteString(turnModeReply)
	if task.TriggerCommentContent != "" {
		authorLabel := "A user"
		if task.TriggerAuthorType == "agent" {
			name := task.TriggerAuthorName
			if name == "" {
				name = "another agent"
			}
			authorLabel = fmt.Sprintf("Another agent (%s)", name)
		}
		fmt.Fprintf(&b, "[NEW COMMENT] %s just left a new comment. Focus on THIS comment — do not confuse it with previous ones:\n\n", authorLabel)
		fmt.Fprintf(&b, "> %s\n\n", task.TriggerCommentContent)
		// MUL-4195: comments that arrived before this run started were folded
		// into it rather than dropped. The trigger above is the newest; the
		// agent must ALSO address these earlier ones so no deliberate user
		// instruction is silently lost. Prefer the embedded detail so the agent
		// does not have to guess which thread each folded comment lives in
		// (they may span multiple threads — review should-fix #3); fall back to
		// a thread-agnostic issue-wide fetch hint for old servers that only send
		// the ids.
		if len(task.CoalescedComments) > 0 {
			fmt.Fprintf(&b, "This run also covers %d earlier comment(s) posted before it started — you must read and address them too, not just the one above. They may be in different threads, so each is reproduced here with its own thread:\n\n", len(task.CoalescedComments))
			for _, cc := range task.CoalescedComments {
				authorLabel := "A user"
				if cc.AuthorType == "agent" {
					name := cc.AuthorName
					if name == "" {
						name = "another agent"
					}
					authorLabel = fmt.Sprintf("Another agent (%s)", name)
				} else if cc.AuthorName != "" {
					authorLabel = cc.AuthorName
				}
				fmt.Fprintf(&b, "- comment %s", cc.ID)
				if cc.CreatedAt != "" {
					fmt.Fprintf(&b, " (%s, %s)", authorLabel, cc.CreatedAt)
				} else {
					fmt.Fprintf(&b, " (%s)", authorLabel)
				}
				if cc.ThreadID != "" {
					fmt.Fprintf(&b, " [thread %s]", cc.ThreadID)
				}
				b.WriteString(":\n")
				fmt.Fprintf(&b, "  > %s\n", strings.ReplaceAll(strings.TrimSpace(cc.Content), "\n", "\n  > "))
			}
			fmt.Fprintf(&b, "\nIf you need the surrounding discussion for any of them, fetch its thread with `liexiu issue comment list %s --thread <thread-id> --tail 30 --compact --output json` using the thread id shown above.\n\n", task.IssueID)
		} else if len(task.CoalescedCommentIDs) > 0 {
			// MUL-5442: this fallback used to send the agent at `--recent 30`.
			// That flag caps THREADS, not comments, and every returned thread
			// carries all of its descendants — so on an issue with fewer than 30
			// root threads it returned the entire comment history to locate a
			// handful of ids. It also contradicted the brief's own catch-up step,
			// which tells the agent to read in two bounded steps and never make
			// one bulk pull (MUL-5372): the platform was recommending exactly the
			// shape it forbids elsewhere.
			//
			// The replacement is a per-id lookup, which is what makes it
			// deterministic: `--thread` accepts ANY comment id, reply or root, and
			// the server resolves it to the containing thread. So each id can be
			// fetched directly and bounded, without knowing its thread and without
			// guessing which threads look recent.
			//
			// `--since` is only a prefetch, never the guarantee. Two ways it can
			// miss an id, so the per-id pass below is unconditional:
			//   - A retry inherits the previous attempt's coalesced_comment_ids
			//     verbatim (queries/agent.sql RetryTask), while the anchor is
			//     recomputed from the last STARTED task's started_at
			//     (GetLastTaskStartedAtForIssueAndAgent). An inherited id can
			//     therefore predate the anchor.
			//   - The anchor is only populated when some comment landed after it,
			//     which is independent of where these ids sit.
			// It is also not a precise fetch in the other direction: the window
			// carries the trigger comment and unrelated comments too.
			fmt.Fprintf(&b, "This run also covers %d earlier comment(s) posted before it started — you must read and address every one of them, not just the one above: %s. They may be in DIFFERENT threads, so do not assume they share the triggering thread.\n\n",
				len(task.CoalescedCommentIDs), strings.Join(task.CoalescedCommentIDs, ", "))
			if task.NewCommentsSince != "" {
				fmt.Fprintf(&b, "Start with `liexiu issue comment list %s --since %s --compact --output json`. Treat that as a candidate window, not a guarantee — it also carries unrelated comments, and a retried run can carry ids older than the window. Check every id above against the result.\n\n",
					task.IssueID, task.NewCommentsSince)
			}
			fmt.Fprintf(&b, "Fetch each id you still need directly: `liexiu issue comment list %s --thread <comment-id> --tail 30 --compact --output json`. `--thread` accepts a reply id, not just a thread root, so you do not need to know which thread the comment lives in. If it is older than those 30 replies, page back with the `Next reply cursor` values (`--before` / `--before-id`) until it appears. Do not finish this turn until every id above is accounted for.\n\n",
				task.IssueID)
		}
	}
	fmt.Fprintf(&b, "Start by running `liexiu issue get %s --output json` to understand your task, then decide how to proceed.\n\n", task.IssueID)
	// Comment-reading pointer. Warm path with new comments: issue-wide
	// since-delta count, but steer the agent to read the triggering thread
	// first. Warm resumed path with no new comments: the trigger is already
	// injected, so don't force a duplicate thread read. Cold path: read the
	// triggering thread, not the flat timeline. Final fallback (no trigger id,
	// shouldn't happen here): plain read.
	if hint := execenv.BuildNewCommentsHint(task.IssueID, task.TriggerCommentID, task.TriggerThreadID, task.NewCommentsSince, task.NewCommentCount); hint != "" {
		b.WriteString(hint)
	} else if task.PriorSessionID != "" {
		b.WriteString(execenv.BuildResumedCommentsHint(task.IssueID, task.TriggerCommentID, task.TriggerThreadID))
	} else if cold := execenv.BuildColdCommentsHint(task.IssueID, task.TriggerCommentID, task.TriggerThreadID); cold != "" {
		b.WriteString(cold)
	} else {
		fmt.Fprintf(&b, "Read the discussion: scan with `liexiu issue comment list %s --roots-only --summary --compact --output json`, then expand what matters with `--thread <thread-id> --tail 30`.\n\n", task.IssueID)
	}
	// Reply routing. When this run coalesced comments spanning MORE THAN ONE
	// root thread, answer each thread in its own thread instead of dumping one
	// merged comment (MUL-4348). Same-thread follow-ups collapse to a single
	// group upstream, so they keep the ordinary single-parent path below and can
	// never be split into duplicate replies.
	if targets := commentReplyThreads(task); len(targets) >= 2 {
		b.WriteString(execenv.BuildMultiThreadCommentReplyInstructions(task.IssueID, targets))
	} else {
		b.WriteString(execenv.BuildCommentReplyInstructions(provider, task.IssueID, task.TriggerCommentID))
	}
	return b.String()
}

// commentReplyThreads groups this run's trigger + coalesced comments by their
// root thread, in first-seen order (coalesced comments oldest-first, the newest
// trigger last). A run that coalesced several @mentions from the SAME thread
// yields a single group, so same-thread follow-ups get exactly one consolidated
// reply and can never be split into duplicates; comments from different root
// threads yield one group each so the agent replies inside each thread instead
// of merging them into one blob (MUL-4348).
//
// The reply for each thread targets the NEWEST comment that triggered this run
// in that thread (coalesced comments arrive oldest-first and the trigger is the
// newest overall, so a simple last-write-wins yields the newest per thread).
// That nests the answer next to the most recent question in the thread rather
// than at the thread root, and makes the trigger's own thread (--parent =
// trigger comment) consistent with every other thread instead of a special
// case. Returns nil when there is no trigger or only a single distinct thread —
// the caller then keeps the existing single-parent reply path unchanged.
func commentReplyThreads(task Task) []execenv.ThreadReplyTarget {
	if task.TriggerCommentID == "" {
		return nil
	}
	// A comment with no explicit thread id is a root comment: it is its own
	// thread, so fall back to the comment id itself as the thread key.
	threadKey := func(threadID, commentID string) string {
		if threadID != "" {
			return threadID
		}
		return commentID
	}

	order := make([]string, 0, len(task.CoalescedComments)+1)
	parentByThread := make(map[string]string, len(task.CoalescedComments)+1)
	// note records first-seen order but lets the newest comment win the reply
	// target: inputs are chronological (coalesced oldest-first, trigger last),
	// so the last write for a thread is its newest triggering comment.
	note := func(threadID, parentID string) {
		if _, ok := parentByThread[threadID]; !ok {
			order = append(order, threadID)
		}
		parentByThread[threadID] = parentID
	}

	// Coalesced (older) comments first: reply under the specific comment that
	// mentioned the agent, not the thread root, so a mid-thread mention gets its
	// answer next to the question.
	for _, cc := range task.CoalescedComments {
		note(threadKey(cc.ThreadID, cc.ID), cc.ID)
	}
	// The newest trigger last: it always wins its own thread's reply target,
	// overriding any earlier coalesced comment that shared the trigger's thread.
	note(threadKey(task.TriggerThreadID, task.TriggerCommentID), task.TriggerCommentID)

	if len(order) <= 1 {
		return nil
	}
	targets := make([]execenv.ThreadReplyTarget, 0, len(order))
	for _, tid := range order {
		targets = append(targets, execenv.ThreadReplyTarget{ThreadID: tid, ParentID: parentByThread[tid]})
	}
	return targets
}
