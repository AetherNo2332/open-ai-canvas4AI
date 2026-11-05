# 平台 Agent 服务与总控：A0 接缝映射与合同冻结

> 对应计划：[2026-11-05-agent-service-sdk-rollout.md](2026-11-05-agent-service-sdk-rollout.md) A0；设计合同：[2026-11-05-agent-service-sdk-design.md](../design/2026-11-05-agent-service-sdk-design.md)。
> 分支 `feat/agent-service-sdk`，叠在 `feat/creation-platform-sdk`（PR #130，`d6fdc5169`）之上。A0 是执行门槛，只产出映射与合同，不计作运行功能。

## A0 前提核验（基础 S0–S8）

| 项 | 状态 | 依据 |
| --- | --- | --- |
| 基础 SDK 代码 | 已实现 | `backend/internal/creationplatform`、`internal/app/creation_platform*.go`、`web/src/sdk/creation-platform` |
| S0–S8 强制验收项 | 通过（隔离集成） | 基础计划 S0–S8 勾选与验收记录；Codex 审查 8 项问题已修复 |
| 基础总标准 F1、F8 | 未通过 | 真实页面、真实供应商与对象存储尚未验证 |
| 合并状态 | 未合并 | PR #130 CI 进行中 |

结论：基础 SDK 的代码与隔离验收满足 A0 门槛；F1/F8 作为已知差额带入，不视为已验证。后续切片若依赖真实供应商行为，按“未验证”记录。

## A0-A：基础接缝 P1–P5 映射

| ID | 设计要求 | 基础 SDK 实际 | 差额 | 处理切片 |
| --- | --- | --- | --- | --- |
| P1 | 结构化文本请求：system、messages、工具定义与结果、toolChoice、多模态引用、输出上限；结果含工具调用、用量、推理 | `GenerationRequest` 只有 `prompt` 与 `parameters`（`maxTokens`、`systemPrompt`、`stream`），结果只有 `text`。现有 Agent 模型步走 `CreateTask` 的 `input.agentRequests.canonical`（`generation.CanonicalAgentRequest`：messages/tools/toolChoice/systemPrompt），不经平台合同 | 缺结构化请求与工具调用结果 | A4：在平台加受信任的 Go 内部入口（不开放 HTTP），由 Agent 经 Go 校验后提交；HTTP 合同不变 |
| P2 | 原子 Prepare/Commit、统一幂等、费用授权、业务绑定、结构化错误、单次上限 | 已有：`submitResolved`（适配器入口）、`existingPlatformSubmission`、`applyCostAuthorization`、`commitPlatformTask`、`replayConcurrentWinner`、`cp.Error` | 无 | 直接复用 |
| P3 | 层级预算授权、并发预留、在途与待核对占额、唯一账单归属 | 只有订单级 `ChargeLimit*` 与按 `BusinessRef` 汇总 | 缺 BudgetGrant 树与预留 | A3：在基础计费层新增 |
| P4 | 任务快照、持久事件、结果恢复、稳定资源 ID、访问鉴权 | 已有：`TaskSnapshot`、`platform_task_events`、`RecoverMedia`、资源访问 | Agent 事件流独立，不混用任务游标 | 复用；Agent 事件另建 |
| P5 | BusinessRef、身份注入、平台错误、模型选择合同 | 已有：`cp.Caller`、`cp.BusinessRef`、`cp.Error`、`cp.ModelSelection` | 无 | 复用 |

## 现有 Agent 代码事实（基线 `d6fdc5169`）

