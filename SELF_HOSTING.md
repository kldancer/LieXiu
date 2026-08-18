# Self-Hosting Guide

Deploy LieXiu on your own infrastructure in minutes.

## Architecture

| Component | Description | Technology |
|-----------|-------------|------------|
| **Backend** | REST API + WebSocket server | Go (single binary) |
| **Frontend** | Web application | Next.js 16 |
| **Database** | Primary data store | PostgreSQL 17 with pgvector |

Each user who runs AI agents locally also installs the **`liexiu` CLI** and runs the **agent daemon** on their own machine.

## Quick Install (Recommended)

Two commands to set up everything — server, CLI, and configuration.

<details open>
<summary><b>macOS / Linux</b></summary>

<br/>

```bash
# 1. Install CLI + provision the self-host server
curl -fsSL https://raw.githubusercontent.com/kailonyang/liexiu/main/scripts/install.sh | bash -s -- --with-server

# 2. Configure CLI, authenticate, and start the daemon
liexiu setup self-host
```
</details>
<details>
<summary><b>Windows (PowerShell)</b></summary>

<br/>

```powershell
# 1. Install CLI + provision the self-host server
$env:LIEXIU_MODE="with-server"; irm https://raw.githubusercontent.com/kailonyang/liexiu/main/scripts/install.ps1 | iex

# 2. Configure CLI, authenticate, and start the daemon
liexiu setup self-host
```
</details>

This installs the `liexiu` CLI, checks out the latest self-host assets, pulls the official LieXiu images from GHCR, and configures everything for localhost.

Open http://localhost:3000. The first browser visit uses the local owner bootstrap secret from `.env` and binds the canonical owner and Workspace. No email provider or public signup is required. See [Step 2 — Bootstrap the Owner](#step-2--bootstrap-the-owner) for details.

> **Prerequisites:** Docker and Docker Compose must be installed. The script checks for this and provides install links if missing.
>
> **CLI only?** If the self-host server is already running and you only need the CLI on a macOS/Linux machine, install it with Homebrew:
>
> ```bash
> brew install kailonyang/tap/liexiu
> ```

---

## Step-by-Step Setup (Alternative)

If you prefer to run each step manually:

### Step 1 — Start the Server

**Prerequisites:** Docker and Docker Compose.

```bash
git clone https://github.com/kailonyang/liexiu.git
cd liexiu
make selfhost
```

`make selfhost` automatically creates `.env` from the example, generates random `JWT_SECRET` and `LIEXIU_OWNER_BOOTSTRAP_SECRET` values, and starts all services via Docker Compose.

By default it pulls the latest stable release images from GHCR. To build the backend/web from your current checkout instead, run `make selfhost-build`.
If the selected GHCR tag has not been published yet, `make selfhost` now tells you to fall back to `make selfhost-build`.
`make selfhost-build` uses local `liexiu-backend:dev` / `liexiu-web:dev` tags, so it does not overwrite the pulled `:latest` images.

Once ready:

- **Frontend:** http://localhost:3000
- **Backend API:** http://localhost:8080

