<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
  <source media="(prefers-color-scheme: light)" srcset="docs/assets/logo-light.svg">
  <img alt="LieXiu" src="docs/assets/logo-light.svg" width="50">
</picture>

# 列宿 · LieXiu

**A local-first, visual control plane for teams of heterogeneous AI agents.**

[中文](README.zh.md) · [Architecture](docs/diy/正式设计/README.md) · [Wave progress](docs/diy/进度板/14-Wave%204-6实施路线与进度总览.md) · [Self-hosting](SELF_HOSTING.md)

</div>

LieXiu is a deeply customized personal fork of upstream Multica v0.4.26. It is evolving into a
vendor-neutral project management system where Planner, Executor, Reviewer, and Integrator agents
can decompose a mission, coordinate through an explicit task DAG, exchange durable messages, pass
review gates, and deliver an integrated result.

The name comes from the Chinese astronomical idea of stars arranged in ordered stations: many
independent actors, each with a role, moving as one system.

## Product shape

One PostgreSQL-backed execution truth is projected through three complementary surfaces:

- **Project board** — missions, task dependencies, owners, gates, budgets, and delivery state.
- **Pixel world** — a spatial, game-like view of agent activity and collaboration.
- **Agent detail** — prompts, tool calls, transcripts, artifacts, reviews, cost, and failures.

The Project Command Center, Mission board, pixel world, Replay, and Inspector are implemented as
complementary projections of the same execution facts. The pixel world is never a second source
of truth.

## Architecture

- Next.js/React Web and Electron Desktop clients
- Go control plane, daemon, CLI, and WebSocket event transport
- PostgreSQL + pgvector
- Mission DAG, role policy, assignment, run, artifact, review, human-gate, and budget domains
- Multiple agent CLI runtimes, including Codex, Claude Code, Cursor, Kimi, OpenCode, and dsh
- Local/self-hosted execution; runtime and model providers remain replaceable

DeepSeek Harness is treated as one strong runtime and plugin ecosystem inside LieXiu, not as the
product's control plane. Core orchestration and visualization remain owned by LieXiu.

## Current status

The personal-product subtraction, orchestration foundation, real Planner, deterministic role and
Runtime routing, structured collaboration mailbox, formal pixel world, Replay, Project Command
Center, heterogeneous Runtime acceptance, repeatable self-host upgrades, restart recovery,
security boundaries, and operator documentation through Wave 6 are complete. This remains a
personal self-host development baseline, not a published cloud service.

See the [Wave board](docs/diy/进度板/14-Wave%204-6实施路线与进度总览.md)
for the authoritative scope and progress.

## Local development

Requirements: Node.js 20+, pnpm 10.28+, Go 1.26+, and Docker.

```bash
pnpm install
make setup
make dev
```

The default local endpoints are Web `http://localhost:3000`, API `http://localhost:8080`, and
PostgreSQL `localhost:5432`. Configuration lives in `.env`; all LieXiu-specific environment
variables use the `LIEXIU_*` prefix.

Useful checks:

```bash
go -C server build ./...
pnpm --filter @liexiu/core typecheck
pnpm --filter @liexiu/views typecheck
pnpm --filter @liexiu/web typecheck
pnpm --filter @liexiu/desktop typecheck
./scripts/check-brand-identity.sh
```

## Provenance

LieXiu preserves upstream attribution in Git history, LICENSE, NOTICE, immutable migrations, and
the historical research documents. The active product, packages, CLI, Desktop identity, Compose
stack, and database use the LieXiu name.
