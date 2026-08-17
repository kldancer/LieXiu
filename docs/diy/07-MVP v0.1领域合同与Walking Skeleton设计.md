# MVP v0.1 领域合同与 Walking Skeleton 设计

## 1. 文档定位与权威范围

本文冻结 LieXiu DIY 项目首个可实现版本的领域合同，直接约束 Wave 1A 的数据库、服务、API、测试和最小
可视化实现。本文是以下语义的唯一实现依据：

- Mission、TaskNode、Assignment、Run、Artifact、ReviewVerdict、Activity 的身份、字段和事实所有权；
- 确定性计划、任务依赖、业务状态、运行状态和恢复规则；
- Orchestrator Command、AgentTask 桥接、幂等和并发边界；
- 总看板、执行详情和像素世界共用的 Projection；
- Walking Skeleton 的范围、接口、测试场景和完成标准。

02 文档继续拥有总体产品与架构方向，03 文档继续拥有功能减法边界，04 文档继续拥有实施优先级，06 文档
继续拥有上游 Multica `v0.4.26` 原始执行链事实。若这些文档中的概念级示例与本文的字段、状态或迁移顺序不一致，
MVP v0.1 实现以本文为准。

本文只冻结首个黄金场景，不为多租户、A2A、长期记忆、插件市场、复杂地图或任意规模 DAG 设计通用平台。
Wave 1A 的实时完成状态和后续分波见
[08-Wave 实施路线与进度总览](./08-Wave%20实施路线与进度总览.md)。

## 2. 冻结结论

MVP v0.1 采用以下方案：

1. 保持 monorepo、单 server 和单 PostgreSQL，不拆编排微服务。
2. Mission 与 TaskNode 分别以根 Issue 和子 Issue 的 `id` 作为领域身份；扩展表只补编排语义，不复制标题、
   描述、Project、Workspace 或父子关系。
3. Orchestrator 是业务状态、依赖就绪、角色分派、Review 返工和自动 Run 创建的唯一写入者。
4. Run 表达一次业务执行尝试；AgentTask 继续表达 daemon 可领取的物理执行载体。二者分层并一一桥接，不能互相
   改名或合并。
5. `issue_dependency` 是 TaskNode DAG 的迁移起点，MVP 只接受唯一方向的 `blocked_by` 关系，不新建平行依赖表。
6. 关系状态是当前事实源；Activity 是追加式审计和投影增量，不做完整事件溯源。
7. WebSocket 只通知“数据已变化”。任何视图在丢消息、刷新或重启后都必须通过 HTTP Projection 恢复。
8. 首版为 Web-only。Desktop、Mobile、正式像素美术和真实 LLM Planner 均不进入 Walking Skeleton。
9. 首版通过确定性 JSON 计划和 fake runtime 验证控制面；真实厂商切换只验证接口兼容，不作为默认测试条件。
10. DeepSeek Harness 可以在后续作为一种 Runtime Adapter 实现接入，不拥有 Mission、DAG、Review 或视图状态。

## 3. 首个产品闭环

Walking Skeleton 固定使用三个 TaskNode：

```text
A（Executor） ─┐
               ├─> C（Integrator）
B（Executor） ─┘
```

完整业务链如下：

1. owner 在一个 Project 中创建 Mission。
2. owner 提交确定性计划，包含 A、B、C、依赖、角色、验收标准和交付物要求。
3. Orchestrator 原子校验并接纳计划；任何节点或依赖无效时不保留半成品。
4. owner 启动 Mission，A、B 进入 ready，C 保持 pending。
5. Orchestrator 为 A、B 分别建立 Assignment、Run，并通过现有 TaskService 产生 AgentTask。
6. fake runtime 执行 Run，形成结构化 Artifact；Run 成功只使 TaskNode 进入 review，不直接完成任务。
7. Reviewer 对 Artifact 给出不可变 verdict。至少一个节点第一次返回 `changes_requested`，触发新的 Assignment、
   Run 和 Artifact；旧历史保持可查。
8. A、B 的最新 Artifact 均 approved 后，A、B 完成，C 才进入 ready。
9. Integrator 只能接收已批准 Artifact，产生最终 Artifact，并在批准后完成 C。
10. 所有 TaskNode 完成后 Mission 完成。总看板、执行详情和像素占位世界从同一 Projection 展示该过程。

## 4. 领域所有权与身份

### 4.1 Project、Mission 与 TaskNode

Project 继续使用现有 `project`。Mission 和 TaskNode 是 Issue 在编排域中的明确角色：

| 对象 | 领域身份 | 复用事实 | 新增事实 |
| --- | --- | --- | --- |
| Mission | 根 Issue `id` | workspace、project、title、description、owner、父子结构 | 计划版本、业务状态、策略上限、revision |
| TaskNode | 子 Issue `id` | workspace、project、title、description、parent | 节点 key、角色要求、验收条件、业务状态、优先级、revision |

约束：

- 一个 Mission Issue 必须没有 orchestration parent；一个 TaskNode Issue 的 parent 必须是该 Mission。
- 同一 Mission 内 `node_key` 唯一，作为计划和 UI 的稳定短标识；数据库主键仍是 Issue UUID。
- 标题、描述、Project 和 Workspace 只在 Issue 保存一份，扩展表不得复制。
- 普通 Issue 不自动成为 Mission 或 TaskNode；只有 Orchestrator Command 能建立扩展记录。
- MVP 不允许已启动 Mission 更换 Project，也不允许 TaskNode 跨 Mission 移动。

