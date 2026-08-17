<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
  <source media="(prefers-color-scheme: light)" srcset="docs/assets/logo-light.svg">
  <img alt="LieXiu" src="docs/assets/logo-light.svg" width="50">
</picture>

# 列宿 · LieXiu

**A local-first, visual control plane for teams of heterogeneous AI agents.**

[中文](README.zh.md) · [Architecture](docs/diy/02-%E5%8F%AF%E8%A7%86%E5%8C%96%E5%A4%9A%E6%99%BA%E8%83%BD%E4%BD%93%E9%A1%B9%E7%9B%AE%E7%AE%A1%E7%90%86%E5%B7%A5%E5%85%B7%20DIY%E7%BB%93%E8%AE%BA%E4%B8%8E%E6%9E%B6%E6%9E%84%E5%9F%BA%E7%BA%BF.md) · [Wave progress](docs/diy/08-Wave%20%E5%AE%9E%E6%96%BD%E8%B7%AF%E7%BA%BF%E4%B8%8E%E8%BF%9B%E5%BA%A6%E6%80%BB%E8%A7%88.md) · [Self-hosting](SELF_HOSTING.md)

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

The board and execution control plane are the current implementation focus. The pixel world is a
planned projection of the same event stream, not a second source of truth.

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

The personal-product subtraction waves and the orchestration/data foundation through Wave 3 are
complete. The next gates are real Planner integration, multi-provider runtime collaboration, and
the pixel-world projection. This is an active personal development baseline, not a published cloud
service.

See the [Wave board](docs/diy/08-Wave%20%E5%AE%9E%E6%96%BD%E8%B7%AF%E7%BA%BF%E4%B8%8E%E8%BF%9B%E5%BA%A6%E6%80%BB%E8%A7%88.md)
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
