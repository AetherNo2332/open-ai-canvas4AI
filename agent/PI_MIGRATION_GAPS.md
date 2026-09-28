# Pi 迁移剩余开发：现状审计与实施清单

本文件是 `AGENT.md` 五条硬边界对应的**可执行差距清单**，供接手的 Agent 逐项消项。
结论全部来自 canary 分支当前代码（HEAD `7aa99880`，`VERSION=v1.5.7.1+63e4cfd`），
每条都给出文件与行号，不用历史印象替代现状。

## 基线（本轮实测）

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| Node 依赖 | `cd agent && npm install` | 93 包安装成功 |
| Node 测试 | `cd agent && npm test` | 4/4 通过（14.4s） |
| Go 编译 | `cd backend && go build ./...` | 成功（Go 1.25.1，首次拉取依赖） |
| Go 业务测试 | `CGO_ENABLED=1 go test ./internal/app ./internal/repository ./internal/handler` | `repository` ok、`handler` ok；`app` 有一处 **与 Pi 无关**的既有失败（`playback transcode persist failed: db busy`，582s） |

本机已具备 `gcc` 与 `go1.25.1`（安装在 `/home/a1/.local-go/go`），因此 SQLite 驱动的
Go 测试**可以**真跑，不必再以「无 CGO」为由跳过 —— 这一点修正常见误判。

## 已落地（已核对）

- 内部协议路由已挂载：`backend/cmd/server/main.go:121` 调用
  `handler.RegisterInternalAgentRoutes`，实现在 `backend/internal/handler/internal_agent.go:18`
  （`/internal-agent`，Bearer 常量时间比对，用户归属逐次经 service 校验）。
- 引擎字段贯通：`backend/internal/app/cloud_agent.go:514` 在 `CANVAS_AGENT_ENGINE=pi` 时写入
  `input["agentEngine"]="pi"`；`cloud_agent_runtime.go:248` 解析；`cloud_agent_runtime.go:310`
  落到 `CloudAgentExecution.Engine`。
- 旧调度器不抢 Pi：`backend/internal/repository/cloud_agent.go:128` 的候选查询带
  `(engine IS NULL OR engine <> 'pi')`。
- 租约领取：`cloud_agent_pi_bridge.go:218`(`ClaimPiAgent`)、`:237`(`RenewPiAgentLease`)、
  `:515`(`piAgentLeasedRun` 校验 engine/owner/过期)。
- Node 侧循环：`agent/src/runner.ts`、`bridge.ts`、`pi-stream.ts`、`tool-disclosure.ts`、`server.ts`。

## 差距清单

### G1（硬边界 1）Node 未独立组装 Harness —— 未开始

- 现状：Go 在 `cloud_agent.go:481`(`compileCloudAgentPolicies`) → `:486`(`cloudAgentCanonicalFor`)
  组装 system prompt 与 tool schema，root 任务的模型调用也由 Go 创建（`:517`）。
  Node 只是把收到的 `snapshot.canonical` 原样回传：`runner.ts:162` 调
  `toCanonical(...)`，其中 `:77` 的 `systemPrompt` 取自 `getCurrentSystemPrompt(messagesIn)`。
- 缺：Node 独立读取并组装 `AGENTS.md`/`AGENT.md`、`SOUL.md`、`TOOLS.md`、系统策略与工具描述。
  注意 `SOUL.md`/`TOOLS.md` 当前**在仓库中不存在**（本轮 `ls` 确认）；
  策略源是嵌入的 `backend/internal/prompts/agent-system-policy.md`、
  `agent-media-policy.md`，工具描述源是嵌入的
  `backend/internal/app/agent-tool-descriptions.md`（`cloud_agent_tool_text.go:10`）。
- 关键设计决定（需先定，否则双方会各写一份）：这些 Markdown 的共同来源放哪、
  版本号与哈希如何校验漂移。
- 验收：Node 用未改动的 Markdown 源自行组装 system prompt 与工具描述，
  与 Go 现行 `compileCloudAgentPolicies` 输出逐字节一致（对固定输入做快照对比）。

### G2（硬边界 1/阶段 3.1）工具参数 schema 未共享化 —— 未开始

- 现状：参数 schema 由 Go 源码构造，例如
  `cloud_agent_tools.go:436-462`（`agent_profile_read`、`plan_update`、`ask_user`、
  `finish_run`、`canvas_get_state` … 全部内联 `map[string]any`）。
  已版本化为常量 `cloudAgentToolSchemaVersion = "cloud-agent-tools/v2"`
  （`cloud_agent_tools.go:388`），但 Node 不读取该定义。
- 缺：Go/Node 共用、带版本的 schema 定义；旧运行保留合同快照。
- 验收：同一 schema 源在 Go 与 Node 下展开后等价（同一组工具、同参数、同枚举/约束），
  且有漂移检测测试。

### G3（硬边界 3）单个工具调用重试：CAS 已提供基础保护，但仍需故障注入证明

- 现状（本轮已核对，修正了初稿判断）：
  - `PiToolAdvance`（`cloud_agent_pi_bridge.go:455`）先查回执
    （`piToolReceipt`，`:496`，扫 `state.Canonical.Messages` 里的 `role=tool`）。
  - `advanceCloudAgentTool`（`cloud_agent_runtime.go:1912`）的所有状态推进都经
    `repo.MutateCloudAgent(..., run.Revision, ...)`，而该事务**先做
    `WHERE id=? AND user_id=? AND revision=?` 的带行数校验的 revision 自增**
    （`repository/cloud_agent.go:215-223`，`RowsAffected != 1` → `ErrCreationConflict`）。
  - 因此崩溃后重试若仍拿旧 revision，事务会整体回滚 —— 画布写入与回执同事务，
    不会重复写入。**这是已有保护，不是缺口。**
- 真正的缺口：`PiToolAdvance` 把这种 CAS 冲突以 HTTP 非 2xx 抛给 Node
  （`bridge.ts:120` 抛错）→ worker 整轮退出。协议缺少「调用已在推进中 / 请重读快照」
  这一确定性回执，Node 只能靠崩溃重试，无法区分「未执行」与「已执行但回执未持久化」。
  另外**没有任何 Go 测试**覆盖 Pi 协议（本轮确认：`ClaimPiAgent`、`PiToolAdvance`、
  `PiCheckpointMessage`、`PiModelStep`、`PiToolBatch` 在 `*_test.go` 中零引用）。
- 验收：用 `creationTestService(t)` 风格的 SQLite 测试注入「副作用已提交、回执未落库」
  的崩溃点，证明重试后画布写入与计费各恰好一次，且拿到确定性回执而不是 HTTP 错误。

### G4（硬边界 3）媒体任务重投会产生假失败，而不是重复计费 —— 已修复

- 现状：`enqueueCloudAgentTask`（`cloud_agent_runtime.go:2296`）用确定性任务 ID
  `cloudAgentID(run.UserID, "<runID>:task:<len(state.TaskIDs)>")`（`:2337`）→
  `task_creation.go:122` 把 `task.ID` 设为该值。
- 已核对（修正初稿判断）：`CreateTaskWithCreditReservation`
  （`repository/finance.go:440-453`）在**同一事务**内先 `reserveBillingOrder` 再
  `tx.Create(task)`。重复 ID 会让 `tx.Create` 报主键冲突并**回滚整笔预算预留**，
  所以不会重复扣费。
- 真正的缺口：第二次尝试拿到主键冲突错误后，
  `cloud_agent_runtime.go:2340` 起的 `if media != nil { ... }` 错误分支会把它当成
  业务失败写回模型 —— 而第一次提交的任务其实正在跑。结果是**假失败 + 模型重复生成**，
  且 `state.TaskIDs` 与真实任务可能不一致。
- 验收：以同一 admission ID 重复提交，第二次应返回既有任务（幂等成功），
  不再产生错误工具回执，任务与计费各一份。
- **已实现**：`CreateTask`（`task_creation.go:186-194`）在落库失败时先经
  `cloudAgentTaskForAdmissionID`（`task_creation.go:603-616`）判定是否为同一运行
  登记的确定性任务；命中则返回既有任务，并且完整校验 userID 与 `AgentRunID`，
  缺任一项都不命中。测试：`TestCloudAgentAdmissionIdempotentResubmission`
  （`cloud_agent_admission_idempotency_test.go`）。

### G5（硬边界 3）模型步骤失败后重试语义不明确 —— 已修复（幂等重投部分）

- 现状：`PiFailModelStep`（`cloud_agent_pi_bridge.go:333`）在任务状态为
  queued/running/succeeded 时返回 400「模型任务没有失败」（`:349`）；
  Node `bridge.ts:74-81` 在 status 不是 `succeeded` 时调 `fail`，收到 400 就抛错。
- 缺口：若上一步已失败并已结算（可能已计费），重试会再次进入 `PiModelStep`（`:248`）
  重新入队并再次计费，而旧失败步骤的账单仍在 `state.TaskIDs` 内占用预算（`:2297`）。
  需要区分「可重试的同一步骤」与「已终结、必须换新步骤 ID 的步骤」。
- 验收：同一步骤 ID 重试不产生第二笔计费；失败已结算时给出确定性状态。
- **已实现**：`PiFailModelStep`（`cloud_agent_pi_bridge.go:333`）现在是幂等上报：
  运行已 failed 且同一失败任务、同一失败文案时返回成功；成功的任务永远不能被
  上报为失败；指针指向另一步骤时仍然拒绝。测试：`TestPiFailModelStepIdempotent`。