Mission/TaskNode 一旦建立，扩展表的 business status 是权威状态，现有 `issue.status` 只作为旧列表和搜索入口的
兼容镜像：

| 领域状态 | Issue 兼容状态 |
| --- | --- |
| draft、pending | `backlog` |
| ready、rework | `todo` |
| assigned、running | `in_progress` |
| review | `in_review` |
| blocked、failed | `blocked` |
| completed | `done` |
| cancelled | `cancelled` |

Orchestrator 在同一事务更新二者，但从不根据 `issue.status` 反推业务状态。通用 Issue API、Agent CLI 和旧 Squad
协议对 Mission/TaskNode 的 status、assignee、parent 写入必须拒绝并引导到 Mission Command；否则 Agent 在 Run
结束时可能绕过 Review 提前把 Issue 标成 done。`issue.assignee` 可以作为当前 active Assignment 的只读兼容
镜像，Assignment 表仍是角色和历史的事实源。

### 4.2 Assignment

Assignment 表达“一项业务工作由哪个角色、Agent 和 runtime 承担”，不是一次进程执行。

最小字段：

| 字段 | 含义 |
| --- | --- |
| `id` | UUID 主键 |
| `workspace_id`、`mission_id`、`task_node_id` | 所属范围；MVP 的执行、审查和集成 Assignment 必须有 TaskNode |
| `role` | `executor`、`reviewer` 或 `integrator`；Planner 首版由确定性计划入口代替 |
| `agent_id`、`runtime_id` | 接纳分派时选择的具体实例和 runtime 快照 |
| `status` | `active`、`fulfilled`、`superseded`、`revoked` |
| `sequence` | 同一 TaskNode、role 下从 1 开始递增 |
| `supersedes_id` | 返工或改派所替代的 Assignment，可空 |
| `created_by`、`created_at`、`ended_at` | 创建者和生命周期 |

同一 TaskNode、role 同时最多一个 `active` Assignment。Review 返工或人工改派必须关闭旧 Assignment 并创建
新记录，不原地改写 agent/runtime 历史。一次 Assignment 可以产生多个底层技术重试 Run，但业务返工必须产生
新的 Assignment。

### 4.3 Run 与 AgentTask

Run 表达一次有明确目的的业务执行尝试；AgentTask 是该 Run 在现有 Execution Plane 中的队列载体。

Run 最小字段：

| 字段 | 含义 |
| --- | --- |
| `id` | UUID 主键，同时作为 AgentTask enqueue 幂等键 |
| `workspace_id`、`mission_id`、`task_node_id` | 所属业务范围 |
| `assignment_id` | 本次执行使用的 Assignment |
| `purpose` | `execute`、`review`、`integrate` |
| `attempt` | 同一 Assignment 下从 1 开始递增 |
| `status` | 规范化 Run 状态 |
| AgentTask 映射 | Run 不保存反向 ID；由 `agent_task_queue.orchestration_run_id` 唯一映射并按需查询 |
| `input` | 接纳 Run 时冻结的规范化结构化输入 |
| `dispatch_deadline_at`、`timeout_seconds` | 本次 Run 的派发和执行时间预算快照 |
| `retry_of_id` | 技术重试来源，可空 |
| `failure_kind`、`failure_message` | 规范化失败分类和可读摘要 |
| `started_at`、`finished_at`、`created_at` | 时间事实 |

AgentTask 的 `deferred`、`waiting_local_directory`、lease、session 和厂商原始事件仍由 TaskService、daemon 和
Runtime Adapter 拥有。Orchestrator 只读取规范化映射：

| AgentTask 事实 | Run 解释 |
| --- | --- |
| `queued`、`deferred` | `queued` |
| `dispatched`、`waiting_local_directory` | `dispatched` |
| `running` | `running` |
| `completed` | `succeeded` |
| `failed` | `failed`，并记录规范化 `failure_kind` |
| `cancelled` | `cancelled` |

timeout、runtime offline、protocol error、worktree error 是 `failure_kind`，不是额外 Run 状态。这样 UI 和调度
不依赖厂商私有字符串，也不会因增加失败类别膨胀状态机。

### 4.4 Artifact

Artifact 是不可变、可版本化的交付引用，不复制 worktree 内容。

最小字段：`id`、`workspace_id`、`mission_id`、`task_node_id`、`run_id`、`kind`、`version`、`uri`、
`content_hash`、`summary`、`metadata`、`created_at`。

MVP 允许的 `kind` 只有：

- `branch`
- `commit`
- `diff`
- `file`
- `test_receipt`
- `final_delivery`

`uri` 使用可验证的本地引用或仓库引用；`metadata` 只保存该类型确实需要且 schema 已知的字段，不作为任意
扩展袋。同一 TaskNode、kind 的 `version` 单调递增；旧版本不覆盖、不删除。Artifact 是否可进入后继任务由
ReviewVerdict 决定，Artifact 自身不保存“approved”可变状态。

### 4.5 ReviewVerdict

ReviewVerdict 是 Reviewer 对一个明确 Artifact 版本作出的不可变结论。

最小字段：`id`、`workspace_id`、`mission_id`、`task_node_id`、`review_run_id`、`artifact_id`、`decision`、
`evidence`、`requested_changes`、`created_at`。

`decision` 仅有：

- `approved`：Artifact 满足验收条件，TaskNode 可以完成；
- `changes_requested`：允许在返工预算内创建新的 Assignment 和 Run；
- `rejected`：不可继续自动返工，TaskNode 失败并等待人工处理。