| 事实 | 位置 |
| --- | --- |
| 会话没有独立表：会话身份是 `COALESCE(NULLIF(conversation_id,''), id)`，挂在运行行 `cloud_agent_executions` 上 | `repository/cloud_agent_conversations.go:16`、`model/cloud_agent.go:10` |
| Pi 会话存储 `cloud_agent_pi_sessions`：唯一键 `(user_id, conversation_id)`；**`canvas_id NOT NULL`**；单写者靠 `revision` + `active_run_id` 条件更新，另有 `lease_owner/lease_epoch` 列 | `model/cloud_agent_pi_session.go:21`、`repository/cloud_agent_pi_session.go:19,73,105` |
| 运行请求 `CloudAgentRequest` 以 `CanvasID` 为必需上下文：创建时读画布、冻结画布 Workspace、子代理画布授权、提示缓存键含画布 | `app/cloud_agent.go:24`、`app/cloud_agent_run_create.go:99,111,286,335` |
| 模型步：`PiModelStep` → `CreateTask{ProjectID: CanvasID, Type: canvas_text, Input.agentRequests.canonical}`；首步冻结 Harness 哈希与正文 | `app/cloud_agent_pi_model_step.go:323`、`app/cloud_agent_pi_bridge.go:97` |
| 消息队列 `agent_queued_messages`（唯一 `(user_id, idempotency_key)`）与 `agent_conversation_queues`（主键 `(user_id, conversation_id)`），均带 `canvas_id` | `model/agent_message_queue.go` |
| 临时子代理不可直聊：`NOT EXISTS (SELECT 1 FROM agent_subagent_links l WHERE l.user_id = ? AND l.child_run_id = ?)` | `repository/agent_message_queue.go:22` |
| 子代理链接 `agent_subagent_links`：`canvas_id NOT NULL`、按父 Run 链接 | `model/agent_subagent.go:20` |
| 记忆：每用户一份 `agent_memory_documents`（主键 `user_id`），无作用域；建 Run 时冻结快照注入 | `model/models_agent_memory_document.go:40`、`app/cloud_agent_memory_document.go:368` |
| Pi Worker（Node，约 3400 行）请求体 `canvasId?`，调度键 `${userId}:${canvasId ?? runId}` | `agent/src/bridge.ts:91,549` |
| 公共路由 `/api/agent/runs*`、`/api/agent/conversations*`（含队列）；Worker 路由 `/internal-agent/*`（claim、model-steps、tool-batches、context-compactions 等） | `handler/agent.go`、`handler/agent_message_queue.go`、`handler/internal_agent.go` |
| 规模：`app` 中 Agent 相关约 8 万行、175 个测试文件；前端 Agent 面板 2474 行 + 会话 UI 1784 行 | — |

## 范围判断（实施前必须明确）

计划 A4 要求“画布 Agent 先通过新 Session、Context、基础 SDK 跑完整模型/工具循环”，A8 要求把现有画布面板抽成通用控制器。在现有代码上，这意味着改写约 8 万行 Agent 实现中与画布耦合的入口、Pi 会话存储、队列与前端面板。首期按以下原则收敛，后续切片据此执行：

1. **新增而非替换。** 新的 `agentservice` / `agentorchestration` 领域与表独立实现 Session、Context、工作单、派工、输入与报告；现有画布 Agent（运行、Pi 会话、队列、子代理）保持原路径，不迁移在途运行。
2. **画布 Agent 通过映射接入。** A1 只为现有画布 conversation 建立一对一的 Session 根记录（可重复执行、不复制历史），使统一目录与权限检查能覆盖画布会话；画布 Agent 的执行仍走原 Pi 路径（计划 A4-E“旧在途走旧合同”扩大为“画布 Agent 首期继续走旧执行实现”）。
3. **非画布 Agent 走新执行面。** 短剧、电商与总控的 Session 由新 Runtime 执行：模型步经平台 P1 结构化入口，工具经业务适配器调用既有业务服务。新 Runtime 在 Go 内运行（与短剧调度同类的服务端循环），不新增 Node Worker 协议；Pi Worker 只服务画布 Agent。
4. **费用唯一。** 所有新路径的模型、媒体费用都经平台提交并受 A3 预算树约束；画布 Agent 原有计费不变。

以上与计划字面的差异记为裁定：

- **R-A0-1**：A4 不改写现有 Pi 画布执行链；新 Runtime 在 Go 内实现通用模型/工具循环，经平台 P1 入口计费。理由：画布 Agent 约 8 万行在线逻辑，原地改写的回归面与在途运行风险远超首期目标；计划自身要求“不重建 Pi”“不得双执行”。代价：画布 Agent 首期不经新 Context Builder 与预算树；统一 UI 中画布会话仍由原执行器驱动。
- **R-A0-3**：新 Runtime 在 Go 进程内执行模型/工具循环（模型步是平台任务，结果由 worker 写回），不扩展 `/internal-agent` 协议。理由：避免为非画布业务版本化 Worker 协议。代价：新业务 Agent 首期不能用 Pi 原生技能与压缩，长上下文由 Context Builder 截断与摘要处理。
- **R-A0-2**：A8 新建通用会话组件与 Agent TS SDK，并接入短剧与工作台入口；现有画布面板保留，经 Session 目录可从统一入口打开。理由同上。代价：画布面板与新组件首期并存。

## A0-B：冻结的接口

Go 领域包 `backend/internal/agentservice`（Session、Context、定义注册）与 `backend/internal/agentorchestration`（工作、派工、输入、报告）只定义类型与端口，不导入 `app/service/repository/model`；实现位于 `internal/app/agent_service_host.go` 与 `agent_orchestration_host.go`。所有方法形如 `方法(ctx, Principal, Request) (View/Receipt, error)`，`Principal` 复用 `cp.Caller`（用户由会话或受信任 Go 上下文注入）。