- 仍缺：同一步骤「失败后重新入队」的显式语义（当前 `PiModelStep` 在
  `state.ActiveTaskID != ""` 时返回既有任务视图，失败运行不会重新入队，
  所以不会二次计费 —— 这一点由 `TestPi*` 与既有旧引擎测试间接覆盖）。

### G6（硬边界 4）端到端覆盖缺口 —— 未开始

按 `AGENT.md` 阶段 5.1 逐项核对，当前 Node 测试只有 4 个（`agent/test/*.test.ts`），
均为单元级；下列场景**无**测试：取消/中断、插话、续聊、上下文压缩、技能与记忆、
计划、`finish_run`、跨用户隔离、媒体任务、审批通过/拒绝、画布版本冲突、
旧完成记录转 Pi 上下文、全部旧 SSE 事件对照、租约过期与重复投递。
- 注：`agent/src/runner.ts:142-148` 用 `canonicalCount` 做插话 steer，
  该路径无测试。

### G7（硬边界 2）事件级流映射未验证

- 现状：`pi-stream.ts:31-89` 把 Go 的**整段结果**适配成 Pi 事件序列
  （start/thinking/text/toolcall/done），没有真实增量。
- 缺：与旧 SSE 事件序号的逐事件对照、断线续传、以及 `runner.ts:26-29` 的
  `canvasContent` 看图信封在「看图 / 观察账本 / 压缩」三条路径上的实测。

### G8（硬边界 5）Pi 不可用时的排队/报错语义

- 现状：Node `server.ts:15-30` 在 claim 失败时打日志并退避重试，不退回旧引擎（正确）；
  但运行一旦置为 `engine=pi` 就永远不被旧调度器领取
  （`repository/cloud_agent.go:128`）。需要有「Pi 服务不可用 → 运行排队或明确失败」
  的可观测语义，而不是静默停在 queued。

## 建议的消项顺序

0. ~~**先补 Pi 协议测试**~~ **已完成**：`cloud_agent_pi_bridge_test.go`
   7 个测试全部通过（租约准入、领取、检查点幂等与连续性、批次重放与伪造拒绝、
   推进回执重放、收尾判定幂等、失败步骤拒绝）。
   原第 0 条保留如下供追溯：
0. **先补 Pi 协议测试**：`ClaimPiAgent` / `PiAgentSnapshot` / `RenewPiAgentLease` /
   `PiCheckpointMessage` / `PiModelStep` / `PiModelStepAck` / `PiFailModelStep` /
   `PiToolBatch` / `PiToolAdvance` / `PiNoToolTurn` 目前**零测试引用**，
   而这是唯一的 pi 写入路径。用 `creationTestService(t)`（`internal/app/creation_test.go:18`）
   的 SQLite 夹具即可覆盖租约、检查点幂等与顺序、批次重放。
1. **G4**（已定位到具体错误分支，改动小、收益明确）。
2. **G3**（补确定性回执 + 故障注入；CAS 已挡住重复写入，重点是协议语义与证明）。
3. **G5**（同一步骤重试的计费/状态语义）。
4. **G1 → G2**（Harness 与 schema 共享化；先定共同来源与版本/哈希校验）。
5. **G7 → G6**（事件流对照与端到端覆盖，最后才谈切换）。

`AGENT.md` 的禁令不变：**不要部署、不要启用 `CANVAS_AGENT_ENGINE=pi`。**

## 分层工具披露引起的测试漂移（本轮处理）

`003234f0 feat(agent): persist opened tool categories for the run` 之后，根任务只下发
母类型，子工具必须在本轮打开其类别；但大批既有测试仍在"第一步直接调子工具"，于是
全量 `go test ./internal/app/` 出现 22 个失败。

处理方式（不使用"仅为通过测试"的放宽）：

- 新增 `agentOpenToolCategory`（`cloud_agent_completion_test.go`），把"该类别已打开"
  这一前提写进运行检查点；`agentSettleStep` / `agentSettleBatch` 会按被调用工具的
  类别自动打开。选它而不是模拟一次 `agent_tools_control` 调用，是因为后者会额外
  产生 `tool_completed` 事件与一次计费步骤，污染只关心目标工具的既有断言。
- `writeBatch`（`cloud_agent_tool_preflight_test.go`）与 `agentMediaRun`
  （`cloud_agent_media_test.go`）登记完整合格目录，把这些用例的断言重新集中到预检/
  执行逻辑本身。
- `cloudAgentAdvertisedTool`（`cloud_agent_tool_preflight.go`）：当检查点有版本号但
  **没有**下发工具名明细时不再判定"未下发"。旧结构/损坏检查点会因此把整轮工具全部
  拒掉；授权仍由完整目录与服务端校验负责。

结果：22 → 5（本轮再修掉三个"自构根任务 input"的用例：`cloud_agent_test.go` 的
`TestCloudAgentToolLoopPersistsApprovalAndAppliesCanvasWrite`、
`TestCloudAgentCanvasApprovalAdmissionFailureTerminatesRun`，
以及新增辅助 `advertiseFullToolCatalogForTest` —— 它把运行态的
`AdvertisedToolNames` 登记为完整目录，等价"本步已披露全部合格工具"）。
仍剩：

- `TestCloudAgentCanvasReadArgumentRepairContinuesRun`：`lost strict schema after
  checkpoint` —— 与本轮改动无关，需单独定位（检查点里恢复的 schema 不是严格合同）。
- `TestCloudAgentEmptyCanvasRepairAcrossCheckpoints`：修复预算跨检查点丢失。
- `TestCloudAgentRuntimeEmitsContextPressurePerStep`：根任务上游用量锚点被
  "工具 schema 已变化"作废 —— 需要单独查锚点签名比对。
- `TestPluginViewIncludesDocumentationForEveryOfficialProtocol`、
  `TestPurgeAssetsBatchSharedResourcesAndHistory`：与本轮改动无关，待单独定位。

## 方向变更（用户确认）：不保留旧 agent

用户明确"这是实验性项目，完成目的是移植 pi agent 内核，不要尝试与旧 agent 并存"，
并选择**现在就删旧循环**。本节取代上文"建议的消项顺序"里关于兼容旧循环的取舍。

### 本轮已执行

1. **Pi 成为唯一引擎**：`cloud_agent.go` 的 `CreateCloudAgentRun` 不再读
   `CANVAS_AGENT_ENGINE`，新运行一律写 `input["agentEngine"] = "pi"`。
2. **删除旧 Go 调度循环**：
   - `task_worker` 不再启动 2 秒 tick 的 `advanceCloudAgents` 扫描，也不再在
     `cloud_agent_step` 完成后 `wakeCloudAgentScheduler`。
   - 删除 `advanceCloudAgents`、`wakeCloudAgentScheduler` 两个函数，以及只服务于它
     的 `Service` 字段 `agentSchedulerWake` / `agentSchedulerCursor` / `agentSchedulerMu`。
   - 删除两处只测旧调度的用例（`TestCloudAgentReliabilitySchedulerHeadOfLine500`、
     `TestCloudAgentReliabilityPendingCancellationRecoveredByScheduler`）。
3. **续聊不再由 Go 推进上一轮**：`cloud_agent.go` 去掉 `advanceCloudAgentByID` 调用，
   续聊改为要求上一轮已经终结（否则 409），运行由 Node worker 独占驱动。
4. `advanceCloudAgentByID` / `advanceCloudAgent` 标注为已废弃、仅供历史测试装配，
   新代码不得调用。

### 下一步（未完成）

- 把 `advanceCloudAgent`（旧状态机约 340 行）与 `advanceCloudAgentReadBatch` 真正删除：
  目前它们只被 20 个测试文件、75 处调用点使用（都是旧循环的驱动方式）。删除前需要把
  这些用例改成通过 Pi bridge（`PiModelStep` / `PiToolBatch` / `PiToolAdvance`）驱动，
  或连同旧循环一起删除 —— 不再为旧循环加固。
- Node 侧补齐 pi.dev 约定的装配：`SYSTEM.md` / `APPEND_SYSTEM.md`（替换/追加系统提示）、
  `PI_CODING_AGENT_DIR`、项目 `.pi/`，以及 `agent/` 当前的 Harness Markdown 加载。
- 包选择**已定：不换包**。核对 `node_modules/@earendil-works/pi-agent-core@0.87.1`
  的导出面确认：它自带完整 `harness/` 层（`agent-harness.ts`、`context.ts`、`messages.ts`、
  `prompt-templates.ts`、`skills.ts`、`system-prompt.ts`、`session/`、`compaction/`、
  `tools/`），但 `system-prompt.ts` **只导出 `formatSkillsForSystemPrompt`**，不含
  `SYSTEM.md` / `APPEND_SYSTEM.md` / `PI_CODING_AGENT_DIR` 的文件式装配（那层属于
  `pi-coding-agent`）。
- **已按同一约定自行装配**：新增 `agent/src/system-prompt.ts` +
  `agent/test/system-prompt.test.ts`（6 个用例）：支持 `SYSTEM.md` 替换系统提示、
  `APPEND_SYSTEM.md` 追加、`AGENTS.override.md` 覆盖 `AGENTS.md`、`SOUL.md`/`TOOLS.md`
  纳入上下文、agentDir 为绝对路径校验、20 KiB 读取上限、缺失/空文件不报错。
  测试：`cd agent && npm test` → **15/15 通过**。
