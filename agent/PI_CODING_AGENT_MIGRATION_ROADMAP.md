# Canvas Agent 迁移到 Pi Coding Agent：实施与验收路线图

> **执行顺序更新**：用户选择 canary 直接移除旧编排、接入 Pi Coding Agent，再逐项恢复能力。新的施工顺序、多用户 session 模型与**Pi 协议层**见 [`PI_CODING_AGENT_CANARY_DIRECT_CUTOVER.md`](./PI_CODING_AGENT_CANARY_DIRECT_CUTOVER.md)；本文件继续提供数据合同、能力清单和三端验收矩阵。下文 P1.5→P6 的旧排序不再作为开工顺序。

> 本文是交接用的实施路线图，不是已完成声明。最新工作树核对：2026-09-27，`canary`，HEAD `7aa99880`；代码仍有大量未提交改动。阶段证据以当前代码和 [`PI_MIGRATION_PHASE_STATUS.md`](./PI_MIGRATION_PHASE_STATUS.md) 的最新记录为准。本次更新只改文档，不部署、不推送。

## 0. 目标、依据与当前状态

**目标**：在独立 Node Agent 服务内嵌固定版本 `@earendil-works/pi-coding-agent@0.87.1` SDK，以其 `AgentSession`、资源装配、会话树和恢复流程作为唯一 Agent 编排；**上下文摘要保留项目原有 Go 结构化语义压缩**，Pi 只承接触发钩子、会话投影和压缩边界。`pi-agent-core` 是 SDK 的依赖，不再由本项目直接构造第二套会话编排。Go 继续独占用户鉴权、画布/资源写入、模型渠道及上游适配、任务与计费、持久化、公开 `/api/agent` 和 SSE。Web 只依赖 Go 的公开合同。

**已有实现，不等于目标完成**：`agent/package.json` 已锁定 `pi-agent-core`、`pi-ai` 和 `pi-coding-agent` 0.87.1；生产 `agent/src/runner.ts` 走 `createAgentSession`。Go 已有 `/internal-agent` 领取、模型、工具和检查点接口；工具目录目前为 24 个具体工具，Pi extension 启动时注册符合权限与能力的集合，并由 `tool_call` hook 门控。`CANVAS_AGENT_ENGINE` 的失效开关已从运行配置消除。旧 Go 调度扫描已移除，但旧状态机实现仍在工作区并被大量旧测试调用；不能把旧驱动测试当作新引擎验收。首步合同（占位任务、原子换单、提示正文快照、终态退款）已落代码，详见 [`PI_MIGRATION_PHASE_STATUS.md`](./PI_MIGRATION_PHASE_STATUS.md) §2.13 和当前日记。

### 2026-09-27 工作树增量与下一阻断

| 范围 | 当前状态 | 剩余边界 |
| --- | --- | --- |
| P0 / P1 基线和 SDK 探针 | 文档、线格式样本与真实 SDK 探针已形成；锁定依赖已安装 | 探针没有进入生产 runner；此前测试通过数须在交付时重跑 |
| 运行正确性 M-08～M-10 | `PiFailModelStep` 终态守卫、看门狗 `CleanupPending`、未领取 run 独立超时语义已落代码和测试 | 看门狗之后的实际清理、双 worker 和 PostgreSQL 故障注入仍要跨进程验证 |
| 看图 M-11 | `PiModelStep` 已接回图片引用白名单、水合与超限裁剪；有最小复现和专项测试 | 本地渠道尚无图片输入能力声明，真实模型看图链路未验；视觉观察账本 M-07 仍未接回 |
| 首步提示合同 | Node 不再让 `SYSTEM.md` 替换服务端策略；Go 有策略准入校验；无根任务运行的读取、取消、插话入口已兼容；**首步在首个 `PiModelStep` 冻结 Harness 正文、哈希、策略身份、工具 schema 身份与装配提示身份，之后只比对**；快照回发冻结正文，恢复不重读磁盘 | SDK 版本未入快照（待 Node 上报）；Go 只以包含关系校验策略正文；工具 schema 目前按同名参数结构比对，尚未做版本化段落的全量等价 |
| 计费与数据 | 并发结算闸门已修；**建 run 只创建不可领取的 `holding` 占位任务 + 报价预留 + 执行记录（同一事务）；首个 `PiModelStep` 在同一事务里退还占位预留、预留首步报价、落库真实首步任务并推进阶段；首步准入失败当场给终态并退款；清扫排空补做未收尾运行的退款**；`DrainPendingPiAgentCleanups` 接进看门狗 | Pi v3 entry/operation 表及 lease epoch 仍缺失（`operation` 级幂等与同键不同参数冲突未做）；PostgreSQL 与崩溃注入未验 |

