<div align="center">

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
  <source media="(prefers-color-scheme: light)" srcset="docs/assets/logo-light.svg">
  <img alt="列宿 · LieXiu" src="docs/assets/logo-light.svg" width="50">
</picture>

# 列宿 · LieXiu

**本地优先、可视化、厂商中立的多 AI Agent 团队控制面。**

[English](README.md) · [正式设计](docs/diy/正式设计/README.md) · [Wave 进度](docs/diy/进度板/14-Wave%204-6实施路线与进度总览.md) · [自部署](SELF_HOSTING.md)

</div>

LieXiu 是以上游 Multica v0.4.26 为执行基线、面向个人自用进行深度改造的项目。目标是让规划者、
执行者、审查者、集成者等不同角色的 Agent 围绕一个初始任务完成结构化拆解、显式 DAG 调度、持久通信、
审核返工和最终集成交付，同时可以自由组合 Codex、Claude Code、DeepSeek Harness、Cursor、Kimi 等不同
厂商或本地运行时。

“列宿”取群星列阵、各司其职之意：每个 Agent 都是独立行动者，又共同组成可观察、可控制的协作系统。

## 产品形态

系统只维护一套 PostgreSQL 执行事实，并投影为三个互补界面：

- **项目总看板：** Mission、任务依赖、角色负责人、审核门、预算与交付状态；
- **像素世界：** 以游戏化空间表现 Agent 的活动、等待、通信与协作；
- **角色执行详情：** prompt、工具调用、transcript、Artifact、Review、成本与失败原因。

当前优先完成看板和执行控制面。像素世界是同一事件流的后续可视化投影，不会成为第二套业务状态。

## 技术架构

- Next.js/React Web 与 Electron Desktop
- Go 控制面、daemon、CLI 和 WebSocket 事件通道
- PostgreSQL + pgvector
- Mission DAG、RolePolicy、Assignment、Run、Artifact、Review、Human Gate 与 Budget 领域
- 支持多种 Agent CLI Runtime，包括 Codex、Claude Code、Cursor、Kimi、OpenCode 和 dsh
- 本地/自托管执行，模型和 Runtime 均可替换，不受单一厂商控制

DeepSeek Harness 被定位为 LieXiu 内部的一种强 Runtime 和插件生态，而不是整个产品的控制面；项目编排、
事实模型和三种可视化仍由 LieXiu 自己拥有。

## 当前进度

个人版产品减法、Orchestration 基础和物理 schema 压缩已经推进到 Wave 3 完成。下一阶段是接入真实 Planner、
验证多厂商 Runtime 协作，并实现像素世界投影。当前仓库是持续研发中的个人基线，不代表已经发布线上云服务。

稳定合同维护在[正式设计](docs/diy/正式设计/README.md)，当前范围与进度统一维护在
[Wave 进度板](docs/diy/进度板/14-Wave%204-6实施路线与进度总览.md)。

## 本地开发

依赖 Node.js 20+、pnpm 10.28+、Go 1.26+ 和 Docker。

```bash
pnpm install
make setup
make dev
```

默认本地入口为 Web `http://localhost:3000`、API `http://localhost:8080`、PostgreSQL
`localhost:5432`。配置保存在 `.env`，LieXiu 专属环境变量统一使用 `LIEXIU_*` 前缀。

常用保护命令：

```bash
go -C server build ./...
pnpm --filter @liexiu/core typecheck
pnpm --filter @liexiu/views typecheck
pnpm --filter @liexiu/web typecheck
pnpm --filter @liexiu/desktop typecheck
./scripts/check-brand-identity.sh
```

## 来源保留

Git 历史、LICENSE、NOTICE、不可变 migration 和历史调研文档继续保留上游 Multica 的真实来源；活跃产品、
包、CLI、Desktop、Compose 和数据库统一使用 LieXiu 身份。