- **已接线**：`server.ts` 启动时用 `CANVAS_AGENT_HARNESS_DIR` 读一次 Harness
  （`loadHarnessPrompt`），`runner.ts` 的 `initialState.systemPrompt` 改为
  `assembleSystemPrompt(harness, snapshot.canonical.systemPrompt)` —— 即"服务端策略前缀
  + Node 自行装配的 SYSTEM.md/APPEND_SYSTEM.md/Harness 文件"。`cd agent && npm test`
  → **16/16 通过**，`tsc --noEmit` 通过。
- **过渡期注意**：Go 的 `prompts.LoadAgentPolicies` 仍把同一 Harness 目录追加进策略前缀
  （`agent_policy.go:37` `loadAgentWorkspace`），因此 Node 侧做了去重：prefix 里已有
  `## Workspace <name>` 的段不再重复追加（有专门用例覆盖）。
- 仍缺：让 Go 不再追加 `loadAgentWorkspace`，彻底变成"Go 只给策略前缀、Node 独占
  Harness 装配"。这会改动 `SystemPolicyHash`，需要同步迁移策略快照校验。

## 开发进度（截至本轮）

| 阶段 | 事项 | 状态 | 证据 |
| --- | --- | --- | --- |
| 1 | 内部协议（领取/租约/检查点/模型步/工具批次/推进/收尾） | 已落地 | `internal_agent.go` 全路由 + `cloud_agent_pi_bridge.go` |
| 1 | Pi 协议测试 | 已完成 | `cloud_agent_pi_bridge_test.go` 7 个用例通过 |
| 1 | Pi 成为唯一引擎 | 已完成 | `CreateCloudAgentRun` 一律 `agentEngine=pi`，`CANVAS_AGENT_ENGINE` 已移除 |
| 1 | 删除旧 Go 调度循环 | 已完成 | 删 `advanceCloudAgents`/`wakeCloudAgentScheduler` 及调度字段；`task_worker` 不再启动 |
| 3 | Node 自行装配系统提示 | **已完成** | `agent/src/system-prompt.ts` + `server.ts`/`runner.ts` 接线 |
| 3 | **老 agent 系统提示融入 Pi** | **已完成** | `agent/scripts/sync-harness.mjs` 同步 `SYSTEM_POLICY.md`/`MEDIA_POLICY.md`/`TOOL_DESCRIPTIONS.md` + `SOURCE.json` 哈希 |
| 3 | Go 不再追加 Harness | **已完成** | `prompts.LoadAgentPolicies` 移除 `loadAgentWorkspace` 追加；4 个旧断言测试随之删除 |
| 2 | 逐事件流（增量正文） | **已完成** | `textDraft` 轮询按后缀补差 → 真实 `text_delta`；新增 2 个用例 |
| 2 | SSE 事件序对照 | 未开始 | 需与旧 SSE 逐事件比对 |
| 2 | 看图/观察账本/压缩实测 | 未开始 | `canvasContent` 信封未实测 |
| 3 | 共享版本化工具 schema | **已完成（制品化 + 启动即校验）** | `TOOL_SCHEMA.json`（27 工具，v2）；Go 侧漂移检测、Node 侧结构校验、**worker 启动时用制品交叉校验服务端快照** |
| 3 | Harness↔backend 漂移检测 | **已完成** | `agent/test/harness-sync.test.ts` 用 SOURCE.json 哈希校验，含反向验证 |
| 4 | 工具副作用对账 / 恰好一次 | **已完成（协议层）** | admission 幂等 + 失败上报幂等 + 崩溃重投回执一致性（`TestPiToolAdvanceIsReplaySafeAfterCrash`） |
| 5 | 删旧状态机 `advanceCloudAgent`（约 340 行） | 未开始 | 仍被 20 个测试文件、75 处调用驱动；已标废弃 |
| 5 | 端到端验收与切换 | 未开始 | — |

当前验证基线：`cd agent && npm test` → **19/19 通过**；`go build ./...` 通过；
`go test ./internal/prompts/...` 通过；本机 3000 部署健康（`v1.5.7.1+7aa9988`）。

## 下一轮可直接执行的待办（按依赖排序）

1. ~~**SSE 事件序对照**~~ **已完成**：`assistant_message`（含"正文+工具调用"）与 `reasoning_message` 均已对齐旧合同。原描述：把 Pi 事件流的 `message_end` / 工具回执 / 用量 / 终止原因，与旧
   `/api/agent` SSE 的既有事件逐条比对（旧事件契约见 `cloud_agent_runtime.go` 的
   `state.event(...)` 调用点）。这是 Pi 能否直接替代旧 SSE 合同的前置条件。
2. **删旧状态机**：`advanceCloudAgent`（约 340 行）已被标注废弃，仍被 20 个测试文件、
   75 处 `advanceCloudAgentByID` 调用当作驱动方式。处理方式二选一：把用例改走 Pi bridge
   （`PiModelStep` → `PiToolBatch` → `PiToolAdvance`，带租约），或随旧循环一起删除。
3. **共享版本化工具 schema**：参数 schema 目前在 `cloud_agent_tools.go` 内联构造（版本常量
   `cloud-agent-tools/v2` 已存在），需抽出双方共用的定义并加漂移检测。
4. ~~**工具副作用对账（协议层）**~~ **已完成**：`TestPiToolAdvanceIsReplaySafeAfterCrash`
   证明「回执已提交但响应丢失」后的重投返回**同一回执**、`revision` 不二次推进、
   `role=tool` 消息恰好一条，且陈旧 `revision` 的写入被 CAS 拒绝。
   仍缺：**业务写入工具**（`canvas_apply_ops` 等）的同等注入 —— 需要构造合法 `snapshotHash`
   与 ops，验证画布确实只改一次（当前只证到协议层与读取型工具）。
5. **合同切换**：`CANVAS_AGENT_HARNESS_DIR` 现在只归 Node 使用；若要彻底去掉 Go 侧残留，
   同步迁移策略快照校验。

## 当前基线（本轮实测）

- `cd agent && npm test` → 23/23 通过（完整 Pi 循环、增量正文、系统提示装配、Harness/工具 schema 漂移检测、快照交叉校验）
- `go build ./...` 通过；`go test ./internal/prompts/...` 通过
- 本机 3000：`canvas-canary-3000-web-1` / `backend-1` 均 healthy，`v1.5.7.1+7aa9988`，
  `CANVAS_AGENT_ENGINE=legacy`（该变量已不再被代码读取；Pi 为唯一引擎）

## SSE 事件契约对照（本轮实测，发现阻断项）

旧 journal 的全部事件类型（`grep state.event` 全量枚举，共 25 个）：
`approval_decided` `approval_requested` `assistant_message` `canvas_undone` `canvas_updated`
`completion_blocked` `context_compacted` `context_compaction_requested` `context_images_pruned`
`context_pressure` `context_transition` `durable_event` `generation_task_created`
`legacy_event` `model_failure_recovered` `model_step_stop` `plan_updated` `reasoning_message`
`run_cancelled` `run_failed` `tool_completed` `user_interjection`
`user_interjection_delivered` `user_interjection_dropped` `user_question`

### ~~阻断项~~ 已修复：`assistant_message` 在"正文 + 工具调用"时不会发出

- 旧循环 `cloud_agent_runtime.go:1103` 在**每次**模型返回正文时发 `assistant_message`
  （含正文与工具调用同时出现的情况）。
- Pi 路径只在 `cloud_agent_pi_bridge.go:188`（`PiNoToolTurn`，即**没有工具调用**的收尾步骤）
  发这条事件。
- 后果：模型"边做边说"（正文 + tool calls）时，旧 SSE 会有一条 `final:false` 的过程消息，
  Pi 路径下**前端看不到**。这是事件合同级别的差异，不是渲染细节。
- 另外 `reasoning_message`（推理正文）在 Pi 路径同样没有对应发出点。

**已按方案 1 修复**：`PiMessageCheckpoint`（`cloud_agent_pi_bridge.go`）在收到
`role=assistant` 且正文非空时，与消息检查点**同事务**补发
`assistant_message`（`text`、`final:false`、`messageId` 指向产生它的模型任务）。
正文归一化同时支持 `pi` 的 `[{type:"text",text}]` 与纯字符串（`piAssistantText`）。
测试：`TestPiCheckpointEmitsAssistantMessageEvent`（过程消息补发、`final` 为 false、
纯字符串正文补发、user 消息不发、事件数与正文都对）。

**`reasoning_message` 也已补齐**：同一检查点从 Pi 的 `thinking` 块提取推理正文，
发 `reasoning_message`（`messageId` 带 `:reasoning` 后缀，`truncateRunes(...,8000)`，
与 `cloud_agent_runtime.go:1089` 一致），且推理正文**不会**混入 `assistant_message`。
至此旧 SSE 的 25 个事件类型中，与模型输出直接相关的两条都已对齐。

## 最后一项并存：删旧状态机的精确施工图

`advanceCloudAgent` 占用 `cloud_agent_runtime.go` 的 **917–1260 行（344 行）**，已标废弃。
它内部只调用六个函数，其中四个是 Pi 也需要的共享能力，**不能一起删**：

| 被调用 | 归属 | 处理 |
| --- | --- | --- |
| `s.advanceCloudAgentTool` | 共享（Pi 的 `PiToolAdvance` 也调它） | 保留 |
| `s.advanceCloudAgentContextCompaction` | 共享 | 保留 |
| `s.failCloudAgent` / `s.terminateCloudAgent` | 共享事件与失败路径 | 保留 |
| `s.advanceCloudAgentReadBatch` | 仅旧循环 | 随状态机一起删 |
| `s.advanceCloudAgent(...)`（自递归） | 仅旧循环 | 随状态机一起删 |