后来的 verdict 不修改旧 verdict。TaskNode 的“当前审查结果”是针对最新候选 Artifact 的投影，不是覆盖历史。

### 4.6 Activity

Activity 是状态变化后同事务写入的追加式业务记录。它支持审计、时间线和投影增量，但不是重建全部状态的
Event Store。

最小字段：

| 字段 | 含义 |
| --- | --- |
| `id` | UUID 主键 |
| `workspace_id`、`mission_id` | 分区与聚合范围 |
| `task_node_id`、`run_id` | 可选的细粒度范围 |
| `type` | 本文冻结的 Activity 类型 |
| `actor_type`、`actor_id` | `user`、`orchestrator`、`agent`、`runtime` |
| `subject_type`、`subject_id` | 被改变对象 |
| `causation_id` | 直接导致本 Activity 的 Command 或 Activity |
| `correlation_id` | 一次用户意图或一次调度循环的关联 ID |
| `payload_version` | 从 1 开始的 payload schema 版本 |
| `payload` | 与 type/version 对应的最小结构化数据 |
| `dedupe_key` | workspace 内唯一的幂等键 |
| `sequence` | Mission 内严格递增的展示顺序 |
| `occurred_at` | 数据库时间 |

MVP Activity 类型：

```text
mission.created       mission.plan_accepted  mission.started
mission.blocked       mission.completed      mission.failed      mission.cancelled
task.ready            task.assigned          task.started
task.review_requested task.rework_requested  task.completed
task.blocked          task.failed            task.cancelled
run.queued            run.started            run.succeeded       run.failed          run.cancelled
artifact.created      review.approved         review.changes_requested  review.rejected
```

Activity 与关系状态在同一数据库事务提交。WebSocket 可以在提交后发送 `mission:changed {mission_id,
last_sequence}`；客户端必须按 sequence 补拉，不能把 WebSocket payload 当成状态事实。MVP 不新增独立 Outbox；
当出现跨进程可靠消费者或外部集成后，再以 Activity `id` 作为来源增加 Outbox，而不是提前维护两份事件日志。

## 5. 确定性计划合同

Planner 在 MVP 中是一个确定性 JSON 入口。LLM Planner 后续只能生成同一合同的候选值，不能直接写数据库。

```json
{
  "schema_version": 1,
  "mission_id": "uuid",
  "plan_key": "owner-supplied-idempotency-key",
  "limits": {
    "max_parallel_runs": 2,
    "max_task_attempts": 2,
    "max_rework_cycles": 1
  },
  "nodes": [
    {
      "key": "A",
      "title": "实现子任务 A",
      "description": "边界明确的工作说明",
      "role": "executor",
      "acceptance_criteria": ["存在可验证交付物", "目标测试通过"],
      "artifact_kinds": ["commit", "test_receipt"],
      "depends_on": []
    },
    {
      "key": "B",
      "title": "实现子任务 B",
      "description": "可与 A 并行的工作说明",
      "role": "executor",
      "acceptance_criteria": ["存在可验证交付物", "目标测试通过"],
      "artifact_kinds": ["commit", "test_receipt"],
      "depends_on": []
    },
    {
      "key": "C",
      "title": "集成交付",
      "description": "只集成已批准成果",
      "role": "integrator",
      "acceptance_criteria": ["形成最终交付"],
      "artifact_kinds": ["final_delivery"],
      "depends_on": ["A", "B"]
    }
  ]
}
```

接纳规则：

- `schema_version` 必须为 1；未知字段默认拒绝，避免拼写错误被静默忽略。
- `mission_id` 必须匹配 URL 和当前 workspace；`plan_key` 在 Mission 内唯一。
- 节点数量固定上限 16，依赖深度固定上限 4；Walking Skeleton 使用 3 个节点。
- key 非空、区分大小写、Mission 内唯一；依赖必须引用同计划节点。
- 不允许自依赖、重复边和环；至少一个根节点，至少一个 Integrator 叶节点。
- Integrator 必须依赖至少一个非 Integrator 节点；其输入只能是前置节点已批准 Artifact。
- 每个节点必须有非空验收条件和至少一种允许的 Artifact kind。
- 并发、技术尝试和返工轮次必须在系统硬上限内；MVP 硬上限依次为 8、5、3，不得用负数或“无限”。
- 整个计划在一个事务中接纳。任何校验失败都返回结构化错误列表，Mission 保持 draft。
- MVP 一旦 Mission 启动，不接受就地修改计划。修改必须取消旧 Mission 或在后续版本引入新的 PlanVersion 语义。

## 6. DAG 方向、就绪和传播

`issue_dependency` 在 MVP 中只写入：

```text
issue_id = 当前 TaskNode
depends_on_issue_id = 前置 TaskNode
type = blocked_by
```

例如 C 依赖 A，保存 `(issue_id=C, depends_on_issue_id=A, type=blocked_by)`。

应用层约束：

- 两端都必须是同一 Mission 的 TaskNode；
- `(issue_id, depends_on_issue_id, type)` 唯一；
- 不允许自环；写入整批计划前使用拓扑排序拒绝任意环；
- Orchestrator 之外的入口不得为 Mission TaskNode 写 dependency；
- 已启动 Mission 不允许删除边；取消 Mission 时保留边用于历史查询；
- 新增 migration 移除该表历史外键并改由应用事务清理，遵守仓库“不新增外键/级联”约束；
- 为 `issue_id` 与 `depends_on_issue_id` 保持可用索引，新增唯一索引使用独立 concurrent migration。

