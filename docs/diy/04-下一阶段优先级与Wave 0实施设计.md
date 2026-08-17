# 下一阶段优先级与 Wave 0 实施设计

## 1. 文档定位

本文承接以下三份文档，将已经形成的产品与架构结论转化为下一阶段可执行的优先级、交付物和进入门槛：

- [01-个人 DIY 深度技术调研与架构方案](../调研/01-可视化多AIAgent项目管理工具个人DIY深度技术调研与架构方案.md)
- [02-DIY 结论与架构基线](02-可视化多智能体项目管理工具%20DIY结论与架构基线.md)
- [03-上游 Multica v0.4.26 功能边界与 LieXiu 减法设计](03-上游Multica%20v0.4.26功能边界与LieXiu减法设计.md)

前三份文档分别回答“为什么选择这条路线”“目标系统如何成立”和“哪些既有能力需要保留、改造或删除”。
本文回答“现在先做什么、为什么按这个顺序做、每一步交付什么，以及什么时候可以安全进入大规模改造”。

本文是研发阶段设计，不是一次性对话纪要。实施进度、命令输出、临时失败和测试收据不写入本文；它们应
进入对应任务或临时工作记录。若优先级或阶段门槛发生稳定变化，应直接更新本文的当前结论。

当前 Wave、工作包状态和下一关键路径统一见
[08-Wave 实施路线与进度总览](./08-Wave%20实施路线与进度总览.md)，本文不重复维护动态完成状态。

## 2. 总体结论

下一阶段应完成 Wave 0：把上游 Multica `v0.4.26` 固化为可重复启动、可验证、可安全裁剪的个人研发基线。

此时不应优先进行以下工作：

- 不先实现像素世界的正式美术、地图和复杂动画。
- 不先大规模删除 Chat、Autopilot、Squad 或 AgentTask 相关代码。
- 不先开发 deepseek-harness 插件生态或把项目控制面迁入 dsh。
- 不先引入复杂事件总线、微服务、ECS、向量数据库或 A2A。
- 不先做品牌替换，因为品牌不验证任务闭环和执行可靠性。

正确顺序是先建立回退点和运行基线，再保护准备复用的执行内核，然后冻结 MVP 合同、建立最小垂直闭环，
最后按依赖闭包进入产品减法。

```mermaid
flowchart LR
    B["固定研发分支与版本基线"] --> R["跑通 v0.4.26 原始主链"]
    R --> C["建立不可误删保护面"]
    C --> D["冻结 MVP v0.1 领域合同"]
    D --> S["实现最小编排 Walking Skeleton"]
    S --> M["建立删除依赖闭包"]
    M --> W["进入 Wave 1 分波减法"]
```

## 3. 排序逻辑

### 3.1 为什么不能直接开始大删除

上游 Multica `v0.4.26` 的执行可靠性分布在 server、daemon、数据库查询、WebSocket、worktree 和多个产品入口中。
`agent_task_queue` 不只被 Issue 使用，也与 Chat、Squad、Autopilot、Runtime 和 Usage 等领域交叉。只根据
目录名称删除一个产品域，可能同时破坏任务领取、取消、恢复、会话关联或用量统计。

因此，大删除之前必须回答：

- 哪些调用链属于必须保留的执行内核？
- 哪些行为有现有测试保护，哪些只有代码隐含语义？
- 删除一个入口后，是否仍有后台任务、事件或数据库查询继续产生副作用？
- 如果执行链退化，系统是否有明确、可重复的对照基线？

没有这些答案，代码减少并不等于复杂度减少，只是把显式复杂度变成隐蔽故障。

### 3.2 为什么不能先做完整像素世界

像素世界是 Mission、TaskNode、Run、Artifact、Review 和 Activity 的视觉投影，不是业务事实源。若任务
状态、Activity 类型和 Projection 合同尚未冻结，角色动画只能绑定临时 UI 状态或模拟数据，后续会被迫
重写。

首阶段可以使用方块、简单精灵或状态文本验证事件映射，但不投入正式地图、角色资产、复杂寻路和 ECS。
视觉品质应建立在“同一事件能同时解释总看板、执行详情和像素世界”已经成立之后。