**施工顺序（建议）**
1. 先删 `advanceCloudAgentReadBatch` 与旧状态机本体，让 `advanceCloudAgentByID` 编译失败。
2. 逐个把 20 个测试文件、75 处 `advanceCloudAgentByID(...)` 改成 Pi 驱动：
   领取租约 → `PiModelStep` → 写 `result_json` → `PiToolBatch` → `PiToolAdvance`。
   这些用例守的是 completion / approval / media / interjection / repair 的真实行为，
   仍要保留，只是换驱动方式。
3. 最后删 `advanceCloudAgentByID` 与 `ensureCloudAgentExecution` 的旧入口。

不要在一步里同时删代码和改 75 处用例：那会让失败原因不可分辨（本仓库的验证纪律要求
"失败现象可归因"）。

## 前端接入核对（本轮）

前端**不直接消费 Pi 的内部协议**，它只看 Go 的 `/api/agent` 合同，因此 Pi 迁移不必改前端。
具体链路与已确认的接入点：

1. **事件来源**：`web/src/services/api/agent.ts` 轮询运行快照与 journal 事件，再把它们规范化成
   前端事件名（`run_snapshot` → `run_status` + `assistant_message(final:false)`）。Pi 与旧循环
   写的是**同一张 journal 表**，所以事件天然同源。
2. **渲染分支全集**（`canvas-cloud-agent-panel.tsx`，共 20 个）：`approval_decided`
   `approval_requested` `assistant_delta` `assistant_message` `assistant_snapshot`
   `canvas_updated` `completion_blocked` `context_compacted` `context_compaction_requested`
   `error` `generation_task_created` `model_failure_recovered` `plan_updated`
   `progress_summary` `reasoning_delta` `reasoning_message` `run_failed` `run_status`
   `tool_completed` `tool_failed`。
   其中 `assistant_delta` / `reasoning_delta` / `assistant_snapshot` 是**前端适配层自己产生**的
   （对 `activeMessage` 做差量），不在 Go journal 里。
3. **流式草稿**：`activeMessage` 取自**活动模型任务**的 `text_draft`
   （`cloud_agent_runtime.go:862`）。Pi 路径下 Go 仍然创建并执行该模型任务，`textDraft` 正常增长，
   所以前端的流式显示不需要改动。
4. **无 engine 耦合**：前端代码里没有对 `engine` 字段的分支（本轮 grep 确认），
   因此"运行改为 engine=pi"对前端不可见。

**结论**：前端接入成立，前提是 journal 事件类型与 payload 保持一致 —— 这正是前两轮补齐
`assistant_message`（含"正文+工具调用"）与 `reasoning_message` 的原因；若不补，前端会丢掉
过程消息与推理展示。

**待办**：前端尚无针对 Pi 运行的端到端浏览器验收（阶段 5.1 的一部分）。

## 内部协议实测（本轮）

把 `CANVAS_AGENT_INTERNAL_TOKEN` 注入 3000 的 backend 容器后实测：

- 带 token 调 `POST /internal-agent/claim` → `{"code":0,"data":{"run":null},"msg":"ok"}`
  （路由已挂载、认证通过、无待领取运行时返回 null，语义正确）
- 不带 Authorization 调同一路由 → 空响应（未授权被拒），证明 Bearer 校验生效

**部署注意（本轮发现）**：backend 在 `docker-compose.local.yml` 里**没有发布 8080 端口**
（只有 web 的 3000 对外）。因此 Pi worker 的两种可运行方式：
1. 与 backend 同网络运行（compose 里加 `agent` 服务，或 `docker network` 里起 Node 容器）；
2. 或给 backend 临时发布端口 / 让 worker 走宿主机可达地址。

直接在宿主机跑 Node worker 访问容器 IP 在当前 WSL + 代理环境（`HTTP_PROXY=127.0.0.1:7897`，
`NO_PROXY` 不含容器网段）下不可达 —— 这是环境限制，不是协议问题。

**已按方式 1 接好**（本轮）：`docker-compose.yml` 的 `agent` 服务（`profiles: ["pi"]`）
补上 Harness 挂载与目录变量：

```yaml
CANVAS_AGENT_HARNESS_DIR: /agent/harness
volumes:
  - ./agent/harness:/agent/harness:ro
```

`docker compose --profile pi config` 校验通过。启动方式（token 必须显式给，默认空字符串时
worker 会因缺少 token 拒绝启动，这是有意的安全默认）：

```
CANVAS_AGENT_INTERNAL_TOKEN=<token> docker compose -f docker-compose.yml \
  -f docker-compose.local.yml -p canvas-canary-3000 --profile pi up -d
```

**待办**：起一次真实 worker 跑通"领取 → 模型步 → 工具执行 → 完成"闭环（需要 3000 上
有可登录账号 + 已配置模型渠道，否则模型步会停在 queued）。

## 部署刷新（本轮）

之前 3000 上的 backend 镜像早于本会话的 Go 改动（`assistant_message`/`reasoning_message`
补发、工具 schema 制品测试等），已重新构建并滚动替换：

- `docker compose ... build backend` + `up -d backend` → 成功
- 重启后 `/api/health` → `ready:true`，`build.version=v1.5.7.1+7aa9988`
- 两容器 healthy；内部路由在无 token 时仍不可用（安全默认保持）
- `VERSION` 与分支 HEAD 一致（`v1.5.7.1+7aa9988`），按 AGENTS.md §9 无需再次 bump

至此"源码 → 镜像 → 运行中部署"三者一致。

## agent 独立部署（本轮完成）

按用户要求：agent **作为独立服务跑在 compose 下，不在 backend 容器内**。已构建并实测。

### compose 定义（`docker-compose.yml`）

```yaml
agent:
  profiles: ["pi"]
  image: open-ai-canvas-agent:local
  build: { context: ., dockerfile: agent/Dockerfile }
  environment:
    CANVAS_BACKEND_INTERNAL_URL: http://backend:8080
    CANVAS_AGENT_INTERNAL_TOKEN: ${CANVAS_AGENT_INTERNAL_TOKEN:-}
    CANVAS_AGENT_HARNESS_DIR: /agent/harness
  volumes:
    - ./agent/harness:/agent/harness:ro
  depends_on: { backend: { condition: service_healthy } }
  restart: unless-stopped
  healthcheck: { test: ["CMD","node","-e","process.exit(0)"], interval: 30s, ... }
```

### 实测结果

| 检查 | 结果 |
| --- | --- |
| `docker compose --profile pi build agent` | 成功（`open-ai-canvas-agent:local`） |
| 三容器并存 | `agent-1` healthy（**无端口**）、`backend-1` healthy（8080 仅在 compose 网络）、`web-1` healthy（3000 对外） |
| Harness 挂载 | 容器内 `/agent/harness` 可见全部 5 个文件（SYSTEM_POLICY / MEDIA_POLICY / TOOL_DESCRIPTIONS / TOOL_SCHEMA / SOURCE.json） |
| 环境变量 | `CANVAS_BACKEND_INTERNAL_URL=http://backend:8080`、`CANVAS_AGENT_HARNESS_DIR=/agent/harness`、token 由 `.env.pi` 注入 |
| **跨容器连通** | 从 agent 容器内请求 `http://backend:8080/internal-agent/claim`（错误 token）→ **HTTP 401**，证明网络可达且鉴权生效 |
| 隔离性 | agent 无发布端口、不持有数据库；只经 `/internal-agent` 与 backend 通信 |

### 启动命令

```bash
printf 'CANVAS_AGENT_INTERNAL_TOKEN=<token>\n' > .env.pi   # 已被 .gitignore 的 .env* 覆盖
docker compose --env-file .env.pi -f docker-compose.yml -f docker-compose.local.yml \
  -p canvas-canary-3000 --profile pi up -d
```

**注意**：不指定 token 时 `CANVAS_AGENT_INTERNAL_TOKEN` 为空字符串，worker 会拒绝启动
（`server.ts` 的显式校验），backend 侧 `/internal-agent` 路由也不挂载 —— 这是有意的安全默认。

**仍待**：真实闭环需要 3000 上存在可登录账号 + 已配置模型渠道（当前是全新数据卷、
`CANVAS_REGISTRATION_ENABLED=false`，注册接口返回"请先同意影策服务协议"）。

## 外部技术复核（Astra / gpt-6-astra，codex bridge）

已提交完整进度与四个问题（删旧状态机的施工顺序、完整测试计划与漏掉的失败模式、
写入型工具的故障注入、以及第 2/4/5 项里的隐藏错误假设）。

Astra 的首轮判断（原文要点）：

> 项目目录读取被当前沙箱拒绝（`Permission denied`），因此无法复核 HEAD 或本地实现，
> 会把实测记录作为项目证据。已读到 Pi 0.87.1 上游源码：**工具批次的部分成功、取消和恢复
> 必须单独验证，现有"回执重放安全"测试还不能证明画布写入不会重复。**

这条直接否定了一个容易自我安慰的结论：`TestPiToolAdvanceIsReplaySafeAfterCrash` 只证明了
**协议层**（回执一致、revision 不二次推进、消息恰好一条），**没有**证明画布写入恰好一次。
在补上写入型工具的故障注入之前，不得宣称"写入恰好一次"。

