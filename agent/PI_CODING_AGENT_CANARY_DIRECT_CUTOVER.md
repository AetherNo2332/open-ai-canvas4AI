# Canary 直接切换 Pi Coding Agent 的移植路线

> 状态：实施中。2026-09-27 23:06（Asia/Shanghai）复核：HEAD 为 `31157460`，本地 `canary` 领先 `origin/canary` 两个提交；大量迁移改动仍未提交。本文跟踪代码和隔离验收，不授权部署、推送或修改正在运行的容器。后续动手前重新核对工作树与本地数据状态。
>
> 本文取代 [`PI_CODING_AGENT_MIGRATION_ROADMAP.md`](./PI_CODING_AGENT_MIGRATION_ROADMAP.md) 中“先完成全部兼容层，最后才删除旧循环”的**执行顺序**。旧文档仍是能力清单、数据合同和测试矩阵的参考；[`PI_MIGRATION_TODO.md`](./PI_MIGRATION_TODO.md) 与 [`PI_MIGRATION_PHASE_STATUS.md`](./PI_MIGRATION_PHASE_STATUS.md) 是历史进度，不作为当前代码已完成的证明。

## 一、目标和硬边界

Canary 只保留一个 Agent 会话与事件循环：锁定的 `@earendil-works/pi-coding-agent@0.87.1` `createAgentSession`。Go 的旧 `advanceCloudAgentByID` / `advanceCloudAgent` 编排尽早删除，不设旧引擎回退。现有 Go 业务函数不是“旧 Agent”：鉴权、画布写入与事务、模型渠道、价格与账本、工具预检、审批、媒体任务、原有结构化语义压缩、运行和 SSE 持久化继续由 Go 掌握。Node 不拿数据库写权限或上游密钥。Web 继续调用 `/api/agent`，不接触 Pi 内部协议。

“直接切换”指**代码的唯一执行路径**，不表示把尚未恢复的写入能力悄悄开放。功能未接回时，Go 准入给出可见的 `capability_unavailable`/运行失败，且不创建模型任务、工具副作用或收费订单；该阶段只在隔离数据和模型 stub 上验证。真实模型流量至少要经过首步记账与终态退款门槛。

## 二、当前代码事实与文档漂移

| 事实 | 当前判断 | 对路线的影响 |
| --- | --- | --- |
| Go 定时旧调度入口不再启动；`cloud_agent_runtime.go` 仍定义 `advanceCloudAgentByID`、`advanceCloudAgent`、`advanceCloudAgentReadBatch`。当前 `rg` 在 backend/internal 得到 27 个文件、95 处匹配（含注释与定义） | **路由已硬切，旧循环尚未清掉** | 迁移旧循环承担的业务职责和测试断言，再移除旧编排；逐函数去向见 C0 去向表 |
| Node `runner.ts` 生产使用锁定的 `createAgentSession`，以 Go 持久层返回的 conversation 级 Pi session ID、header 和 entry 树重建 Pi v3 会话；Node 测试现为 59 个 | **会话持久化已有代码与 SQLite 专项测试** | Go 有 owner/conversation 唯一约束、entry 追加、active leaf/revision CAS 与 session lease/epoch；operation ledger 和旧 canonical 历史导入仍缺 |
| `server.ts` 已改为有界 worker pool：默认 4 条独立领取循环，可配置 `CANVAS_AGENT_CONCURRENCY`（1–16）；Compose 默认传 4 | **不同 Pi session 可并行执行** | 同一 conversation 仍由 Go session lease/epoch 串行化；审批/媒体 pending 仍占住一个 worker，持久挂起与释放执行槽尚未实现 |
| Go 合同 v2、`awaiting_first_step`、快照、`holding` 预留、Harness 正文/哈希/策略/schema 身份冻结与首步事务换单均已实现 | **首步账务与快照有 SQLite 专项验收** | 当前定向 Go 测试通过；跨进程、PostgreSQL 与崩溃窗口仍未验证 |
| `PiModelStep` 曾只核对 system 包含服务端策略、披露工具名称；`harnessHash` 主要挡漂移 | **策略与 schema 身份已闭合** | 现在比对：策略正文身份（快照哈希）、Harness 正文与哈希（Go 复算）、装配后系统提示身份、同名工具**参数结构**。剩余：版本化段落的全量等价、SDK 版本声明 |
| 内部请求携带 wire/SDK/session 格式版本身份并由 Go 做版本门；Pi v3 entry 树已落 Go 会话表 | **协议版本身份有了，协议合同仍不完整** | 逐端点 JSON Schema、能力协商、统一 operationId/参数哈希/revision 错误 DTO 和完整双侧 fixtures 仍未完成 |
| `PiNoToolTurn`、工具桥与首步任务仍依赖 Go 状态；usage 定锚、插话投递、压缩、重试阶梯等仍有调用点留在旧循环 | **代码存在但 Pi 路径不可达** | 删除旧编排时逐项登记“保留/改接/淘汰”，完成一项即做负向回归 |

