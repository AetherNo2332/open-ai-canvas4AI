# 平台内部创作 SDK：S0 合同锁定与迁移清单

> 对应计划：[2026-11-05-internal-platform-sdk-rollout.md](2026-11-05-internal-platform-sdk-rollout.md) 第 5 节 S0。
> 分支 `feat/creation-platform-sdk`，基线 `5a0ad2ce6`（与 `upstream4ai/main` 一致）。
> S0 只是设计通过，不能计为运行功能完成。

## S0-A 决策记录

| ID  | 选择                                                                                            | 理由                                                             | 确认状态                 |
| --- | ----------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- | ------------------------ |
| D1  | 首版只统一系统渠道与逻辑模型；自定义渠道保留 `/api/ai/custom` 与原 `/api/tasks` 路径            | 自定义渠道凭据在浏览器、计费不适用，纳入需另补凭据来源与回归清单 | 用户已确认（2026-11-05） |
| D2  | 平台支持单次费用上限与按业务归属汇总；短剧累计预算留在短剧                                      | 平台级累计上限需要事务内预算预留和并发验收，超出首版             | 用户已确认（2026-11-05） |
| D3  | Go 合同 `backend/internal/creationplatform`；TS SDK `web/src/sdk/creation-platform`；合同版本 1 | 同仓库维护，按文档建议                                           | 用户已确认（2026-11-05） |

“不走平台计费”的自定义渠道在任何 SDK 视图中都不显示为“免费”：合同的 `BillingState` 只有 `free` 表示平台确认的零价，自定义渠道任务不经过 SDK。

## S0-B 合同

源码：`backend/internal/creationplatform/contract.go`、`errors.go`、`ports.go`；`web/src/sdk/creation-platform/contract.ts`。
样例：`backend/internal/creationplatform/testdata/*.json`，Go 测试 `contract_test.go` 与 TS 测试 `web/test/creation-platform-contract.test.ts` 读取同一组文件。

| 覆盖要求               | 合同中的落点                                                                                                                                                                                                                                             |
| ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 四种生成能力           | `Capability`：text / image / video / audio，四个 `request-*.json` 各覆盖一种                                                                                                                                                                             |
| 两种模型选择           | `ModelSelection` 联合类型：`logicalModelId`，或 `channelId + modelKey`；同时给出或缺半边返回 `invalid_request`                                                                                                                                           |
| 三类状态               | `ExecutionState`、`ResourceState`、`BillingState` 分列；`snapshot-media-recovering.json` 是“执行成功、交付失败”，`snapshot-billing-pending.json` 是“费用待核对、实收未知”                                                                                |
| 错误                   | `Error{code, reason, message, retryable, taskId, requestId}`；code 为稳定枚举，reason 对齐后端信封                                                                                                                                                       |
| 事件                   | `TaskEvent{id, taskId, revision, type, execution, resources, billing, occurredAt}` 与 `EventPage{events, nextCursor, resyncRequired}`                                                                                                                    |
| 无密钥、无业务专属字段 | `TestContractTypesCarryNoSecretOrBusinessSpecificFields` 反射遍历全部合同类型，禁止 apiKey/secret/baseUrl/header/token/cookie/credential/interfaceType 及 nodeId/shotId/rowId/segmentId/productId/canvasId/episodeId；已用注入 `apiKey` 字段验证它会失败 |

幂等指纹：`GenerationRequest.Fingerprint()` 对去掉 `traceId` 的请求做 JSON 序列化后取 SHA-256；map 键序列化有序，参数顺序不影响；参考素材只有稳定资源 ID，不含临时 URL。

### Go 签名（`ports.go`）

```go
type Caller struct{ UserID string }

type Platform interface {
	Models(ctx, caller, capability) (ModelCatalog, error)
	Quote(ctx, caller, req GenerationRequest) (Quote, error)
	Submit(ctx, caller, req GenerationRequest) (SubmitReceipt, error)
	Task(ctx, caller, taskID) (TaskSnapshot, error)
	Events(ctx, caller, query EventQuery) (EventPage, error)
	Cancel(ctx, caller, taskID) (TaskSnapshot, error)
	Retry(ctx, caller, taskID) (TaskSnapshot, error)
	RecoverMedia(ctx, caller, taskID) (TaskSnapshot, error)
	Balance(ctx, caller) (CreditBalance, error)
	BillingAggregate(ctx, caller, business BusinessRef) (BillingAggregate, error)
}
```