（Astra 仍在输出完整答案；下一轮继续取回复并落盘。）

## 前端陈旧 chunk 事故（本轮定位并修复）

**现象**：修完 `modelCapabilityConfigFor` 漏 import 后，用户仍报 `reasoningSupported is not defined`。

**根因（不在业务代码）**：`docker-compose.local.yml` 给 web 服务挂了
`web-data:/usr/share/nginx/html`。该卷把镜像里的构建产物整个盖住，且从未清理，
累积了 **3235 个 JS 文件、3 个不同版本的面板 chunk**。所以浏览器加载的始终是旧 chunk，
重新构建镜像、`up -d`、甚至 `--force-recreate` 都无效 —— 卷一直存在。

**修复**：删除 web 的卷挂载（镜像里已是构建好的静态产物，无需卷）。

| 指标 | 修复前 | 修复后 |
| --- | --- | --- |
| assets 下 JS 文件数 | 3235 | 1481 |
| 含 `reasoningSupported` 的 chunk | 3（新旧混杂） | 1 |

**验证**：`GET /` → 200；面板 chunk `project-DQEFo6cJ.js` → 200，由
`workspace-route-modules-CHP39TDV.js` 引用；chunk 内已是修复形态
（`reasoningSupported:Et`，来自 useMemo 变量）；agent 交互仍 `status=completed`。

**教训（值得写进部署约定）**：给静态产物目录挂"持久卷"会让镜像更新对用户不可见。
若确实需要该卷（如运行时注入配置），必须在部署流程里清理旧产物，否则新旧 chunk 混用
会表现为随机的 `X is not defined`。

## Pi 0.87.1 两个疑点的自行核验（读依赖包源码）

Codex（Astra）提出两个可能的隐患，我直接读 `node_modules/@earendil-works/pi-*` 源码核验：

### 疑点 1：`runner.ts` 跳过 canonical 的 system 消息 → **不丢失**

- `pi-ai/dist/utils/transcript.d.ts` 的 `normalizeContext(context)` 把
  `context.systemPrompt` + `context.tools` 折成一条**前导 system 消息**
  （`createInitialSystemMessage`），写进 transcript；`getCurrentSystemPrompt(messages)`
  正是靠**回放 system 消息**还原提示。
- 我们的路径正确：`initialState.systemPrompt` 传入装配好的提示 → Pi 内部折成 system 消息
  → `toCanonical` 用 `getCurrentSystemPrompt(messagesIn)` 取回 → 作为
  `canonical.systemPrompt` 回传 Go。`fromCanonical` 跳过 system 只是避免重复注入。
- 实证吻合：实测运行的 `context_pressure` 里 system 桶约 17–19KB（策略 + 工具 schema 都在）。

### 疑点 2：异步订阅回调的持久化不被等待 → **被等待，安全**

`pi-agent-core/dist/agent.js`：

- `subscribe(listener)` 把回调放进 `listeners` 集合；
- 内部 `await listener(event, signal)`（约 430 行）；
- 注释明确：「become idle until all awaited listeners for that event have settled」，
  `waitForIdle()` 也会等 `agent_end` 的监听器结算。

所以 `runner.ts` 里 `agent.subscribe(async (event) => { ... await checkpoint(...) })`
的检查点写入是被 Pi 等待的，事件顺序与持久化顺序一致。

### 但由此暴露一个真实的设计差异（待 Codex 复核）

Pi 用 system 消息的 **`toolsAdded` / `toolsRemoved` 增量**表达工具披露变化，并用
`getCurrentTools` / `resolveTranscriptTools` 解析；而我们的 `toCanonical` 是**每一步重建**
扁平的 `{role, content}` 列表。也就是说「母类型 → 打开类别 → 子工具保留到本轮结束」
这套语义是由**我们自己的 `ToolDisclosure`** 实现，而不是 Pi 原生的。
不是 bug，但属于「移植是否忠实」的问题，需要评估是否改为用 Pi 原生的工具增量机制。

## Codex 复核抓到的两个真实缺陷（已修）

Codex 读了 `D:\13537\open-ai-canvas-canary` 的代码后，**否定了两个我自己核验的疑点**
（与我的结论一致），但指出两处**我误以为已生效、实际未生效**的缺陷：

### 缺陷 1：`server.ts` 读了 harness/schema 却没传给 runner —— 已修

```ts
// 修复前
await runCanvasAgent(bridge, run, controller.signal);
// 修复后
await runCanvasAgent(bridge, run, controller.signal, harness, toolSchema);
```

后果很严重：`runCanvasAgent` 的签名有 `harness?` 与 `toolSchema?` 两个可选参数，
不传时它**静默降级**——系统提示退回 Go 组装的那份（Node 的 Harness 装配完全不生效），
工具 schema 交叉校验也被 `if (toolSchema)` 跳过。
也就是说"Node 独立装配系统提示"与"启动即交叉校验"这两项，在生产路径里**从未真正运行过**。

**教训**：可选参数 + 静默降级 = 接线遗漏不会被任何测试发现。测试直接调用
`runCanvasAgent(bridge, run, signal, harness, schema)`，所以绿；真实入口没传，所以不生效。

### 缺陷 2：schema 校验只比工具名，不比参数定义 —— 已修

原实现只检查"服务端快照里的工具名是否都在制品中"，同名工具的**参数漂移**（例如
`canvas_get_state` 的 `offset` 从 integer 变成 string）不会被发现——而这恰恰是
`TOOL_SCHEMA.json` 存在的意义。现已加稳定序列化（键序无关）后的逐字比较：
参数定义漂移即抛错并列出工具名。

**测试**：新增「同名工具参数定义漂移同样被拒绝」用例，覆盖"漂移必须报错"与
"键序不同但语义相同不得误报"两个方向。`cd agent && npm test` → **24/24 通过**。

## Codex 完整评审（第 25 轮）—— 关键结论

Codex 直接读了 `D:\13537\open-ai-canvas-canary`（确认 HEAD `7aa998806e2aa46d230274bacdf0a859aaf23788`），
并跑了一个隔离探针（未改文件、未动容器）。**结论：Pi 循环确实在工作；但目前不能宣称移植完成。**

### 它核验通过的两点（与我的结论一致）

- `runner.ts` 跳过 canonical 的 system 消息**不丢提示**：调用链是
  `canonical.systemPrompt → assembleSystemPrompt → Agent.initialState.systemPrompt → Pi 自动补入初始 system 消息`；
  工具声明经 `ToolDisclosure.attach → agent.state.tools → Pi declareToolChanges → system 消息的 toolsAdded/toolsRemoved`。
  它同时警告：**不要把那句 `continue` 当修复删掉**，可能造成重复注入，且 Go 的 system 消息不具备 Pi transcript 的完整结构。
- `Agent` 会 `await listener(...)`，所以订阅回调里的 checkpoint 与批次准入**被等待**。
  它跑出的隔离探针顺序：`checkpoint-start → checkpoint-end → execute`（工具执行发生在 checkpoint 完成之后）。

### 它抓到的三个真问题（比"丢提示"严重）

**1. 事务失败语义错误：画布可能已改、回执却报失败**

`applyCloudAgentCanvas`（`cloud_agent_tools.go:1271`）**先保存画布，再调用 recorder**；
recorder 报错回到外层后变成普通 `toolErr`，外层仍可能记录**失败回执并正常提交**。
→ 结果是"画布已变 + 失败回执"。Codex 的原话：
**"同一个事务不等于自动原子失败。必须让存储错误导致事务回滚。"**
业务拒绝可以作为工具回执；**基础设施错误不能按普通业务拒绝吞掉**。

**2. 我新增的 `CreateTask` 幂等回退没覆盖 Pi 准入主路径**

`enqueueCloudAgentTask`（`cloud_agent_runtime.go:2243`）设置了 `creationPrepare`，
而 `CreateTask` 在该分支**提前返回**（`task_creation.go:156-164`），
真正的任务/账单/检查点在后面的 `MutateCloudAgent` 事务里提交。
→ 所以我之前说"admission 幂等已完成"是**错的**，那段回退不能作为 Pi 准入恰好一次的证明。

**3. 恢复流程有三个会导致卡死的缺陷**

- 恢复工具回执发生在 **schema 校验与租约心跳启动之前** → 恢复中等待审批或媒体任务可能**耗尽租约**
- 审批拒绝后的 `rejected` **不在 Node 的停止状态集合**中 → Node 可能不停止
- 初始 system 消息是构造函数临时补入的，**没有作为初始化检查点固定** → 重启可能用**新的 harness 重建旧 run 的提示**（违反"合同快照"原则）
- 中途崩溃后只要 `piMessages.length > 0` 就采用部分 transcript，**可能丢尚未写完的历史**

### 它纠正的数量错误

旧驱动当前是 **22 个测试文件、84 处调用**，不是我报的 20/75。
且"调用数 ≠ 测试数"——`agentSettleStep` 这类 helper 会间接驱动许多测试。

### 它给的故障注入清单（写入与计费）

