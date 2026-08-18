---
name: liexiu-collaborating
description: "Use during a LieXiu orchestration run when another Agent or the Owner needs bounded context, a handoff, artifact/review notice, blocker report, or decision request."
user-invocable: false
allowed-tools: Bash(liexiu *)
---

# Collaborating inside a LieXiu Mission

LieXiu collaboration is an asynchronous, Mission-scoped mailbox. It is not a
chat room and it does not directly change Task, Review, Artifact, blocker, or
Owner-decision state. Use the authoritative product command for those facts;
use this tool only to notify the right recipient or request bounded context.

The same command works across Runtime providers:

```bash
liexiu collaborate send \
  --operation request_context \
  --recipient-type agent \
  --recipient-id <agent-uuid> \
  --dedupe-key '<stable-key-for-this-semantic-message>' \
  --payload-file ./collaboration-payload.json \
  --output json
```

The server derives your Workspace, Mission, Run, TaskNode, Agent identity and
accountable human from the task-scoped credential. Never try to put those
identity fields in the payload. The command is unavailable outside an active
daemon-managed orchestration task.

## Frozen operations

- `request_context`: ask an Agent or member for a bounded fact.
- `respond_context`: answer a `context_request`; pass its message id with
  `--reply-to-message-id` and set `--hops` to the request's hops plus one.
- `send_handoff`: tell the next Agent what was completed, what remains, and
  which evidence to inspect.
- `notify_artifact`: notify an Agent about an Artifact created by this Run;
  pass `--artifact-id`.
- `send_review_feedback`: notify an Agent about feedback on the execution
  Artifact under review; pass that Artifact's `--artifact-id`.
- `report_blocker`: notify an Agent or the Owner of a bounded blocker. This
  does not create or resolve the authoritative blocker fact.
- `request_decision`: ask a member (normally the Owner) for a decision. This
  does not resolve a Human Gate by itself.

Use `liexiu agent list --output json` for Agent UUIDs and
`liexiu workspace member list --output json` for member UUIDs. Do not broadcast.
Choose one explicit recipient.

## Payload and retry rules

The payload must be exactly one JSON object. Prefer a UTF-8 file inside the
task workdir so shell quoting cannot rewrite structured content:

```json
{"summary":"Need the canonical API error contract","evidence_refs":["server/internal/handler/mission.go"],"requested_fields":["status","body"]}
```

Use a stable, single-line `--dedupe-key` for the semantic message, for example
`run:<run-id>:context:api-error-contract`. If a network result is uncertain,
retry with the same dedupe key and, when recorded, the same `--command-id`.
The mailbox bounds TTL, payload size, hop count, permissions, and references;
do not work around a rejection with free-form comments or repeated messages.

Mailbox delivery is asynchronous. A sent message is injected into a later
eligible Run context; do not poll indefinitely or pretend an unanswered
request was answered. If the current result can still meet its frozen contract,
finish it. Otherwise return the correct structured result and record the real
blocker through the authoritative workflow.

Source boundaries are mapped in
`references/collaboration-source-map.md`.