### Scope 与定义

```go
type Scope struct {
	Application string // canvas | drama | ecommerce | orchestration | test
	SpaceType   string // canvas | drama_project | workbench_batch | work
	SpaceID     string
}
type DefinitionView struct {
	ID, Version, Role string // role: supervisor | business
	Application       string
	Tools             []string
}
```

用户身份不在 Scope 内，来自 Principal；Session 行同时保存 `owner_id`。

### 方法（HTTP 前缀 `/api/agent/v2`）

| 方法 | Go 签名要点 | HTTP |
| --- | --- | --- |
| `Definitions.List` | `(ctx, Caller, Scope) ([]DefinitionView, error)` | `GET /definitions?application=` |
| `Sessions.Create` | `(ctx, Caller, CreateSessionRequest{DefinitionID, Scope, WorkItemID?, IdempotencyKey}) (SessionView, error)` | `POST /sessions` |
| `Sessions.Get` / `List` / `Archive` | `Get(ctx, Caller, id)`、`List(ctx, Caller, ListSessionsRequest{Scope?, Cursor, Limit})`、`Archive(ctx, Caller, ArchiveRequest{ID, ExpectedRevision})` | `GET /sessions/:id`、`GET /sessions`、`POST /sessions/:id/archive` |
| `Messages.Send` | `(ctx, Caller, SendMessageRequest{SessionID, MessageID, ExpectedRevision, Text, Attachments []ResourceRef}) (MessageReceipt, error)`；actorKind 由服务端写入 | `POST /sessions/:id/messages` |
| `Runs.Get` / `Cancel` | `Get(ctx, Caller, runID)`、`Cancel(ctx, Caller, CancelRunRequest{RunID, RequestID})` | `GET /runs/:id`、`POST /runs/:id/cancel` |
| `Events.Read` | `(ctx, Caller, ReadEventsRequest{SessionID, After, Limit}) (AgentEventPage, error)`，游标独立于平台任务游标 | `GET /sessions/:id/events?after=` |
| `Approvals.Decide` | `(ctx, Caller, DecideRequest{ApprovalID, Decision, ExpectedAuthorizationVersion, IdempotencyKey}) (ApprovalReceipt, error)` | `POST /approvals/:id/decision` |
| `Context.Build`（内部） | `(ctx, sessionID, expectedRevision) (ContextSnapshot, error)`，不对浏览器开放 | — |
| `Work.Start` | `(ctx, Caller, StartWorkRequest{SourceSessionID, SourceMessageID, Goal, Scope, BudgetGrantID, IdempotencyKey}) (WorkReceipt, error)` | `POST /work` |
| `Work.Delegate` | `(ctx, Caller, DelegateRequest{WorkItemID, ExpectedVersion, DefinitionID, TargetScope, Handoff, BudgetMicrocredits, ActionKey}) (AssignmentReceipt, error)` | 仅总控工具调用（服务端） |
| `Work.AppendInput` | `(ctx, Caller, AppendInputRequest{WorkItemID, ExpectedVersion, SourceMessageID, Text, IdempotencyKey}) (WorkInputReceipt, error)` | `POST /work/:id/inputs` |
| `Work.Pause` / `Resume` / `Stop` | `(ctx, Caller, WorkControlRequest{WorkItemID, ExpectedVersion, Reason, IdempotencyKey}) (WorkReceipt, error)` | `POST /work/:id/{pause,resume,stop}` |
| `Reports.Submit`（执行器内部） | `(ctx, SubmitReportRequest{AssignmentID, LeaseEpoch, WorkVersion, RunID, ReportID, Status, Summary, Resources, Issues}) (ReportReceipt, error)` | — |
| `Work.Get` / `ListReports` | `Get(ctx, Caller, id)`、`ListReports(ctx, Caller, ListReportsRequest{WorkItemID, After})` | `GET /work/:id`、`GET /work/:id/reports` |
| 基础 `BudgetGrants.Create/Allocate/Close/Get` | 位于 `creationplatform` 扩展：`CreateRoot(ctx, Caller, {LimitMicrocredits, WorkRef, IdempotencyKey})`、`Allocate(ctx, Caller, {ParentID, LimitMicrocredits, WorkRef, IdempotencyKey})`、`Close`、`Get` | 仅服务端；`GET /api/platform/v1/budget-grants/:id` 只读 |

错误统一使用 `cp.Error`，新增码 `session_conflict`、`scope_mismatch`、`work_stopped`、`budget_exceeded`、`stale_assignment`。

