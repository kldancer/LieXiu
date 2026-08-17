# CLI and Agent Daemon Guide

The `liexiu` CLI connects your local machine to LieXiu. It handles authentication, workspace management, issue tracking, and runs the agent daemon that executes AI tasks locally.

## Installation

### Homebrew (macOS/Linux)

```bash
brew install kailonyang/tap/liexiu
```

### Build from Source

```bash
git clone https://github.com/kailonyang/liexiu.git
cd liexiu
make build
cp server/bin/liexiu /usr/local/bin/liexiu
```

### Update

```bash
brew upgrade kailonyang/tap/liexiu
```

For install script or manual installs, use:

```bash
liexiu update
```

`liexiu update` auto-detects your installation method and upgrades accordingly.

## Quick Start

```bash
# One-command setup: configure, authenticate, and start the daemon
liexiu setup

# For self-hosted (local) deployments:
liexiu setup self-host
```

Or step by step:

```bash
# 1. Authenticate (opens browser for login or local bootstrap)
liexiu login

# 2. Start the agent daemon
liexiu daemon start

# 3. Done — agents in the canonical Workspace can now execute tasks on your machine
```

`liexiu login` resolves the canonical Workspace for the selected server profile and adds it to the daemon watch list.

## Authentication

### Browser Login

```bash
liexiu login
```

Opens your browser for secure login or local owner bootstrap, creates or stores the supported token, and configures the canonical Workspace.

### Token Login

```bash
liexiu login --token <mul_...>
```

Authenticate using a personal access token directly. Useful for headless environments. Pass `--token=` with an empty value to be prompted interactively (so the token never lands in shell history).

### Check Status

```bash
liexiu auth status
```

Shows your current server, user, and token validity.

### Logout

```bash
liexiu auth logout
```

Removes the stored authentication token.

## Agent Daemon

The daemon is the local agent runtime. It detects available AI CLIs on your machine, registers them with the LieXiu server, and executes tasks when agents are assigned work.

### Start

```bash
liexiu daemon start
```

By default, the daemon runs in the background and writes its log into the state
directory of the profile it was started with — **not always `~/.liexiu/`**:

| Profile | State directory |
| --- | --- |
| Default (no `--profile`) | `~/.liexiu/` |
| Named (`--profile <name>`) | `~/.liexiu/profiles/<name>/` |

That directory holds `daemon.log` (the log), `daemon.pid` (the background
daemon's PID), and `daemon.err.log` (raw crash output; near-empty on a healthy
daemon, since normal logging goes to `daemon.log`).