上述判断以当前工作树和本轮专项验证为准。代码有占位任务、Pi v3 session 表或版本请求头也**不证明**账务、跨进程恢复、完整 wire schema 或 PostgreSQL 已验。本轮未部署，也未检查本地 3000 容器。

### 23:06 工作区验证快照

- Node：`node node_modules/typescript/bin/tsc -p tsconfig.json` 通过；`node --test --test-timeout=20000 --test-force-exit dist/test/*.test.js` 为 **56/56 PASS**。
- Go：在 `open-ai-canvas-backend-test:sticky-tools` 容器内，`go test ./internal/app ./internal/repository ./internal/database -count=1 -timeout 300s -run 'TestPi|TestCloudAgentHarnessBodyDigest|TestCloudAgentPreflight'` 通过；`internal/app` 用时 155.339s，repository 与 database 也通过。
- 完整 `internal/app` 测试套件此前一次运行在 600s 超时，卡在 `TestBannerAnnouncementTitleRunsWithEmoji` 的插件文件 `fsync` 路径；不能据此宣称全量 Go 测试通过。
- 当前 HEAD 仍为 `31157460`，本轮没有推送、部署或检查 3000 容器。定向测试不是跨进程、浏览器或 PostgreSQL 验收。

### 23:24 并发 worker 增量

- `agent/src/worker-pool.ts` 为每个 worker 创建独立身份和 claim loop；`CANVAS_AGENT_CONCURRENCY` 接受 1–16，缺省为 4。Compose 配置解析确认 `agent` 服务的值为 `4`。
- Node 全套更新为 **59/59 PASS**，包含两条不同 run 同时进入执行段、致命协议/配置错误仍上报 Go 的行为测试。
- Go `TestSettleBillingOrderIsExactlyOnceUnderConcurrentSettlement` 与 `TestPiSession` 仓储用例通过；验证并发结算闸门和 session 写入 CAS。
- 此改动只并行不同 worker；审批/媒体 pending 仍会占住一个 worker，尚未实现挂起/唤醒调度，也未做压力或跨进程测量。
- 代码已提交为 `ebe0bc39`（`feat(agent): run bounded concurrent Pi workers`）；尚未部署或启动容器。

## 三、账号、会话与运行的持久化模型

上版路线遗漏了会话基数：**一个账号可以有多个 conversation；一个 conversation 对应一棵 Pi session 树；一次用户提交/Agent 执行是一个 run。** Pi session 不是进程级单例，也不等于用户账号或单个 run。Pi 0.87.1 的 `AgentSession` 管一段对话，其 `SessionManager` 管 entry 树与 active leaf；`inMemory()` 只表示 Node 不写本地文件，持久化必须由 Go 数据库承担。

| 层级 | ID 与归属 | 生命周期 |
| --- | --- | --- |
| 账号 | `user_id`，只能取自 Go 已认证用户 | 可拥有多个画布和多个 conversation；Node 传来的 user ID 不作为授权事实 |
| 对话 | `conversation_id`，归属 `(user_id, canvas_id)` | 新建首轮时创建；同一对话的续聊、标题、归档、活动分支都挂在这里；现有 `CloudAgentExecution.ConversationID` 可用根 run ID 回填 |
| Pi 会话 | 稳定 `pi_session_id`，与一个 conversation 一一对应 | 存 Pi v3 header、全量追加式 entries 和 `active_leaf_id`；一个账号的不同对话绝不共用 SessionManager |
| 运行 | `run_id`，归属一个 `pi_session_id`，保留 `parent_run_id` | 每条新用户消息新建 run；任务、账单、工具回执、SSE 按 run 记账；会话历史随对话持续存在 |

建议迁移出 `cloud_agent_pi_sessions`（`pi_session_id` 主键，`user_id`、`canvas_id`、`conversation_id`、`header_json`、格式版本、`active_leaf_id`、`revision`、`active_run_id`、租约 owner/epoch/expiry、标题/归档时间，唯一 `(user_id, conversation_id)`）、`cloud_agent_pi_entries`（`pi_session_id + entry_seq` 主键，`entry_id` 在该 session 内唯一，`parent_id`、来源 `run_id`、`entry_json`/hash）和 `cloud_agent_pi_operations`（`user_id + pi_session_id + run_id + operation_id` 唯一，参数 hash、任务/订单/工具回执与状态）。`CloudAgentExecution` 增加 `pi_session_id` 外键；现有 run 事件表仍按 `(run_id, sequence)` 为 Web 提供 SSE。所有查询和变更同时带 `user_id` 与 session/run ID，校验 canvas 归属；仅凭全局 ID 或内部 Bearer Token 不放行数据。