就绪公式：

```text
ready(node) = mission.status == running
           && node.status in (pending, rework)
           && every prerequisite.status == completed
           && no active assignment/run exists for node
           && mission active runs < max_parallel_runs
```

传播规则：

- 前置节点完成后，Orchestrator 在同一调度循环重新计算直接后继节点。
- 前置节点 failed、blocked 或 cancelled 时，后继节点不会自动执行，并进入 blocked，原因包含前置节点 ID。
- 人工 Retry 成功后可以解除由该前置节点造成的 blocked；不提供“假装依赖已完成”的自动 skip。
- 取消 Mission 会取消所有非终态节点和可取消 Run；已完成历史保持不变。

## 7. 状态机

### 7.1 Mission 状态

```text
draft -> ready -> running -> completed
                    |  |----> blocked -> running/failed/cancelled
                    |  |----> failed
                    `-------> cancelled
draft/ready ----------------> cancelled
```

| 当前状态 | Command/事实 | 下一状态 | 唯一写入者 | 失败或恢复 |
| --- | --- | --- | --- | --- |
| 不存在 | CreateMission | draft | Orchestrator | 重复 command 返回原 Mission |
| draft | SubmitPlan 通过 | ready | Orchestrator | 校验失败保持 draft，不写半计划 |
| draft、ready | CancelMission | cancelled | Orchestrator | 重复取消幂等 |
| ready | StartMission | running | Orchestrator | 无有效计划则拒绝 |
| running | 所有 TaskNode completed | completed | Orchestrator | 终态不可回退 |
| running | 未完成且无可调度节点、存在可恢复阻塞 | blocked | Orchestrator | 人工 Retry/解决阻塞后回 running |
| running、blocked | 不可恢复节点失败或返工预算耗尽 | failed | Orchestrator | 终态不可自动回退 |
| running、blocked | CancelMission | cancelled | Orchestrator | 发起子 Run 取消；迟到结果不得改终态 |

Mission 的 `current_phase`（planning、executing、reviewing、integrating）是 Projection 推导值，不进入权威状态，
避免同时维护两套阶段状态。

### 7.2 TaskNode 状态

```text
pending -> ready -> assigned -> running -> review -> completed
              ^                    |          |
              |                    |          `-> rework -> ready
              |                    `-> failed/blocked
              `-----------------------------------------
任意非终态 -> cancelled
```

| 当前状态 | Command/事实 | 下一状态 | 关键副作用 |
| --- | --- | --- | --- |
| pending、rework | 依赖和预算满足 | ready | `task.ready` |
| ready | DispatchReadyNodes | assigned | 建 active Assignment 和 queued Run |
| assigned | Run started | running | `task.started`；重复 started 幂等 |
| assigned、running | Run 技术失败且可重试 | assigned | 同 Assignment 新 Run，旧 Run 保留 |
| assigned、running | Run 技术失败且预算耗尽 | failed 或 blocked | 可恢复外部条件用 blocked；业务不可恢复用 failed |
| running | execute/integrate Run succeeded 且 Artifact 有效 | review | 建 Reviewer Assignment/Run，`task.review_requested` |
| review | verdict approved | completed | 关闭 Assignment，后继节点重新计算 |
| review | verdict changes_requested 且返工预算可用 | rework | 关闭旧 Assignment，创建下一轮 Executor/Integrator Assignment |
| review | verdict rejected 或返工预算耗尽 | failed | Mission 随后归约 |
| pending、ready、assigned、running、review、rework、blocked | CancelMission | cancelled | 取消活动 Run，历史不删除 |
| blocked | RetryTaskNode 条件满足 | pending 或 ready | 清除阻塞原因并重新计算依赖 |

`completed`、`failed`、`cancelled` 是终态。MVP 不提供 reopen；需要重做时创建新的 Mission，避免篡改已交付历史。

### 7.3 Assignment 状态

| 当前状态 | 事实 | 下一状态 |
| --- | --- | --- |
| active | 对应工作 approved | fulfilled |
| active | Review 要求业务返工并建立新 Assignment | superseded |
| active | 人工改派 | superseded |
| active | Mission/TaskNode 取消 | revoked |

Assignment 状态不直接决定 TaskNode 状态，只记录分派生命周期；状态转换仍由 Orchestrator 在同一事务完成。

### 7.4 Run 状态

| 当前状态 | AgentTask/Command 事实 | 下一状态 | 恢复规则 |
| --- | --- | --- | --- |
| 不存在 | Dispatch 创建 | queued | `run_id` 幂等，重复调度不重复建 Run |
| queued | AgentTask claim | dispatched | 允许重复观察 |
| dispatched | AgentTask start | running | 允许乱序观察时从 DB 终态归一化 |
| queued、dispatched、running | AgentTask completed | succeeded | 只接受一次终态 |
| queued、dispatched、running | AgentTask failed | failed | 记录 failure_kind；策略决定新 Run |
| queued、dispatched、running | cancel 生效 | cancelled | 迟到成功不得覆盖 cancelled |

`succeeded`、`failed`、`cancelled` 是终态。技术重试创建新 Run，并设置 `retry_of_id`；Review 返工建立新
Assignment 后再创建 Run，不使用 `retry_of_id` 混淆两种原因。

## 8. Orchestrator Command 合同

