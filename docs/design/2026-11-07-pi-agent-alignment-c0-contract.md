# Pi Agent 全域执行合同（C0）

> 本文件是[C0 合同与迁移清单](../plans/2026-11-07-pi-agent-service-alignment.md)的落地记录，合同权威仍是
> [Pi Agent 服务与总控设计对齐](./2026-11-07-pi-agent-service-alignment.md)。
> 代码合同在 `backend/internal/agentservice/piexecution.go`，回归在 `piexecution_test.go`。

## 1. 执行协议与责任边界（C0-C）

### 1.1 被取代的历史裁定

| 历史条目 | 旧内容 | 当前状态 |
| --- | --- | --- |
| R-A0-1 | 非画布业务由 Go 进程自行跑模型/工具轮次 | **被取代**。所有 Agent 的模型与工具循环由同一 Pi Worker Runtime 执行，Go 只提供模型步、工具网关与状态。 |
| R-A0-3 | 画布单独接 Pi，其余业务沿用 Go 循环 | **被取代**。画布只是 Pi Runtime 的一个 Host 适配器，业务之间不按执行内核分叉。 |
| 旧 B0–B6 运行器选择 | `agent_runner.go` + `agent_business_turn.go` 作为业务主循环 | `agent_runner.go` 演进为消息调度、Pi 执行投递、事件唤醒与状态投影；模型/工具循环迁入 Pi Runtime。 |

### 1.2 责任边界

| 层 | 负责 | 不负责 |
| --- | --- | --- |
| Go 控制面 | 身份、Definition/Session/Scope、持久消息队列、工作/Assignment、预算/审批、模型步与压缩准入、工具网关、业务事务、报告与事件 | 模型会话循环、工具调度、原生压缩执行、Pi 检查点生成 |
| Pi Worker | 领取运行、租约心跳、`createAgentSession` 循环、工具注册与调度、原生压缩准备/提交、检查点上报、恢复 | 授权判定、计费、跨 Session 数据共享、业务状态真相 |
| 业务适配器 | 业务上下文、工具 Schema、调用既有业务服务 | 会话身份、授权、计费 |

### 1.3 冻结合同

- 协议版本：`pi-execution/v1`（`agentservice.PiExecutionProtocolVersion`）。
- 每条待执行消息只选择一个执行协议；切换必须记录到消息、任务、费用与操作回执。禁止同一消息同时被 Go 循环和 Pi Worker 推进。
- 首步冻结 Definition 版本、有效工具 Schema、模型限制、提示与技能快照；恢复使用原合同，新轮次才读取新版本。

## 2. 通用执行 DTO（C0-A）

Go 合同位于 `backend/internal/agentservice/piexecution.go`；Node 侧 `agent/src/bridge.ts` 的 `CANVAS_PI_WIRE_IDENTITY` 在 C1 增加 `pi-execution/v1` 兼容读取分支。

### 2.1 `AgentRunSnapshot`

字段：`protocolVersion`、`ownerId`、`sessionId`、`runId`、`triggerMessageId`、`leaseEpoch`、`piSessionRevision`、`definition{id,version,role,application,spaceTypes,title,tools}`、`scope{application,spaceType,spaceId}`、`effectiveTools[]`、`modelLimits`、`execution`、`prompt`、`skills[]`。

失败语义（`Validate`）：

| 条件 | 错误码 | 原因 |
| --- | --- | --- |
| 协议版本不一致 | `invalid_request` | 执行协议版本不一致 |
| owner 缺失 | `unauthorized` | 执行者不能为空 |
| session/run/触发消息缺失 | `invalid_request` | 会话/运行/触发消息不能为空 |
| `leaseEpoch <= 0` | `stale_lease` | 租约代次无效 |
| 角色非法 | `invalid_request` | Agent 定义角色无效 |
| Scope 不完整 | `scope_mismatch` | 会话范围必须包含业务、空间类型与空间 ID |
| 工作执行缺 grant | `budget_exceeded` | 工作执行缺少预算授权 |
| 工作执行缺授权版本 | `invalid_request` | 工作执行缺少授权版本 |
| 工作执行缺 Assignment 代次/ID | `stale_assignment` | 工作执行缺少 Assignment 代次/Assignment |
| 工具缺参数 Schema | `invalid_request` | 工具缺少参数 Schema |
| 业务运行无有效工具 | `invalid_request` | 业务执行缺少有效工具 |
| 上下文窗口缺失 | `invalid_request` | 模型限制缺少上下文窗口 |

### 2.2 `ExecutionBinding`

`workItemId`、`assignmentId`、`assignmentEpoch`、`budgetGrantId`、`authorizationVersion`。空工作项代表独立执行（独立咨询、无工作归属），此时不伪造根工作。有工作项则五项必须完整，由 Go 从授权记录解析，模型与前端不能写入。

### 2.3 `SessionMessage`

`targetSessionId`、`senderUserId`、`actor`、`messageId`、`body`、`resourceRefs[]`、`runId`、`leaseEpoch`、`workItemId`。

- `messageId` 是幂等键；重复投递返回同一受理结果。
- `actor` 只接受服务端角色词表（`user`/`supervisor`/`executor`/`system`），请求体不能自选为新角色。
- 正文与资源引用不能同时为空；资源引用逐项非空。
- 发送执行必须携带 `runId + leaseEpoch`，用于审计“谁把消息送进了队列”。

### 2.4 `RecoveryReport`