身份只来自 `Caller`：HTTP 适配层取登录会话用户，Go 编排用业务对象所有者。`BusinessRef` 不参与授权。

### HTTP 映射（S1–S4 落地，统一 `{code,data,msg,reason}` 信封）

| 方法 | 路径                                                                    | 对应                                                             |
| ---- | ----------------------------------------------------------------------- | ---------------------------------------------------------------- |
| GET  | `/api/platform/v1/models?capability=`                                   | `Models`                                                         |
| POST | `/api/platform/v1/quotes`                                               | `Quote`                                                          |
| POST | `/api/platform/v1/generations`                                          | `Submit`，返回 `SubmitReceipt`                                   |
| GET  | `/api/platform/v1/tasks/{id}`                                           | `Task`                                                           |
| GET  | `/api/platform/v1/events?taskId=&application=&projectId=&after=&limit=` | `Events`（JSON 分页，按游标续读；不新增 SSE 路径，nginx 无需改） |
| POST | `/api/platform/v1/tasks/{id}/cancel` / `retry` / `recover-media`        | 对应动作，遵循快照 `actions`                                     |
| GET  | `/api/platform/v1/billing/balance`                                      | `Balance`                                                        |
| GET  | `/api/platform/v1/billing/aggregate?application=&projectId=`            | `BillingAggregate`                                               |

资源上传与授权访问复用既有 `/api/resources`、`/api/resources/access`，文本增量复用 `/api/tasks/{id}/text-deltas`，不建立第二套资源持久化。平台错误在信封中以 `reason` 携带合同 `code`。

## S0-C 调用方清单

### 迁移（首版接入平台合同）

| 业务           | 入口                                                                                                                                                                          | 能力                      | 迁移位置                                                    |
| -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------- | ----------------------------------------------------------- |
| 电商工作台     | `app/workbench_bridge.go:CreateWorkbenchTask` ← `pages/workbench/image-workbench-page.tsx:createBatchTask`（系统/逻辑模型分支）                                               | image, video              | S5：后端改经 `Platform.Submit`，前端经 SDK 提交与等待       |
| 短剧编排       | `app/drama_orchestrator.go:createTaskWith` / `submitDramaTask`                                                                                                                | text, image, video        | S6：Go 内调用 `Platform.Submit`，幂等键取作业 ID + 尝试序号 |
| 画布命令提交   | `app/canvas_task_submitter.go`（`tasks.Service.Submit`，现唯一调用方为分镜拆分）                                                                                              | text, image, video, audio | S7：Prepare/Commit 复用平台提交核心与事务绑定端口           |
| 画布浏览器生成 | `lib/canvas/canvas-project-generation.ts`、`pages/canvas/canvas-*-generation-executor.ts`、`use-canvas-media-tools.ts`、`use-canvas-generation-retry.ts`（系统/逻辑模型分支） | 全部                      | S7：经 SDK 提交，结果放置仍在画布消费层                     |
| 通用任务 API   | `services/api/generation-task.ts`、`task-center.ts`、`resources.ts` 的通用调用                                                                                                | 全部                      | S4：迁入 SDK，业务包装留在原位                              |
| 任务重试       | `app/task_lifecycle.go:retryTask`                                                                                                                                             | 全部                      | S3：平台任务的重试只经 `Platform.Retry`，遵循允许动作       |

### 相邻调用方（回归验证，不迁移）

| 调用方                                                                                                                                                                                                                                                                                                          | 理由                                                         |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------ |
| 云端 Agent：`cloud_agent_media.go`、`cloud_agent_media_approval.go`、`cloud_agent_pi_model_step.go`、`cloud_agent_context_compaction.go`、`cloud_agent_pi_native_compaction.go`、`cloud_agent_run_create.go`、`cloud_agent_task_enqueue.go`、`cloud_agent_write_approval.go`、`cloud_agent_storyboard_split.go` | Agent 运行、审批、预算不在首版（第 1 节）；S7-E 验证不被绕过 |
| 智能创作：`app/creation.go`（`PrepareCreationSubmission`/`ExecuteCreationSubmission`）及 `services/creative-agent-controller.ts`、`approved-tool-execution.ts`、`art-critique-execution.ts`、`pages/create/`                                                                                                    | 报价确认与会话混合，计划第 2 节判定不能整体搬入 SDK          |
| 文本前端回放：`createTextReplayTask`（`input.replay`）                                                                                                                                                                                                                                                          | 前端自管文本，无计费无队列                                   |
| 视频处理（画质增强）：`video_processing.go`                                                                                                                                                                                                                                                                     | 按输出时长结算的专用路径，非生成意图                         |
| 任务中心手工创建：`pages/tasks/index.tsx:submitTask`                                                                                                                                                                                                                                                            | 手工高级入口，保留原 `/api/tasks`                            |
| 技能草稿、字幕高亮、列表模式文本：`lib/canvas/skill-drafting.ts`、`lib/timeline/subtitle-highlight-runner.ts`、`pages/canvas/list-mode-generator.ts`                                                                                                                                                            | 首版不迁移，S8-A 再评估                                      |