Command 统一包含 `command_id`、`workspace_id`、`actor`、`expected_revision` 和业务 payload。`command_id` 在
workspace 内唯一；重复 Command 返回第一次提交的结果。`expected_revision` 用于拒绝并发的陈旧写入。

| Command | 允许 actor | 作用 |
| --- | --- | --- |
| `CreateMission` | owner | 创建根 Issue 和 Mission 扩展 |
| `SubmitPlan` | owner；未来可由 Planner 提案后经 owner 接纳 | 原子校验并建立 TaskNode、依赖和策略 |
| `StartMission` | owner | 启动并触发首次就绪计算 |
| `CancelMission` | owner | 终止 Mission，并请求取消活动 Run |
| `DispatchReadyNodes` | orchestrator | 按并发和角色策略建立 Assignment、Run、AgentTask |
| `ReconcileRun` | orchestrator | 将 AgentTask 权威事实归一化到 Run 和 TaskNode |
| `RecordReviewVerdict` | reviewer runtime，经 Orchestrator 校验 | 保存 verdict 并进入完成、返工或失败 |
| `RetryTaskNode` | owner | 解除可恢复 blocked TaskNode；failed 终态不重开 |

Planner、Executor、Reviewer、Integrator 和前端都不能直接 UPDATE Mission/TaskNode/Run 状态。Agent 输出只作为
Command 输入或 Artifact 候选，Orchestrator 校验后才改变业务事实。

`RetryTaskNode` 仅接受 blocked TaskNode 和 running/blocked Mission，不重新打开 failed 终态。请求除统一
`command_id`、Mission `expected_revision` 外，还必须包含 `expected_task_revision`；成功时以同一事务清除阻塞、
恢复根 Issue 投影并写入 `task.retry_requested`，随后复用 `AdvanceMission`。Command 重放不重复 Activity、Run 或
AgentTask，第一次请求若在事务提交后、推进前中断，重放会补完确定性推进。

内部自动推进使用一个确定性 `AdvanceMission(mission_id, correlation_id)` 循环：锁定 Mission、计算终态、传播
依赖、按稳定排序选择 ready 节点，并在没有新状态变化时退出。稳定排序为 TaskNode 数值 priority 从高到低、
创建顺序从早到晚、节点 key；同一输入必须产生相同调度决定。

### 8.1 Quick Create Mission 入口合同

Quick Create 是便捷的 Mission 创建入口，不是旧的 AgentTask 快速执行入口。长期合同固定如下：

| 项目 | 合同 |
| --- | --- |
| HTTP 方法与路径 | `POST /api/issues/quick-create`；过渡期间路径可以保留，但语义已改为 Mission Command |
| 请求字段 | 必填 `command_id`（UUID）、`prompt`；可选 `project_id` |
| 禁止字段 | 不接受 `priority`、`due`、`parent`、`attachments`，也不在该入口选择 Agent 或 Squad |
| 内部操作 | 先幂等 `CreateMission`，再幂等 `SubmitPlan`；计划固定为 `executor -> integrator` 两个节点 |
| 成功结果 | 返回同一 Mission 的 `status=ready`；不自动调用 `StartMission` 或 `AdvanceMission` |
| 业务副作用 | 只建立 Mission、固定计划、TaskNode 和依赖；不创建 Assignment、Run 或 AgentTask |

`prompt` 是该便捷 Mission 的用户输入；Quick Create 不开放任意计划、角色、优先级或父级关系的定制。固定
计划必须以 `executor` 节点为 `integrator` 节点的前置节点，具体执行仍须由后续显式 `StartMission` 和编排推进
触发。这样 Quick Create 成功返回 `ready` 只表示 Mission 已具备执行资格，不表示已经开始执行。

`command_id` 在 workspace 内是幂等键。同一 `command_id` 的重放必须返回第一次请求创建的同一 Mission、同一
固定计划和同一 `status=ready` 结果，不得复制 Mission、TaskNode、依赖或后续执行。实现必须覆盖部分成功：若
第一次请求在 `CreateMission` 成功后、`SubmitPlan` 完成前中断，重试须根据同一 `command_id` 找回原 Mission 并
补交计划；只有两步都成功后才返回 `ready`。任何计划校验或权限错误都不能写入半计划。

该合同允许旧的 `/api/issues/quick-create` handler、路由和过渡适配层继续存在，但它们只能把请求转换为上述
两个 Orchestrator Command；不得保留“创建 Issue 后直接 `CreateQuickCreateTask`/启动 AgentTask”的旧语义。
旧查询或内部函数若暂时保留，只能用于读取和兼容历史 quick-create 任务，生产调用必须为零；它们不能成为
Mission、TaskNode、Assignment、Run 或执行状态的事实所有者，并在 W1B.6 白名单最终收缩时删除。

## 9. AgentTask 桥接与模块边界

### 9.1 两种方案比较

| 方案 | 优点 | 代价与风险 | 结论 |
| --- | --- | --- | --- |
| A. Orchestrator 通过窄 ExecutionGateway 调用现有 TaskService | 复用 claim、daemon、runtime、取消、恢复和 worktree；可渐进迁移旁路 | 过渡期 Run 与 AgentTask 两层并存，需要幂等桥接 | 采用 |
| B. 立即用新 Run 队列替换 AgentTask | 表面模型更整齐 | 重写成熟 Execution Plane，回归面覆盖 Chat、Squad、Autopilot、daemon 和恢复 | 拒绝 |

推荐依赖方向：