## A0-C：迁移清单

| 入口 | 首期处理 |
| --- | --- |
| 画布 Agent 创建、续轮、队列、插话、模型步、压缩、工具/媒体、审批、历史、子代理 | 保持原实现；A1 为每个画布 conversation 建立 Session 映射（只读目录与权限），不迁移执行 |
| 画布前端面板与历史 | 保持；A8 统一入口可列出并打开画布会话 |
| 短剧、电商、总控 Agent | 新建：Session、Context、Runtime、工具适配器、工作单 |
| 原收费入口 | 新路径的模型与媒体费用一律经平台（P1 结构化入口 + 既有 `submitResolved`），并受 BudgetGrant 约束 |
| 临时子代理 | 保持原限制（不可直聊）；新的 business Session 不受该排除条件影响 |

## A0-D：持久结构（迁移 v86 起，按实际序列追加）

| 表 | 关键字段 | 约束 |
| --- | --- | --- |
| `agent_sessions` | id, owner_id, definition_id, definition_version, role, application, space_type, space_id, work_item_id, legacy_conversation_id, revision, lease_owner, lease_epoch, lease_expires_at, active_run_id, status, archived_at | 唯一 `(owner_id, idempotency_key)`；唯一 `(owner_id, legacy_conversation_id)`（非空时）；Scope 列创建后不可变（仓储层拒绝更新） |
| `agent_session_messages` | id, session_id, owner_id, message_id, actor_kind, body, attachments_json, revision_at_send, status, run_id | 唯一 `(owner_id, session_id, message_id)` |
| `agent_session_runs` | id, session_id, owner_id, lease_epoch, status, budget_grant_id, work_item_id, assignment_id, frozen_contract_json | 索引 `(session_id, status)` |
| `agent_session_events` | id, session_id, owner_id, position, type, payload_json | 唯一 `(session_id, position)` |
| `agent_memory_notes` | id, owner_id, scope_kind(session/business_space/user_preference/legacy), session_id, application, space_id, body, revision | 旧用户记忆迁为 legacy，只读、不注入 |
| `agent_work_items` | id, owner_id, parent_work_id, goal, version, status, source_session_id, source_message_id, scope, budget_grant_id, result_json | 唯一 `(owner_id, source_message_id, action_key)` |
| `agent_assignments` | id, work_item_id, executor_session_id, lease_epoch, status, end_reason | 每工作至多一个 `status != ended`（部分唯一索引，SQLite/PostgreSQL 均支持） |
| `agent_work_inputs` / `agent_work_reports` | 来源消息、目标、正文、版本；报告 report_id | 唯一 `(work_item_id, idempotency_key)`、`(assignment_id, report_id)` |
| `platform_budget_grants` / `platform_budget_reservations` | 基础层：id, owner_id, parent_id, limit, allocated, status, version；预留 task_id、amount | 单父、同付款账户、预留与订单一一对应 |

Agent 侧不新建账户、订单或第二份消息历史；画布会话的历史仍在原 Pi 表。

## A0-E：文件规划

| 文件 | 切片 | 责任 |
| --- | --- | --- |
| `internal/agentservice/{contract,ports,registry,session,context}.go` | A1、A2 | 类型、端口、定义注册、Scope 规则、Context 组装规则（纯函数） |
| `internal/agentorchestration/{contract,ports,work}.go` | A5 | 工作状态机、派工与报告规则（纯函数） |
| `internal/model/agent_session.go`、`agent_work.go`、`platform_budget.go` | A1、A3、A5 | 持久行 |
| `internal/repository/agent_session.go`、`agent_work.go`、`platform_budget.go` | A1、A3、A5 | 条件更新、唯一约束、事务 |
| `internal/database/migrations_agent_service.go` | A1–A5 | v86 起迁移 |
| `internal/app/agent_service_host.go`、`agent_runtime.go`、`agent_orchestration_host.go`、`agent_business_{canvas,drama,ecommerce}.go`、`creation_platform_structured.go`、`creation_platform_budget.go` | A1–A7 | 组合根实现 |
| `internal/handler/agent_sessions.go`、`agent_work.go` | A1、A5 | HTTP 适配 |
| `web/src/sdk/agent/{types,client,controller}.ts`、`web/src/components/agent/conversation/` | A8 | 前端 SDK 与共用组件 |

## 证据

- 基础接缝：上表 P1–P5 依据基础 SDK 实际源码与 PR #130 测试证据。
- 现有代码事实：本文“现有 Agent 代码事实”各行均附源码位置，基线 `d6fdc5169`。
- A0 不包含运行验证；后续切片的测试名以实际落地为准。