### 3.3 为什么不能先接入真实 LLM Planner

Planner 输出存在随机性。若一开始同时调试计划质量、DAG 校验、调度、runtime、Review 和 UI，问题无法
快速归因。首个编排闭环应以固定 JSON 或开发表单提交确定性计划，先证明控制面正确，再替换为真实 Planner。

同理，自动化测试默认使用 fake runtime。真实 Codex、dsh 或其他厂商 runtime 只作为显式 smoke test，
因为它们依赖本机账号、网络、安装状态并可能消耗额度。

### 3.4 为什么领域合同必须先于完整实现

业务任务状态和一次 runtime 运行状态必须分层。若不先冻结状态、命令、事件、幂等和失败恢复规则，后端、
总看板、Reviewer 和像素世界会各自形成一套状态解释，最终无法可靠恢复和回放。

设计 Gate 不是提前设计所有未来能力，而是只冻结首个黄金场景不可缺少的合同。未进入黄金场景的 A2A、
长期记忆、多人协作和复杂触发器继续延后。

## 4. 优先级一：固化研发基线

### 4.1 目的

把 `v0.4.26` 从只读 tag 转化为可持续改造、可回退、可追踪的个人产品研发起点。

### 4.2 分析逻辑

Git tag 通常处于 detached HEAD，适合验证历史版本，不适合作为长期开发位置。后续减法会同时修改前端、
Go server、数据库和部署配置；没有固定开发分支和最初快照，无法区分上游基线、自有设计和阶段性删除。

基线固定也意味着开发过程中不滚动追随上游 `main`。上游吸收应以里程碑为单位评估，不能在一个垂直切片
中途持续改变参照物。

### 4.3 工作内容

- 从 `v0.4.26` 创建长期开发分支。
- 将 `docs/diy` 中的调研、架构、减法和阶段设计纳入版本控制。
- 保留上游 remote，记录基线 tag，但不启用日常自动合并。
- 为个人产品建立独立版本序列，例如从 `v0.1.0-dev` 开始；不沿用上游产品发布语义。
- 确认工作区中不存在会被后续批量修改误带入的无关变更。
- 记录源码、数据库迁移和前端依赖锁文件对应的同一基线。

### 4.4 交付物

- 一个从 `v0.4.26` 派生的长期开发分支。
- 一个包含当前设计文档的初始可回退提交。
- 清晰的上游吸收策略和自有版本策略。
- 干净、范围明确的研发工作区。

### 4.5 完成门槛

- `git describe` 可以追溯到 `v0.4.26`。
- 研发提交不再发生在 detached HEAD。
- 所有 DIY 权威文档可从版本历史恢复。
- 后续任务能够明确区分上游代码、个人产品改造和临时验证产物。

## 5. 优先级二：跑通并记录原始运行基线

### 5.1 目的

验证准备复用的上游 Multica 执行内核在本机环境中真实可用，并建立后续改造的行为对照。

### 5.2 分析逻辑

静态阅读只能证明代码存在，不能证明本地工具链、数据库迁移、daemon、runtime、WebSocket 和 worktree 可以
协同工作。先运行原始基线，才能区分“上游本身的问题”和“DIY 改造引入的回归”。

本阶段验证的是上游 Multica 原始主链，而不是目标产品完整黄金场景。Planner、Task DAG、Reviewer 返工和三个
视图一致性尚未全部实现，不应把它们作为原版必须通过的条件。

### 5.3 工作内容

- 检查 Node.js、pnpm、Go、Docker 与仓库要求是否匹配。
- 使用仓库权威入口 `make dev` 完成依赖安装、PostgreSQL 启动、迁移、server 和 Web 启动。
- 完成本地登录或验证流程，创建 Workspace、Project 和 Issue。
- 启动并连接 daemon，确认 runtime inventory 可以被 server 观察。
- 运行一个现有 AgentTask，观察排队、领取、执行、日志和终态。
- 验证取消、失败、重试和任务历史保留。
- 验证 worktree 创建、隔离执行、分支或现场保存。
- 验证 WebSocket 断开、页面刷新和 server/daemon 重启后的状态表现。
- 运行仓库现有 `make check`，将上游基线失败与环境限制单独记录。