```text
HTTP handler
    -> orchestration Service（Command、状态机、事务）
        -> orchestration Repository（Mission/TaskNode/Run/Activity）
        -> ExecutionGateway
            -> 现有 TaskService -> AgentTask -> daemon -> Runtime Adapter

AgentTask terminal event / 周期 reconciliation
    -> RunReconciler -> orchestration Service
```

TaskService 不反向依赖 orchestration。RunReconciler 可以被现有任务事件唤醒，但必须周期扫描非终态 Run，保证
进程崩溃或 WebSocket 丢失后仍能恢复。

唯一新增执行接口保持最小：

```go
type ExecutionGateway interface {
	Enqueue(ctx context.Context, req EnqueueExecutionRequest) (EnqueueExecutionResult, error)
	Cancel(ctx context.Context, req CancelExecutionRequest) (CancelExecutionResult, error)
}
```

`EnqueueExecutionResult` 返回 `agent_task_id`、当前状态和 `idempotent`；`CancelExecutionResult` 返回
`agent_task_id` 与取消后的权威 AgentTask 状态。Gateway 只接受 Orchestrator 已持久化的 Run 决策，不接收厂商
prompt 或任意执行参数。

`EnqueueRequest` 必须包含 `run_id`、Issue、Agent、Runtime、purpose 和规范化输入。`agent_task_queue` 增加可空的
`orchestration_run_id`；为非空值建立唯一 concurrent index。Enqueue 使用 `run_id` 幂等：若进程在 AgentTask
写入后崩溃，重试必须返回已存在的 AgentTask，而不是创建第二个执行。Run 表不保存 `agent_task_id` 反向引用，
从根源上消除两个可独立更新的桥接事实。

Wave 1B 后，TaskService 只作为 Orchestrator/Run 已决定执行的桥接与 Execution Plane；Autopilot、Issue、Chat、Channel、Squad 和评论入口不得再成为生产者。静态边界测试不得扩大白名单。随着
入口迁移，白名单只能缩小，最终自动编排任务只允许由 ExecutionGateway 创建。

### 9.2 推荐代码归属

不提前拆出多层通用框架。Wave 1A 建议只增加：

```text
server/internal/service/orchestration/
  service.go          Command 入口和事务编排
  transition.go       纯状态转移与不变量
  plan.go             Plan v1 校验和 DAG 计算
  projection.go       MissionProjection 组装
  execution.go        ExecutionGateway 适配与 Run reconciliation

server/internal/handler/mission.go
server/pkg/db/queries/orchestration.sql
packages/core/orchestration/
packages/views/orchestration/
apps/web/.../missions/
```

只在确有两个实现或测试替身价值时定义接口。状态转移、DAG 校验和 Projection 组装优先写成无 I/O 的纯函数；
数据库 repository 不为每张表制造一层抽象。

### 9.3 Runtime 厂商中立边界

MVP 不在业务层再发明一套与现有 daemon 协议平行的 Runtime SDK。现有 Execution Plane 继续负责能力发现、
启动、取消、恢复、输入、日志、usage 和终态上报；ExecutionGateway 是 Orchestrator 能看到的唯一执行边界。

所有 Runtime 实现，包括未来的 DeepSeek Harness 插件，都必须归一化以下事实：

| 类别 | 规范化合同 |
| --- | --- |
| identity | runtime/profile ID、provider、model，只作为配置和运行快照 |
| capabilities | code、tool、MCP、resume、streaming、structured output 等显式能力集合 |
| lifecycle | accepted、started、progress、input_required、terminal |
| terminal | succeeded、failed、cancelled；timeout/offline/protocol error 进入 failure_kind |
| result | text、结构化 output、Artifact 候选和可选 raw provider reference |
| usage | tokens、wall time、model、可选成本估算 |

Orchestrator 只能按 capability 和规范化终态决策，不能出现 `if provider == deepseek/openai/anthropic` 的业务
分支。厂商私有 session、event 和错误码可以在 adapter 内保留用于诊断，但不得成为 Mission、TaskNode、Review
或 Projection 的字段。DeepSeek Harness 只有在其插件确实需要独立 start/cancel/resume 适配时才新增实现，不把
它升级为整个产品的编排底座。

## 10. 数据库演进合同

### 10.1 扩展表

Wave 1A 按 expand-migrate-contract 新增：

- `mission`：`issue_id` 主键、status、plan_key、plan_schema_version、plan、limits、revision、时间戳；其中
  `plan` 严格符合本文 Plan v1 schema，不是任意 metadata；
- `task_node`：`issue_id` 主键、mission_id、node_key、role、acceptance_criteria、artifact_kinds、priority、status、
  block_reason、rework_count、revision、时间戳；
- `orchestration_assignment`；
- `orchestration_run`；
- `artifact`；
- `review_verdict`；
- `orchestration_activity`；
- `agent_task_queue.orchestration_run_id` 可空列。

表名使用 `mission`、`task_node`，但其主键就是 Issue ID；不得再保存一套 Mission/TaskNode 标题和描述。

### 10.2 仓库迁移约束

- 不修改 `v0.4.26` 已发布 migration；只追加 forward migration。
- 新表不建立数据库外键或级联，所有关系完整性和清理由应用事务及测试保证。
- 普通列、表和约束 migration 与每个 concurrent index migration 分离。
- 唯一性首先由普通 constraint 或独立 unique concurrent index表达，写入仍必须处理并发冲突。
- 不立即删除 `issue_dependency.type` 或历史非编排关系；Orchestrator 只读取其管理的 TaskNode `blocked_by` 边。
- 不立即改写现有 `activity_log`；新 Activity 合同先独立落表，避免旧产品 action/payload 语义污染编排投影。
- 所有自动状态写入在 Mission 行锁和 revision 保护下完成；跨 Mission 不持有长事务。

