# LieXiu 深度重命名设计与迁移记录

## 1. 决策

本项目的正式品牌为 **列宿 · LieXiu**。名称取“群星列阵、各司其职”之意，对应多个异构 AI Agent
按 Planner、Executor、Reviewer、Integrator 等角色组成团队，通过 Mission DAG 协同，并在项目总看板、
像素世界和执行详情三个视图中呈现同一运行事实。

## 2. 唯一标识合同

| 层级 | 正式标识 |
| --- | --- |
| 中文品牌 | 列宿 |
| 英文品牌 | LieXiu |
| 代码与仓库名 | `liexiu` |
| 计划 GitHub 仓库 | `github.com/kailonyang/liexiu` |
| Go module | `github.com/kailonyang/liexiu/server` |
| pnpm scope | `@liexiu/*` |
| CLI 与二进制 | `liexiu` |
| 环境变量前缀 | `LIEXIU_*` |
| Desktop URL scheme | `liexiu://` |
| Desktop app ID | `io.github.kailonyang.liexiu` |
| Docker Compose、数据库、角色 | `liexiu` |
| 容器镜像 | `ghcr.io/kailonyang/liexiu-backend`、`ghcr.io/kailonyang/liexiu-web` |

## 3. 迁移边界

- 活跃代码、测试、配置、构建、发行、CLI、Desktop、Docker、数据库和用户可见文案全部使用 LieXiu 身份。
- 已应用的数据库 migration 文件和 `schema_migrations` 记录保持不可变；需要修改持久数据时新增 migration。
- Git 历史、迁移前备份、动态验证收据和明确标注的“上游 Multica v0.4.26”溯源不伪造为 LieXiu。
- 新 GitHub 仓库由用户创建；本轮不创建远端、不 commit、不 push。旧远端只保留为 `upstream`。

## 4. 数据安全

用户库在重命名前已升级到 migration 339，并生成完整快照
`.work/backups/multica-before-personal-v1-schema-20260817.dump`。Docker 数据卷迁移采用“复制后切换”，
旧 `multica_pgdata` 保留为回退副本，不执行删除。

## 5. 实际完成状态

2026-08-18 已完成代码、构建、运行时、持久数据和物理目录的深度重命名：

- 项目目录为 `/Users/kailonyang/go/src/liexiu`，Git 分支与既有未提交工作保持完整；
- 原 `origin` 已改为只承载溯源关系的 `upstream`，新 GitHub 远端等待用户建库后再配置；
- 用户数据库、应用角色、Compose project、容器、网络和主数据卷均使用 `liexiu`；
- 旧 `multica_pgdata` 与重命名前完整 dump 均保留。新卷中的旧 bootstrap 角色仅因 PostgreSQL/pgvector
  系统对象所有权而保留，已关闭登录；
- 1 个实验工作区、1 个实验项目、1 个假 Runtime、8 个 Wave 0 工作目录和 18 个测试邮箱已定向改为
  LieXiu 身份。没有新增 migration，也没有改写既有 migration 文件；
- 调研文档和 v0.4.26 设计文档继续以“上游 Multica”记录来源，LICENSE、NOTICE、Git 历史和备份不伪造。

## 6. 完成判定

1. 活跃运行标识残留守卫通过；
2. Go module 全量构建和隔离数据库 compile-only 通过；
3. pnpm lockfile、四包 typecheck、core/Web/views/Desktop 适用测试通过；
4. Compose 配置可解析，LieXiu PostgreSQL 可连接且最新 migration 339、核心数据可见；
5. 新路径上的 Git、Compose 与 Desktop 运行身份一致。

动态命令和测试数量收据见 `.work/tasks/liexiu-deep-rename/receipt.md`。