### 5.4 真实 Agent 的使用规则

- 自动化保护默认使用测试创建的 fake executable 或 fake runtime。
- 普通测试不得自动发现或执行用户安装的 Agent CLI。
- 真实 Codex、dsh 或其他 runtime smoke 必须显式启用。
- 运行前确认账号、网络、工作目录、写入范围、超时和可能产生的额度消耗。
- 真实 smoke 只证明集成入口可用，不替代确定性自动化测试。

### 5.5 交付物

- 本机可重复执行的启动、停止和验证路径。
- 原始 Issue → AgentTask → daemon → runtime → result 的业务链和实现链。
- 基线测试结果及已知环境限制，但不把一次 pass/fail 写入长期架构结论。
- server、daemon、Web、PostgreSQL 和 worktree 的最小运行拓扑。

### 5.6 完成门槛

- 新环境按记录步骤可以重复启动 Web、server 和数据库。
- 至少一个 AgentTask 能到达确定终态，日志和结果可查。
- 取消或失败不会留下无法解释的永远运行状态。
- worktree 现场可定位，刷新或重连不会改变权威任务事实。
- 已知基线失败与后续 DIY 回归可以被区分。

## 6. 优先级三：建立不可误删保护面

### 6.1 目的

用最小 characterization tests 固定准备保留的执行行为，使 Wave 1 删除产品域时能够及时发现误伤。

### 6.2 分析逻辑

减法的安全边界不是文件数量，而是行为合同。已有代码可能在命名上属于待删领域，却承担任务取消、租约、
会话清理或用量记录等仍需保留的职责。先冻结行为，才能判断一段代码应删除、迁移还是重新归属。

保护测试不需要覆盖整个上游产品。它只保护目标系统将继续依赖的最小内核，并优先测试最容易因跨域删除而
退化的异步和恢复路径。

### 6.3 最小保护矩阵

| 保护对象 | 最低保护行为 | 主要风险 |
| --- | --- | --- |
| AgentTask | create、claim、start、complete、fail、cancel、retry | 删除入口时破坏队列状态或历史 |
| Runtime Adapter | start、cancel、timeout、offline、resume rejection、normalized result | 业务层绑定某厂商私有状态 |
| daemon | claim/lease、heartbeat、重启恢复、重复领取保护 | 幽灵任务、重复执行、租约失效 |
| worktree | 创建、隔离、完成、取消后保留现场 | 并行写冲突或结果丢失 |
| Issue | 父子关系、Agent assignee、Issue 与 Run 关联 | TaskNode 与 Run 被错误合并 |
| Realtime | 事件丢失后重新读取权威状态 | UI 假完成、幽灵 Agent |
| Usage | 每次 Run 的模型、token、耗时和成本 | 删除 Billing 时误删预算事实 |

### 6.4 测试策略

- 后端状态和事务行为使用 Go 测试。
- Runtime 子进程默认使用测试创建的 fake executable。
- 共享前端业务逻辑在 `packages/core` 测试，业务视图在 `packages/views` 测试。
- 平台路由和接线才进入 `apps/web` 测试。
- 关键全链路使用少量 Playwright E2E，不用大量 UI 测试替代领域测试。
- WebSocket 恢复测试必须验证重新拉取权威状态，而不是假定每个事件都会到达。
- 测试描述业务行为，不绑定即将删除的 Chat、Squad 或 Autopilot 页面细节。

### 6.5 交付物

- 一组覆盖执行主链、失败和恢复的最小自动化测试。
- AgentTask 创建入口和状态修改入口清单。
- 测试与目标领域对象的映射，能判断一次失败影响 Mission、Run 还是 UI 投影。
- 真实 runtime smoke 与默认自动化测试的明确隔离。

### 6.6 完成门槛