| 注入位置 | 故障 | 必须断言 |
|---|---|---|
| `applyCloudAgentCanvas` 保存成功后、recorder 前 | 返回持久化错误 | 画布/历史/mutation/回执/事件/revision **全部回滚** |
| recorder 中 mutation 已写、事件未完成 | recorder 报错 | 不允许"画布已变＋失败回执" |
| `advanceCloudAgentTool` 已记结果、未 `cloudAgentSave` | 返回错误 | 游标、画布、回执保持提交前状态 |
| `MutateCloudAgent` 提交成功、HTTP 响应写出前 | 断开/杀 backend | 重启后返回**原成功回执**，版本与 mutation 数不再增加 |
| Node 收到回执、未 checkpoint `toolResult` | 杀 Node 后新 worker 接管 | 补齐一条 `toolResult`，**不再写画布、不再收费** |
| 两个 service 同时 `PiToolAdvance` | barrier 强制同时读旧状态 | 只有一个提交，另一个冲突重读得到同一回执 |
| 工具执行中租约过期被接管 | 暂停旧请求、启动新 owner | 旧执行者不能提交新副作用；新执行者可恢复且不重复 |

断言必须**同时**检查：画布 revision/内容、画布历史、`CloudAgentCanvasMutation`、
canonical tool 回执、Pi `toolResult` 检查点、工具终态事件、`CallIndex`、账单/流水/账户余额。

计费另需三组：准入事务、结算事务（`SettleBillingOrder` 账户更新后、账单状态前失败）、
**并发结算**（两个连接同读同一未结算订单）。它特别提醒 `SettleBillingOrder`
（`finance.go:749`）先读订单状态再更新账户，**不能仅凭事务与顺序重试判定并发安全**。

### 它给的删旧状态机阶段

1. 固定基线（用**忽略行尾差异**的 diff 记录真实修改；7 个失败用例逐个记录测试名/错误/触发条件，不能凭描述归因）
2. 把仍需保留的业务职责从旧循环抽出（步数限制、上下文预算、插话消费、图片投递与观察、完成规则、损坏状态处理、cleanup），由 Pi bridge 调用这些窄入口
3. 建真实 Pi 编排测试（用真实 0.87.1 `Agent` + 确定性 stub 控制模型返回；**不要 mock 掉 Agent**）
4. 补严格跨进程测试（Node worker + Go HTTP + 真实数据库，模型上游用可控 stub）

（Codex 完整答复还有约 6.7KB 未取：73% 处的 C 阶段剩余、D 测试矩阵、E 时间顺序。）

### Codex 评审（续）：D 测试矩阵与"最容易漏掉"的结论

**它认为我遗漏最多的是：恢复路径与正常路径不等价。** 代码信号：

| 位置 | 问题 |
|---|---|
| `runner.ts:155` | 先 `recoverToolResults`，之后才校验 schema、启动心跳 → 恢复中等待审批或媒体任务**可能超过 45 秒租约** |
| `DecideCloudAgentApproval` | 把拒绝设为 `rejected`（明确业务终态），但 **Node 的停止集合漏掉它**；`executeTool` 对 `pending` 无限轮询 → 可持续续租占住 worker。**"拒绝后续跑"不是现有合同，应先决定是否改变产品语义** |
| `server.ts` | 一次只 await 一个 run → 等待审批的 run 会**占住该 worker**，影响其他 run |
| Pi bridge / Node | 保留**两套消息视图**，Node 只对部分新增图片消息做同步；Go 更新 canonical ≠ Pi 下次请求已使用更新 |
| 旧循环的职责 | 上下文压缩、步数准入、插话消费等**不会因换 Pi 自动出现**；`PiModelStep` 没有等价完整前置流程 |
| `piToolReceipt` | 主要按 `callId` 查回执 → **必须明确唯一作用域**，并测跨步骤重复 callId、同 ID 不同参数、压缩后查询 |
| `fromCanonical` | 把工具结果设为 `isError:false`、忽略非法历史工具参数 → **不适合作为未验证历史 run 的迁移器** |
| `system-prompt.ts` | **实际实现没有兑现"AGENTS.md > CLAUDE.md"**：没有 override 时两者都会加载 → 需选定并测试明确合同 |

**D 测试矩阵（P0/P1，19 行，节选关键项）**

P0：启动入口与 schema（首次请求必须含装配结果；schema 不兼容要在**任何恢复副作用之前**被拒）；
Pi 异步事件（assistant 持久化成功后才准入工具）；**初始化中断**（任意初始 checkpoint 崩溃后恢复，历史完整、无重复、提示版本固定）；
工具提交窗口；任务与账务（每准入身份一个任务/预授权，每订单只结算一次，退款不重复）；
租约与重复投递（旧 owner 无提交权限；**恢复期间持续续租**）；拒绝/取消/完成的终态单调；
步数/金额/媒体预算（**在下一次副作用准入前强制**，不能靠模型自觉）；媒体恢复；上下文压缩（call/result 配对完整，**未完成调用不被裁掉**）；
权限与归属；**历史 run 与 cleanup**（每个遗留非终态都有明确归宿，清理任务不因删除旧调度器永久悬挂）。

P1：SSE 断线/重复/乱序；流式草稿（增长/替换/缩短/空终稿都不拼出重复正文）；工具恢复语义；
错误与资源故障（DB 暂时故障、429、超时、配额、损坏 JSON 不得变假成功或无限重试）；实际部署与浏览器验收。

### Codex 评审（续）：E 时间顺序与切换门槛

```
固定基线、列出失败与行为合同
    ↓
确定 transcript / 幂等身份 / 租约 / 终态合同
    ├─ Node：初始化、恢复、心跳、错误传播、真实 Pi 测试
    ├─ Go：事务失败、准入、并发结算、业务规则入口
    └─ 测试清单：迁移纯业务测试，设计 SSE 与历史 run 验收
    ↓
跨进程故障测试、媒体与压缩完整路径
    ↓
清零旧驱动依赖、处理七个失败、删除旧状态机
    ↓
隔离环境升级/恢复演练、真实浏览器验收
```

硬依赖：故障测试必须先定幂等身份/事务边界/终态语义；压缩恢复必须先定"哪份 transcript 是编排事实来源"；
删除旧循环必须晚于其业务职责有新调用方与替代测试；历史 run 演练必须用最终检查点版本与恢复协议。

**历史 run 不要统一改 `engine="pi"`**：
- Pi run：按检查点版本恢复；不支持的状态明确停止并做账务/资源收尾
- 空 engine 旧 run：默认受控终止或取消，保留已提交任务与结果；只有通过专门转换验证的状态才迁移
- 终态且 `CleanupPending`：由独立幂等清理机制处理，**不能依赖已删除的旧循环**

**七个失败用例只能落入三种结果**：迁移中修复的业务缺陷 / 被新测试替代的旧实现断言 / 独立且明确归属的已有缺陷。
**不能靠 skip、放宽断言或恢复旧调度器变绿。**

**明文切换门槛（未完成即不可宣称可切换）**：
写入原子性、并发账务、恢复期间租约、拒绝/取消终态、硬预算、媒体收尾、上下文因果顺序、
历史 run 处置、旧驱动测试依赖清零。**现有一次 `completed` 与正常 SSE 序列只证明短路径可运行。**
---

# Pi 移植执行清单（细化版）

依据：Codex 完整评审 + 本会话实测。每项含**改动位置 / 验收方式 / 依赖关系**。
标注 `[门槛]` 的项，未完成即不可宣称可切换。

## 阶段 0：固定基线（无依赖，先做）

| # | 动作 | 验收 |
|---|---|---|
| 0.1 | 用忽略行尾差异的 diff 记录真实改动：`git diff --ignore-cr-at-eol --stat` | ✅ **已测：30 个文件，+663 / −312**（不是 2400 项，CRLF 噪声已排除） |
| 0.2 | 记录失败用例的测试名 + 错误 + 触发条件 | ✅ **已测：12 个失败**（含我改动引入的 5 个，已全部归类并修复）；现回落到 7 个既有失败 |
| 0.3 | 记录旧驱动引用基线 | ✅ **已对账**：仅 `advanceCloudAgentByID` = 20 文件/75 处；加 `advanceCloudAgent(` = **22 文件/84 处**（Codex 的数字正确，我此前的 20/75 少算了后一 pattern） |

命令：`cd backend && CGO_ENABLED=1 go test ./internal/app/ -count=1 2>&1 | grep -E "^--- FAIL"`

## 阶段 1：合同固化（阶段 2-5 的硬前置）

| # | 合同 | 落点 | 验收 |
|---|---|---|---|
| 1.1 | **transcript 事实来源**：明确 Pi transcript 为编排事实，Go canonical 为投影 | `agent/src/runner.ts` `toCanonical` / `fromCanonical` | 文档化 + 单测断言投影方向 |
| 1.2 | **幂等身份**：run + callId 的唯一作用域 | `cloud_agent_pi_bridge.go` `piToolReceipt` | ⬜ 待做：测跨步骤重复 callId、同 ID 不同参数、压缩后查询 |
| 1.3 | **租约合同**：恢复期间必须持续续租；心跳早于恢复 | `agent/src/runner.ts:155` 附近 | 见 3.2 |
| 1.4 | **终态合同**：`rejected` 属 Node 停止集合 | `runner.ts` + `bridge.ts` | ✅ **已实施**（见下） —— 结论：现有合同**已明确禁止续跑**，修它是恢复合同而非改语义 |
| 1.5 | **提示版本固定**：初始 system 作为初始化检查点固化，重启不得用新 harness 重建旧 run | `runner.ts` + `cloud_agent_pi_bridge.go` | 见 3.3 |

## 阶段 2：Go 侧正确性（P0，可与阶段 3 并行）