新建对话时，Go 在同一事务创建对话/session header、首个 run、首步占位预留。续聊时从已鉴权的父 run 解析 `conversation_id`，验证同一用户与画布、父 run 已终结且是当前尾部，按 session revision CAS 设置 `active_run_id`；同一对话同时提交两条续聊返回明确冲突或持久排队，不暗中并行修改同一 Pi 树。不同对话可以并行，**同一账号的不同对话也可以并行**。旧终态 run 按 `(user_id, conversation_id)` 分组导入；旧活动 run 按既定清理规则终结，不伪装为 Pi entry。

Worker 从 Go 公平领取跨用户的就绪 run，同时取得 session 级租约与 epoch。全局和每账号各设并发上限；同一 session 始终只有一个可写 worker。Node 为**每条活动 conversation**构造独立 `SessionManager.inMemory(cwd, {id: pi_session_id}, entries)`，从 Go 加载该 session 的 header、entries 和 active leaf；若 active leaf 不是最后追加的 entry，用公开 `branch(active_leaf_id)` 恢复并核对投影。每个有副作用的边界用 `getEntries()` 差集、`getLeafId()`、预期 revision/epoch 向 Go 原子提交，不依赖普通消息不会触发的 `entry_appended`。等待审批/媒体时持久化 `active_run_id` 与挂起原因，释放 Node 执行槽和进程内对象；醒来重新领租约、从 Go 重建。完成后 `dispose()`，不把所有用户的 session 长期缓存到同一进程。

会话级条目记录来源 run，以便从 Pi 历史准确归属每轮账单、模型调用和工具副作用。每个新 run 重新计算当前模型能力、权限与 Harness 合同；变更通过 Pi 的 system/tool/model patch 进入同一会话树，并记录版本，不继承上一轮的授权作为本轮授权。原有 Go 结构化语义压缩作用于该 session 的活动分支，保留非活动分支和审计回执。对话列表、标题和归档用新增的用户鉴权 API 提供；原 `/api/agent/runs` 与 SSE 合同保持可用，Web 按登录账号与 canvas 给会话列表和缓存分区。

**并发验收**：A/B 两个账号各有多段对话同时执行，A 同账号两段对话可并行；同一对话两次并发续聊只接受一个活动 run；杀掉两个 Node worker 后只从各自的 Pi entries 恢复；A 请求 B 的 session、run、entry、审批、SSE 均拒绝；账号切换后 Web 缓存不串；审批挂起不阻塞其他对话。SQLite 和 PostgreSQL 都要验证唯一约束、CAS、租约 fencing 与索引查询。

## 四、Pi 协议层（SDK 适配与内部 wire）

本项目嵌入 `pi-coding-agent` SDK，不启动 Pi CLI 的 JSONL/RPC 子进程，所以**无需另造 Pi CLI 协议**。但必须有独立的 **Pi SDK 适配层 + Canvas 内部线协议**：前者隔离锁定版 SDK 的 `AgentSession`、`SessionManager`、Provider 与工具事件；后者是 Node 与 Go 间可版本化、可重放、可鉴权的业务传输。Go 的上游 OpenAI/Claude 等渠道协议仍是第三个边界，不由 Pi SDK 直接访问。

| 边界 | 输入与输出 | 权威和责任 |
| --- | --- | --- |
| Pi SDK → Node 适配 | Pi v3 header/entries、active leaf、`AgentSession` 事件、`ToolDefinition`、`streamSimple`、压缩 hook | Node 将 SDK 类型映射成稳定 DTO；不在 Go 中重写 Pi 循环或让 Pi 事件直接成为公开 SSE |
| Node ↔ Go 内部 wire | 领取/租约、会话加载/追加、模型步骤/结果、工具批次/回执、控制/挂起、压缩、终态 | Go 验证服务身份、用户/画布归属、权限、价格、版本、幂等和事务；Node 不因持有内部 token 就能替任意用户行动 |
| Go → Web 公开合同 | 现有 `/api/agent`、按 `(run_id, seq)` 追加的 SSE 与运行视图 | Go 从已提交业务事实投影；前端不解析 Pi entry 或内部 wire |

内部协议以 `canvas-pi-wire/v1` 独立定义 Markdown 语义和共享 JSON Schema/fixture。每次有副作用的请求携带 `protocolVersion`、`piSdkVersion`、`sessionFormatVersion`、`piSessionId`、`runId`、稳定 `operationId`、参数/信封哈希、预期 run/session revision 与 `leaseEpoch`；Go 从 run/session 表确定 `userId`，不能把 Node 传来的用户字段当权限。未知版本或字段明确失败，不靠 `DisallowUnknownFields` 返回无说明的 400。SDK 0.87.1 与 session 格式 v3、内部 wire v1 是**三个独立版本**，不可混成一个 `version`。