- 删除一个产品入口导致 AgentTask、daemon 或 worktree 行为变化时，测试能够失败。
- runtime 更换不要求修改 Project、Mission、TaskNode、Artifact 或 Review 领域测试。
- 取消、重试、重启和断线至少各有一个确定性保护场景。
- 测试不依赖用户本机已安装或已登录的真实 Agent CLI。

## 7. 优先级四：冻结 MVP v0.1 领域合同

### 7.1 目的

形成一份可以直接指导首个垂直切片实现的正式设计，消除后端、前端、runtime 和可视化对核心状态的不同解释。

### 7.2 分析逻辑

现有 02 和 03 文档已经形成架构和产品边界，但尚未把所有实现所需合同细化到字段、命令、状态转移、失败
分支和幂等规则。若直接编码，这些未决语义会被分散固化在 handler、SQL、React Query 和动画映射中。

MVP 设计只覆盖首个黄金场景，不为未来多租户、A2A、长期记忆、插件市场和复杂地图制造扩展框架。

### 7.3 必须冻结的合同

- Mission、TaskNode、Assignment、Run、Artifact、ReviewVerdict 和 Activity 的字段、主键及事实所有权。
- Planner 候选计划的 JSON Schema、版本、验收条件和拒绝原因。
- `issue_dependency` 的方向、唯一性、环检测、删除和就绪计算规则。
- 业务状态与 runtime 状态的完整转移矩阵。
- Orchestrator 接受的 Command、产生的领域结果和人工操作权限。
- AgentTask 的唯一自动创建入口及幂等键。
- Activity 的类型、actor、subject、causation、correlation、payload version 和去重规则。
- Artifact 的类型、版本、内容地址和审批关系。
- Review 驳回、返工次数、重试、超时、取消、runtime 离线和依赖失败的恢复规则。
- Human Gate 的触发、批准、拒绝和终止语义。
- Runtime Adapter 的能力发现、启动、取消、恢复、输入、规范化事件和终态接口。
- 总看板、执行详情和像素世界共用的 Projection/ViewModel。

### 7.4 需要在实现前收敛的三个选择

#### 首版展示面

采用 Web-only。`apps/desktop` 暂不作为首阶段交付面；02 文档中的 Desktop 实现路径应与 03 文档的
Web-only 选择统一。Web 黄金场景稳定后，再决定 Desktop 保留薄壳还是删除。

#### 单 owner 登录方式

建议使用本地 owner bootstrap，并保留浏览器安全会话与 CSRF。首版不引入邀请、席位、公开注册和外部
OAuth；具体采用本地密码或开发验证码，需要在身份收缩设计中选择一种，不长期保留并行入口。

#### 首版 Artifact 范围

建议只支持本地 branch、commit、diff、文件和测试收据引用。远程 PR、GitHub checks 和外部 CI 属于后续
VCS Adapter，不进入首个闭环。

### 7.5 数据库约束

- 不新增数据库外键或级联动作，关系校验和依赖清理由应用事务负责。
- 新增索引使用 `CREATE INDEX CONCURRENTLY` 或 `CREATE UNIQUE INDEX CONCURRENTLY`，每个并发索引独立迁移。
- 不平行新建任务依赖表，优先规范化现有 `issue_dependency`。
- 当前关系状态是权威事实；Activity 用于审计和投影，不升级为完整事件溯源。
- 新 schema 只服务已冻结黄金场景，不为推测性扩展预留任意 metadata。

### 7.6 交付物

- 一份 MVP v0.1 编排与状态机正式设计。
- 一张完整状态转移和失败恢复矩阵。
- Planner schema、Runtime Adapter 和 Projection 的最小接口定义。
- schema 变更清单、API 端点清单和分层测试计划。

上述合同已收敛到
[07-MVP v0.1 领域合同与 Walking Skeleton 设计](./07-MVP%20v0.1领域合同与Walking%20Skeleton设计.md)，
后续实现不在本优先级文档中重复维护字段和状态定义。

### 7.7 完成门槛

- 同一业务事件在 server、Web 和像素投影中只有一种解释。
- 每个状态的进入者、退出条件、失败路径和恢复动作明确。
- LLM 不能绕过 Orchestrator 直接修改业务状态。
- Runtime 私有事件不会进入业务状态机分支。
- 未决问题不会迫使实现同时维护两套入口或双写。