### 显式例外

| 例外                                                                                                                                                                       | 理由                             |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------- |
| 自定义渠道（`/api/ai/custom`、`/api/tasks` 带 `baseUrl+apiKey`、工作台自定义分支）                                                                                         | 决策 D1                          |
| 系统渠道浏览器直连代理 `/api/ai/system/:channelId/*`（`ReserveProxyBillingWithBody`）及其前端调用：`plugin-host.ts`、`editor-ai-assistant.tsx`、`model-connection-test.ts` | 同步代理，不产生任务；首版不纳入 |
| 工作流 / RunningHub（`workflow_provider.go`、`workflow-test-workbench.tsx`）                                                                                               | 通用工作流引擎不在首版           |
| 时间线渲染与转写（`task_timeline.go`、`drama_orchestrator.go:createDramaRenderTask`、`editor-export.tsx`、`editor-transcription.tsx`）                                     | 渲染保留原能力，非模型生成       |
| 管理端模型测试 `TestAdminChannelModel`                                                                                                                                     | 管理诊断，不产生任务             |
| 预演提交 `canvas/tasks/previs.go:SubmitPrevis`                                                                                                                             | 无生产调用方，服务端预演已下线   |

清单中发现：`character_turnaround` 与 `project_asset_image` 两个结果处理函数在前后端都没有产生方，迁移不涉及，留作待办核对。

## S0-D 文件规划

| 文件                                                        | 状态          | 责任                                                                                                                                       | 输入 → 输出                                  |
| ----------------------------------------------------------- | ------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------- |
| `backend/internal/creationplatform/contract.go`             | 新增（S0）    | 合同类型、结构校验、幂等指纹                                                                                                               | 请求 JSON → 校验错误 / 指纹                  |
| `backend/internal/creationplatform/errors.go`               | 新增（S0）    | 平台错误码与 HTTP 状态                                                                                                                     | code → reason/status                         |
| `backend/internal/creationplatform/ports.go`                | 新增（S0）    | `Platform` 端口与 `Caller`                                                                                                                 | —                                            |
| `backend/internal/app/creation_platform*.go`                | 新增（S1–S3） | 组合根实现：请求解析为既有任务输入（解析）、复用 `CreateTask` 准入与计价（准入）、幂等记录与业务绑定写入（持久化）、快照投影；不含业务采用 | `Caller+GenerationRequest` → `SubmitReceipt` |
| `backend/internal/model/models_creation_platform.go`        | 新增（S2/S3） | `platform_submissions`、`platform_task_events` 表结构                                                                                      | —                                            |
| `backend/internal/repository/creation_platform.go`          | 新增（S2/S3） | 幂等记录、事件读取、汇总查询                                                                                                               | —                                            |
| `backend/internal/database/migrations_creation_platform.go` | 新增（S2/S3） | 新表、唯一约束与事件触发器（SQLite / PostgreSQL）                                                                                          | —                                            |
| `backend/internal/handler/creation_platform.go`             | 新增（S1）    | HTTP 适配：会话 → `Caller`，平台错误 → 信封                                                                                                | HTTP → `Platform`                            |
| `backend/internal/service/aliases_creation_platform.go`     | 新增（S1）    | 稳定导入面再导出                                                                                                                           | —                                            |
| `web/src/sdk/creation-platform/contract.ts`                 | 新增（S0）    | TS 合同与同口径校验                                                                                                                        | —                                            |
| `web/src/sdk/creation-platform/client.ts`、`transport.ts`   | 新增（S4）    | 可注入传输、幂等重投、等待与订阅                                                                                                           | —                                            |
| 业务采用                                                    | 保持原位      | 工作台批次、短剧剧情段版本、画布节点放置                                                                                                   | 平台快照 → 业务对象                          |

依赖方向：`creationplatform` 不导入 `app`/`service`/`repository`/`model`；`app` 实现 `Platform`；`handler` 通过 `service` 别名使用；架构守卫已对新包通过。