**当前实现状态：**Node 所有内部请求已发送 `X-Agent-Protocol-Version`、`X-Pi-SDK-Version`、`X-Pi-Session-Format`；Go 在鉴权后拒绝缺失或不匹配版本（HTTP 426）。共享身份制品为 `agent/harness/PI_WIRE_IDENTITY.json`，Node 测试校验 SDK 身份与锁定依赖一致，Go 测试校验服务端常量与制品一致。**尚未完成：**逐端点 JSON Schema、协议能力协商响应、每请求 `operationId` / 参数哈希 / revision DTO 和未知字段机器可读错误 fixture。因此目前只有版本门和身份合同，C1 Pi 协议层仍未验收。

协议类型按行为拆开：

1. **会话**：Go 保存 header、全量追加式 Pi entries、活动 leaf 和来源 run；Node 用 SDK 公共 `SessionManager.inMemory(..., entries)` 恢复，并校验 DB leaf。追加接口一次提交 `newEntries + activeLeafId + expectedRevision + leaseEpoch`。普通 `appendMessage()` 不保证发 `entry_appended`，因此以 `getEntries()` 差集为准。
2. **模型**：适配层把 Pi 的有效 system、活动分支消息、工具声明及取消信号提交 Go；Go 选上游、准入计费并回传正文/推理/工具调用、真实 usage、stop reason 和错误。映射为 Pi `start → text/thinking/toolcall start-delta-end → done|error`，每个开始都有结束且恰好一个终态；仅在 Go 确实给出增量时上报增量，不把最终全文伪装成流。`aborted`、`length`、工具调用和上游失败不得归成同一停止原因。
3. **工具**：保留模型原始 `callId`、名称与 JSON 参数文本；Pi/TypeBox 的参数解析或强制转换不能替代 Go 对原始批次的完整预检。Go 在任何写入前验证整批披露、schema、归属、能力、审批、版本和预算，顺序执行，并持久化每个调用的稳定回执。重复投递同键同哈希返回原结果，同键异哈希冲突。
4. **控制与错误**：区分合同错误（终结并清理）、CAS/失租（停止旧 worker，重读对账）、上游可重试失败（按 Go 重试及计费策略）、等待审批/媒体（持久挂起并释放执行槽）、拒绝/取消（终态，无后续工具结果或模型请求）。传机器可读 `reason`、`retryAfter` 与终态，不让 Node 凭 HTTP 状态猜全部业务语义。

验收时用相同 fixture 在 TypeScript 和 Go 两侧校验全部内部端点的字段、版本、未知字段处理和错误映射；再用**真实锁定版 SDK**对拍 system/tool 声明、Pi v3 分支恢复、模型事件顺序、usage/stop、工具批次及崩溃重投。协议层通过后才能认为 C1 的 SDK 接线可进入 C2/C3；只有构造出 `createAgentSession` 不算通过。涉及协议变化时先更新共享 schema/fixture，再更新两侧实现和版本。

## 五、施工顺序：先换循环，再恢复能力

### C0 冻结基线和清理清单（不阻塞第一切片）

1. 固定当前工作树标识、公开 `/api/agent` 与 SSE 事件集合、31 项工具清单（7 母类型、24 子工具）、模型信封脱敏样本、账务不变量和已有 7 个 Go 测试失败。用代码而不是旧日结更新表格。
2. 给旧 `advanceCloudAgent*` 中每一段标去向：**Pi 会话控制**、**Go 业务服务**、**仅旧编排**。尤其核对模型结果/停止原因、usage、上下文预算、插话、视觉账本、媒体回写、审批、完成判定与清理。只移除第三类。
3. 旧运行处理：旧终态保持只读；旧活动运行在隔离 canary 中排空或显式终结、退款和发终态事件，不把空 engine 历史运行改写为 `pi`。新轮次绝不交给旧代码。

**退出证据**：有逐函数去向表和测试迁移表；运行/任务/订单数量可对账。这里不要求把所有能力先实现。

#### C0 去向表（2026-09-27 静态核对，行号取 `cloud_agent_runtime.go`）

**第一类 · 删除（仅旧编排，Pi 路径不经此处）**