### 10.3 事务与幂等不变量

- Command 结果、关系状态和对应 Activity 同事务提交。
- 同一 Mission 的 Activity sequence 在该事务中分配并保持单调。
- `plan_key`、`command_id`、Activity `dedupe_key`、Run `(assignment_id, attempt)` 和 AgentTask
  `orchestration_run_id` 均有唯一保护。
- 一个 TaskNode、role 同时最多一个 active Assignment。
- 一个 Run 最多绑定一个 AgentTask，一个 AgentTask 最多绑定一个 Run；唯一映射只存于 AgentTask。
- 终态 UPDATE 必须带允许的前态条件；并发完成、失败和取消只有一个能成功。
- 外部执行 enqueue 失败时 Run 保持 queued 并记录可恢复原因；后台调度按幂等键重试，不在请求线程无限循环。

## 11. 失败恢复与时间预算

| 场景 | 权威结果 | 自动动作 | 止损点 |
| --- | --- | --- | --- |
| Plan schema、引用或 DAG 无效 | Mission 保持 draft | 返回全部可定位校验错误 | 不部分保存 |
| 找不到满足角色的 Agent/Runtime | TaskNode blocked | 等待 owner 改派或 Runtime 恢复 | 不忙循环创建 Run |
| enqueue 暂时失败 | Run 保持 queued | 按 run_id 幂等重试 | 超过 dispatch deadline 后 blocked |
| daemon 在 dispatched/running 时离线 | AgentTask 由既有恢复逻辑处理 | Reconciler 映射最终事实 | 不由前端推断失败 |
| runtime offline 或 dispatch timeout | Run failed + failure_kind | 在 `max_task_attempts` 内创建技术重试 | 达上限后 TaskNode blocked，等待 owner `RetryTaskNode` |
| timeout、provider network 或 skill bundle unavailable | Run failed + failure_kind | 在 `max_task_attempts` 内创建技术重试 | 达上限后 TaskNode failed |
| protocol、worktree、agent error 或 unknown | Run failed + failure_kind | 不自动重试，fail closed | TaskNode failed |
| Review 要求返工 | TaskNode rework | 新 Assignment、Run、Artifact 版本 | 达 `max_rework_cycles` 后 failed |
| 重复 terminal callback | 原终态不变 | 由状态条件和 dedupe 忽略 | 不产生重复 Activity |
| cancel 与 success 竞争 | 首个合法终态获胜 | 后到结果记录诊断但不改业务终态 | Mission cancelled 优先阻止后继调度 |
| WebSocket 丢失 | 数据库状态不变 | 客户端按 last_sequence 重拉 Projection/Activity | 不尝试从动画反推状态 |
| server 重启 | 非终态 Mission/Run 保留 | startup reconciler 扫描并推进 | 单轮有界，不无限持锁 |

时间预算由 Mission limits 和系统硬上限共同控制：

- HTTP Command 只负责持久化一个有界事务，不等待 Agent 执行；
- 单次 AdvanceMission 最多处理 16 个节点，达到上限后交还后台下一轮；
- dispatch deadline、Run timeout 由配置提供并写入 Run 快照，运行中修改配置不改变旧 Run；
- Walking Skeleton `max_parallel_runs=2`、`max_task_attempts=2`、`max_rework_cycles=1`；
- 自动推进在本轮没有状态变化时立即停止，不以轮询次数伪造进展。

## 12. Projection 与三个可视化面

三个视图只能读取同一个 `MissionProjection`，不得各自拼装业务状态或计算 DAG ready。

Projection 至少包含：

| 区域 | 字段 |
| --- | --- |
| Mission | id、title、status、current_phase、progress、limits、revision、last_sequence |
| Nodes | id、key、title、role、status、dependency_ids、block_reason、active_assignment、latest_run、latest_artifact、latest_verdict |
| Team | agent、role、runtime、provider/model 展示信息、capability、当前节点 |
| Activity | recent items、next/previous cursor、last_sequence |
| Selection | 选中 Run 的日志引用、Artifact 历史、Review 历史、retry/rework lineage |

映射规则：

- 项目总看板按 TaskNode 业务状态分组，Run 状态只作为卡片的执行细节。
- 像素世界用 Mission/TaskNode 状态决定区域，用 Activity 决定一次性动作。相同 Projection 必须得到确定性
  角色位置和动画；动画结束不回写业务状态。
- 执行详情以 Run 为中心展示 AgentTask 日志、usage、Artifact、Review 和 lineage，但“任务是否完成”读取
  TaskNode 状态。
- provider/model/runtime 只是 Team 和 Run 的配置快照；切换厂商不改变 Mission、DAG 或 Artifact schema。
- 客户端先 GET snapshot，再从 `last_sequence` 接收或补拉 Activity。发现 sequence 缺口时放弃本地增量并重拉。

MVP 像素世界只实现方块角色、固定区域和少量状态动画：idle、walking、working、reviewing、blocked、done。
正式精灵图、地图编辑器、碰撞、寻路、昼夜和装饰系统都延后。

## 13. HTTP 边界

MVP 公开接口：