## 8. 优先级五：实现最小编排 Walking Skeleton

### 8.1 目的

以最少功能贯通 Mission、DAG、Orchestrator、AgentTask、Artifact、Review、Activity 和一个可观察界面，证明
LieXiu 目标架构能够建立在上游 Multica 执行内核之上。

### 8.2 分析逻辑

Walking Skeleton 的价值不是演示视觉效果，而是尽早验证各层接口和事实所有权。使用确定性计划和 fake
runtime，可以把控制面问题与 LLM 质量、供应商网络和账号状态分离。

该切片应垂直贯通数据库、service、handler、前端 query 和最小 UI，不能只在某一层搭建大量抽象。

### 8.3 最小场景

1. 用户创建一个 Mission。
2. 用户通过固定 JSON 或开发表单提交三个 TaskNode：`A`、`B` 并行，二者完成后执行 `C`。
3. Orchestrator 校验节点、依赖、验收条件、并发和预算。
4. Scheduler 只把就绪 TaskNode 转换为现有 AgentTask。
5. fake runtime 执行并产生结构化 Artifact。
6. Reviewer 可以批准或驳回 Artifact。
7. 驳回后创建新 Run，旧 Run、旧 Artifact 和旧 verdict 保持可查。
8. 两个前置节点批准后，后继节点才可执行。
9. 一个简单时间线页面展示持久 Activity 和当前任务投影。
10. 像素世界先以方块或占位角色读取同一 Projection，验证事件映射，不制作正式美术系统。

### 8.4 明确不包含

- 不接入真实 LLM Planner。
- 不实现任意规模 DAG 和无限委派。
- 不实现完整 Kanban、Gantt 或复杂像素地图。
- 不实现真实多厂商并行作为默认测试条件。
- 不实现远程 PR、A2A、长期记忆或插件市场。
- 不重写现有 daemon 和 AgentTask，只通过窄边界复用。

### 8.5 交付物

- 可运行的三节点 Mission 垂直切片。
- 确定性 DAG 校验与调度测试。
- Artifact、Review 和返工历史。
- Activity 时间线和一个最小像素投影占位界面。
- 从 TaskNode 到 AgentTask 的明确映射及幂等保护。

### 8.6 完成门槛

- `A`、`B` 可以并行，`C` 不会提前执行。
- Review 驳回创建新 Run，不覆盖历史。
- 刷新或 WebSocket 丢失后，时间线与当前状态可恢复。
- fake runtime 可替换为一个真实 runtime，而无需修改 Mission 和 DAG 数据模型。
- 总览、执行详情和像素占位投影读取同一权威 Projection。

## 9. 优先级六：建立 Wave 1 删除依赖闭包

### 9.1 目的

把 03 文档中的产品减法结论转化为可实施的代码删除单元，确保每一波删除完整且不误伤执行主链。

### 9.2 分析逻辑

一个产品域不等于一个目录。仅删除页面会留下 API、后台任务和 secrets；仅删除 handler 会留下前端 schema
和无效导航；仅删除表会破坏仍在运行的查询。删除单元必须覆盖从用户入口到部署配置的完整依赖闭包。

### 9.3 每个待删域的核对链

```text
页面与导航
→ 前端 query、mutation、schema、store、locale
→ API route 与 handler
→ service 与领域事件
→ SQL query、table、index 与 migration
→ WebSocket responder、background job、timer
→ 环境变量、secret、权限与 feature flag
→ package、镜像、Compose、CI 与发布配置
→ 测试和长期文档
```

### 9.4 删除顺序

第一批优先处理与 AgentTask 主链耦合较低的叶子域：

1. Mobile。
2. 独立文档站和营销页面。
3. PostHog 与 Feedback。
4. Helm/Kubernetes 发行物。
5. Channel 集成与 Composio。
6. Billing、Subscription、Stripe 和 Cloud Credits。
7. 插件分发体系，保留本地 Skills 与 Runtime Adapter。