| 函数 | 行 | 说明 |
| --- | --- | --- |
| `advanceCloudAgentByID` | 934 | 旧调度入口 |
| `advanceCloudAgent` | 961 | 旧循环主体（约 340 行） |
| `advanceCloudAgentReadBatch` | 1809 | 旧循环的连续只读批推进 |
| `executeCloudAgentToolCall` | 1910 | 保留为单次 Go 业务工具操作；Pi 与尚未删除的旧调度器共同调用 |
| `executeCloudAgentMediaCall` | 2592 | 保留为 Go 媒体执行/回写操作；Pi 与清理恢复路径调用 |

**第二类 · 保留（Pi 路径已用）**：`ensureCloudAgentExecution`、`cloudAgentExecutionOutput`、`cloudAgentDecode` / `cloudAgentDecodeForExecution` / `cloudAgentRestoreTranscript` / `validateCloudAgentRuntime` / `validateCloudAgentPolicySnapshot*`、`cloudAgentSave` / `cloudAgentBoundEventPayload`、`enqueueCloudAgentTask`（含首步换单）、`terminateCloudAgent` / `failCloudAgent` / `failCloudAgentAdmission`、`cloudAgentModelFailure` / `cloudAgentSafe*`、`DecideCloudAgentApproval` / `CancelCloudAgent`。单次工具业务操作 `executeCloudAgentToolCall` / `executeCloudAgentMediaCall` 已由 Pi bridge 直接调用；旧 scheduler 也暂时复用它们。

**第三类 · 保留但必须先改接（业务内容在旧循环里，Pi 路径尚未接）**

| 函数 | 行 | 去向 |
| --- | --- | --- |
| `validateCloudAgentCalls` | 1367 | Go 业务服务：Pi 工具批次预检 |
| `compactCloudAgentContext` | 1301 | Go 业务服务：C5 结构化压缩 |
| `cloudAgentToolResult` | 1499 | Go 业务服务：工具回执入账与事件 |
| `cloudAgentModelToolResult` | 1663 | Go 业务服务：模型可见的回执投影 |
| `cloudAgentReceiptItemSummary` | 1695 | Go 业务服务：回执摘要 |
| `cloudAgentRebaseWriteSnapshot` | 1706 | Go 业务服务：写入快照重放 |
| `recordCanvasBatchHash` | 1739 | Go 业务服务：画布批次哈希 |
| `cloudAgentReadResultInContext` | 1762 | Go 业务服务：只读结果落上下文 |
| `cloudAgentBatchableReadTool` | 1785 | Go 业务服务：可批处理只读判定 |
| `cloudAgentInvalidateReadCache` | 1789 | Go 业务服务：读缓存失效 |
| `cloudAgentToolErrorIsBusiness` | 2276 | Go 业务服务：错误分类 |
| `cloudAgentMediaCall` | 2301 | Go 业务服务：媒体调用识别 |
| `cloudAgentMediaError` | 2549 | Go 业务服务：媒体错误面 |

第三类在 C4/C5 接到 Pi 工具批次、媒体回执与压缩前后，**不得随第一类一起删**。

#### C1 测试迁移表（调用 `advanceCloudAgent*` 的 29 个文件，约 110 处）

| 分组 | 文件（调用处数） | 处置 |
| --- | --- | --- |
| 媒体 | `cloud_agent_media_test.go` (18)、`cloud_agent_media_writeback_test.go` (4)、`cloud_agent_media_prompt_test.go` (1) | 改写成 Pi 媒体回执 + 画布回写合同测试；媒体提交与结算属 Go 业务，断言保留 |
| 画布与工具 | `cloud_agent_tool_preflight_test.go` (8)、`cloud_agent_canvas_events_test.go` (6)、`cloud_agent_tool_dispatch_test.go` (3)、`cloud_agent_canvas_atomicity_test.go` (2)、`cloud_agent_storyboard_test.go` (2)、`cloud_agent_tool_categories_test.go` (1)、`cloud_agent_tool_repair_test.go` (1)、`cloud_agent_read_arguments_test.go` (1) | 改成对 `PiToolBatch` 的整批预检/原子性断言；旧循环的"逐步推进"细节删除 |
| 模型与账务 | `cloud_agent_stop_reason_test.go` (9)、`cloud_agent_step_timeout_test.go` (3)、`cloud_agent_model_selection_test.go` (1) | 改成 `PiModelStep`/`PiFailModelStep` 的停止原因、超时与模型选择断言 |
| 上下文与视觉 | `cloud_agent_vision_pairing_test.go` (9)、`cloud_agent_context_compaction_test.go` (8)、`cloud_agent_context_pressure_test.go` (1)、`cloud_agent_vision_delivery_test.go` (1)、`cloud_agent_pi_vision_placeholder_test.go` (1) | 视觉配对与压缩改到 Pi 检查点/压缩钩子；未接通前保留用例但不得放宽断言 |
| 控制与其他 | `cloud_agent_test.go` (9)、`cloud_agent_approval_settings_test.go` (7)、`cloud_agent_completion_test.go` (6)、`cloud_agent_projection_test.go` (6)、`cloud_agent_interjection_test.go` (3)、`cloud_agent_plan_test.go` (2)、`cloud_agent_diagnostics_test.go` (1)、`cloud_agent_persist_test.go` (1)、`cloud_agent_reliability_test.go` (1)、`cloud_agent_skill_feedback_test.go` (1) | 终态、审批、完成、投影、插话、计划、持久化的业务断言保留并改走 Pi 入口；纯循环实现断言删除 |

