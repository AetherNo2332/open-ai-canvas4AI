# canary 与 dev 的 Agent 能力对照

> 历史对照快照。该文后文中的“7 个母类型/31 个工具”记录的是当时的分层披露实现；截至 2026-09-28，当前目标与代码已改为扁平注册 24 个满足权限/能力的具体工具，分类仅作元数据，运行期由 Pi `tool_call` hook 与 Go 逐次复核。当前实现及测试状态以 [`PI_MIGRATION_DAILY_2026-09-28.md`](./PI_MIGRATION_DAILY_2026-09-28.md) 和 [`PI_CODING_AGENT_MIGRATION_ROADMAP.md`](./PI_CODING_AGENT_MIGRATION_ROADMAP.md) 为准。

> 对照对象：
> - **dev** = `b2bd0ae1`（`canvas4ai/dev` 并入 canary 的那个 tip；该 ref 上**没有 `agent/` 目录**）
> - **canary** = 当前工作树（含未提交改动）
>
> 所有结论都带 `file:line`，且经过"发射点 → 调用者 → 是否被 Pi 路径可达"的逐跳核对，
> 而不是"代码里存在就算能做"。**"代码里存在但 Pi 路径不可达"和"没有这段代码"在运行时是同一件事**，
> 前者更危险，因为它看起来是通的。

## 0. 一句话结论

- **工具面：canary ⊇ dev。** 叶子工具 **24 个完全相同**，canary 另加 7 个母类型工具（共 31），用于分层披露。
- **公开合同：完全一致。** `/api/agent` 导出面逐项相同，25 类 SSE 事件名逐项相同。
- **运行时保障：canary 有 4 项退化。** dev 的循环是生产路径，canary 的循环是死代码，
  于是挂在旧循环上的**上下文压缩、usage 定锚、插话投递、截断/超时恢复**在 Pi 路径上**全部不可达**。
- **并发：canary 从"多 run 并发推进"退化为"单 worker 串行"。**

## 1. 结构差异

| | dev (`b2bd0ae1`) | canary（工作树） |
| --- | --- | --- |
| Agent 位置 | Go 进程内 | 独立 Node 服务 `agent/` |
| 循环驱动 | `advanceCloudAgents()`（`cloud_agent_runtime.go:852`），由 `task_worker.go:51` **每 2 秒** tick | `agent/src/server.ts` 单个 `while` 循环 + `await runCanvasAgent(...)` |
| 每轮推进量 | `ActiveCloudAgentsAfter(cursor, 50)` → **每 tick 最多 50 个 run** | **一次一个 run** |
| 控制面 | 无 | `backend/internal/handler/internal_agent.go`（`/internal-agent`，12 个端点） |
| 编排用的 `advanceCloudAgent*` | **生产路径**（`cloud_agent.go:389` 调 `advanceCloudAgentByID`） | **已废弃**：`cloud_agent_runtime.go:906` 注释"Go 侧不再有调度入口，只保留给历史测试装配"，无生产调用者 |
| compose 服务 | backend、web | backend、web、**agent**（`profiles: ["pi"]`，无对外端口） |
| 前端 | — | `web/src/services/api/agent.ts` 导出面、`web/src` 的 agent 文件集合**与 dev 完全相同** |

## 2. 能做（canary 相对 dev 的新增能力）

| # | 能力 | 证据 |
| --- | --- | --- |
| 1 | **分层工具披露**：模型先只见 7 个母类型工具，打开类别后才见合格子工具 | `cloud_agent_tool_categories.go`、`agent/harness/TOOL_SCHEMA.json`（`cloud-agent-tools/v2`，31 项）、`ToolDisclosure`（`agent/src/tool-disclosure.ts`） |
| 2 | **独立 worker 进程 + 租约/fencing** | `cloud_agent_pi_bridge.go` `ClaimPiAgent` / `RenewPiAgentLease` / `piAgentLeasedRun` |
| 3 | **真实增量正文与推理流** | `bridge.ts` 轮询 `textDraft` 补差 → `text_delta`；实测一次运行产出多条 `reasoning_delta` |
| 4 | **停滞运行看门狗** | `SweepStalledPiAgentRuns`（`cloud_agent_pi_bridge.go:726`），`task_worker` 60 秒 ticker |
| 5 | **worker 致命错误可见化** | `PiFailRun`（`:687`）+ `POST /runs/:id/fail` |
| 6 | **并发结算安全** | `SettleBillingOrder` 改为条件状态转移作为唯一闸门（本轮修复，见 `PI_MIGRATION_PHASE_STATUS.md` §3） |
| 7 | **工具 schema 制品化 + 漂移检测** | `agent/harness/TOOL_SCHEMA.json` + Go/Node 双侧校验 |
| 8 | **12 个内部协议端点** | `internal_agent.go`（claim / snapshot / renew / messages / no-tool-turn / model-steps ×4 / tool-batches / advance / fail） |