以下领域不作为第一刀：

- Chat 与 Floating Chat。
- Autopilot。
- Squad。
- Inbox。
- Agent、Runtime、AgentTask、Usage。

这些领域与任务创建、会话、队列和实时事件耦合更深，应在保护测试和 Orchestrator 控制面建立后处理。

### 9.5 交付物

- 每个待删产品域的依赖闭包清单。
- 入口、写路径、后台副作用和数据库对象清单。
- 明确的保留、迁移、删除决策，不使用模糊的“暂时隐藏”。
- 每个删除单元对应的目标测试和完成检查。
- Wave 1 可独立合并、可验证的任务拆分。

### 9.6 完成门槛

- 每个删除任务都能说明不会破坏哪一条执行保护合同。
- 删除完成标准覆盖页面、后端、数据库、配置、依赖和文档。
- Chat、Autopilot 等深耦合域不会与叶子域一起被大爆炸式删除。
- 删除波次之间系统持续可启动、可执行、可恢复。

## 10. Wave 0 的建议工作包

下一阶段可以拆成以下有序工作包。前一项未达到门槛时，不启动依赖它的高风险修改。

| 工作包 | 内容 | 主要产出 | 是否改业务代码 |
| --- | --- | --- | --- |
| W0.1 基线治理 | 分支、版本、文档、上游策略 | 可回退研发基线 | 否 |
| W0.2 运行验收 | 启动、登录、daemon、AgentTask、worktree、检查 | 原始行为基线 | 否，必要时只修阻塞性基线问题 |
| W0.3 代码地图 | AgentTask 创建/状态入口、跨域依赖、后台任务 | 保留面与删除面清单 | 否 |
| W0.4 保护测试 | fake runtime、队列、恢复、worktree、realtime | 不可误删保护面 | 仅测试与必要测试接口 |
| W0.5 MVP 设计 Gate | 状态机、命令、事件、schema、Projection | v0.1 正式实现合同 | 否 |
| W0.6 Walking Skeleton | 三节点 DAG、Artifact、Review、Activity、占位投影 | 首个垂直闭环 | 是 |
| W0.7 Wave 1 计划 | 叶子域依赖闭包与删除任务 | 可实施减法计划 | 否 |

W0.2 发现基线缺陷时，只修复阻塞后续保护与垂直切片的缺陷，不顺带清理待删产品域。W0.4 只为既有行为
增加保护，不提前重构 daemon。W0.6 是首个真正改变产品领域的工作包，应在 W0.5 合同冻结后开始。

## 11. Wave 0 总体完成标准

只有同时满足以下条件，才进入 Wave 1 产品减法：

- 研发工作位于从 `v0.4.26` 派生的长期分支，DIY 文档和版本基线可追溯。
- Web、server、PostgreSQL 和 daemon 可以按固定入口重复启动。
- 原始 AgentTask、日志、取消/重试和 worktree 行为有可解释基线。
- AgentTask、Runtime、daemon、worktree、Realtime 和 Usage 的关键保留行为有自动化保护。
- MVP v0.1 的状态转移、命令、事件、失败恢复和 Projection 合同已经冻结。
- 最小三节点 Mission 能通过 fake runtime 完成执行、Review、返工和后继调度。
- 页面刷新或 WebSocket 丢失不会改变业务事实。
- 一个真实 runtime smoke 可以在显式授权下运行，但默认测试不依赖它。
- 第一批待删叶子域具有完整依赖闭包和独立验证计划。
- 没有为了未来能力引入第二控制面、双写、微服务或通用插件框架。

## 12. 当前执行入口

本文形成的是优先级和进入门槛，不继续维护易陈旧的“下一项任务”。实际推进默认从
[08-Wave 实施路线与进度总览](./08-Wave%20实施路线与进度总览.md)中“当前关键路径”的首个未完成项开始。

稳定顺序保持为：先固定执行基线和保护面，再冻结 MVP 合同并完成 Walking Skeleton，然后迁移自动派发旁路，
最后按完整依赖闭包执行产品减法。任何临时调整都不能绕过对应 Wave 的退出门槛。
