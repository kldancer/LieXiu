# Mentioning source map

The runtime contract is intentionally limited to member, agent, issue, and the
`all` sentinel. The parser and trigger behavior are implemented in:

| Contract | Source |
| --- | --- |
| `mention://member/<uuid>`, `mention://agent/<uuid>`, `mention://issue/<uuid>`, and `mention://all/all` parsing | `server/internal/util/mention.go` |
| Agent mention trigger computation and attribution | `server/internal/handler/comment.go` |
| Agent invocation and pending-task checks | `server/internal/handler/agent_access.go`, `server/pkg/db/queries/agent.sql` |
| Runtime prompt mention guidance | `server/internal/daemon/execenv/runtime_config_sections.go` |

Member and issue links render without enqueuing work. An explicit agent mention
may enqueue one ordinary agent task, subject to workspace, invocation, runtime,
and pending-task checks. `@all` is a broadcast sentinel and does not enqueue a
task.
