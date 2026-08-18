# LieXiu 单 Owner、自托管与研发环境设计

## 1. 产品身份

个人版只有一个 `local_instance`，绑定一个 canonical Owner 和一个 canonical Workspace。`workspace_id` 仍作为查询、
授权和数据隔离范围存在，但 UI 与公开 API 不提供创建、切换或成员管理产品。

身份初始化分为两种明确模式，二者共用同一个幂等 `local_instance` 合同：

- 个人开发模式：显式设置 `LIEXIU_AUTO_LOGIN=true` 后，由浏览器首次访问触发初始化和安全会话建立；空库创建内置
  `LieXiu Owner` 与 `LieXiu` Workspace，已有库只在恰好存在一个 Owner 关系时自动绑定，歧义数据拒绝处理；
- 生产/通用自托管模式：必须由带部署 secret 的显式 bootstrap 请求初始化，不能在 server 启动时静默创建身份。

个人开发模式不是匿名模式：它只省略交互式登录步骤，不省略 Owner、成员关系、JWT session、CSRF、审计字段或
Workspace 数据边界。该模式同时要求后端连接来自 loopback、浏览器来源页为 `localhost/127.0.0.1/::1`，并在
`APP_ENV=production` 时强制关闭；因此本地 Next 代理不能把局域网来源伪装成本机，Forwarded Header 也不参与该
判断。Bootstrap secret 仅来自部署环境，不写数据库明文、不进入 URL、日志和长期文档。

## 2. 认证与授权

- 浏览器使用 HttpOnly、SameSite session/JWT，并对写请求执行 CSRF 保护；
- 个人开发模式由公开配置仅暴露 `auto_login=true` 能力位；前端优先请求幂等本机会话，响应不返回 JWT；
- CLI/daemon 使用可撤销、可轮换、最小范围的 PAT/daemon token；
- 所有 Mission、Agent、Runtime 和 Workspace 读写验证 canonical Owner；
- 公共 bootstrap status 只返回是否已初始化，不泄露 Owner/Workspace 身份；
- 只读 roster 和 canonical Workspace 元数据可以保留，邀请、成员写入、公开注册和 OAuth 不存在；
- Runtime 权限由 RolePolicySnapshot 冻结，不能因 provider 支持更多工具而自动扩大。

历史 Workspace/Mission/Run 数据不自动合并、改 owner 或重分配。个人版物理 schema 已收敛为当前基线；历史 migration
保持不可变，未来 schema 变化继续使用向前 migration 和可验证回退策略。

## 3. 本地与远端职责

| 环境 | 权威职责 | 适用工作 |
| --- | --- | --- |
| 本地 Mac | 源码编辑和最快反馈环 | HMR、目标单测、类型检查、fake runtime、UI/daemon 联调 |
| 远端 AMD64 | 干净环境和长期运行事实 | clean build、全量适用门禁、长时 Mission、多 Runtime、候选自托管 |

本地是默认开发入口，远端不是用来替代本地反馈的“试错机”。源码只通过 Git commit/branch 传递，不用 SCP/rsync
覆盖远端活动工作树。远端 checkout 必须可识别、可复现且与待验证 commit 一致。

## 4. 运行拓扑

控制面和 daemon 可以同机，也允许远端控制面搭配本地 daemon。daemon 通过注册事实声明 runtime、capabilities、
架构、容量和在线状态；调度器不得通过硬编码 hostname 选择执行位置。

默认测试与黄金场景使用确定性 fake runtime，不扫描或误调用用户安装的真实 Agent CLI。真实 Codex、dsh 或其他 Runtime
只在显式 smoke/acceptance profile 中启用，并使用隔离 worktree、最小权限和可控预算。

## 5. 数据和密钥边界

- 开发库、验收库和长期自用库分离；迁移先在隔离库验证，再作用于目标库；
- 物理数据清理必须先分类、确认目标和建立可恢复快照；授权不会从一次清理自动延续到下一次；
- `.env`、Runtime token、provider key、bootstrap secret 和 PAT 不提交 Git；
- 远端长期服务不以 root 运行，数据卷、worktree 和日志采用明确最小权限；
- 日志不得输出 prompt 中的秘密、认证 header、密钥或完整私有 Artifact；
- Worktree 是执行现场，Artifact 是交付事实；清理现场不能删除仍被 lineage 引用的 Artifact。

## 6. 开发与交付闭环

1. 在本地建立目标边界和最小测试，优先用 fake runtime 完成实现反馈；
2. 运行受影响 Go/TypeScript/UI 检查，不因文档或局部改动无条件扩大为全仓验证；
3. 通过 Git 把固定 commit 交给远端，执行干净构建和目标 runtime smoke；
4. 长时任务设置 deadline、进度信号、预算和止损条件；
5. 只有真实入口、原角色、目标 API、Projection 和运行事实闭环后才判定完成；
6. 动态命令与收据写入 `.work`，正式设计只维护稳定合同。

## 7. 自托管最低门槛

- PostgreSQL migration 状态明确且备份/恢复经过验证；
- `LIEXIU_AUTO_LOGIN` 已关闭，Owner bootstrap 已关闭，session/CSRF/PAT 语义正常；
- server、Web、daemon 的版本和 commit 可追踪；
- Runtime health、capacity 和离线恢复可观察；
- Mission/Run 重启恢复、预算、取消和 Human Gate 通过黄金场景；
- Web 可从 Snapshot + Activity sequence 恢复，不依赖内存或 WebSocket 恰好在线；
- 升级前可以停止新派发，升级失败可以回到已知应用版本而不回写破坏性数据。
