# i18n 术语与中文产品文案规范

本文是翻译术语和中文产品文案的唯一权威来源。代码命名、包边界和路由规则以仓库根 `CLAUDE.md` 为准。

## 核心区分

日常概念完整翻译；品牌、通用缩写和必须与 schema/命令对照的标识符保留英文。API、DB 字段和字面命令始终保持源码形式。

`issue` 表示用户提交的一件工作，在产品界面中翻译为“任务”；`task` 表示智能体的一次物理执行，在中文界面保留为 `task`，二者不能混用。

| 英文 | 中文 |
| --- | --- |
| Workspace | 工作区 |
| Agent | 智能体 |
| Issue | 任务 |
| Project | 项目 |
| Daemon | 守护进程 |
| Runtime | 运行时 |
| Artifact | 产物 |
| Review | 审查 |
| Comment / Reply | 评论 / 回复 |
| Member | 成员 |
| Label | 标签 |
| Settings | 设置 |
| Usage | 用量 |

以下内容不翻译：LieXiu、GitHub、Google、Anthropic、OpenAI、Claude、Codex、Cursor，以及 API、CLI、URL、SDK、OAuth、JWT、SSO、WebSocket、HTTP、JSON、YAML、SQL。

角色和 schema 状态保持小写英文，例如 `owner`、`admin`、`member`、`backlog`、`todo`、`in_progress`、`in_review`、`done`、`blocked`、`cancelled`。代码、API 和 DB 仍使用 `issue`、`task`、`workspace_id` 等原始标识符。

## 通用 UI 用词

| 英文 | 中文 |
| --- | --- |
| Search | 搜索 |
| Email | 邮箱（标签）/ 邮件（动作） |
| Password | 密码 |
| Sign in / Sign out | 登录 / 退出登录 |
| Save / Cancel / Delete | 保存 / 取消 / 删除 |
| Confirm / Continue / Back | 确认 / 继续 / 返回 |
| Edit / New / Create / Add | 编辑 / 新建 / 创建 / 添加 |
| Remove / Send / Open / Close | 移除 / 发送 / 打开 / 关闭 |
| Preview / Download / Upload | 预览 / 下载 / 上传 |
| Done / Loading | 完成 / 加载中 |
| Active / Archived | 活跃（或启用）/ 已归档 |
| Status / Priority | 状态 / 优先级 |
| Assignee / Reporter | 负责人 / 报告人 |
| Description / Title | 描述 / 标题 |
| Error / Warning | 错误 / 警告 |

## 格式与 key

- 英文词与中文之间加一个空格；纯中文概念不加空格。
- i18next 用 `_one` / `_other`；中文只需要 `_other`。
- 计数使用 `{{count}} 个任务`、`{{count}} 个智能体`、`{{count}} 个工作区`、`{{count}} 条评论`。
- 插值使用 `{{var}}`，中文可按自然语序调整位置。
- key 使用三层语义结构：`feature.component.action`。
- 共享文案放 namespace 顶层；Web-only 和 Desktop-only 分别放 `web`、`desktop` 段。

## 中文风格

- 使用全角中文标点；引号保持源码一致的直引号 `"..."`；省略号使用 `...`。
- 简洁直白，避免“对于 X 来说”“作为 X”“我们的”等翻译腔。
- 错误信息温和但明确，例如“无法保存修改”。
- 按钮以动词开头，通常 2–4 个字；Tooltip 使用完整短句；placeholder 给出可执行示例。
- 代码注释只写英文。中文解释放设计文档，不进入 Go/TypeScript 源码注释。

新增术语时先判断它是日常概念还是必须保留的技术标识符，再同步相关 locale JSON、测试和本文件；其他文档不得覆盖本规范。