| Method | Path | 作用 |
| --- | --- | --- |
| POST | `/api/missions` | CreateMission |
| POST | `/api/missions/{id}/plan` | SubmitPlan |
| POST | `/api/missions/{id}/start` | StartMission |
| POST | `/api/missions/{id}/cancel` | CancelMission |
| POST | `/api/missions/{id}/tasks/{taskNodeID}/retry` | RetryTaskNode |
| GET | `/api/missions/{id}` | 完整 MissionProjection snapshot |
| GET | `/api/missions/{id}/activities` | 按 sequence/cursor 增量读取 |
| GET | `/api/missions/{id}/runs/{runID}` | Run、日志引用、Artifact、Review 和 lineage 详情 |

所有写接口要求现有浏览器安全会话、CSRF、workspace membership、`command_id` 和 `expected_revision`。内部
Dispatch、Reconcile 和 Review 结果不暴露为可由普通浏览器任意调用的无鉴权端点；它们通过 server 内部服务或
现有受保护的 AgentTask 回报路径进入。

## 14. 最小公共测试接口与八个关键场景

测试只需要两个稳定入口：

1. 对 Orchestrator Service 提交 Command，并读取关系状态与 Activity；
2. 注入 fake ExecutionGateway，控制 enqueue、start、terminal 和 cancel 结果。

必须覆盖的八个端到端场景：

1. **计划原子性**：重复 key、未知依赖、自环或环均拒绝，Mission 保持 draft，无残留 TaskNode/边。
2. **DAG 就绪**：启动后 A、B ready/dispatch，C pending；只有 A、B completed 后 C 才 dispatch。
3. **调度幂等**：并发两次 Advance/Dispatch 对同一 TaskNode 只产生一个 active Assignment、Run 和 AgentTask。
4. **执行与审查分层**：AgentTask completed 使 Run succeeded、TaskNode review；只有 approved 才 completed。
5. **返工历史**：changes_requested 产生新 Assignment/Run/Artifact 版本，旧 Run、Artifact、verdict 可查。
6. **取消竞争**：Mission cancel 与 Run success 并发时只有合法终态，且不会继续创建后继 Run。
7. **进程恢复**：模拟 enqueue 后未回填、daemon 离线和 server 重启，按 run_id/reconciler 恢复且不重复执行。
8. **投影恢复**：漏掉任意 WebSocket Activity 后重新 GET，三视图得到同一状态、last_sequence 和 lineage。

此外保留 06 文档的 AgentTask、daemon、runtime 和 worktree characterization tests；新的领域测试不得发现、登录
或调用真实厂商 CLI。

## 15. Wave 1A 实施切片

Wave 1A 按以下最短闭环推进：

1. 新增 schema、sqlc query 和纯状态机/DAG 测试；不改现有产品入口。
2. 建 Orchestrator Service、Command 幂等和 Activity 同事务写入。
3. 建 ExecutionGateway 与 `orchestration_run_id` 幂等桥接，继续复用 TaskService/daemon/fake runtime。
4. 实现 A、B、C 确定性计划和 RunReconciler，完成 success、fail、cancel、retry、review/rework。
5. 暴露 MissionProjection 和最小 HTTP API。
6. 建一个 Web 页面：左侧简化看板，中部像素占位投影，右侧选中 Run 详情；三者共用一次 query。
7. 通过八个关键场景和现有执行保护后，再迁移 Quick Create/assignee 等 AgentTask 旁路；Quick Create 的迁移
   目标是固定两节点计划、幂等补交和 `ready` 返回，不自动 Start/Advance，也不产生 AgentTask。

不在 Wave 1A 内执行：

- 大规模删除 Chat、Squad、Autopilot、Billing 或 Desktop；
- 接入真实 LLM Planner、DeepSeek Harness 插件或多厂商压力测试；
- 重写 daemon、AgentTask、worktree 或 Realtime；
- 建通用工作流 DSL、任意 metadata、事件溯源框架或微服务消息总线；
- 制作正式像素角色资产和复杂游戏系统。

## 16. 完成标准与后续删除条件

MVP v0.1 合同只有在以下条件同时满足时才算由实现兑现：

- 三节点 Mission 通过 fake runtime 完成一次并行执行、一次 Review 返工和最终集成；
- 每个状态转移都由 Orchestrator 发起或确认，LLM、前端和 Runtime 无直接业务状态写入口；
- 同一 run_id 不会创建两个 AgentTask，终态竞争不会产生两个业务结果；
- 关系状态、Activity 和 Projection 在刷新、事件丢失与 server 重启后保持一致；
- 总看板、像素占位世界和执行详情读取同一个 MissionProjection；
- 替换 fake runtime 为任一已支持 runtime 时，不修改 Mission、TaskNode、Artifact、Review 或 Projection 模型；
- Wave 0 AgentTask、daemon、runtime、cancel、retry、worktree 和恢复保护继续通过。

以下抽象只有满足真实条件后才能新增：

- 第二种执行内核出现后，才扩展 ExecutionGateway，而不是为推测厂商能力增加方法；
- 外部可靠消费者出现后，才新增 Outbox；
- 用户确实需要自定义角色策略后，才新增 RolePolicy 表；MVP 的 executor、reviewer、integrator 是固定枚举；
- 真实计划修订需求出现后，才引入完整 PlanVersion 和 Mission reopen；
- 复杂跨项目依赖出现后，才考虑独立 TaskDependency 模型；
- 独立扩缩容或故障域出现后，才拆 Orchestrator 服务；
- Web 黄金场景稳定后，才决定 Desktop 是薄壳还是删除。