| # | 缺陷 | 改动 | 验收（故障注入） |
|---|---|---|---|
| 2.1 `[门槛]` | **画布已改、回执报失败** | `cloud_agent_tools.go:1271` `applyCloudAgentCanvas`：存画布后 recorder 失败必须**回滚事务**，不得降级为普通 `toolErr` | 在保存成功后、recorder 前注入持久化错误 → 断言画布/历史/mutation/回执/事件/revision **全部回滚** |
| 2.2 `[门槛]` | **Pi 准入幂等** | ✅ **已验证**：恰好一次由**单事务**保证（任务创建 + 预算预授权 + 运行检查点同在 `MutateCloudAgent` 内），`PiModelStep` 在 `ActiveTaskID` 非空时返回既有任务。我此前的 `CreateTask` 回退确实不生效，但**不需要**它 | `TestPiModelStepAdmissionIsExactlyOnce`：重投返回同一 taskId、任务数=1、预授权数=1、检查点记录一致 |
| 2.3 `[门槛]` | **并发结算** | `finance.go:749` `SettleBillingOrder`：加订单锁或条件状态转移 + 账务幂等键 | 两连接同读同一未结算订单并发结算 → 只扣一次（账户须另有足额预留，避免余额不足偶然挡住） |
| 2.4 | 工具推进的存储错误 | `advanceCloudAgentTool`：已记结果、未 `cloudAgentSave` 时失败必须回滚 | 断言游标/画布/回执保持提交前状态 |

## 阶段 3：Node 侧恢复与错误传播（P0，与阶段 2 并行）