> **Note:** If you prefer to run the Docker Compose steps manually, see [Manual Docker Compose Setup](#manual-docker-compose-setup) below.

### Step 2 — Bootstrap the Owner

Set `LIEXIU_OWNER_BOOTSTRAP_SECRET` in `.env` to a random value of at least 32 bytes and keep it in the deployment secret store. `make selfhost` generates one for a new `.env`; if you copied the file manually, create it with `openssl rand -hex 32`.

Open http://localhost:3000 and complete the local bootstrap entry. The first successful bootstrap binds one local owner and one canonical Workspace. Later visits use the normal secure browser session; CLI and daemon authentication use PAT/JWT or daemon credentials.

An existing database is never rewritten to remove or merge Workspaces. If legacy data contains more than one possible owner or Workspace, bootstrap fails closed and requires an explicit operator decision under the local instance contract. Do not solve that state by deleting rows.

### Step 3 — Install CLI & Start Daemon

The daemon runs on your local machine (not inside Docker). It detects installed AI agent CLIs, registers them with the server, and executes tasks when agents are assigned work.

Each team member who wants to run AI agents locally needs to:

### a) Install the CLI and an AI agent

```bash
brew install kailonyang/tap/liexiu
```

You also need at least one AI agent CLI installed:
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`claude` on PATH)
- [Antigravity CLI](https://antigravity.google/docs/cli-install) (`agy` on PATH)
- [CodeBuddy Code](https://www.codebuddy.ai/docs/cli/quickstart) (`codebuddy` on PATH)
- [DevEco Code](https://gitcode.com/openharmony-sig/deveco-code) (`deveco` on PATH)
- [Codex](https://github.com/openai/codex) (`codex` on PATH)
- [GitHub Copilot CLI](https://docs.github.com/en/copilot) (`copilot` on PATH)
- [OpenClaw](https://github.com/openclaw/openclaw) (`openclaw` on PATH)
- [OpenCode](https://github.com/anomalyco/opencode) (`opencode` on PATH)
- [Hermes](https://github.com/NousResearch/hermes) (`hermes` on PATH)
- [Pi](https://pi.dev/) (`pi` on PATH)
- [Cursor Agent](https://cursor.com/) (`cursor-agent` on PATH)
- Kimi (`kimi` on PATH)
- [Reasonix](https://github.com/esengine/DeepSeek-Reasonix) (`reasonix` on PATH; run `reasonix setup` first)
- Kiro CLI (`kiro-cli` on PATH)
- Qoder CLI (`qodercli` on PATH)
- Qoder CN CLI (`qoderclicn` on PATH)
- Trae CLI (`traecli` on PATH)
- [Grok Build CLI](https://docs.x.ai/) (`grok` on PATH)
- Qwen Code (`qwen` on PATH)
- [QwenPaw](https://github.com/agentscope-ai/QwenPaw) (`qwenpaw` on PATH; pick its model in QwenPaw's own configuration)
- [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (`dsh` on PATH with the LieXiu runtime profile installed; set `DEEPSEEK_API_KEY`)

### b) One-command setup

```bash
liexiu setup self-host
```

This automatically:
1. Configures the CLI to connect to `localhost` (ports 8080/3000)
2. Opens your browser for local owner bootstrap or authentication
3. Resolves the canonical Workspace
4. Starts the daemon in the background

For on-premise deployments with custom domains:

```bash
liexiu setup self-host --server-url https://api.example.com --app-url https://app.example.com
```

To verify the daemon is running:

```bash
liexiu daemon status
```

> **Alternative:** If you prefer manual steps, see [Manual CLI Configuration](#manual-cli-configuration) below.

### Step 4 — Verify & Start Using

1. Open the canonical Workspace in the web app at http://localhost:3000
2. Navigate to **Settings → Runtimes** — you should see your machine listed
3. Go to **Settings → Agents** and create a new agent
4. Create an issue and assign it to your agent — it will pick up the task automatically

---

## Usage Dashboard Rollup

The Usage / Runtime dashboards read from a derived `task_usage_hourly` table populated by `rollup_task_usage_hourly()`. As of MUL-2957 the backend runs this rollup **in-process** on every replica via a DB-backed scheduler (`sys_cron_executions`); a fresh self-host install needs no operator action and the bundled `pgvector/pgvector:pg17` image works without changes — you do **not** need to swap it for an image that ships `pg_cron` or register an external scheduler.

Multiple backend replicas are safe: each replica ticks every 30 seconds and tries to claim the current 5-minute UTC plan, but the unique key `(job_name, scope_kind, scope_id, plan_time)` means only one wins each plan. Inspect steady-state operation:

```sql
SELECT plan_time, status, attempt, runner_id,
       error_code, error_msg, started_at, finished_at
  FROM sys_cron_executions
 WHERE job_name = 'rollup_task_usage_hourly'
 ORDER BY plan_time DESC
 LIMIT 20;
```

Full reference (audit table semantics, advisory lock 4246, the standalone backfill command, flag descriptions, the `v0.3.4 → v0.3.5+` migration auto-hook) lives in [Advanced Configuration → Usage Dashboard Rollup](SELF_HOSTING_ADVANCED.md#usage-dashboard-rollup).

> **Upgrading from `v0.3.4` to `v0.3.5+`?** As of MUL-2957 the `migrate up` command runs an idempotent monthly-slice backfill automatically right before applying migration `103`, so the upgrade completes in a single invocation — no operator step required. If you are still on a pre-MUL-2957 binary or the auto-hook fails for an environmental reason, run `backfill_task_usage_hourly` against the same database and re-run the upgrade. See [Advanced Configuration → Usage Dashboard Rollup](SELF_HOSTING_ADVANCED.md#usage-dashboard-rollup) for the recovery flow.

### Compatibility paths (existing deployments only)

External schedulers — **`pg_cron` registered on the database, an external cron job, or a systemd timer** — that call `SELECT rollup_task_usage_hourly()` directly were the only option before MUL-2957 and remain a supported compatibility path. They are no longer the recommended setup; new deployments should rely on the in-process scheduler instead. The SQL function holds advisory lock 4246 internally, so the in-process scheduler and any pre-existing external schedule can coexist without ever double-writing the rollup.

If you already have a `pg_cron` job in production, the safe sequence to retire it is:

1. Confirm the in-process scheduler is healthy on at least one backend replica — recent SUCCESS rows should be landing in `sys_cron_executions` for `rollup_task_usage_hourly`:

   ```sql
   SELECT plan_time, status, runner_id, finished_at
     FROM sys_cron_executions
    WHERE job_name = 'rollup_task_usage_hourly'
      AND status = 'SUCCESS'
    ORDER BY plan_time DESC
    LIMIT 5;
   ```

2. Once SUCCESS rows are arriving on schedule, unschedule the redundant `pg_cron` entry:

   ```sql
   SELECT cron.unschedule('rollup_task_usage_hourly')
     FROM cron.job WHERE jobname = 'rollup_task_usage_hourly';
   ```

3. Leave the `pg_cron` extension itself installed unless you are sure no other workload depends on it. The bundled `pgvector/pgvector:pg17` image does **not** ship `pg_cron`, so nothing in LieXiu's default install needs it; uninstalling `pg_cron` from a custom image that other workloads still use is a separate decision.

External cron / systemd timer setups that call `SELECT rollup_task_usage_hourly()` directly can be retired the same way — once `sys_cron_executions` shows steady SUCCESS rows from the in-process scheduler, the external job is redundant and can be removed.

## Stopping Services

If you installed via the install script:

```bash
curl -fsSL https://raw.githubusercontent.com/kailonyang/liexiu/main/scripts/install.sh | bash -s -- --stop
```

If you cloned the repo manually:

```bash
# Stop the Docker Compose services (backend, frontend, database)
make selfhost-stop

# Stop the local daemon
liexiu daemon stop
```

## Upgrading

Do not upgrade an important instance from `latest`. Pin `LIEXIU_IMAGE_TAG` in `.env` to an exact,
immutable release tag, stop the local daemon and user writes, and create restorable PostgreSQL and
upload backups before the backend starts its forward migrations. Then pull and start that exact
version:

```bash
liexiu daemon stop
docker compose -f docker-compose.selfhost.yml pull
docker compose -f docker-compose.selfhost.yml up -d
curl --fail http://localhost:8080/readyz
```

An application-only rollback is safe only when the previous application supports the migrated
schema. Otherwise restore the pre-upgrade database and uploads together in an isolated, verified
deployment. See [Advanced Configuration — Backup, Restore, Upgrade, and Release Gate](SELF_HOSTING_ADVANCED.md#backup-restore-upgrade-and-release-gate)
for the authoritative maintenance sequence, recovery rules, golden checks, and known limitations.
If the selected GHCR tag has not been published, validate a fixed Git commit with `make selfhost-build`;
do not silently substitute a different image.

> **Upgrading from `v0.3.4` to `v0.3.5+` fails with `refusing to drop legacy daily rollups: ...`?** That's migration `103`'s fail-closed guard: it requires `task_usage_hourly` to be seeded before the legacy daily rollups are dropped. As of MUL-2957 `migrate up` runs that backfill automatically right before applying `103`, so the upgrade completes in a single invocation. If you are still on a pre-MUL-2957 binary or the auto-hook fails, run `backfill_task_usage_hourly` manually first, then re-run the upgrade. Full instructions in [Advanced Configuration → Usage Dashboard Rollup](SELF_HOSTING_ADVANCED.md#usage-dashboard-rollup).

---

## Manual Docker Compose Setup

If you prefer running Docker Compose steps manually instead of `make selfhost`:

```bash
git clone https://github.com/kailonyang/liexiu.git
cd liexiu
cp .env.example .env
```

Edit `.env` — at minimum, change `JWT_SECRET`:

```bash
JWT_SECRET=$(openssl rand -hex 32)
```

Then start everything:

```bash
docker compose -f docker-compose.selfhost.yml pull
docker compose -f docker-compose.selfhost.yml up -d
```

## Manual CLI Configuration

If you prefer configuring the CLI step by step instead of `liexiu setup`:

```bash
# Point CLI to your local server
liexiu config set server_url http://localhost:8080
liexiu config set app_url http://localhost:3000

# Login (opens browser)
liexiu login

# Start the daemon
liexiu daemon start
```

For production deployments with TLS:

```bash
liexiu config set app_url https://app.example.com
liexiu config set server_url https://api.example.com
liexiu login
liexiu daemon start
```

## Advanced Configuration

For environment variables, manual setup (without Docker), reverse proxy configuration, database setup, and more, see the [Advanced Configuration Guide](SELF_HOSTING_ADVANCED.md).