处置原则：**先移业务、再删函数、最后删测试**；三者在同一个可编译切片内完成，`go vet` 必须保持通过（本机没有 cgo，`go vet` 的类型检查是当前唯一能自动发现的调用面清单）。

### C1 同一切片完成代码硬切与最小 Pi 会话

先落实 §四的最小 `canvas-pi-wire/v1`（领取、会话头、模型步骤、终态）及双侧 fixture；C2/C3/C4 再按同一版本化规则扩展账务、Pi entries、工具和挂起语义。

1. 从 Go 删除旧 `advanceCloudAgentByID` / `advanceCloudAgent` 编排与只服务该编排的调度状态。保留或抽出被 Pi bridge 使用的画布、审批、媒体、完成、清理等业务函数。删除纯实现测试时，以同等业务断言的 Pi 路径测试替代，不能简单跳过旧失败。
   **状态：未做。**去向表与测试迁移表已出（见 C0 节），但按"先移业务、再删函数、最后删测试"的顺序，删函数会连带 29 个文件约 110 处调用，必须在同一可编译切片内完成；本机没有 cgo，`go vet` 的类型检查是唯一自动的调用面清单。
2. 完成并验证工作树中正在施工的 Node `createAgentSession` 接线；以服务端策略和仓库业务 Harness 建 system，只注册当轮合格的 Canvas 工具，关闭默认 coding 工具。`pi-agent-core` 可保留为依赖，但生产不再手写 `new Agent` 循环。当前按 run 临时 `inMemory` 的做法必须在 C3 替换成 §三的 conversation 级恢复。
   **状态：接线已完成，52 个 Node 用例实跑通过。**`runner.test.ts` 的 C1 判据守住"无 `new Agent(`、无值导入 `pi-agent-core` 的 `Agent`、必须走 `createAgentSession`"。本轮修掉两个真实缺陷：工具批次必须先于批次准入提交 assistant 检查点（否则每批都被 `PiToolBatch` 403）、母类型披露判据要与 Go 权威一致。
3. 建立最小自定义 Provider：Pi `streamSimple` 把上下文、工具声明与取消信号送到 Go 内部模型步骤；Go 仍决定实际渠道、模型能力、上游协议和费用。先用固定模型 stub 跑“用户输入 → 模型正文 → 完成”闭环。
   **状态：代码在树，闭环只在 Node 侧 stub 验证过（`runner.test.ts` 9 用例），Node ↔ Go 真实 HTTP 未跑。**
4. 尚未开放的能力在准入处显式拒绝。Node 异常和 Pi 不可用要让 run 可见地排队或失败，不能产生永远 `running` 的记录。
   **状态：已落。**`PiFailModelStep` 终态守卫、看门狗 `CleanupPending`、未领取 run 独立超时，以及本轮新增的清扫排空与首步准入失败当场终结。

**退出证据**：生产调用图仅有 Pi 会话循环；无旧编排调用；真实 SDK 测试证明首个 system、模型上下文和终态。此阶段只允许隔离 stub 流量。
**当前缺口**：旧编排调用未清零（Agent 用例 130 PASS / 43 FAIL，43 个失败即旧循环驱动用例）；跨进程未验。
**可验收条件已具备**：容器内 `CGO_ENABLED=1 go test ./internal/app -run ...` 可跑真实 SQLite 事务，因此每迁移一个文件就能立刻跑它自己的用例，不必等整包。
**注意**：整包直跑会卡在依赖外网的用例（容器无外网，已定位 `TestAdminStorageListStatsAndPreview`），表现为超时而非失败；迁移验证按用例名过滤。

### C2 闭合首步、模型与账务（真实模型流量门槛）

1. 完成当前部分实现的 v2 合同：建 run 时冻结 Go 策略正文身份与工具 schema；首个 Pi 步将 Harness **正文**、哈希、SDK 版本和最终信封身份与首步任务在同一受控状态转换中固定。重启后从存储恢复旧正文，不重读新磁盘文件替换在途提示。
   **状态：除 SDK 版本外已落代码。**建 run 冻结策略身份与工具 schema 身份；首个 `PiModelStep` 冻结 Harness 正文（Go 复算哈希，拒绝"报一个哈希、发另一份正文"）、装配后系统提示身份，之后每步只比对；快照随 `PiAgentSnapshot` 回发，恢复不重读磁盘。SDK 版本待 Node 上报。跨语言哈希有固定向量双端断言。