## 3. 做不了（dev 能做、canary 的 Pi 路径做不到）

判据统一为：**该事件的发射函数 / 该功能的入口是否被 Pi 路径可达**。

| # | 能力 | dev | canary | 证据 |
| --- | --- | --- | --- | --- |
| 1 | **上下文压缩** | ✅ 循环内触发 | ❌ **不可达** | `cloudAgentRequestCompaction` 只被 `cloud_agent_runtime.go:1225/1263` 调用，二者都在死区间 `advanceCloudAgent`(933–1272) 内 |
| 2 | **压缩任务结果回收** | ✅ | ❌ **不可达** | `advanceCloudAgentContextCompaction` 只被 `cloud_agent_runtime.go:969` 调用（同一死区间） |
| 3 | **usage 定锚**（用上游实测用量校准下一步压力） | ✅ `:1163` | ❌ **不可达** | `recordCloudAgentTokenAnchor` 只被 `cloud_agent_runtime.go:1202` 调用（同一死区间） |
| 4 | **插话投递到模型** | ✅ `cloudAgentDrainInterjections` 在循环内调用 | ❌ **不可达，且会导致运行失败** | `cloudAgentDrainInterjections` 只被 `cloud_agent_runtime.go:1197` 调用（死区间）。见下方 §3.1 |
| 5 | **截断/超时/空输出的恢复** | ✅ | ❌ **不可达** | `correctCloudAgentStepTimeout`（`runtime.go:1050`）、`correctCloudAgentEmptyOutputEscalation`（`:1045`）、`cloudAgentEscalateTruncatedStep`（`:1085`）——调用点全在死区间 |
| 6 | **模型步骤停止原因事件** | ✅ | ❌ | `model_step_stop` 唯一发射点在 `advanceCloudAgent` 内 |
| 7 | **多 run 并发推进** | ✅ 每 tick 最多 50 个 | ❌ 单 worker 串行 | `server.ts` 单 `while` + `await runCanvasAgent(...)`；`runner.ts:128-137` 的工具执行也是 `for` + `await` 串行 |
| 8 | **等待审批/媒体时不占用执行槽** | ✅ 循环不推进即让出 | ❌ 串行 worker 被占住 | 同上；roadmap §3.4 |

### 3.1 插话：不只是"不生效"，会直接把运行判失败

这是本轮新发现，路线图与 `HANDOFF.md` 都没有列到。完整链路（逐跳已验证）：

1. `InterjectCloudAgent`（`cloud_agent_interjection.go:83`）把文本存进 `state.PendingInterjections`，并发 `user_interjection` —— **可达** ✅
2. 唯一的消费者 `cloudAgentDrainInterjections`（`:144`）把它注入对话 —— **调用点 `cloud_agent_runtime.go:1197` 在死区间** ❌
3. 而 `cloudAgentCompletionBlockers`（`cloud_agent_completion.go:72`）在 `PendingInterjections` 非空时返回 `pending_interjection` 阻塞项 —— 它的调用者 `cloudAgentEvaluateCompletion`（`:88`）经 `cloudAgentFinishRun`（`:265`）被 `advanceCloudAgentTool` 调用，而 `advanceCloudAgentTool` **是 Pi 路径可达的**（`cloud_agent_pi_bridge.go:537`）
4. 于是模型申请 `finish_run` → 被 `pending_interjection` 拦下 → 催办 2 次（`cloudAgentCompletionNudgeLimit = 2`，`:32`）→ 用尽 → `cloudAgentFailBlockedCompletion`（`:326`）把状态写成 **`failed`** 并 `cloudAgentDropInterjections`

**净效果**：用户在 Pi 运行中发一条插话 → 模型永远看不到这句话 → 本轮**以 `failed` 收场**，插话被丢弃。

### 3.2 25 类 SSE 事件中 4 类在 Pi 路径永远不会发出

| 事件 | 可达发射点 | 前端是否渲染 |
| --- | --- | --- |
| `context_compaction_requested` | 无 | ✅ 面板 + `agent-context-usage.ts`（永远不亮） |
| `context_compacted` | 无 | ✅ 面板 + `agent-context-usage.ts`（永远不亮） |
| `model_failure_recovered` | 无 | ✅ 面板（永远不亮） |
| `model_step_stop` | 无 | ❌ 无分支 |

其余 21 类均确认可达。**事件名集合与 dev 逐项相同，但其中 4 类的语义在 Pi 路径上是空的** ——
这正是"合同看起来一致、运行时并不一致"的典型形态。