**当前第一优先级是完成首步合同，再切换 SDK 生产路径。** 已确定沿用现有“首个任务报价预留”语义，使用专门的不可领取占位任务承载建 run 时的预留；这不是“建 run 就锁住整轮预算”。若产品明确改为锁定整轮预算，需要另做财务模型设计，不能把 `MaxCharge` 误作已预留额。占位任务的状态必须保证 `ClaimNextTask` 永不领取；首个 `PiModelStep` 在一笔事务内重新报价、释放占位预留、创建真实首步任务并更新 run；取消、失联和终态清理必须退还占位预留。详见 [`PI_MIGRATION_HANDOFF.md`](./PI_MIGRATION_HANDOFF.md) 与阶段状态 §2.8。

**证据顺序**：当前代码、配置、锁文件和运行测试结果优先；[`PI_MIGRATION_DAILY_2026-09-27.md`](./PI_MIGRATION_DAILY_2026-09-27.md) 与 [`PI_MIGRATION_GAPS.md`](./PI_MIGRATION_GAPS.md) 是历史记录，其中早期“仅 4 个测试”“尚未切换”与后续日结相冲突，不能直接复制为现状。仓库根 [`AGENTS.md`](../AGENTS.md) 是协作约定；旧 `AGENT.md` 在当前工作树已删除。用户给出的目标高于旧文档中“仅移植 core、不换包”的决定。

**上游依据（固定 tag，实施时核对导出的 TS 类型）**：