2. `holding` 占位任务保持不可被普通任务 worker 领取。首个 `PiModelStep` 在一笔事务中按最终模型信封重新报价、退占位预留、预留真实首步、创建真实任务、推进 run 阶段并记录 operation；并发重投得到同一任务，同一键不同参数哈希返回冲突。取消、未领取超时、失租和失败都释放占位预留。
   **状态：换单与退款已落代码；"同一键不同参数哈希返回冲突"未做**（需要 operation 级幂等，属 C3）。退款路径：首步准入失败当场退、首步前取消退、清扫排空退、失租/未领取超时经看门狗置 `CleanupPending` 后由新增的 `DrainPendingPiAgentCleanups` 退。事务级用例已写（`cloud_agent_pi_first_step_test.go`），受本机缺 cgo 阻断。
3. Provider 回传增量正文、推理、工具调用、上游 usage/cache、stop reason 和取消；Node 事件与 Go task/result/SSE 不重复拼正文。模型能力（窗口、输出、视觉、推理）与价格由 Go 最终路由确定，Node 的占位 `1M/32768/0` 不进入真实准入。所有自动重试和摘要调用也必须走 Go 任务和账本。
4. 在模型步骤准入前执行步数、金额、输入窗口和媒体预算；无法得到可靠上游 usage 时按 Go 明确的估算/待核算路径结算，不能静默记零。`MaxCharge` 是上限，账户预留额按实际未结订单核对。

**退出证据**：SQLite 与 PostgreSQL 各跑建 run 前/后、换单事务中、换单提交后响应丢失、首步前取消、失租清扫；账户预留额等于未结订单之和，真实收费模型步骤至多一单，run 有确定终态。
**当前状态：SQLite 一半通过，PostgreSQL 未做。**容器内（`open-ai-canvas-backend-test:sticky-tools`，Go 1.25 + gcc，真实 cgo）已验证：换单、正文/哈希门、首步准入失败退款、真实 `SweepUnclaimedPiAgentRuns` + 收尾排空退款、跨语言哈希向量，共 5/5 PASS。跑通后修掉三个真实缺陷（首步准入失败不退款、`failCloudAgent` 不置收尾标记、收尾退款被"状态不可解码"挡住）。仍未验证：PostgreSQL、崩溃注入、跨进程首步。
**迁移基线**：Agent 相关用例 130 PASS / 43 FAIL，43 个失败全部是驱动旧 `advanceCloudAgentByID` 的用例（建 run 改 v2 后语义失效），即 C1 要迁移的那批。

### C3 持久会话与精确恢复

按 §三实施 conversation 级 Pi v3 session、entry、active leaf、operation 与业务回执；Node 只经内部 API 追加。操作键绑定用户、session、run、step/call ID 和参数哈希，lease epoch 用于 fencing，不能当幂等键。恢复顺序为：领 run 与 session 租约 → 续租 → 按用户读取冻结合同和该 session 的 Pi entries → 恢复活动分支 → 对账已完成 model/tool operation → 订阅事件 → 继续。模型任务、工具结果、画布 mutation、计费订单与 SSE 事件需要按各自事务边界留下可查回执。

**退出证据**：在提交前/后、回执前/后杀 Node 或 Go，恢复后的下一模型信封与预期一致；双 worker 只有有效 epoch 能提交；不同账号与不同 conversation 的 entries/回执不串；无重复画布写入、生成任务、模型订单或事件。

### C4 按风险恢复画布与 Harness 能力

1. **只读**：`canvas_list_node_types`、`canvas_get_state`、表格/分镜读取、`model_list`，以及只读母类型披露。先验证租户、画布与能力过滤。
2. **视觉**：`canvas_inspect_image`、OCR、标注、图层；恢复 `resource:` 受控水合、真实图片输入和观察账本。观察只从实际模型看过的图及可归属的正文入账。
3. **写入**：分镜/批量表编辑、`canvas_apply_ops`、排布、媒体生成。母类型成功后本轮追加合格子工具，下一轮收回；描述来自 Markdown，参数来自版本化 schema。Go 对**整批**先做披露、参数、用户/画布、版本、审批和额度预检，再顺序执行；调用回执与画布版本可对账。
4. **控制**：审批通过/拒绝、取消、插话、续聊、计划、技能、记忆和 `finish_run`。等待审批/媒体时持久挂起并释放 worker 槽；有界并发和唤醒替代单 worker 无限轮询。拒绝/取消后无工具结果或下一模型调用。