## 4. 媒体、计费与其他核对结论

| 维度 | 结论 | 证据 |
| --- | --- | --- |
| 媒体任务收尾 | ✅ **Pi 已接线**（不是缺口） | `advanceCloudAgentMedia` 被 `cloud_agent_pi_bridge.go:535`（`PiToolAdvance`）与 `cloud_agent_recovery.go:69` 调用 |
| 工具集 | ✅ 叶子工具 24 个与 dev **完全相同**，canary 多加 7 个母工具 | `agent/harness/TOOL_SCHEMA.json` vs dev 的 `cloud_agent_tools.go` |
| 公开 API | ✅ 导出面相同 | `web/src/services/api/agent.ts` diff 为空 |
| 账务幂等 | ✅ 本轮已修并发双结算 | 见 `PI_MIGRATION_PHASE_STATUS.md` §3 |
| 权威模型能力 | ✅ 本轮已修（执行路径附回渠道模型能力） | 见同文件 §4.6 |
| 模型渠道 `thinking` | ✅ 本轮已补齐（dev 也没有） | 见同文件 §4.5.2 |

## 5. 结论与优先级建议

**能宣称的**：canary 的 agent 在**工具面、公开 API、SSE 事件名、计费与权限边界**上没有丢东西；
能力上是 dev 的超集，并且新增了分层披露、独立 worker、租约、看门狗、schema 制品等。

**不能宣称的**：canary 的 agent 在**上下文治理**（压缩 + 定锚）、**插话**、**步骤级恢复**、**并发**四个维度上**弱于 dev**，
而且这四项都不是"功能没做"，而是"代码在库里但挂在了已废弃的循环上"。

按影响排序建议的修复顺序：

1. **插话**（§3.1）—— 唯一会**主动把运行判失败**的一项，且用户操作可触发。
2. **上下文压缩 + 定锚**（§3 第 1–3 项）—— 长会话会一路顶到模型窗口，`context_pressure` 读数对下一步没有作用。
3. **并发**（§3 第 7–8 项）—— 一个等审批的 run 会占死整个 worker。
4. **步骤级恢复**（§3 第 5 项）—— 截断/超时不再自愈，直接整轮失败。

这四项共同的最小修法是：把旧循环里那四个窄入口（`cloudAgentDrainInterjections`、
`cloudAgentRequestCompaction`、`recordCloudAgentTokenAnchor`、三个 `correct*`）
**从 `advanceCloudAgent` 里摘出来，改由 Pi bridge 的等价位置调用**，
然后删掉 `advanceCloudAgent` / `advanceCloudAgentByID`。
这与路线图阶段 4「旧循环职责抽取」是同一件事，本对照给出了它的精确清单。

---

## 6. 补充：dev 侧独立审计的修正与细化

对本文件第 3 节的 dev 侧做了一次独立的逐函数审计，结论方向一致，补充三点**只属于 dev 的既有缺陷**
（它们不是 canary 的退化，写在这里是为了避免把 dev 理想化）：

1. **dev 的压缩触发用的是本地估算，不是上游定锚。** `runtime.go:1215` 用 `cloudAgentRequestEstimatedTokens(&canonical)`
   喂给 `:1224` 的 `cloudAgentRequestCompaction`；定锚只决定 `tokenSource` 标签（`compaction.go:90-95`）。
   这与 `usage_anchor.go:12-14` 的注释以及面板展示的 `provider` 投影不一致。
   → 新计划要求"按真实窗口 85% 线 + 实测 usage 定锚"，比 dev 现状更严。
2. **dev 也没有 run 级租约 / fencing / leader election**，只有 per-process `agentSchedulerMu`（`runtime.go:853`）
   + revision CAS；每个 backend 进程都在跑调度循环。→ canary 的 run 租约 + fencing 是**真实新增**。
3. **dev 的 `waiting_approval` 同样会永久挂住**：它被排除在调度扫描（`repository/cloud_agent.go:128`）
   与 5 分钟看门狗（`stuck.go:17`）之外。→ "等审批卡住"不是 canary 独有。
   另：dev 的每次重试都会**新开任务 + 新账单**（`runtime.go:2032` + `task_creation.go:139`）。

**一处口径澄清**：本文件说的"25 类事件"是 `state.event(id, "<kind>", …)` 这一层的 journal 事件，
用**同一套提取脚本**在两侧各跑一次，结论是逐项相同。若把流层合成事件（`assistant_delta` / `assistant_snapshot` /
`tool_failed` / `unreadable_event`）也算进来，两侧同样一致，但数量会变成 28 —— 计数取决于口径，比较结论不变。