失败类别：`conflict`、`rate_limited`、`timeout`、`http_5xx`、`network`；操作词表：`model`、`tool`、`checkpoint`、`compaction`、`control`、`phase`、`renew`、`snapshot`。未知类别或操作被拒绝，避免审计口径被悄悄扩张。恢复语义是“重读权威状态”，不是重放失败请求。

## 3. 画布耦合迁移清单（C0-B）

| # | 现有耦合 | 位置 | 迁移方向 |
| --- | --- | --- | --- |
| 1 | Pi 会话表要求 CanvasID | `backend/internal/model/cloud_agent_pi_session.go:26`（`CanvasID not null`） | 增加通用 `SessionID`/Scope 投影；CanvasID 改为可选，仅画布适配器写入；唯一键改为 `(owner, session)`，旧 `(owner, conversation)` 兼容读取 |
| 2 | 运行行以 CanvasID 编排 | `backend/internal/model/cloud_agent.go:34` | 增加 `SessionID`、`Application/SpaceType/SpaceID` 列；`CanvasID` 保留为画布投影，不参与通用调度 |
| 3 | 运行创建强校验画布 | `backend/internal/app/cloud_agent_run_create.go:99,140,286,381` | 通用 Host 按 Definition+Scope 解析，画布路径走画布适配器；业务运行不再要求 CanvasProject |
| 4 | 输入队列以 CanvasID 校验与切换 | `backend/internal/model/agent_message_queue.go:11,38`；`backend/internal/app/agent_message_queue.go:128,213` | 队列键改为 `owner+sessionId`；画布兼容层由适配器校验；总控发送走同一队列 |
| 5 | Worker 调度键带画布 | `agent/src/bridge.ts:549`；`agent/src/event-scheduler.ts:134,139,147` | 调度键改为 `userId:sessionId`，画布/业务/总控统一；旧运行无 sessionId 时回退 `runId` |
| 6 | 协议身份为画布专用 | `agent/src/bridge.ts:20-24`（`canvas-pi-wire/v4`） | C1 增加 `pi-execution/v1` 协商；Go 与 Worker 双方校验，旧运行继续按 v4 读取 |
| 7 | Snapshot 的 `request.canvasId` 可选但其余仍画布 | `agent/src/bridge.ts:91` | 通用 Snapshot 增 `sessionId/definition/scope/execution`；画布字段进入 `request` 兼容块 |
| 8 | 准入公平性按画布计数 | `backend/internal/model/agent_admission.go:35`；`backend/internal/repository/agent_admission.go:109,210` | 计数键从 canvas 改为 session scope；画布运行与业务运行共享同一用户级上限，画布上限保留为适配器配置 |
| 9 | 模型步/压缩/工具路由以 run 为键但内部按画布校验 | `backend/internal/handler/internal_agent.go`；`backend/internal/app/cloud_agent_pi_*.go` | 路由保持不变；内部授权校验改为 `SessionID + ExecutionBinding`，画布规则收敛到适配器 |
| 10 | 历史与记忆注入按画布 | `backend/internal/app/cloud_agent_memory_document.go`、`cloud_agent_run_create.go:311-335` | 通用 Context Builder（`agent_service_context.go`）作为唯一入口；画布文档、业务文档、Session note 按 Scope 分范围 |
| 11 | 业务 Go 循环与自建摘要仍存在 | `backend/internal/app/agent_business_turn.go`、`agent_runner.go`、摘要相关实现 | C6 切换：调度器只投递 Pi 执行；自建摘要停用，改 Pi 原生压缩（保留 Go 计费与持久化桥） |

## 4. 迁移与切换步骤（C0-D）

1. **加列不删列**：为运行、Pi 会话、输入队列增加通用身份列（session/scope/execution），保留 CanvasID 供旧运行读取；迁移必须同时提供 SQLite 与 PostgreSQL 路径，并登记到数据库模型清单与复制工具。
2. **双读单写**：Pi 会话读取按 `SessionID`，缺失时按 `(owner, conversation)` 回退并记录迁移标记；新运行只写通用列。
3. **在途运行冻结**：已有 queued/running 的 Go 循环运行继续按旧协议完成或失败，不切到 Pi；切换记录写入运行状态与审计。新消息只投递 `pi-execution/v1`。
4. **历史投影**：既有会话消息转为只读上下文，保留来源、顺序、MessageID 与已执行回执；Pi entries 不复制，仍按会话读取。重复展示以 MessageID 去重。
5. **工具回执**：在途工具调用按 `(run, taskId, callId, operationId)` 恢复；迁移后同一操作只允许一份有效回执，迟到回执只进入审计。
6. **回退条件**：Worker 不可用时运行保持可审计的待执行状态；恢复后从原消息与检查点继续。禁止用“重新调用模型/工具”代替恢复。
7. **验收门槛**：C1 验证非画布领取与恢复；C2 验证同源 Runtime 与压缩；C6 验证在途切换、复制工具与三业务回归。

## 5. C0 验收证据

| 验收 | 证据 |
| --- | --- |
| C0-A | `go test ./internal/agentservice -run 'TestAgentRunSnapshot\|TestSessionMessageContract\|TestRecoveryReportContract' -count=1` 通过；四类角色 Snapshot 全字段校验，非画布（drama/ecommerce）使用完整 Scope。 |
| C0-B | 第 3 节迁移表，逐项给出文件与行号及目标形态。 |
| C0-C | 第 1 节取代标记、责任边界与 `pi-execution/v1` 冻结版本。 |
| C0-D | 第 4 节迁移、恢复、回退与门槛；C1/C2/C6 继续验证。 |