The Desktop app runs its own named profile, so on a machine that has ever run
both, `~/.liexiu/daemon.log` and `~/.liexiu/profiles/<name>/daemon.log` both
exist and both read as plausible logs — only one is being written to. Don't
guess: `liexiu daemon logs` prints the absolute path it resolved (see
[Logs](#logs)).

To run in the foreground (useful for debugging):

```bash
liexiu daemon start --foreground
```

#### Following a replaced binary

A CLI-launched daemon periodically compares its own compile-time version against
the `--version` output of the `liexiu` binary it would re-exec. When they differ
— `brew upgrade liexiu`, a re-download, a local `make build` — it waits for any
running task to finish, then restarts into the new binary. A running task is
never interrupted; if the daemon is busy the restart is deferred to the next
check, and `liexiu daemon status` shows why it's still on the old version.

This is separate from the GitHub self-update poller: disabling that does not stop
the daemon from following a binary you installed yourself. To turn it off:

```bash
LIEXIU_DAEMON_AUTO_RELOAD=0 liexiu daemon start
# or
liexiu daemon start --no-auto-reload
# or persist it
liexiu config set disable_auto_reload true
```

Agent CLIs (codex, claude, ...) are handled differently: when one of them is
upgraded in place, the daemon re-probes its version and re-registers the runtime
**without restarting**, so subsequent tasks pick up the new CLI while LieXiu's
availability stays independent of a third party's release cadence.

Desktop-managed daemons ignore both, because the Desktop app owns its bundled
CLI's lifecycle.

### Stop

```bash
liexiu daemon stop
```

### Status

```bash
liexiu daemon status
liexiu daemon status --output json
```

Shows PID, uptime, detected agents, and watched workspaces.

### Logs

```bash
liexiu daemon logs              # Last 50 lines
liexiu daemon logs -f           # Follow (tail -f)
liexiu daemon logs -n 100       # Last 100 lines
liexiu daemon logs --profile staging
```

Every run first prints the absolute path it resolved, so you always know which
profile's log you are looking at:

```
$ liexiu daemon logs -n 100
Reading /Users/you/.liexiu/profiles/desktop-mbp/daemon.log (profile: desktop-mbp)
...
```

That line goes to stderr, before the tail starts — so it also shows up under
`-f`, and piping or redirecting the command still yields log content only:

```bash
liexiu daemon logs -n 500 | grep ERROR   # the path line is not in the pipe
```

Without `--profile`, the default profile's log is read. If it doesn't exist the
command says so and names the path it looked for, which is the fastest way to
find out that the daemon you care about is running on a different profile —
`liexiu daemon status --profile <name>` confirms which one is live.

### Supported Agents

The daemon auto-detects these AI CLIs on your PATH:

| CLI | Command | Description |
|-----|---------|-------------|
| [Claude Code](https://docs.anthropic.com/en/docs/claude-code) | `claude` | Anthropic's coding agent |
| [Antigravity CLI](https://antigravity.google/docs/cli-install) | `agy` | Google Antigravity CLI |
| [CodeBuddy Code](https://www.codebuddy.ai/docs/cli/quickstart) | `codebuddy` | Tencent CodeBuddy Code (reads `CODEBUDDY.md`, not `CLAUDE.md`) |
| [DevEco Code](https://gitcode.com/openharmony-sig/deveco-code) | `deveco` | OpenHarmony DevEco Code |
| [Codex](https://github.com/openai/codex) | `codex` | OpenAI's coding agent |
| [GitHub Copilot CLI](https://docs.github.com/en/copilot) | `copilot` | GitHub's coding agent (model routed by your GitHub entitlement) |
| OpenCode | `opencode` | Open-source coding agent |
| OpenClaw | `openclaw` | Open-source coding agent |
| Hermes | `hermes` | Nous Research coding agent |
| [Pi](https://pi.dev/) | `pi` | Pi coding agent |
| [Cursor Agent](https://cursor.com/) | `cursor-agent` | Cursor's headless coding agent |
| Kimi | `kimi` | Moonshot coding agent |
| [Reasonix](https://github.com/esengine/DeepSeek-Reasonix) | `reasonix` | DeepSeek-focused ACP coding agent (run `reasonix setup` first) |
| Kiro CLI | `kiro-cli` | Kiro ACP coding agent |
| [Qoder CLI](https://docs.qoder.com/) | `qodercli` | Qoder ACP coding agent |
| [Qoder CN CLI](https://help.aliyun.com/en/lingma/qodercli-cn/product-overview/what-is-qoder-cli-cn) | `qoderclicn` | Qoder CN ACP coding agent |
| [Trae](https://docs.trae.cn/cli) | `traecli` | ByteDance TRAE CLI (ACP via `traecli acp serve`) |
| [Grok Build CLI](https://docs.x.ai/) | `grok` | xAI Grok Build CLI (ACP via `grok agent stdio`) |
| [Qwen Code](https://github.com/QwenLM/qwen-code) | `qwen` | Alibaba Qwen Code (`qwen -p` with stream-json) |
| [QwenPaw](https://github.com/agentscope-ai/QwenPaw) | `qwenpaw` | QwenPaw ACP coding agent (ACP via `qwenpaw acp`; model is fixed by its own configuration) |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | `dsh` | DeepSeek Harness (`dsh --profile liexiu --stdio`; requires the LieXiu runtime profile to be installed; reads AGENTS.md and .dsh/skills/) |

You need at least one installed. The daemon registers each detected CLI as an available runtime.

### How It Works

1. On start, the daemon detects installed agent CLIs and registers a runtime for each agent in each watched workspace
2. It polls the server at a configurable interval (default: 3s) for claimed tasks
3. When a task arrives, it creates an isolated workspace directory, spawns the agent CLI, and streams results back
4. Heartbeats are sent periodically (default: 15s) so the server knows the daemon is alive
5. On shutdown, all runtimes are deregistered

### Configuration

Daemon behavior is configured via flags or environment variables:

| Setting | Flag | Env Variable | Default |
|---------|------|--------------|---------|
| Poll interval | `--poll-interval` | `LIEXIU_DAEMON_POLL_INTERVAL` | `3s` |
| Heartbeat interval | `--heartbeat-interval` | `LIEXIU_DAEMON_HEARTBEAT_INTERVAL` | `15s` |
| Agent timeout | `--agent-timeout` | `LIEXIU_AGENT_TIMEOUT` | `0` (no cap; bounded by the watchdogs) |
| Codex semantic inactivity timeout | `--codex-semantic-inactivity-timeout` | `LIEXIU_CODEX_SEMANTIC_INACTIVITY_TIMEOUT` | `10m` |
| OpenCode idle watchdog | — | `LIEXIU_OPENCODE_IDLE_WATCHDOG` | `10m` (`0` falls back to the generic idle watchdog; cannot extend it) |
| Max concurrent tasks | `--max-concurrent-tasks` | `LIEXIU_DAEMON_MAX_CONCURRENT_TASKS` | `20` |
| Daemon ID | `--daemon-id` | `LIEXIU_DAEMON_ID` | hostname |
| Device name | `--device-name` | `LIEXIU_DAEMON_DEVICE_NAME` | hostname |
| Runtime name | `--runtime-name` | `LIEXIU_AGENT_RUNTIME_NAME` | `Local Agent` |
| Workspaces root | — | `LIEXIU_WORKSPACES_ROOT` | `~/liexiu_workspaces` |
| GC enabled | — | `LIEXIU_GC_ENABLED` | `true` (set `false`/`0` to disable) |
| GC scan interval | — | `LIEXIU_GC_INTERVAL` | `2h` |
| GC TTL (done/cancelled issues) | — | `LIEXIU_GC_TTL` | `24h` |
| GC orphan TTL (no `.gc_meta.json`) | — | `LIEXIU_GC_ORPHAN_TTL` | `72h` |
| GC artifact TTL (completed tasks) | — | `LIEXIU_GC_ARTIFACT_TTL` | `12h` (set `0` to disable) |
| GC artifact patterns | — | `LIEXIU_GC_ARTIFACT_PATTERNS` | `node_modules,.next,.turbo` |
| GC repo cache TTL (`.repos`) | — | `LIEXIU_GC_REPO_TTL` | `720h` (30d; set `0` to disable) |
| GC repo maintenance | — | `LIEXIU_GC_REPO_MAINTENANCE_ENABLED` | `true` (set `false`/`0` to disable heavy Git maintenance only) |
| GC Hermes memory TTL (per-agent `memories/`) | — | `LIEXIU_GC_HERMES_MEMORY_TTL` | `2160h` (90d; set `0` to disable) |
| GC Hermes session TTL (per-conversation `state.db`) | — | `LIEXIU_GC_HERMES_SESSION_TTL` | `336h` (14d; set `0` to disable) |

#### Workspace garbage collection

The daemon periodically scans `LIEXIU_WORKSPACES_ROOT` and reclaims disk space in four modes:

- **Full task cleanup** — when an issue's status is `done` or `cancelled` and has been idle for `LIEXIU_GC_TTL`, the entire task directory is removed.
- **Orphan cleanup** — task directories with no `.gc_meta.json` (e.g. left over from a daemon crash) are removed once they exceed `LIEXIU_GC_ORPHAN_TTL`.
- **Artifact-only cleanup** — when a task has been completed for at least `LIEXIU_GC_ARTIFACT_TTL` but the issue is still open, regenerable build outputs whose directory basename matches `LIEXIU_GC_ARTIFACT_PATTERNS` are removed. The daemon also reclaims the exact managed path `codex-home/.sandbox-bin`; old task metadata without `completed_at` becomes eligible for this managed-only cleanup after its `.gc_meta.json` file has been idle for `LIEXIU_GC_ORPHAN_TTL`. The rest of the task (source, `.git`, `output/`, `logs/`, `.gc_meta.json`, Codex auth/config/session state) is preserved so the agent can resume it.
- **Managed-cache reclamation** — the exact managed path above is reclaimed for *every* task kind once the task has been completed for `LIEXIU_GC_ARTIFACT_TTL`, not just for issue tasks whose issue is still open. It applies even while the parent record says the directory itself must stay — an active chat session, a still-running autopilot run — and even when the parent record could not be reached this cycle, because the contents are regenerable and the next run re-provisions them on demand. A task currently running on the directory is never touched. Set `LIEXIU_GC_ARTIFACT_TTL=0` to disable this along with the rest of artifact cleanup.

- **Repo cache eviction** — the bare git clones under `.repos/` are shared object stores: each task workdir is a `git worktree` off one of them rather than its own clone, so a task's `.git` is only a pointer file. They are evicted only when all of the following hold: the repo is no longer attached to any workspace this daemon watches, it has no worktrees left, and no task has created a worktree from it for `LIEXIU_GC_REPO_TTL`. A cache created before this stamp existed is not treated as ancient — its clock starts at the first GC cycle that sees it, so upgrading does not wipe every cache. Evicting is safe by construction: the next task that needs the repo re-clones it on demand, so a wrong eviction costs a clone, not a failure.

  Short worktree cleanup and eligible cache eviction continue on every GC cycle, including while agents are active. Heavy repo maintenance (`reflog expire` and `git gc`) starts only while the daemon is otherwise idle. A checkout or newly claimed task cancels it and takes priority; interrupted work remains pending for a later idle GC cycle. Operators can disable only these heavy commands with `LIEXIU_GC_REPO_MAINTENANCE_ENABLED=false` without disabling worktree cleanup or cache eviction.

- **Hermes session store reclamation** — a conversation's Hermes transcript (`state.db`) lives at `<profile dir>/hermes-sessions/<agent-id>/<hermes-profile>/<conversation>/`, outside any task directory, so a follow-up turn can resume it (see [Hermes agent memory](#hermes-agent-memory)). A store untouched for `LIEXIU_GC_HERMES_SESSION_TTL` is removed. The default matches the Codex session store rather than the memory store above: these hold full transcripts, and reclaiming an idle one costs a thread that starts fresh (with a continuity notice), not an agent that forgot what it learned. A store a running task holds is never reclaimed.
- **Hermes memory store reclamation** — a Hermes agent's long-term memory (`memories/`) lives at `<profile dir>/hermes-state/<agent-id>/<hermes-profile>/`, outside any task directory, so it survives across tasks and issues (see [Hermes agent memory](#hermes-agent-memory)). A store untouched for `LIEXIU_GC_HERMES_MEMORY_TTL` is removed, giving a deleted agent's memory an eventual-reclamation guarantee. The default is deliberately long: these are a handful of markdown files, and reclaiming one is user-visible amnesia rather than a cache miss. A store a running task holds is never reclaimed.

Configured patterns are basename-only — entries containing `/` or `\` are silently dropped — and `.git` subtrees are never descended into. The managed Codex cache is matched by its exact relative path, so a repository's own `.sandbox-bin` is not removed unless an operator explicitly adds that basename to `LIEXIU_GC_ARTIFACT_PATTERNS`. The default list (`node_modules`, `.next`, `.turbo`) is intentionally narrow; extend it per deployment if your repos consistently produce other regenerable directories (for example, `LIEXIU_GC_ARTIFACT_PATTERNS=node_modules,.next,.turbo,target,__pycache__`). To disable artifact cleanup entirely, including the managed Codex cache, set `LIEXIU_GC_ARTIFACT_TTL=0`.

`liexiu daemon disk-usage` reports the `.repos` footprint on its own line rather than folding it into the per-task totals — every task in a workspace checks out from that shared cache, so attributing it to individual task directories would double-count it. Note that the repo cache is reclaimed on the schedule above and not by any per-issue status change, so it is normal for it to persist after every task directory is gone.

Agent-specific overrides:

| Variable | Description |
|----------|-------------|
| `LIEXIU_CLAUDE_PATH` | Custom path to the `claude` binary |
| `LIEXIU_CLAUDE_MODEL` | Override the Claude model used |
| `LIEXIU_CLAUDE_ARGS` | Default extra arguments for Claude Code runs |
| `LIEXIU_ANTIGRAVITY_PATH` | Custom path to the `agy` binary |
| `LIEXIU_ANTIGRAVITY_MODEL` | Override the Antigravity model used |
| `LIEXIU_CODEBUDDY_PATH` | Custom path to the `codebuddy` binary |
| `LIEXIU_CODEBUDDY_MODEL` | Override the CodeBuddy model used |
| `LIEXIU_CODEBUDDY_ARGS` | Default extra arguments for CodeBuddy runs |
| `LIEXIU_DEVECO_PATH` | Custom path to the `deveco` binary |
| `LIEXIU_DEVECO_MODEL` | Override the DevEco Code model used |
| `LIEXIU_CODEX_PATH` | Custom path to the `codex` binary |
| `LIEXIU_CODEX_MODEL` | Override the Codex model used |
| `LIEXIU_CODEX_ARGS` | Default extra arguments for Codex runs |
| `LIEXIU_COPILOT_PATH` | Custom path to the `copilot` binary |
| `LIEXIU_COPILOT_MODEL` | Override the Copilot model used (note: GitHub Copilot routes models through your account entitlement, so this may not be honoured) |
| `LIEXIU_OPENCODE_PATH` | Custom path to the `opencode` binary |
| `LIEXIU_OPENCODE_MODEL` | Override the OpenCode model used |
| `LIEXIU_OPENCLAW_PATH` | Custom path to the `openclaw` binary |
| `LIEXIU_OPENCLAW_MODEL` | Override the OpenClaw model used |
| `LIEXIU_HERMES_PATH` | Custom path to the `hermes` binary |
| `LIEXIU_HERMES_MODEL` | Override the Hermes model used |
| `LIEXIU_PI_PATH` | Custom path to the `pi` binary |
| `LIEXIU_PI_MODEL` | Override the Pi model used |
| `LIEXIU_CURSOR_PATH` | Custom path to the `cursor-agent` binary |
| `LIEXIU_CURSOR_MODEL` | Override the Cursor Agent model used |
| `LIEXIU_KIMI_PATH` | Custom path to the `kimi` binary |
| `LIEXIU_KIMI_MODEL` | Override the Kimi model used |
| `LIEXIU_REASONIX_PATH` | Custom path to the `reasonix` binary |
| `LIEXIU_REASONIX_MODEL` | Override the Reasonix model used |
| `LIEXIU_KIRO_PATH` | Custom path to the `kiro-cli` binary |
| `LIEXIU_KIRO_MODEL` | Override the Kiro model used |
| `LIEXIU_QODER_PATH` | Custom path to the `qodercli` binary |
| `LIEXIU_QODER_MODEL` | Override the Qoder model used |
| `LIEXIU_QODERCLICN_PATH` | Custom path to the `qoderclicn` binary |
| `LIEXIU_QODERCLICN_MODEL` | Override the Qoder CN model used |
| `LIEXIU_TRAECLI_PATH` | Custom path to the `traecli` binary |
| `LIEXIU_TRAECLI_MODEL` | Override the Trae model used (a model id from your logged-in traecli catalog, e.g. `Doubao-Seed-2.1-Pro`) |
| `LIEXIU_GROK_PATH` | Custom path to the `grok` binary (defaults to `grok` on PATH; often `~/.grok/bin/grok`) |
| `LIEXIU_GROK_MODEL` | Override the Grok model used (e.g. `grok-4.5`) |
| `LIEXIU_QWEN_PATH` | Custom path to the `qwen` binary |
| `LIEXIU_QWEN_MODEL` | Override the Qwen Code model used |
| `LIEXIU_QWEN_ARGS` | Daemon-wide extra Qwen arguments (POSIX shellword parsing; managed protocol flags are filtered) |
| `LIEXIU_QWENPAW_PATH` | Custom path to the `qwenpaw` binary |
| `LIEXIU_QWENPAW_ARGS` | Daemon-wide extra QwenPaw arguments (POSIX shellword parsing; managed protocol flags are filtered) |
| `LIEXIU_DSH_PATH` | Custom path to the `dsh` binary |
| `LIEXIU_DSH_MODEL` | Override the DeepSeek Harness model used (a model id from the dsh catalog, e.g. `deepseek-official/deepseek-chat`) |

If a previously generated `~/.liexiu/hooks` wrapper is first on `PATH` and calls the same command name again, the daemon skips that hooks directory during built-in agent discovery and records the real binary path behind it. If your interactive shell still recurses when you run `claude`, `codex`, or `hermes` manually, remove the hooks entry from your shell startup file or replace the wrapper body with an absolute `exec /path/to/real-binary "$@"`.

The daemon launches Qoder and Qoder CN as `qodercli --yolo --acp` and `qoderclicn --yolo --acp`, respectively, matching their ACP “bypass permissions” mode so tool runs do not block on interactive approval in headless runs.
The daemon launches Qwen Code as `qwen -p <prompt> --output-format stream-json`. It writes the task brief to `QWEN.md`; when an agent has managed `mcp_config`, the daemon writes a 0600 per-run JSON file and passes it through `--mcp-config <path>`, then removes it after the process exits. A null config preserves Qwen Code native MCP settings.

#### `mcp_config` on ACP runtimes

ACP-family runtimes — Hermes, Kimi, Kiro, Grok, Qoder, Reasonix, Trae, Qwenpaw, and any custom runtime profile whose `protocol_family` is one of them — receive MCP servers **over the ACP session protocol**, not through a config file. The daemon translates the agent's `mcp_config` into ACP's `McpServer` array and sends it with `session/new`, and again with that runtime's resume request (`session/resume` on Hermes, Kimi, Qoder and Reasonix; `session/load` on Kiro, Grok, Trae and Qwenpaw) so a resumed task keeps the same tools.

Nothing is written to the runtime's own config file, and the runtime's own file is not read or merged. `~/.hermes/…`, `~/.jcode/mcp.json` and the like stay untouched; an agent's servers travel with its tasks instead of being installed per machine.

Two consequences are worth knowing before debugging a missing MCP tool:

- **`mcp_config` must use the canonical envelope**, `{"mcpServers": {"<name>": {…}}}`. Runtime-native config files that nest servers under `servers`, `mcp`, or `mcp_servers` are stored as-is but yield no servers; the daemon logs a warning naming the key it found. Entries themselves use the Claude-style shape (`command`/`args`/`env` for stdio, `url`/`headers`/`type` for remote).
- **Remote transports depend on what the runtime declares.** ACP v1 requires an omitted capability to be treated as unsupported, so `http` and `sse` entries are dropped with a warning unless the `initialize` response declares `agentCapabilities.mcpCapabilities` with that transport set to true. The built-in Hermes runtime is a verified exception: it declares no `mcpCapabilities` yet accepts both transports, so remote entries are still forwarded to it. That exception covers the Hermes binary only — a custom runtime profile with `protocol_family: hermes` runs a different implementation and keeps the standard rule. Stdio is never gated.

If a configured server produces no tools, check the daemon log for those warnings first, then confirm the runtime itself exposes the server's tools to the model — some ACP adapters apply their own tool-profile filtering after connecting.


The daemon launches QwenPaw as `qwenpaw acp --workspace <per-task dir>`. It writes the task brief to `AGENTS.md`, and materialises the run's bound skills into `<per-task dir>/skills/` plus a `skill.json` manifest, so QwenPaw discovers them through its own workspace skill discovery. `acp` and `--workspace` are reserved: `custom_args` cannot override them. QwenPaw is the one runtime with no `LIEXIU_QWENPAW_MODEL`: its `session/set_model` writes to a shared, persistent agent config rather than the session, so LieXiu never sends it a model and leaves that choice to QwenPaw's own configuration.

#### Hermes agent memory

Hermes discovers skills only from its own home, so binding LieXiu skills to a Hermes agent makes the daemon build a per-task `HERMES_HOME` overlay for that agent. The agent's long-term memory (`memories/`) does **not** live inside that task-scoped overlay: it is linked to a persistent store at

```
<profile dir>/hermes-state/<agent-id>/<hermes-profile>/
```

so the same agent keeps its memory across tasks and issues. `<hermes-profile>` is the profile the agent resolves to (`default`, a named profile from `-p/--profile` or `active_profile`, or a hash for an out-of-tree custom `HERMES_HOME`) — pointing an agent at a different profile gives it a different memory line, matching Hermes' own "a profile is an isolated instance" model.

Consequences worth knowing:

- **Memory is agent-scoped but runtime-local.** One agent's memory is never visible to another, and the user's own `~/.hermes/memories` is never read or written. The store lives in this runtime's LieXiu profile directory, so it does **not** follow the agent to another machine — an agent that runs on two runtimes has a separate memory line on each. Everything else in the home — auth, config, plugins — is still shared from the user's real home by symlink, so the agent does not need its own login.
- **To carry existing local memory in**, copy it into the store once: `cp -R ~/.hermes/memories/. "<profile dir>/hermes-state/<agent-id>/default/"`. To wipe an agent's memory, delete that directory.
- **Conversation history is covered too, in a separate store.** Hermes keeps every ACP session in `<HERMES_HOME>/state.db`, which the overlay links to a per-conversation store at `<profile dir>/hermes-sessions/<agent-id>/<hermes-profile>/<issue-id | chat_\<chat-session-id\>>/`, so a follow-up turn resumes the actual transcript. The shard is per conversation rather than per agent on purpose: tasks of one conversation run one after another, so a shard has a single writer at a time, while two issues never share a database. A host that cannot create the link (Windows without symlink privileges) keeps the database task-local instead, untouched — the link is proven creatable before anything is moved, and a copy is never used, because a copied SQLite database would absorb the turn's writes into a file the next task discards.
- **Concurrent tasks of one agent are last-writer-wins.** Hermes rewrites its memory files whole, so two tasks writing memory at the same time can overwrite each other.
- **Every Hermes agent gets the overlay in practice**, so every one of them gets a persistent memory store. The daemon builds the overlay only when a task carries skills, but the server appends LieXiu's built-in skills to every agent's skill set (`LoadAgentSkillBundles`), so that list is never empty — leaving an agent's own skill list empty does not opt out of the overlay, and is not a way to keep using the host's `~/.hermes/memories`.

`LIEXIU_CLAUDE_ARGS`, `LIEXIU_CODEX_ARGS`, `LIEXIU_CODEBUDDY_ARGS`, `LIEXIU_QWEN_ARGS`, and `LIEXIU_QWENPAW_ARGS` are parsed with POSIX shellword quoting, so values such as `--model "gpt-5.1 codex" --sandbox read-only` are split like a shell command line. Agent arguments are applied in this order: hardcoded LieXiu defaults, daemon-wide env defaults, then per-agent `custom_args` from the task.

### Self-Hosted Server

When connecting to a self-hosted LieXiu instance, the easiest approach is:

```bash
# One command — configures for localhost, authenticates, starts daemon
liexiu setup self-host

# Or for on-premise with custom domains:
liexiu setup self-host --server-url https://api.example.com --app-url https://app.example.com
```

Or configure manually:

```bash
# Set URLs individually
liexiu config set server_url http://localhost:8080
liexiu config set app_url http://localhost:3000

# For production with TLS:
# liexiu config set server_url https://api.example.com
# liexiu config set app_url https://app.example.com

liexiu login
liexiu daemon start
```

### Profiles

Profiles let you run multiple daemons on the same machine — for example, one for production and one for a staging server.

```bash
# Set up a staging profile
liexiu setup self-host --profile staging --server-url https://api-staging.example.com --app-url https://staging.example.com

# Start its daemon
liexiu daemon start --profile staging

# Default profile runs separately
liexiu daemon start
```

Each profile gets its own config directory (`~/.liexiu/profiles/<name>/`), daemon state, health port, and workspace root. Daemon state means that profile's own `daemon.log`, `daemon.err.log`, and `daemon.pid` live in that directory too — see [Start](#start) for the layout, and pass `--profile <name>` to `daemon status` / `daemon logs` to act on it.

## Workspaces

### Canonical Workspace

Self-hosted instances expose one canonical Workspace. Login/bootstrap resolves it through `GET /api/workspaces/canonical`, and the CLI does not enumerate, create, invite, or switch Workspaces.

```bash
liexiu workspace get
liexiu workspace get --output json
liexiu workspace update --name "My Workspace"
liexiu workspace member list
```

`--workspace-id <id>` and `LIEXIU_WORKSPACE_ID` remain low-level scope overrides for compatible headless and daemon commands. They are not a Workspace discovery or switching product flow. Use `--profile <name>` for isolation between server accounts; each profile keeps its own token and daemon state.

## Issues

### List Issues

```bash
liexiu issue list
liexiu issue list --status in_progress
liexiu issue list --priority urgent --assignee "Agent Name"
liexiu issue list --assignee-id 5fb87ac7-23b5-4a7a-81fa-ed295a54545d
liexiu issue list --full-id
liexiu issue list --limit 20 --output json
liexiu issue list --status todo --sort position       # board order (the default)
liexiu issue list --sort created_at --direction desc  # newest first
```

Table output shows a routable issue `KEY` such as `MUL-123`; copy that key into follow-up commands like `issue get`, `issue comment list`, `issue status`, or `--parent`. Add `--full-id` when you need canonical UUIDs. Available filters: `--status`, `--priority`, `--assignee` / `--assignee-id`, `--project`, `--metadata`, `--limit`. Use `--assignee-id <uuid>` for unambiguous filtering when names overlap.

Results come back in board order (`position`, ascending) by default. Pass `--sort` to change the column (`position`, `title`, `created_at`, `start_date`, `due_date`, `priority`) and `--direction asc|desc` to flip the order. `position` is always ascending (it is the manual drag order), so `--direction` is rejected when `--sort` is `position` or omitted — use it only with `title`, `created_at`, `start_date`, `due_date`, or `priority`.

Use `--metadata key=value` (repeatable; combined with AND) to filter by per-issue metadata. The value is JSON-parsed: `true`/`false` become bool, numbers become numbers, anything else is a string. Wrap as `'"42"'` to force a string when the value would otherwise sniff as a number:

```bash
liexiu issue list --metadata pipeline_status=waiting_review
liexiu issue list --metadata pr_number=482 --metadata is_blocked=true
```

### Get Issue

```bash
liexiu issue get <id>
liexiu issue get <id> --output json
```

### Create Issue

```bash
liexiu issue create --title "Fix login bug" --description "..." --priority high --assignee "Lambda"
liexiu issue create --title "Fix login bug" --assignee-id 5fb87ac7-23b5-4a7a-81fa-ed295a54545d
```

Flags: `--title` (required), `--description`, `--status`, `--priority`, `--assignee` / `--assignee-id`, `--parent`, `--project`, `--due-date`. Pass `--assignee-id <uuid>` (mutually exclusive with `--assignee`) when scripting against the IDs returned by `liexiu workspace member list --output json` / `liexiu agent list --output json`.

### Update Issue

```bash
liexiu issue update <id> --title "New title" --priority urgent
liexiu issue update <id> --position 4.5
```

`--position` sets the raw ordering value within the board column (lower sorts first). For relative moves, `issue reorder` is easier because it works out the value for you.

### Reorder Issue

Move an issue within its current status column. The new ordering value is computed the same way the board's drag-and-drop computes it, so the CLI and UI agree on where the issue lands.

```bash
liexiu issue reorder <id> --top              # top of its status column
liexiu issue reorder <id> --bottom           # bottom of its status column
liexiu issue reorder <id> --before <other>   # directly above another issue in the same column
liexiu issue reorder <id> --after  <other>   # directly below another issue in the same column
```

Pick exactly one of `--top`, `--bottom`, `--before`, or `--after`. Reorder stays inside the issue's current column, so `--before` / `--after` must name an issue in that same column. To move an issue to a different column, change its status first with `issue status`, then reorder within the new column.

### Assign Issue

```bash
liexiu issue assign <id> --to "Lambda"
liexiu issue assign <id> --to-id 5fb87ac7-23b5-4a7a-81fa-ed295a54545d
liexiu issue assign <id> --unassign
```

Pass `--to-id <uuid>` to assign by canonical UUID (mutually exclusive with `--to`); useful when names overlap across members and agents.

### Change Status

```bash
liexiu issue status <id> in_progress
```

Valid statuses: `backlog`, `todo`, `in_progress`, `in_review`, `done`, `blocked`, `cancelled`.

### Comments

```bash
# List comments — flat timeline, chronological. Hard cap of 2000 rows; on
# long-running issues prefer one of the thread-aware reads below to keep
# context windows tight.
liexiu issue comment list <issue-id>

# Single thread (root + every descendant). Anchor may be the root itself
# or any reply inside the thread — the server walks up to the root.
liexiu issue comment list <issue-id> --thread <comment-id>

# Single thread, capped to the N most recent replies. The thread root is
# always included (even with --tail 0), so an agent landing on a long
# thread keeps the "what is this about" context without dragging hundreds
# of replies into its prompt.
liexiu issue comment list <issue-id> --thread <comment-id> --tail 30

# Scroll older replies inside the same thread. --before / --before-id are
# the reply cursor that the previous response emitted on stderr as
# `Next reply cursor: --before <ts> --before-id <reply-id>`.
liexiu issue comment list <issue-id> --thread <comment-id> --tail 30 \
    --before <ts> --before-id <reply-id>

# Most recently active threads (root + every descendant), grouped by
# thread. Returns N complete conversational arcs, oldest-active first so
# the freshest thread sits closest to "now" in an agent prompt.
liexiu issue comment list <issue-id> --recent 10

# Scroll older threads. Under --recent, --before / --before-id are a
# THREAD cursor (thread last_activity_at + root id), emitted on stderr as
# `Next thread cursor: --before <ts> --before-id <root-id>`.
liexiu issue comment list <issue-id> --recent 10 \
    --before <ts> --before-id <root-id>

# Incremental polling. Combines with --thread or --recent; filters out
# replies created on or before <ts> from the page (the thread root is
# exempt so the agent always gets context).
liexiu issue comment list <issue-id> --thread <comment-id> --tail 30 \
    --since <RFC3339-timestamp>

# Add a comment
liexiu issue comment add <issue-id> --content "Looks good, merging now"

# Reply to a specific comment
liexiu issue comment add <issue-id> --parent <comment-id> --content "Thanks!"

# Delete a comment
liexiu issue comment delete <comment-id>
```

**`--before` / `--before-id` semantics depend on the paging mode**, by
design — same flag, different scope:

| Mode | What the cursor walks | stderr label |
| --- | --- | --- |
| `--recent N` | Older *threads* (last_activity_at, root_id) | `Next thread cursor` |
| `--thread <id> --tail N` | Older *replies* inside that thread (created_at, id) | `Next reply cursor` |

Outside those two modes (`--thread` without `--tail`, or no `--thread`
and no `--recent`) the cursor flags are rejected so they cannot silently
no-op. The server emits the cursor headers (`X-LieXiu-Next-Before` /
`X-LieXiu-Next-Before-Id`) only when an older page actually exists —
exact-boundary pages (e.g. `--tail 3` on a thread with exactly 3
replies) intentionally return no cursor so callers stop paginating.

When `--since` is combined with `--recent` or `--thread --tail`, the
server additionally suppresses the cursor once the cursor target itself
is older than `since`. Older pages walk strictly older rows, so they
cannot satisfy `> since` either — emitting a cursor there would just
hand back root-only pages until the caller reaches the start of the
thread / issue. Incremental polling stops at the first page whose
cursor target falls before the watermark.

### Metadata

Per-issue metadata is a small KV map agents use to track pipeline state (PR number, pipeline status, waiting_on, ...). Keys match `^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$`, values are primitives (string / number / bool), max 50 keys per issue, blob capped at 8KB.

The bar for writing is high: pin a value only when it is materially important to the issue AND likely to be re-read by future runs on this same issue (the PR URL, the deploy URL, what we're blocked on). Most runs write zero new keys — that's the expected case. Don't pin runtime bookkeeping like `attempts`, single-run investigation notes, large logs, secrets/tokens, or description/comment copies — see the agent runtime prompt for the full anti-pattern list.

```bash
# List every key on an issue
liexiu issue metadata list <issue-id>

# Read a single key
liexiu issue metadata get <issue-id> --key pipeline_status

# Write a single key — value auto-typed (true/false → bool, numbers → number, else string)
liexiu issue metadata set <issue-id> --key pipeline_status --value waiting_review
liexiu issue metadata set <issue-id> --key pr_number --value 482
liexiu issue metadata set <issue-id> --key is_blocked --value true

# Force a specific type when sniffing would pick the wrong one
liexiu issue metadata set <issue-id> --key code --value 42 --type string

# Remove a key
liexiu issue metadata delete <issue-id> --key pipeline_status
```

All writes are single-key atomic — concurrent agents writing different keys do not lose each other's updates. To query, use `liexiu issue list --metadata key=value` (see *List Issues* above).

### Subscribers

```bash
# List subscribers of an issue
liexiu issue subscriber list <issue-id>

# Subscribe yourself to an issue
liexiu issue subscriber add <issue-id>

# Subscribe another member or agent by name
liexiu issue subscriber add <issue-id> --user "Lambda"

# Unsubscribe yourself
liexiu issue subscriber remove <issue-id>

# Unsubscribe another member or agent
liexiu issue subscriber remove <issue-id> --user "Lambda"
```

Subscribers receive notifications about issue activity (new comments, status changes, etc.). Without `--user`, the command acts on the caller.

### Execution History

```bash
# List all execution runs for an issue
liexiu issue runs <issue-id>
liexiu issue runs <issue-id> --full-id
liexiu issue runs <issue-id> --output json

# View messages for a specific execution run
liexiu issue run-messages <task-id>
liexiu issue run-messages <short-task-id> --issue <issue-id>
liexiu issue run-messages <task-id> --output json

# Incremental fetch (only messages after a given sequence number)
liexiu issue run-messages <task-id> --since 42 --output json

# Aggregated token usage for an issue (sum across all its task runs)
liexiu issue usage <issue-id>
liexiu issue usage <issue-id> --output json
```

The `usage` command returns the aggregated token usage for an issue, summed across all of its task runs: input tokens, output tokens, cache read/write tokens, and the run count (`task_count`). It wraps `GET /api/issues/<id>/usage` — the same figures the issue detail view shows. Use `--output json` to feed cost analysis tooling.

The `runs` command shows all past and current executions for an issue, including running tasks. Table output uses short task UUID prefixes by default; pass `--full-id` to print canonical task UUIDs. The `run-messages` command accepts full task UUIDs directly; copied short task prefixes must be scoped with `--issue <issue-id>` so the CLI only checks that issue's runs. It shows the detailed message log (tool calls, thinking, text, errors) for a single run. Use `--since` for efficient polling of in-progress runs.

## Projects

Projects group related issues (e.g. a sprint, an epic, a workstream). Every project
belongs to a workspace and can optionally have a lead (member or agent).

### List Projects

```bash
liexiu project list
liexiu project list --status in_progress
liexiu project list --output json
```

Available filters: `--status`.

### Get Project

```bash
liexiu project get <id>
liexiu project get <id> --output json
```

### Create Project

```bash
liexiu project create --title "2026 Week 16 Sprint" --icon "🏃" --lead "Lambda"
```

Flags: `--title` (required), `--description`, `--status`, `--icon`, `--lead`, `--start-date`, `--due-date`. Dates are calendar days (`YYYY-MM-DD`).

### Update Project

```bash
liexiu project update <id> --title "New title" --status in_progress
liexiu project update <id> --lead "Lambda"
liexiu project update <id> --due-date 2026-04-15
```

Flags: `--title`, `--description`, `--status`, `--icon`, `--lead`, `--start-date`, `--due-date`. For the date flags, pass an empty string (e.g. `--start-date ""`) to clear the date.

### Change Status

```bash
liexiu project status <id> in_progress
```

Valid statuses: `planned`, `in_progress`, `paused`, `completed`, `cancelled`.

### Delete Project

```bash
liexiu project delete <id>
```

### Associating Issues with Projects

Use the `--project` flag on `issue create` / `issue update` to attach an issue to a
project, or on `issue list` to filter issues by project:

```bash
liexiu issue create --title "Login bug" --project <project-id>
liexiu issue update <issue-id> --project <project-id>
liexiu issue list --project <project-id>
```

## Setup

```bash
# One-command setup for LieXiu Cloud: configure, authenticate, and start the daemon
liexiu setup

# For local self-hosted deployments
liexiu setup self-host

# Custom ports
liexiu setup self-host --port 9090 --frontend-port 4000

# On-premise with custom domains
liexiu setup self-host --server-url https://api.example.com --app-url https://app.example.com
```

`liexiu setup` configures the CLI, opens your browser for authentication, and starts the daemon — all in one step. Use `liexiu setup self-host` to connect to a self-hosted server instead of LieXiu Cloud.

## Configuration

### View Config

```bash
liexiu config show
```

Shows config file path, server URL, app URL, and default workspace.

### Set Values

```bash
liexiu config set server_url https://api.example.com
liexiu config set app_url https://app.example.com
liexiu config set workspace_id <workspace-id>
```

`config set workspace_id <id>` is a low-level compatibility override — it writes the value verbatim without checking that the Workspace exists or that you have access. Prefer login/bootstrap and the canonical Workspace endpoint for normal operation; there is no Workspace switching flow.

## Autopilot Commands

Autopilots are scheduled/triggered automations that dispatch agent tasks (either by creating an issue or by running an agent directly).

### List Autopilots

```bash
liexiu autopilot list
liexiu autopilot list --full-id
liexiu autopilot list --status active --output json
```

Autopilot table IDs are short UUID prefixes; follow-up autopilot commands accept copied prefixes when they are unique in the current workspace. Use `--full-id` to print canonical UUIDs.

### Get Autopilot Details

```bash
liexiu autopilot get <id>
liexiu autopilot get <id> --output json   # includes triggers
```

### Create / Update / Delete

```bash
liexiu autopilot create \
  --title "Nightly bug triage" \
  --description "Scan todo issues and prioritize." \
  --agent "Lambda" \
  --mode create_issue \
  --subscriber "Alice"

liexiu autopilot update <id> --status paused
liexiu autopilot update <id> --description "New prompt"
liexiu autopilot update <id> --subscriber "Alice" --subscriber "Bob"
liexiu autopilot update <id> --clear-subscribers
liexiu autopilot delete <id>
```

`--mode` accepts `create_issue` (creates a new issue on each run and assigns it to the agent) or `run_only` (enqueues a direct agent task without creating an issue). `--agent` accepts either a name or UUID.
`--subscriber` accepts a workspace member name or user ID and may be repeated; on update it replaces the autopilot's subscriber template. Subscribers receive inbox notifications for issues created by a `create_issue` autopilot. Use `--clear-subscribers` to remove all autopilot subscribers.

### Manual Trigger

```bash
liexiu autopilot trigger <id>            # Fires the autopilot once, returns the run
```

### Run History

```bash
liexiu autopilot runs <id>
liexiu autopilot runs <id> --limit 50 --output json
```

### Schedule Triggers

```bash
liexiu autopilot trigger-add <autopilot-id> --cron "0 9 * * 1-5" --timezone "America/New_York"
liexiu autopilot trigger-update <autopilot-id> <trigger-id> --enabled=false
liexiu autopilot trigger-delete <autopilot-id> <trigger-id>
```

Only cron-based `schedule` triggers are currently exposed via the CLI. The data model also defines `webhook` and `api` kinds, but there is no server endpoint that fires them yet, so they're not surfaced here.

## Other Commands

```bash
liexiu version              # Show CLI version and commit hash
liexiu update               # Update to latest version
liexiu agent list           # List agents in the current workspace
```

## Output Formats

Most commands support `--output` with two formats:

- `table` — human-readable table (default for list commands)
- `json` — structured JSON (useful for scripting and automation)

```bash
liexiu issue list --output json
liexiu daemon status --output json
```

## Error Messages

The CLI funnels command errors returned to the top-level handler through a
single user-facing translation layer (`server/internal/cli/errors.go`) so that
what you see on the terminal is a short, actionable sentence rather than a raw
Go error, an HTTP status line, or an internal `resolve issue: ...` chain. (A
few commands print their own output or run deliberate fast probes — for example
`setup`'s short `/health` reachability check — and don't go through this
layer.) The underlying detail is still available on demand (see `--debug`).

### What you see

- **Friendly, single-line message.** Transport failures (timeout, DNS,
  connection refused, TLS) and HTTP status failures (401/403/404/409/400·422/
  429/5xx) are each rendered as one clear sentence with a next step — for
  example a timeout suggests checking the network or raising
  `LIEXIU_HTTP_TIMEOUT`, and a 401 tells you to run `liexiu login`.
- **Server-provided validation messages are preserved.** For a 400/422 that
  carries a message from the server, that message is shown verbatim
  (`Invalid request: <server message>`); only when there is none do you get the
  generic "check your values / run with --help" hint.
- **No leaked internals by default.** Raw URLs, status lines, JSON bodies, and
  the internal verb chain are hidden unless you ask for them.

### Language

Messages default to **English**, matching the rest of the CLI's help output.
If a Chinese locale is detected in `LC_ALL`, `LC_MESSAGES`, or `LANG` (in that
precedence order), messages switch to **Chinese**. No flag is needed; set the
locale as usual:

```bash
LANG=zh_CN.UTF-8 liexiu issue get MUL-9999   # 错误信息显示为中文
```

### Exit codes

The process exit code is tiered so scripts can branch on the failure class:

| Exit code | Meaning |
| --- | --- |
| `0` | success |
| `1` | generic / unclassified error |
| `2` | network error (timeout, DNS, connection refused, TLS, offline) |
| `3` | authentication / authorization (HTTP 401, 403) |
| `4` | not found (HTTP 404) |
| `5` | validation (HTTP 400, 422) |

```bash
liexiu issue get MUL-9999
if [ $? -eq 4 ]; then echo "no such issue"; fi
```

### Seeing the full detail (`--debug`)

Pass the global `--debug` flag (or set `LIEXIU_DEBUG=1`) to print the complete
original error chain — the internal verb chain, the request method/path/status,
and the raw server body — underneath the friendly message. Use it when you need
to file a bug or understand exactly what the server returned:

```bash
liexiu issue list --debug
LIEXIU_DEBUG=1 liexiu issue update MUL-1234 --title "x"
```

### Request timeout

API requests use a default timeout of 30 seconds. Override it with
`LIEXIU_HTTP_TIMEOUT` when you are on a slow network; it accepts a Go duration
(`45s`, `2m`) or a plain number of seconds (`45`). Command-level deadlines are
always at least this value, so raising it takes effect across all commands.

```bash
LIEXIU_HTTP_TIMEOUT=60s liexiu issue list
```