每恢复一组，Go 准入和公开 Web 状态同时打开；未恢复组继续明确拒绝。子工具必须再过 Go 权限/能力检查，Pi 的 active tool 集合不能充当授权。

**退出证据**：24 子工具与 7 母类型的权限组合通过；只读与写入互不越权；媒体任务结算和画布回写幂等；一个 run 等审批不阻塞另一用户。

### C5 恢复原有上下文治理

以 **Pi 活动分支**作为下一模型信封和压缩输入的单一来源。Go 从真实 usage 校准上下文压力；达到有效窗口约 85% 或未知窗口兜底线时，在下一模型准入前启动原 `agentcontext` 结构化语义压缩任务。沿用 `BuildPrompt`、`Parse`、服务端事实覆盖、fallback checkpoint、长度约束和最近完整轮次保留。通过 `session_before_compact` 注入自定义 `CompactionResult`；先用真实 0.87.1 SDK 探针证明 Go 完整轮次边界可映射到 `firstKeptEntryId`。若不能忠实表达，采用受控 context edit，**不启用 Pi 默认摘要**。

压缩前后都保持工具 call/result 成对，视觉观察、审批、计划及未完成媒体调用作为事实保留；摘要任务由 Go 计费。恢复 `context_compaction_requested`、`context_transition`、`context_compacted` 和失败保底事件。压缩后重新测预算，仍超窗则停止模型调用。

**退出证据**：真实下一请求变短，关键事实完整，事件序号连续，摘要只收费一次，断言没有第二个 Pi 默认摘要请求。

### C6 全量验收与收尾

| 层 | 最小验收矩阵 |
| --- | --- |
| Agent | 真 SDK + 假 Go Bridge：首步、工具动态披露、stream 平衡、取消、session v3 重建、分支/工具 patch、压缩、并发等待、fatal 后终态；不 mock AgentSession |
| Backend | SQLite/PostgreSQL：租户隔离、完整工具 schema 与批次预检、画布事务、模型/媒体/摘要账务、双 worker fencing、首步与工具重复投递、审批拒绝、清理退款、SSE 单调续传 |
| Web | 真实应用页面：创建/续聊、只读/编辑/看图/媒体、审批、插话、取消、压力环、刷新、`Last-Event-ID` 重连、跨用户隔离及明确错误；不使用静态仿页面 |
| 跨服务 | 隔离 Compose + 固定模型 stub，对每项旧能力核对公开结果、下一模型信封、task/order/operation、画布 revision 和事件序号；至少做一次真实图像渠道验收 |

最终删除旧编排残余、旧引擎配置和只为旧实现存在的测试；保留旧完成记录的读取/续聊转换。把未解决的既有测试失败逐项归为“业务回归、已由新断言替代、独立缺陷”，不得以 skip 或放宽断言收尾。同步 DB、内部 API、公开 SSE 和部署专题文档。**本计划完成的标准是功能与账本验收通过，不是代码里出现 `createAgentSession`。**

## 六、第一张施工单

1. 重新核对正在变化的工作树；列出旧 `advanceCloudAgent*` 内各段去向与其测试调用者。
   **已完成（本节）：**旧编排 2811 行；第一类删除 5 个函数、第二类保留清单、第三类 13 个待改接函数；测试调用者 29 个文件约 110 处，已按媒体／画布工具／模型账务／上下文视觉／控制五组列表。
2. 定义最小 `canvas-pi-wire/v1` 的 schema、错误语义与双侧 fixture，核对当前所有 `/internal-agent` 端点的实际线格式。
   **部分：**本轮核对了模型步骤与快照端点的事实线格式（发现并修掉 `harness` 字段未在 Go 侧声明会让首步变成空 400）；版本化 schema 与双侧 fixture 未做。
3. 在一个可编译切片中移除旧编排并完成生产 `createAgentSession` 接线；使用模型 stub 验证首个 system、流事件与完成状态，默认工具关闭。
   **一半完成：**生产 `createAgentSession` 接线已完成并有 52 个用例实跑通过；旧编排未删，仍被 29 个文件阻塞，删除必须与业务改接同一切片。
4. 然后完成当前已有占位任务的首步原子换单、Harness 正文冻结与退款；在此门槛之前不把新的 canary 代码用于真实模型请求。
   **代码已落、未验收：**换单、正文冻结、四类退款路径与新用例都在树里；受 cgo 阻断未跑事务级用例，也尚未在真实环境用它跑过一次模型请求。

交付记录逐项标“代码存在 / 专项测试通过 / 跨进程通过 / 未验证”，附实际命令和结果。不得把旧交接中的测试数或本地 3000 健康状态当作本轮验证。
