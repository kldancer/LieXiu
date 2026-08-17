# 单 Owner 身份与 Canonical Workspace 产品闭包合同

## 1. 文档定位

本文冻结 Wave 1C.10 的删除与保留边界。11 文档继续拥有 bootstrap、singleton、凭据和
空库/已有库选择语义；本文只定义 bootstrap 稳定后，公开身份、邀请、成员管理和多 Workspace
产品面如何退出。实施顺序和实时状态仍分别由 10 与 08 维护。

本波不删除、合并、转移或改写已有 Workspace、Mission、Run、Artifact、Activity 数据，也不把
`member` 从授权事实降级为 UI 状态。

## 2. 冻结结论

1. `local_instance` 是 canonical owner 与 canonical Workspace 的唯一绑定事实。客户端不得用
   Workspace 列表第一项、最近访问记录、URL 或 localStorage 反推 canonical Workspace。
2. 新增受认证的 `GET /api/workspaces/canonical`。它只向 singleton owner 返回 canonical
   Workspace；未初始化、绑定损坏、调用者不是 owner 均 fail closed。公开 bootstrap status 继续
   不返回身份或 Workspace 信息。
3. 删除公开邮箱验证码、Google OAuth、signup allowlist、邀请、成员写管理、leave/delete/create
   Workspace 和 Workspace switcher。`/auth/logout`、JWT/HttpOnly/SameSite/CSRF、PAT、CLI token、
   Daemon JWT/PAT 兼容继续保留。
4. `GET /api/workspaces/{id}/members` 和底层 roster query 保留为内部读取能力，因为 assignee、
   mention、Agent/Skill/Runtime/Squad owner 显示和安全检查仍依赖它。成员 create/update/delete 的
   产品路由删除；bootstrap 内部仍可创建唯一 owner member。
5. `GET/PATCH /api/workspaces/{id}` 保留，用于 canonical Workspace 元数据、repos、issue prefix、
   runtime 和 Mission scope。公开 list/create/delete/leave 与 invitation routes 删除。
6. Core 在过渡期可将 canonical Workspace 包装为长度恒为一的内部 query cache，以避免一次性
   重写 realtime、路径和持久化命名空间；它不再调用 Workspace list API，也不暴露创建/切换产品。
   该兼容数组不构成第二事实源，Wave 3 可再做命名收缩。
7. Desktop 登录页改用同一 local bootstrap；删除邀请 deep link、Google callback 和跨 Workspace
   选择产品，但保留 token session coordinator、Daemon token 同步和 canonical URL/tab scope。
8. invitation 与 verification 历史表在本波保留，不做破坏性 `DROP`；先停止所有运行时读写并删除
   query/generated 消费者。未来物理收缩只能用新的 forward migration。
9. notification preference 暂时保留：Inbox listener 仍读取它。本波只删除与成员/邀请入口绑定的
   产品设置；旧 Inbox 的事实归属在 1C.13 统一收敛。
10. onboarding、Chat、Mika、Agent Builder 明确留给 1C.11；不得借本波删除它们的领域数据或
    execution transcript。

## 3. API 与运行时矩阵

| 范围 | 删除 | 保留/新增 |
| --- | --- | --- |
| 公开认证 | `send-code`、`verify-code`、Google OAuth、signup 限制 | bootstrap status/setup、logout、安全 session |
| canonical 路由 | Workspace list/first-item 推断 | `GET /api/workspaces/canonical` |
| Workspace | create、delete、leave、switch 产品 | canonical get/update、workspace header/scope |
| Member | invite/create、role update、delete、成员管理页 | owner membership、安全 middleware、只读 roster |
| Invitation | 创建、撤销、接受、拒绝、列表、邮件和 deep link | 历史表原地保留但零运行时读写 |
| CLI/Daemon | workspace create/switch/invite | canonical get/update、roster list、PAT/JWT/daemon registration |
| Mission/Run | 无 | 所有 `workspace_id`、owner authorization、Execution Plane |

`GET /api/workspaces/canonical` 的响应复用 `WorkspaceResponse`，不返回其他 Workspace，也不接受
Workspace ID/slug 选择参数。它必须验证当前 JWT/PAT 用户等于 singleton owner，并再次验证 owner
membership；不能只靠客户端 header。

## 4. 实施顺序

1. 增加 canonical authenticated read，并让 Web/Desktop/Core 的 session 恢复只依赖它。
2. 停止公开 auth、invitation、member-management、Workspace create/delete/leave 生产路由。
3. 删除 Web/Desktop/CLI 的邀请、成员管理、OAuth、Workspace create/switch 产品入口。
4. 删除不再使用的 API client、types、store actions、handler/service/query/generated 和邮件装配。
5. 删除 OAuth/mail/signup/workspace-creation 配置、依赖、脚本、测试和陈旧文档。
6. 用静态扫描与安全回归证明没有产品入口，同时验证 bootstrap、canonical route、Mission、Daemon、
   PAT、CSRF、跨 Workspace 拒绝和只读 roster。

每一步失败时只恢复该步最近移除的消费者或注册；不得删除 singleton、放宽 owner 检查、恢复
Workspace 第一项推断或改写历史 Workspace 数据。

## 5. 完成标准

Wave 1C.10 只在以下事实同时成立时完成：

1. Web/Desktop/CLI 均无公开注册、OAuth、邀请、成员写管理和 Workspace create/switch/delete/leave；
2. server 无对应公开路由、后台邮件生产者或运行时 invitation/verification 读写；
3. canonical endpoint 是刷新和重登后的唯一 Workspace 发现来源；
4. owner bootstrap、Cookie/CSRF、PAT、Daemon JWT/PAT、owner-only 和跨 Workspace 拒绝通过；
5. Mission 创建与 Projection、Run/Artifact/Activity Workspace scope 不变；
6. 只读 roster 与 canonical Workspace metadata/runtime/repo 能力仍可用；
7. invitation/verification 历史数据没有被破坏性删除，配置和长期文档只描述单 owner 模式。