| # | 问题 | 改动 | 验收 |
|---|---|---|---|
| 3.1 | schema 校验晚于恢复 | ✅ **已实施**：`assertToolSnapshotMatchesSchema` 与租约心跳均提到 `recoverToolResults` 之前 | 恢复中等待审批/媒体时租约不耗尽 |
| 3.2 | `rejected` 不在停止集合 | ✅ **已实施**：统一 `isTerminalRunStatus`（completed/failed/cancelled/**rejected**）；Go 新增 `terminated` 回执信号；`executeTool` 遇终止立即返回 | 拒绝后 Node 停止；不再无限续租占住 worker |
| 3.3 | 初始提示未固定 | 初始化写一个**原子快照**（prompt/schema/harness 版本），恢复按快照重建 | 任意初始 checkpoint 崩溃后恢复：历史完整、无重复、**提示版本固定** |
| 3.4 | `server.ts` 串行 await | 单 run 阻塞 worker → 改为可并行处理多个 run（或明确串行上限并文档化） | 一个等待审批的 run 不阻塞其他 run |
| 3.5 | 错误传播 | ✅ **已实施**：订阅回调包裹 try/catch 记录 `listenerFailure`，`continue()` 后显式抛出；**模型自身的 error/aborted 不抛**（合法业务结果，按既有语义结束） | `checkpoint 持久化失败必须抛出，不能当成正常结束` |
| 3.6 | 订阅请求缺 signal | ✅ **已实施**：订阅回调接收 Pi 的 `(event, signal)`，`runSignal` 用于 checkpoint / startToolBatch / snapshot | 失租后请求被中止 |

## 阶段 4：旧循环职责抽取（P0）

旧循环承担、Pi 当前**没有**等价流程的职责，抽成 Pi bridge 可调用的窄入口：

| 职责 | 现状 | 验收 |
|---|---|---|
| 步数/金额/媒体硬预算 | `PiModelStep` 无等价前置 | 预算必须在**下一次副作用准入前**强制，不能靠模型自觉 |
| 上下文压缩前置 | 无 | 压缩后 call/result 配对完整，**未完成调用不被裁掉**；下一次请求真实使用新上下文 |
| 插话消费 | 无等价 | 插话不丢、不乱序 |
| 图片投递与观察 | Node 只同步部分新增图片消息 | 两套视图同步一致 |
| 完成规则 / 损坏状态 / cleanup | 部分缺失 | 每个遗留非终态有明确归宿；cleanup 不因删旧调度器悬挂 |

## 阶段 5：删除旧驱动

| # | 动作 | 验收 |
|---|---|---|
| 5.1 | 按 Codex 的分组表迁移 84 处调用（approval_settings 7、canvas_events 等 13、media 19、completion 等 13、context 9、stop_reason 等 13、reliability 10），混合测试要拆 | 业务规则留 Go 层、编排交真实 Pi、跨进程故障用少量严格集成测试 |
| 5.2 | 建真实 Pi 编排测试：真实 0.87.1 `Agent` + 确定性 stub（**不要 mock 掉 Agent**） | 披露→调用→结果→下一轮、审批、终止、重启恢复顺序均有断言 |
| 5.3 | 跨进程故障测试：Node worker + Go HTTP + 真实数据库，模型上游 stub | 覆盖阶段 2 的提交窗口、双 worker、媒体结算、SSE 重连 |
| 5.4 | 删除 `advanceCloudAgentByID` / `advanceCloudAgent`，**保留** Pi 仍用的 `advanceCloudAgentTool` / `advanceCloudAgentMedia` | 生产与测试中旧驱动引用**为零**；没有改名复制出另一套循环 |
| 5.5 | 7 个失败用例归入三类之一 | **不得靠 skip、放宽断言或恢复旧调度器变绿** |

## 阶段 6：切换门槛与演练

历史 run 处置（**不要统一改 `engine="pi"`**）：
- Pi run：按检查点版本恢复；不支持的状态明确停止并做账务/资源收尾
- 空 engine 旧 run：默认受控终止或取消，保留已提交任务与结果
- 终态且 `CleanupPending`：交给独立幂等清理机制

`[门槛]` 全清单：写入原子性、并发账务、恢复期间租约、拒绝/取消终态、硬预算、媒体收尾、
上下文因果顺序、历史 run 处置、旧驱动测试依赖清零。

最后：隔离环境升级/恢复演练 + 真实浏览器验收（镜像与源码制品匹配、冷热缓存、审批、SSE、画布回写）。

## 阶段 3.1 / 3.2 / 1.4 实施记录（本轮）

### 结论先行：「续跑」的语义澄清

「续跑」= **Pi agent loop 是否继续**（是否还会发起后续模型请求），与以下两者无关：
Node worker loop（`server.ts` 领取下一个 run）、已删除的 Go 调度循环。

**现有合同已经明确禁止续跑** —— `cloud_agent_runtime.go` 拒绝路径的注释是硬约束：

> Rejection is a user control-plane decision, not a failed tool invocation. Make it terminal
> before the scheduler can advance the pending call; **no tool result, canvas mutation,
> generation task or follow-up model request may be produced from this decision.**

所以 Node 继续循环**不是需要重新决策的产品语义，而是违反既有合同的 bug**。只有主动想
改成"拒绝后可继续"时才需要动语义（当前不做）。

### 两个缺陷与修法

**缺陷 A：停止集合漏 `rejected`**
- `runner.ts:160` 原为 `completed`/`failed`；`:223` 原为 `completed`/`failed`/`cancelled`
- 修：统一 `isTerminalRunStatus`（含 `rejected`），两处都改用它；恢复后若已终态也直接返回

**缺陷 B：`pending` 无限轮询占死 worker**
- `advanceCloudAgentTool:1860` 对非 running/queued 状态**静默 `return nil`**（不产生回执）
- `PiToolAdvance` 末尾 → `{Pending: true}` → `bridge.executeTool` 的 `for(;;)` 每 900ms 无限轮询
- 修（三层配合）：
  1. Go `PiToolReceipt` 新增 `terminated`；`PiToolAdvance` 入口与推进后都判终态并返回该信号
  2. `bridge.executeTool`：`if (!receipt.pending || receipt.terminated) return receipt;`
  3. `runner.ts` 的 executeCanvas：`terminate: receipt.terminated === true || (finish_run && !isError)`
     —— 让 Pi 循环在终态时停下

### 阶段 3.1：心跳与 schema 校验前置

原实现顺序是 `检查点初始化 → recoverToolResults → schema 校验 → … → 创建 lease 心跳`，
意味着**恢复期间完全不续租**，等待审批或媒体任务超过 45s 就会失租。

已改为：`schema 校验 → 启动租约心跳 → 检查点初始化 → recoverToolResults → …`。
顺带修掉一个作用域陷阱：`finally` 与 `try` 是兄弟作用域，`onShutdown` 必须在 try 外声明，
否则 finally 里不可见（TS 会直接报错）。

### 验收

- `cd agent && npm test` → **24/24**
- `go test ./internal/app -run TestPi` → **ok**，新增
  `TestPiToolAdvanceSignalsTerminationInsteadOfPending` 覆盖 rejected/cancelled/failed/completed
  四种终态都必须返回终止信号而非 pending
- `npx tsc --noEmit` 通过；`go build ./...` 通过

## 阶段 0.2 + 2.1 + 部分 1.4/1.5 执行记录（本轮）

### 0.2 失败基线：12 个（不是 7 个）

跑全量 `go test ./internal/app/` 得到 **12 个失败**，其中 **5 个是我本会话改动引入的**
（移除 Go 侧 Harness 追加 + 移除续聊里的旧驱动调用），按 Codex 的三分类法处理如下：

| 失败用例 | 分类 | 处理 |
|---|---|---|
| `TestCloudAgentAdmissionAndContinuation` | 旧实现断言（依赖已删驱动推进上一轮） | 测试显式置终态 |
| `TestCloudAgentContinuationReadsLatestProfileWithoutChangingParent` | 同上 | 同上 |
| `TestCloudAgentReliabilityFailedContinuation` | 同上 | 同上 |
| `TestCloudAgentContinuesAfterContractChange` | **真实缺口**：superseded 旧轮无人归档 | 见下 |
| `TestCloudAgentHarnessChangeBlocksContinuation` | **真实缺口**：harness 不再进哈希 | 见下 |

新增测试辅助 `markAgentRunTerminalForTest`（`cloud_agent_persist_test.go`）：
旧驱动删除后，测试不能再靠 `advanceCloudAgentByID` 把上一轮推进到终态；生产里这一步由
Pi worker 完成，测试显式构造终态即可（这些用例真正断言的是"上一轮终结后能否继续"）。

### 修掉的两个真实缺口

**缺口 1：superseded 运行永远停在 running**

`cloud_agent.go` 的续聊路径原来在 `superseded` 时**什么都不做**，隐含依赖已删除的旧驱动
在推进时发现合同不匹配才归档。删除驱动后，用旧合同创建的运行会永远停在 `running`：
用户看到卡住的一轮，"活跃运行"语义也被占用。
→ 已改为在续聊时**显式归档**（`terminateCloudAgent`，仅当尚未终态），并记日志。
这与 Codex 的要求一致：*不支持的状态明确停止并执行收尾*。

**缺口 2：Harness 变更不再阻断续聊**

装配权移到 Node 后，`SystemPolicyHash` 不再覆盖 Harness 内容 —— 于是"改动 Harness 指令
后旧运行必须阻断续聊"这条合同失效（等于丢失合同快照）。
→ 已改为把 Harness 内容**折进哈希但不并入 `system.Text`**：
`policyHash(id, version, system.Text + "\n\n" + workspace)`。
正文装配权仍在 Node（不重复装配），但"本轮用的是哪一版 Harness"重新成为合同的一部分。

### 阶段 2.1：画布写入原子性（`[门槛]`）已实施

**缺陷**：`advanceCloudAgentTool` 把**所有** `toolErr` 一律降级成工具失败回执后照常提交。
画布保存与"变更记录"共用同一个 `MutateCloudAgent` 事务，于是 recorder 失败时会出现
**画布已改 + 失败回执**：模型看到失败会重试写入，而画布其实已被改过一次。

**修法**：新增 `cloudAgentToolErrorIsBusiness(err)`，只把**已知业务拒绝**作为回执
（`cloudAgentArgumentError` / `cloudAgentReadLoopError` / 快照冲突 / `*AppError`）；
其余（数据库、存储、事务等基础设施错误）**向上返回以回滚整笔事务**。

**故障注入测试** `TestCanvasWriteRollsBackWhenRecorderFails`（`cloud_agent_canvas_atomicity_test.go`）：

1. 在 `MutateCloudAgent` 事务内调用 `applyCloudAgentCanvas`，注入返回错误的 recorder
   → 断言错误向上传递、**画布内容与注入前逐字节相同**、注入的节点不存在
2. **反证**：把同一错误吞掉（返回 nil）时，断言画布**确实保留了写入**
   —— 证明这个分类不可省，也复现了修复前的行为

### 当前失败数：12 → 7

修完上述两项后，剩余 **7 个失败**与最初的既有失败集合一致（分层工具披露漂移 5 个 +
与 Pi 迁移无关 2 个）：`TestCloudAgentAutoMediaSubmitsWithoutApproval`、
`TestCloudAgentCanvasReadArgumentRepairContinuesRun`、`TestCloudAgentEmptyCanvasRepairAcrossCheckpoints`、
`TestCloudAgentRuntimeEmitsContextPressurePerStep`、`TestCloudAgentSkillMissingAndDistinctPaths`、
`TestPluginViewIncludesDocumentationForEveryOfficialProtocol`、`TestPurgeAssetsBatchSharedResourcesAndHistory`。

按 Codex 要求，这 7 个**不得靠 skip 或放宽断言**变绿，需逐个归入三分类之一（阶段 5.5）。

## 事故：agent 已输出但 UI 一直显示"运行中"（本轮定位并修复）

### 现象

画布 Agent 的正文已经流式显示出来，但运行状态一直停在 `running`，UI 永远显示"正在运行"。

### 现场证据

```
runId = agaf81df7778bd1fe6f5b551cf5f3ac195
status = running
events = [context_pressure, reasoning_delta×4, assistant_delta]   ← 有正文，无终态
```

关键特征：**有 `assistant_delta`、没有 `assistant_message`、没有任何终态事件**。
`assistant_delta` 是 Node 从模型任务的 `textDraft` 差量产生的（说明模型确实产出并流出了正文），
而 `assistant_message` 由 Go 在收到 assistant 检查点时补发 —— 它缺失说明 **Node 的 Pi 循环
在把 assistant 消息 checkpoint 上去之前就失败了**。

### 根因

**`agent/harness/TOOL_SCHEMA.json` 不是超集。**

该制品原先用默认请求（`permissionMode=auto`，**未开视觉**）生成，因此不含
`canvas_inspect_image`（该工具只在 `VisionEnabled` 时暴露）。而我在本会话加的
**worker 启动期交叉校验** `assertToolSnapshotMatchesSchema` 会把"快照里有制品中没有的工具"
判为失败并**抛错**：

```
Pi worker run failed: server snapshot has tools missing from cloud-agent-tools/v2: canvas_inspect_image
```

于是每一次运行都在校验阶段就失败，运行留在 `running`；模型那边已经把正文流完了，
所以前端看到"有输出 + 永远运行中"。

**这是我自己引入的检查把一个数据漂移放大成了 agent 循环全面不可用** —— 值得记下的设计教训：
启动期硬失败只适合"必然一致"的合同，而工具集合会随权限/视觉/技能/记忆开关变化。

### 修复

制品改为**任意运行配置下工具集合的超集**：用
`permissionMode=auto` + `VisionEnabled=true` + `HasMemories=true` + 非空 `SkillIDs` 生成，
工具数 27 → **31**（含 `canvas_inspect_image`）。Node 侧校验语义仍是
"快照 ⊆ 制品 且 同名工具参数定义一致"，这在该前提下是成立的。

### ~~自愈~~ **更正：不是自愈，是靠"修制品 + 重启 worker"才恢复**

我最初写成"租约过期后自愈"是**错的**。子代理用 revision 对账证明：worker 每 45 秒把运行
重新领回来、立刻在校验处失败、再等租约过期，**无限重复**：

| run | revision | 对照健康运行 | 推算领取次数 |
|---|---|---|---|
| `agaf81df…` | 17 | 同形状健康运行 = 12 | ≈ 5 次（3.75 分钟 / 45s） |
| `age9d3be7…` | 79 | ≈ 12 | ≈ 67 次（≈50 分钟 / 45s） |

`MutateCloudAgent` 每事务只 +1 revision（`repository/cloud_agent.go:215-223`），
`ClaimPiAgent` 领取时 +1（`:145-166`），据此推算。收尾那次领取的
`lease_expires_at = 19:05:12 + 45s`，与"上一租约在 19:05:12 才过期"吻合。

**真正让它们恢复的是**：19:03 我重新生成制品（27→31 工具）+ 19:04 重建 agent 容器。
换句话说，**制品不变、worker 不重启，这两个运行会永远卡在 running**。

### 已补的修复

**1. 启动期致命错误"运行可见"**（已实施并部署验证）

- Node：`FatalWorkerError`（`tool-disclosure.ts`）标记重试无用的配置/协议错误；
  `server.ts` 捕获后调 `bridge.failRun(run, message)` 再抛
- Go：`PiFailRun`（`cloud_agent_pi_bridge.go`）+ 路由 `POST /internal-agent/runs/:id/fail`
  写 `status=failed` + `run_failed(reason=pi_worker_fatal)`；只接受租约持有者，已终态时幂等 no-op
- 测试：`TestPiFailRunMarksRunFailedWithReason`（含幂等与越权拒绝）
- 部署验证：后端二进制含 `pi_worker_fatal`（构建时间 19:15 > 改动 19:06）；
  路由对畸形 JSON 返回 **400** 而非 404，证明已挂载

**2. 租约看门狗**（已实施并部署）

`ClaimPiAgent` 只靠 `lease_expires_at` 让运行被反复领取，**worker 崩溃或配置错误时
没有任何一方把运行推向终态** —— 这是"UI 卡在运行中"的结构性原因。已补：

- `repository.StalledPiAgentRuns(expiredBefore, limit)`：查
  `engine='pi' AND status IN (queued,running,waiting_approval) AND lease_expires_at < cutoff`
- `Service.SweepStalledPiAgentRuns()`：把停滞运行写为 `failed` +
  `run_failed(reason=pi_worker_stalled)`，失败文案提示检查 agent 日志；
  遇 `ErrCreationConflict` 跳过（有并发推进就交给它），已终态幂等忽略
- `task_worker`：**60 秒 ticker** 调用（旧的 `advanceCloudAgents` 扫描已删除，
  但看门狗必须保留，否则 worker 挂掉就没人收尾）
- 阈值 `piStalledLeasePeriods = 6`（6×45s = 4.5 分钟），留足余量避免误杀慢步骤

测试：`TestSweepStalledPiAgentRunsTerminatesAbandonedRun`（终结 + 幂等）与
`TestSweepStalledPiAgentRunsLeavesActiveRunAlone`（租约未过期的活跃运行不得被误杀）。

**3. 前端仍缺"长时间无进展"反馈**（未做，优先级低 —— 后端现在会终结停滞运行，
前端自然会收到终态）