- [Coding Agent SDK](https://github.com/earendil-works/pi/blob/v0.87.1/packages/coding-agent/docs/sdk.md)：`createAgentSession`、`SessionManager`、`modelRuntime`、`resourceLoader`、`customTools`，以及 `agent_settled` 完成语义。
- [Custom Provider](https://github.com/earendil-works/pi/blob/v0.87.1/packages/coding-agent/docs/custom-provider.md)：自定义 `streamSimple` 的事件、取消、用量和请求观测合同。
- [Session Format](https://github.com/earendil-works/pi/blob/v0.87.1/packages/coding-agent/docs/session-format.md)：v3 树形 entry、system/tool patch、compaction、usage 和 context edit。
- [Compaction](https://github.com/earendil-works/pi/blob/v0.87.1/packages/coding-agent/docs/compaction.md)：阈值、`session_before_compact` 和会话压缩条目格式；其默认摘要策略不是 Canvas 的结构化语义压缩合同。

## 1. 不可突破的运行边界

| 责任 | Node / Coding Agent | Go backend | Web |
| --- | --- | --- | --- |
| 身份和归属 | 只持有经 Go 签发的 run/租约上下文，不决定 userId | 会话、画布、资产、任务每次校验归属；内部服务认证及租约 fencing | 使用用户会话；不得访问 `/internal-agent` |
| 编排 | `AgentSession` 的提示、事件、工具披露、队列、压缩边界与恢复；`agent_settled` 后判定本次 SDK 运行已停 | 原有结构化语义压缩、审批、取消、业务完成判定、硬预算、任务状态和资源清理 | 显示运行、审批、插话、取消、结果 |
| 模型 | Canvas Provider 把 Pi 模型步骤送给 Go；不持有上游 API 密钥 | 模型路由/协议/上游 URL、任务、用量、计费、provider 异常 | 展示经 Go SSE 投影的内容 |
| 工具 | Pi extension 启动时注册所有合格具体工具；`tool_call` hook 检查本批准入；顺序执行 | 每次工具调用重新预检权限、能力、参数、版本、审批；执行且入账 | 不信任工具回执即可预测画布变化，重取/合并服务端版本 |
| 数据 | 会话活跃态可驻内存；每个不可重放的边界先经 Go 持久化 | 数据库唯一写入方，原子提交操作/回执/事件/状态 | 本地缓存只是展示，不作恢复事实来源 |

Node 不直接访问数据库、画布存储、模型上游，也不暴露默认 `bash`、文件读写或任意网络工具。工具描述仍从 Markdown Harness 同步；参数 schema 由共享版本化制品提供，Go 端运行时校验始终是最终准入。现有 `finish_run`、视觉观察账本、技能、记忆、计划、审批、媒体任务和画布版本语义均保留。

### 一次请求的目标链路

1. Web 发起 `/api/agent`；Go 验证用户、画布及模型权限，创建 run、第一条用户消息和**不可变的初始合同快照**。根模型请求也由 Node 领取后发起，避免目前“Go 先跑第一步，Node 尚未初始化会话”的双起点。
2. Node 领取带租约 epoch 的 run，先续租，再恢复持久化的 Pi v3 会话树与 active leaf；校验 prompt/schema 版本和未完操作回执；创建 `AgentSession`，订阅事件后 `prompt`/恢复。不可通过直接赋值 `session.agent.state.messages` 恢复，因为 `SessionManager` 才是模型上下文事实来源。
3. Coding Agent 的 Canvas Provider 将常规模型请求和重试交给 Go 内部模型任务接口；**语义压缩仍走 Go 原有 `cloud_agent_context_compaction` 任务**，不能让 Pi 默认摘要另外发起一次请求。Go 以 run/step/参数哈希幂等准入，使用现有模型任务链和上游适配，流回正文、推理、工具调用、usage、stop reason。Node 把终态消息和 Pi entry 经检查点接口提交，Go 同事务写公开 SSE journal。
4. Pi 每轮启动时注册符合权限/能力的具体工具。工具类别仅作分类元数据；`tool_call` hook 对照当前模型批次准入，Go 仍对每个调用重新完整预检，再按顺序执行。审批/媒体等待由持久状态表示，不占死唯一 worker。成功、失败、拒绝、取消、失租必须落到稳定回执或明确终态。
5. Go 投影公共状态和 SSE；Web 以事件序号断点续传。`agent_settled` 只表示 SDK 本次不会自动续跑，run 的业务完成仍按 Go `finish_run` 与硬规则决定。

## 2. 数据如何落库与恢复

### 2.1 事实来源和表设计

> 多用户会话决策更新：`Pi session` 按 **conversation** 持久化，`run` 是该会话内的一轮；一个账号可拥有多个并行 conversation，同一 conversation 只允许一个活动 run。具体主键、租约和恢复规则见 [`PI_CODING_AGENT_CANARY_DIRECT_CUTOVER.md`](./PI_CODING_AGENT_CANARY_DIRECT_CUTOVER.md) §三。下表原先以 `run_id` 作为 session 主键的草案已被该决策取代。

保留 `CloudAgentExecution`（run 元数据、engine、lease、revision、状态）、`CloudAgentEventRecord`（公开 SSE、按 run + sequence 追加）、`CloudAgentMessageRecord`（旧历史及前端投影）和 `CloudAgentCanvasMutation`。**不要把 Pi v3 树强塞进现有 `kind=pi, sequence` 的线性消息数组**：当前 repository 会按数量删除消息尾部，无法表达分支、active leaf、system/tool patch 与 compaction entry。

建议下一版数据库迁移增加（最终字段名以实现约定为准，SQLite 和 PostgreSQL 同步）：

| 对象 | 最低字段与约束 | 用途 |
| --- | --- | --- |
| `cloud_agent_pi_sessions` | `pi_session_id` PK、`user_id`、`canvas_id`、`conversation_id`、`format_version=3`、header、`active_leaf_id`、`last_entry_seq`、`revision`、`active_run_id`、会话租约/epoch；唯一 `(user_id, conversation_id)` | 一个用户可有多个会话；同会话串行、不同会话并行；存会话头、活动分支和恢复位置 |
| `cloud_agent_pi_entries` | `(pi_session_id, entry_seq)` 联合 PK；`entry_id` 在 session 内唯一；`user_id`、来源 `run_id`、`parent_id`、`type`、`entry_json`、`entry_hash`、时间戳 | 原样保留整段对话的 Pi entry 树和 system/tool patch；只追加，不因压缩删原始执行历史 |
| `cloud_agent_pi_operations` | `(user_id, pi_session_id, run_id, operation_id)` 联合唯一；`kind`、`call_id`、`step_id`、`arguments_hash`、`status`、`task_id`/`billing_order_id`/`receipt_json`、`lease_epoch`、时间戳 | 每轮模型、工具、压缩、媒体准入和回执；跨重启查重，区分同 callId 不同参数 |

`operation_id` 在 Node 创建时固定，Go 校验 `(run_id, kind, operation_id, arguments_hash)`：同键同哈希返回原回执，同键异哈希报冲突；工具另以 `(run_id, model_step_id, call_id)` 作稳定作用域。不要只查 `callId` 或依赖 canonical 历史，因为它可被压缩。画布写入、mutation、操作回执、run revision 与 SSE 事件在**同一 Go 事务**中提交；外部模型/媒体任务无法与数据库同事务时用“先持久化准入/任务 ID、再执行、最后幂等结算”的状态机。

检查点接口建议以 `expected_revision + lease_epoch + entries[] + active_leaf_id + projection + events[]` 原子提交；所有字段限制大小、校验类型和树关系，拒绝越权 parent、倒退序号和不同内容重放。Go 返回已提交的最大 entry_seq 和事件序号；Node 在收到成功前不得推进下一项有副作用的操作。租约失效后旧 worker 的任何提交都应被 fencing 拒绝。存储时保留可重放的 Pi JSON（含提示版本与工具定义）；图片只存资产引用/安全元数据，不持久化短时签名 URL 或大块 base64。敏感日志只输出 ID、哈希、事件类型和耗时。P1 必须验证 SDK 0.87.1 将这些 DB entry 重新导入 `SessionManager` 的**公开方法**；若只能通过 JSONL 文件导入，文件只能作短时适配缓存，Go 数据库仍是权威，且崩溃后须从 DB 完整重建。

### 2.2 续聊、旧数据和清理

一次用户对话可以含多个 run；**一个 conversation 对应一个持久 Pi session**，续聊在同一 session 的活动分支上追加新 run，保留 `parent_run_id` 与每轮权限/提示合同。会话级租约及 `active_run_id` 保证同一会话只有一个可写 worker；不同账号或同账号的不同会话可并行。新 run 重新校验用户、画布、模型和工具权限；旧终态会话转换须记来源版本及摘要，不能跨用户/跨画布导入。

旧终态记录维持可读；需要续聊时一次性转成 Pi v3 的导入条目，并标记来源与转换版本。旧非终态空 engine run 不能批量改写 `engine=pi`；逐项受控终止或清理财务/媒体资源。`CleanupPending` 由独立幂等清理处理。恢复顺序为：取得租约 → 心跳 → 读取 session/operations → 重建 `SessionManager` → 对账未完模型与工具任务 → 订阅事件 → 续跑。恢复测试要覆盖任一提交窗口崩溃及双 worker 争用。

### 2.3 现有数据层待修点

- `PiCheckpointMessage` 当前以 `role=user|assistant|toolResult|system` 和线性序号为主；扩展为 v3 entry 合同并处理迁移版本，不直接复用旧入口冒充树存储。
- 初始 system/Harness/工具 schema 快照必须在第一个 Pi 消息前原子固定。重启后读取原快照，不能用当前磁盘上的新 Harness 重算旧 run。
- `piToolReceipt` 当前按 `callId` 搜索运行态；改查独立操作账本，覆盖跨步骤重复 ID、参数冲突和压缩后回执。
- 数据迁移新增索引与约束；同步仓库数据库专题文档，测试 SQLite/PostgreSQL 的并发和迁移回滚。

## 3. 如何连接模型上游、计费与多模态

### 3.1 Canvas Provider（Node → Go → 上游）

通过受控扩展的 `pi.registerProvider()` 注册 Canvas 原生 Provider，并交给 Coding Agent 的 `ModelRuntime` 使用；不用 CLI 默认配置或 Node 直接请求渠道。SDK 创建时显式传 `modelRuntime`、从 Go 能力快照构造的 `model`、禁用默认工具的设置以及受控 `resourceLoader`。当前 `pi-agent-core` 的 `streamFn` 实现只能作为适配器原型；Coding Agent SDK 无同名顶层构造参数，需在阶段 1 用真实 0.87.1 类型和最小探针验证扩展点。Go 继续通过 `backend/internal/app/provider*.go`、`provider_protocol.go`、`provider_http_client.go`、`internal/provider` 和 `internal/outbound` 完成不同上游协议、URL 和 SSRF 边界适配。

每次 Provider 请求携带 `run_id`、会话 entry/step ID、尝试号、幂等键、模型逻辑名、精确渠道版本、工具 schema 版本、可取消 signal；Go 返回真实模型能力：`contextWindow`、输出上限、推理/视觉/工具支持、路由和计费单位。当前 Node 写死 `contextWindow=1_000_000`、`maxTokens=32768`、零费用只可视作占位；必须由 Go 的实际渠道/逻辑路由能力替换。模型切换先准入再写 Pi `model_change` entry，并让下一请求的渠道、预算和工具能力同时更新。

Provider 将 `TranscriptContext` 的 system、tool patch 和消息投影到现有 Go 模型任务输入；对不支持中途 system 的上游按 Pi 的折叠语义处理。流式合同：`start` 后按序发平衡的 `text/thinking/toolcall start-delta-end`，最后恰好一个 `done` 或 `error`；取消映射为 aborted；返回真实 usage、缓存 token、stop reason、provider request ID。支持 `onPayload`/`onResponse` 的观测回调，不泄漏请求正文。不能把“任务完成后补齐全文”当成流式通过；当前 `textDraft` 轮询实现可沿用但需验证增量不重复、断线后的事件投影不乱序。

**图片与链接**：Web/Go 的资产引用、`image_url`、短时签名 URL 与 Pi 的 `ImageContent {type:"image", data, mimeType}` 不同。建立单一受控转换层：Go 校验资产归属、尺寸、MIME、来源和可访问性，按模型协议决定传已授权 URL 还是读取并编码；Node 不将短时链接或 base64 写入会话持久层。看图工具、附件、生成参考图分别做视觉能力门槛；图像字节超限/过期/不可访问要有明确错误与可重试边界。新模型渠道或 upstream URL 仍由 Go 的白名单/私网设置控制，Node 只传逻辑模型 ID。

### 3.2 统一账务与硬预算

所有会产生模型费用的步骤都走 Go 的任务准入与结算：普通对话、自动重试、上下文摘要、溢出恢复、分支摘要及任何 SDK 内部模型调用；媒体生成继续走既有媒体任务账务。对每个 operation 记录用途、模型、预授权订单、最终 usage、结算/退款状态。Pi 自带 session cost 仅用于展示与诊断，**Go 账本为实际扣费事实**；不能用 Node 自报费用扣款。无 usage 时按现有 Go 策略进入可审计的估算/待核算状态，不能默默记零。

`SettleBillingOrder` 的并发条件状态转移闸门已落代码，并有双连接专项测试；提交后响应丢失、重复回调和失败退款仍需放入跨进程故障注入。硬预算在**每次新模型/工具/媒体副作用准入前**检查步数、金额、媒体数量、上下文上限，不靠 prompt 或 Pi 默认软限制。失败、取消、拒绝、失租都要最终结算或释放预授权；运行终态不应留下悬挂订单。

## 4. Harness、工具披露与上下文

### 4.1 资源和提示

通过 Coding Agent 的 `resourceLoader` / 配置扩展点接入现有 `SYSTEM.md`、`APPEND_SYSTEM.md`、`AGENTS.md`、`SOUL.md`、`TOOLS.md`、技能和记忆。业务 system 策略、能力快照、画布上下文仍由 Go 生成受信任的输入；Node 负责唯一一次装配并写入首个 Pi system entry。技能/记忆按用户和画布权限查询，附版本/来源；不得把仓库根协作 `AGENTS.md` 自动当成最终用户的业务提示，也不得让文件内容修改权限合同。提示变更只影响新 run；续聊前校验合同兼容或明确新会话导入。

### 4.2 工具闭环

版本化的 `cloud-agent-tools/v3` 制品列出 24 个具体工具，描述仍从 Markdown 读取。Go 根据当前用户、画布、权限模式、视觉能力、技能和记忆生成合格快照；Node 在 Pi session 建立时通过 `ExtensionAPI.registerTool()` 注册该集合，并在 `tool_call` hook 中拒绝不合格工具或未获本批准入的调用。上一模型步使用的工具名只加到下一模型请求的 function description，不携带参数或结果；每轮新建 registry，自然清空短期记录。Go 与 Node 对同名参数 schema 做漂移校验；SDK 默认 coding 工具全部关闭。分类母工具仅作为旧 session 恢复时的兼容信息，不再进入模型可调用目录。

Go 对**整个批次**先预检，再顺序执行；预检包含工具是否已披露、JSON Schema、用户归属、画布版本、能力/额度/审批。工具结果和操作回执同时持久化。审批拒绝是终态，不能生成工具结果、画布写入或下一步模型请求；取消与失租同理按合同终止。等待审批/媒体时释放 worker 执行槽，保留 run 的独立租约/可恢复唤醒，避免当前 `server.ts` 串行 `await` 阻塞其他用户。`finish_run` 需 Go 确认完成条件和清理，而非只凭 Pi 最后一个 assistant 消息。

### 4.3 压缩与上下文治理

**语义压缩算法继续由现有 `backend/internal/agentcontext` 与 `cloud_agent_context_compaction.go` 实现**：按真实窗口的 85% 输入线（未知窗口用字节/条数兜底）暂停模型准入；`agentcontext.BuildPrompt` 组装会话、创作锚点、偏好、决策与服务端执行事实；通过 Go 的独立 `cloud_agent_context_compaction` 任务生成结构化检查点；`agentcontext.Parse` 校验形状并用服务端事实覆盖模型自报的操作历史与待办；失败时走 `cloudAgentFallbackCheckpoint`；`cloudAgentBoundCheckpoint` 限制大小，`cloudAgentCompleteTurnTail` 保留最近完整轮次。保留原有 `context_compaction_requested`、`context_transition`、`context_compacted` 事件及压缩次数限制。Pi 默认摘要策略**不参与生产摘要**，也不得与 Go 各压一次。

Pi v3 session 的 active branch projection 是待压缩会话与下一次模型输入的唯一来源；把该投影交给上述 Go 算法，不能让旧 Go `state.Canonical.Messages` 独立漂移。优先通过 `session_before_compact` 返回 Go 检查点组成的自定义 `CompactionResult`，利用 Pi 保存 `compaction` entry、`firstKeptEntryId` 与活动分支，但摘要内容、保底和保留轮次仍由原算法决定；先用真实锁定 SDK 验证该映射可以精确保留完整轮次，否则用受控 context edit 表达结果，禁止静默改用默认摘要。画布当前节点/版本、视觉观察账本、审批状态、计划、技能/记忆引用和未完成工具调用作为不可压缩事实保留；摘要前确认所有 tool call/result 成对，待审批/媒体调用不被截断。

插话按 Pi steer/follow-up 语义决定进入当前轮还是下一轮，顺序、去重和取消需有持久游标。真实渠道窗口来自 Go；压缩阈值、预留 token 和检查点长度按模型能力配置，不用占位的 1M。摘要调用经 Go 计费且只准入一次；失败时使用原有服务端保底检查点并重新测预算，仍超窗则停止下一次模型调用，不能盲发。

## 5. 阶段计划与退出条件

| 阶段 | 当前状态及接下来要做的事 | 退出条件 |
| --- | --- | --- |
| P0 基线与三端合同 | **已形成**。已有失败用例、内部线格式、公开事件与固定信封样本；每次代码变化后只更新受影响的合同与证据 | 旧循环能力清单覆盖视觉、媒体、工具、上下文、插话、审批、计划、技能、记忆、完成与清理；不拿旧测试结果当本轮实测 |
| P1 SDK 可行性 | **探针完成；生产接线在当前工作树施工中，未作本轮运行验收**。锁定 0.87.1，真实 `createAgentSession`、`SessionManager.inMemory`、Provider、动态工具和压缩 hook 已在探针验证 | 保留探针；生产接线后用同一 SDK 类型和事件重跑，不用旧 `new Agent` 的测试代替 |
| P1.5 首步合同与计费承载 | **代码已落，验收未过**。合同版本、`awaiting_first_step`、快照（策略身份／Harness 正文与哈希／工具 schema 身份／装配提示身份）、占位任务、首步原子换单、首步准入失败与清扫排空的退款都已实现，见阶段状态 §2.13 | 首步由 Node 最终提示驱动；旧 run 仍可读；新 run 首步前可取消；重启后用原快照继续；占位任务永不被 Go worker 领取；同一 run 至多一张未结占位订单，真实首步只计费一次；SQLite/Postgres 故障注入后账户预留与未结订单对平。**当前只做到编译与 Node 侧实跑；Go 侧事务用例受本机缺 cgo 阻断，跨进程与 PostgreSQL 未验证** |
| P2 Pi v3 持久层与生产 SDK | **未开始**。建立 session／entry／operation 三表、lease epoch、active leaf 和追加式检查点；恢复时按操作回执对账；将生产 runner 改为 `createAgentSession`，关闭默认工具，只注册 Canvas 工具 | `SessionManager` 从 DB entry 重建下一步同一模型信封；断在提交前／后均无重复模型、工具、画布或计费；双 worker 仅有效租约可提交；旧活动 run 有明确处置 |
| P3 Provider、上游与账务 | **部分基础已做**：权威渠道能力和并发结算闸门已接。继续把 `streamSimple` 送往 Go 任务链，回传真实 usage、stop、thinking 和取消；接 M-01／M-02／M-05／M-06／M-20，所有模型、重试和摘要调用入账 | 路由、价格、上下文窗口来自 Go；无 usage 时显式回退而非默记零；空输出／截断／超时阶梯可达；步数、金额、媒体和输入窗口在副作用前阻断；任务、订单、operation 可逐笔对账 |
| P4 画布工具与运行控制 | **部分完成**。具体工具已通过 extension 注册并由 Pi hook 门控，Go 比对 schema 并整批预检；观察账本、视觉、媒体、审批、计划、技能、记忆、`finish_run` 的全链路与多 run 隔离仍需验收 | 24 个具体工具在只读/编辑/视觉/技能/记忆权限组合下通过；真实模型看图；调用与回执恰好一次；审批拒绝/取消无后续副作用；等待一个 run 不阻塞另一用户 |
| P4.5 原有语义压缩 | **未开始**。以 Pi 活动分支为输入，Go 的 `agentcontext` 和原压缩任务生成唯一结构化摘要；先探针验证最近完整轮次到 `firstKeptEntryId` 的映射，不成立则明确采用受控 context edit | `context_compaction_requested`／`context_transition`／`context_compacted` 恢复可达；摘要失败走原有服务端保底；压缩后真实下一请求变短；工具调用和结果成对；不发生 Pi 默认摘要的第二次模型调用 |
| P5 清旧驱动与公开兼容 | **未开始**。迁移旧驱动测试、逐个归因既有 7 个失败，删除 `advanceCloudAgentByID`／`advanceCloudAgent`，保留 Pi 使用的业务函数；同步 DB、API、SSE 和部署专题文档 | 旧驱动生产与测试引用均为零；无 skip 或放宽断言；Web 只消费 `/api/agent`；旧完成记录仍可读 |
| P6 隔离验收 | **本地 SQLite 短路径曾通过，完整矩阵未做**。SQLite 与 PostgreSQL、双用户、双 worker、模型 stub、提交窗口故障、媒体收尾、真实浏览器与 SSE 断线续传 | §6 矩阵逐项通过；旧活动轮次排空或受控终止；Pi 不可用有可见排队／失败语义。**本路线图不包含生产部署。** |

阶段 P1 若发现 SDK 不能在不绕开 Go 模型/账务或不能可靠恢复 DB session 的条件下承载这些合同，先记录最小可复现证据与替代接口方案，不直接用 CLI 私有文件或复制 Coding Agent 内核绕过去。各阶段可拆小 PR，但不得在存储/计费/恢复门槛未过时宣称“完成移植”。

## 6. 验收测试设计

测试采用固定的模型 stub 响应、可复现的用户/画布/资产 fixture 和真正的 SDK 会话事件；比较**公开合同与业务结果**，不强求不同引擎的隐藏推理文本字节相同。记录每次测试的源码 commit、数据库版本、依赖锁、镜像标识、请求 ID 与事件序号。敏感 prompt/密钥不入测试报告。

### 6.1 Web（API 适配、状态与真实页面）

| 层级 | 场景 | 断言 |
| --- | --- | --- |
| API/事件解析单测 | `/api/agent` 创建、续聊、插话、审批、取消；`assistant_message`、`reasoning_message`、`tool_*`、`approval_*`、`run_*`、未知事件 | 公开字段兼容，事件按 `(runId, sequence)` 去重；未知事件不崩 UI；无内部 Pi entry 泄露 |
| 面板状态测试 | 正文 delta + 完整快照、推理、工具进行中、审批等待、取消/拒绝/失败/完成、模型能力缺失 | 正文不重复、不把推理混入正文；终态去掉“运行中”；审批/取消按钮状态正确，能力缺失不白屏 |
| 浏览器端到端 | 真实 web + Go + Agent + stub 模型，两用户两画布；刷新、断网重连、`Last-Event-ID`/after 重放、冷/热浏览器缓存 | 每用户仅见自己的 run 与画布；刷新后事件与消息一致无重复；画布节点/版本与服务端一致；实际加载本次构建 chunk |
| 浏览器异常 | agent 容器不可用、worker fatal、工具 schema 漂移、过期图片、拒绝审批、媒体失败 | 可见错误及可恢复动作；无永久“运行中”；不因旧静态卷/缓存呈现旧代码 |

Web 专项测试落在 `web/src/services/api/agent.ts`、面板状态逻辑及项目真实路由。纯函数测试用既有 Bun 测试；浏览器测试只验真正应用页面，不用静态仿制页。前端不应新增 `pi` engine 分支来修复后端合同缺陷。

### 6.2 Agent（真实 Coding Agent SDK + 假 Go Bridge）

| 合同 | 用例与注入点 | 成功标准 |
| --- | --- | --- |
| Provider | 固定上游给 text、thinking、多个 tool call、无工具、length/error/abort、缓存 usage；随机切断流 | Pi 事件成对、仅一个终态；精确 text delta/工具参数；真实 usage/stop 原样入 Go 投影；取消不触发下一请求 |
| 提示与工具 | Harness 版本变化、权限/能力门槛、同模型步跨类别调用、下一轮重置、schema 漂移 | 首个 system 固定；所有合格具体工具首步即注册；hook 与 Go 双重拒绝不合格调用；上一模型步工具名只出现在下一步 schema 描述 |
| 会话存取 | Pi v3 system/tool patch、分支、model_change、compaction、context_edit；中途 crash/reload | `SessionManager` 重建的下一模型信封与 crash 前预期一致；active leaf 不丢；没有重发已完成 tool/模型 operation |
| 压缩 | 阈值、overflow、工具调用和结果间压缩、图片观察 ledger、计划/记忆引用；模型摘要失败 | 原 `agentcontext.Checkpoint` 字段与最近完整轮次保留；服务端事实覆盖模型自报；失败走服务端保底；无第二次 Pi 默认摘要请求；调用与结果成对、不可压缩事实完整、压缩后真实下一请求变短 |
| 队列/并发 | 一个 run 等审批/媒体，另一个 run 就绪；进程 SIGTERM；租约丢失 | 后者可完成；前者恢复后只执行一次；失租 worker 停止提交；拒绝/取消后没有工具结果或下一模型请求 |

此层以 `createAgentSession` 真 SDK 驱动，假 Go Bridge 只模拟 HTTP/回执；不要 mock SDK 的 `AgentSession`、`SessionManager` 或事件循环。对需要 HTTP 时序的用例，起独立 Go stub。`cd agent && npm test` 加 SDK 专项套件，`npm run build` 必须通过。

### 6.3 Backend（handler → app → repository → 数据库/模型链）

| 合同 | 测试数据和故障 | 断言 |
| --- | --- | --- |
| 鉴权/隔离 | A/B 用户，各自 canvas、asset、run；伪造 `X-Agent-User-ID`、过期 token/lease | 对外及内部每个入口重新校验；跨用户 403/404，不能读/写/续聊/审批他人数据 |
| 持久化 | session v3 entry、重复 entry ID、错误 parent、旧 format、迁移重放；SQLite 与 PostgreSQL | 唯一约束生效；active leaf/revision 原子更新；旧终态可读，旧活动态有明确处置 |
| 模型准入/上游 | 同 operation 并发两次、同键不同哈希、上游 429/5xx/超时、路由切换、取消、图像 URL 过期/SSRF | 相同调用同 task/order；冲突被拒；路由和权限由 Go 控制；取消/失败有终态与结算；私网限制不被 Node 绕过 |
| 计费 | 双连接同时结算同一订单，足额余额；预授权后崩溃、结算响应丢失、重复 usage、摘要/重试 | 账户只扣/退一次；每个真实收费调用可追溯；无悬挂订单；硬预算在副作用之前阻断 |
| 画布工具 | 批次第二项非法、版本冲突、审批拒绝、保存后 recorder 报错、媒体任务完成/失败 | 整批预检先于任何写入；基础设施错误事务回滚画布/mutation/回执/事件；业务拒绝稳定记录；媒体清理幂等 |
| SSE | 同事务写消息+事件、N 条事件后断线、重连带 last ID、重复投递、终态重放 | 序号严格单调、无跳号/重复；结果与 DB revision 一致；拒绝/取消/fatal 必达终态 |

后端测试先覆盖 repository 的并发与迁移，再覆盖内部 handler 的认证和 app 业务流程；按仓库分层 `HTTP → handler → service → app → repository` 走真实入口。现有 `TestPi*`、原子性和准入测试是起点，不能替代跨进程故障测试。任何 Go schema 改动同步 `docs/content/docs/backend/backend-database.mdx` 和相关 API/SSE 文档。

### 6.4 跨服务验收矩阵与硬门槛

隔离 Compose 同时跑 web、Go、Agent、模型 stub；至少一套 SQLite 和一套 PostgreSQL。固定请求逐条对比旧/新**模型信封和公开结果**：只读、画布编辑、看图、媒体生成、权限拒绝、审批通过/拒绝、插话/续聊、取消、压缩、模型切换。再故障注入：Go 提交前/后断线、Node checkpoint 失效、worker 被杀、双 worker 抢租约、模型响应已产生而 SSE 尚未发送、媒体 task 已提交未确认。每例核对：Pi active leaf、Go operation/task/order、画布 mutation、公开事件 sequence、Web 最终视图。

切换的阻断项：写入原子性；并发账务；恢复期间续租与 fencing；拒绝/取消终态；硬预算；媒体收尾；压缩因果顺序；旧 run 处置；旧驱动引用清零；跨用户隔离；SSE 断线续传；Agent 不可用时运行可见地排队或失败。不能以一次 `completed`、`ready:true`、构建成功或跳过失败测试替代这些证据。生产切换另行申请和规划，先排空旧活动轮次，准备数据库备份、迁移回滚与可观测指标；本路线图不授权部署。

## 7. DeepSeek 接手顺序与交付格式

1. 从 `git status --short` 开始，阅读根 `AGENTS.md`、本文件、最新 [`PI_MIGRATION_TODO.md`](./PI_MIGRATION_TODO.md)、[`PI_MIGRATION_PHASE_STATUS.md`](./PI_MIGRATION_PHASE_STATUS.md)、[`PI_MIGRATION_HANDOFF.md`](./PI_MIGRATION_HANDOFF.md) 及相关代码。保留现有未提交改动；历史日结和测试数不替代本轮核对。
2. 从 P1.5 未完成项接手：先实现显式合同版本、`awaiting_first_step` 和可恢复提示/schema 正文快照，再做不可领取占位任务、首步原子换单及终态退款；用旧 run 可读、首步真实信封、重复投递、取消与账务对平验收。P0/P1 探针已完成，无需重新从零设计。
3. 随后按 §5 的 P2→P6 顺序推进。每阶段记录改动文件、实际测试命令/结果、失败归因及脱敏的 task/order/operation/event 对账。特别保留原 Go 结构化语义压缩，并把默认 Pi 摘要的额外模型调用列为负向断言。
4. 完工报告逐项标“已实现 / 测试通过 / 未验证 / 阻断”，附数据库版本与当前 commit。本路线图不包含生产部署。

**建议能力**：普通代码阅读与专项诊断即可；遇到重复失败按根 `AGENTS.md` 的验证纪律缩小复现。本文及上游固定 tag 文档是移植边界的主要参考。
