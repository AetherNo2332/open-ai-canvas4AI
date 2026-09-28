# Pi 移植现状与 TODO 清单

> **施工顺序已调整**：canary 先删除旧编排并把生产 runner 接到 Pi Coding Agent，再恢复能力。以 [`PI_CODING_AGENT_CANARY_DIRECT_CUTOVER.md`](./PI_CODING_AGENT_CANARY_DIRECT_CUTOVER.md) 为新路线；本文的阶段编号与“下一步”段落保留为历史清单。当前工作树已出现 `holding` 占位任务与合同 v2 代码，但首步换单、退款和恢复尚不能据此判定完成。

> 初始基线：`canary`，HEAD `7aa99880`；下方早期差异计数是历史快照，当前差异请以 `git status` 为准。
> 本清单只收录**经本轮代码核对或实测**的条目；每条给出证据与验收方式。
> 未经核对的推测一律标 `未验证`，不混进结论。
>
> **状态新鲜度（2026-09-28）**：本清单下方 M-01～M-27 状态表混有旧工作区快照，不应当作当前实现状态；请先读 [`PI_MIGRATION_PHASE_STATUS.md`](./PI_MIGRATION_PHASE_STATUS.md) 开头的“当前状态复核”和 [`PI_MIGRATION_DAILY_2026-09-28.md`](./PI_MIGRATION_DAILY_2026-09-28.md)。M-02 的 Pi 用量锚点已随 `cf0b9207` 提交；M-03 的 Go 结构化压缩与模型限额接入在当前工作树，尚未提交。本文后续验收要求保留为未完成门槛。
>
> **历史进度注记**：以下阶段编号与 M-01～M-27 表格记录的是较早施工快照。当前 Pi 直切实现已包含 holding 占位任务、首步换单、Pi v3 session、worker pool 与 Go 模型步骤；压缩和限额接入见 2026-09-28 状态记录。不要再引用旧段落中“当前 `CreateCloudAgentRun` 仍提交可执行根模型任务”的判断。

---

## 一、现状快照

### 1.1 已完成并已验证

| 编号 | 事项 | 证据 |
| --- | --- | --- |
| D-01 | P0 基线与三端合同 | `PI_MIGRATION_P0_BASELINE_AND_CONTRACTS.md`；7 个既有失败逐名复现；25 类 journal 事件逐项枚举 |
| D-02 | P1 SDK 可行性探针（真实 0.87.1，9 用例） | `agent/test/pi-sdk-probe.test.ts`；最新交接记录 `npm test` **46/46**（本次文档更新未重跑） |
| D-03 | P1 扩展点 ADR（含对路线图 4 处修正） | `PI_MIGRATION_P1_SDK_EXTENSION_POINTS.md` |
| D-04 | P3 并发结算 `[门槛]` | `SettleBillingOrder` 条件状态转移闸门；3 个测试（含确定性反证）；`-count=20` 无抖动 |
| D-05 | P5 `CANVAS_AGENT_ENGINE` 漂移消除 | 代码/compose/文档三处；`go test ./cmd/server/...` 通过 |
| D-06 | HANDOFF #5 路由交集高估 | TDD 先复现 `475520 > 427520`；4 个新用例 |
| D-07 | HANDOFF #2 渠道模型 `thinking` | Go 字段 + 管理端开关 + 往返测试 |
| D-08 | HANDOFF #3 执行路径附回权威能力 | `provider.go` 附回渠道模型能力；2 个新用例 |
| D-09 | 既有前端编译/运行阻断 | `ComposerControls` 缺 2 个 props → 运行时会白屏；修后 `typecheck`/`build`/测试全绿 |
| D-10 | 工具批次线格式 400（部署实测发现） | `cloudAgentCall` 缺 `type`；双侧对拍测试 |
| D-11 | 确定性 4xx 无限重试 | 归类为 `FatalWorkerError`；4 个用例 |
| D-12 | 3000 本地端到端验收 | 真实运行 `completed`，含 2 次 `tool_completed`；非终态运行 0、悬挂账单 0 |

### 1.2 环境状态（会影响后续可执行性）

| 项 | 状态 |
| --- | --- |
| Docker / 3000 栈 | 上次本地部署记录三容器 healthy、schema 41；阶段 2 的 Node/Go 改动尚未部署。本次文档更新未重新核对运行状态 |
| 前端环境 | ✅ `bun 1.3.13` + `web/node_modules`（1443 包） |
| Go | ✅ `go1.25.1`，需显式 `export PATH=/home/a1/.local-go/go/bin:$PATH` |
| Node | ✅ `v22.23.2`；`agent/node_modules` 已装 |
| `/mnt/d` 导入开销 | ⚠️ SDK 冷启动 **75.6s**（原生 ext4 为 659ms）；SDK 测试建议在原生 FS 镜像跑 |

---

## 二、TODO 清单

### T0 门槛：不修完不得宣称"移植完成"

| 编号 | 事项 | 现状与证据 | 验收 |
| --- | --- | --- | --- |
| **M-01** | **模型步结果带上上游 usage** | `providerTextResult = { Text, Reasoning }`（`provider_text.go:1062`），`providerTextTaskResult` 只发 `mode/text/reasoning` → Node `CanvasModelResult.usage` 恒空 → `pi-stream.ts:109` 散成全零 `emptyUsage`。**这是 M-02 的前置** | 模型步 `result_json` 含 input/output/cacheRead/cacheWrite/cost；Node `AssistantMessage.usage` 非零；有单测覆盖（含无 usage 时的显式回退） |
| **M-02** | usage 定锚接回 Pi 路径 | `recordCloudAgentTokenAnchor` 唯一调用点 `cloud_agent_runtime.go:1202`，在标为"已废弃、新代码不得再调用"的 `advanceCloudAgent`(933–1272) 内 | Pi 每步后调用；`context_pressure` 的 `measurementSource` 由估算变实测；签名/模型/窗口变更时正确作废 |
| **M-03** | 上下文压缩接回 Pi 路径（**按 06:59 新方案**） | 同上，`cloudAgentRequestCompaction` 只在 `:1225/:1263`（死区间）。新手册要求：Go 结构化压缩为唯一生产摘要，`session_before_compact` 返回自定义 `CompactionResult` | 见 §三 的可行性结论；验收含"**不产生 Pi 默认摘要的第二次模型调用**"、保留最近完整轮次、三个压缩事件恢复可达 |
| **M-04** | 插话投递接回 Pi 路径 | `cloudAgentDrainInterjections` 唯一调用点 `:1197`（死区间）；`PiAgentSnapshot` 无插话字段，Node 无插话代码 | 插话进入下一轮上下文并 emit `user_interjection_delivered`；不再出现"模型正文被当插话回灌" |
| **M-05** | 重试阶梯接回 Pi 路径 | `correctCloudAgentStepTimeout:1050` / `correctCloudAgentEmptyOutputEscalation:1045` / `cloudAgentEscalateTruncatedStep:1085` 全在死区间；当前模型步失败 = 整轮 `failed` | 空输出/截断/超时按原阶梯升级；每次重试的计费可对账 |
| **M-06** | 预算闸门接到 Pi 模型步准入 | `cloudAgentContextBudgetForRequest` 唯一调用点 `:1189`（死区间）；Pi 路径预算只用于遥测；`PiModelStep` 仅校验消息数≤500、systemPrompt≤128KiB | 超预算在**准入前**阻断；未知窗口走字节/条数兜底；不再靠 2MiB/1000 条的硬上限兜底 |
| **M-07** | 视觉观察账本接回 Pi 路径 | 记录函数 `cloudAgentRecordImageObservations(text, hasToolCalls)` 在 `!hasToolCalls` 时必 `return 0`（`vision.go:355-358`），唯一活调用传 `false`（`pi_bridge.go:229`）；账本填充点 `:1246` 在死区间 | `canvas_inspect_image` 后账本有记录；`RequiresVisualInspection` 有读取判据；图片裁剪以账本替代 |

### T1 正确性缺陷

| 编号 | 事项 | 现状与证据 | 验收 |
| --- | --- | --- | --- |
| **M-08** ✅ | `PiFailModelStep` 终态守卫 | 已终态重投返回幂等结果；成功任务上报失败仍拒绝，见 `cloud_agent_pi_lifecycle_gates_test.go` | 专项测试已记入阶段状态；双 worker 竞态仍在 P6 跨进程验证 |
| **M-09** ✅ | 看门狗置 `CleanupPending` | 停滞终结时已设置清理标志，见 `SweepStalledPiAgentRuns` 与专项测试 | 标志位已验；实际子任务／媒体清理与故障注入仍在 P6 验收 |
| **M-10** ✅ | 未领取 run 的排队语义 | `StalledPiAgentRuns` 已排除 NULL 租约；独立的 `SweepUnclaimedPiAgentRuns` 超时给出 `pi_worker_unavailable` | 首次领取仍接受 NULL 租约；跨进程不可用与恢复路径仍在 P6 验收 |
| **M-11** ✅ | 看图 `resource:` 占位符水合 | 已确认为迁移回归：Pi 路径此前漏传 `referenceImages`。`PiModelStep` 已接回 `cloudAgentImageReferences` 与白名单，专项测试覆盖水合、裁剪和失败关闭 | 代码与专项测试已验；真实模型看图运行仍未做（本地渠道未声明图片输入能力），列入 P6 |
| **M-12** | 单 worker 串行 + 无限轮询 | `server.ts` 单 `while` + `await`；`bridge.executeTool` 遇 `pending` 每 900ms 轮询无上限，期间 15s 心跳持续续租 → 一条等审批的 run 饿死整个队列 | 多 run 并发；等待审批/媒体释放执行槽；轮询有上限与退避；roadmap §3.4 |
| **M-13** | 审批永不超时 | 旧路径与 canary 都有此问题（dev 侧 `waiting_approval` 也被排除在调度与看门狗之外） | 审批有超时策略与可观测状态 |

### T2 阶段工作

| 编号 | 阶段 | 事项 | 验收 |
| --- | --- | --- | --- |
| **M-14** | P2 | `cloud_agent_pi_sessions` / `_entries` / `_operations` 三表迁移（SQLite + PostgreSQL 同步） | 唯一约束生效；active leaf/revision 原子更新；迁移可回滚 |
| **M-15** | P2 | 初始 system/Harness/schema 快照在首个 Pi 消息前原子固定 | 重启后旧 run 用原快照，不用磁盘新 Harness |
| **M-16** | P2 | `piToolReceipt` 幂等身份作用域 | 跨步骤重复 `callId`、同 ID 不同参数、压缩后查询均有测试 |
| **M-17** | P2 | lease epoch fencing + 恢复顺序（取租约→心跳→读 session→重建→对账→订阅→续跑） | 任一提交窗口崩溃无重复副作用；双 worker 仅一方可提交 |
| **M-18** | P2 | 旧 run 处置：Pi run 按检查点恢复；空 engine 旧 run 受控终止；`CleanupPending` 交独立幂等清理 | 每个遗留非终态有明确归宿 |
| **M-19** | P4 | SDK 资源装配 + 动态披露走 `setActiveToolsByName`（初始活跃集须在建 session 后立刻设置） | 母/子工具披露与恢复后一致；system/tool patch 进会话树 |
| **M-20** | P4 | 硬预算前置（步数/金额/媒体数/上下文上限） | 在下一次副作用准入前阻断，不靠 prompt |
| **M-21** | P5 | 删除旧驱动 `advanceCloudAgentByID` / `advanceCloudAgent` | 生产与测试引用**为零**（当前 `advanceCloudAgentByID` 77 处 / 22 文件） |
| **M-22** | P5 | 7 个既有失败归入三分类 | 迁移中修复 / 被新测试替代 / 独立已有缺陷；**不得 skip 或放宽断言** |
| **M-23** | P6 | PostgreSQL 一侧 + 故障注入（提交窗口崩溃、双 worker 抢租约、媒体收尾、SSE 断线） | roadmap §6.4 矩阵 |
| **M-24** | P6 | 真实浏览器验收（面板渲染、刷新、断线续传、跨用户隔离） | 真实应用页面，不用静态仿制页 |

### T3 验证基础设施

| 编号 | 事项 | 理由 |
| --- | --- | --- |
| **M-25** | ~~恢复 Docker/WSL 集成~~ → 已自行恢复 | 记录在案：WSL 集成会随 Docker Desktop 重启短暂掉线，届时 `docker` 命令消失、`/tmp` 被清空；容器本身会随 distro 重启，恢复后仍是同一批镜像 |
| **M-26** | 扩展跨进程线格式测试 | 本轮 400 缺陷正是"没有跨进程线格式测试"造成的；已有 `bridge-wire-contract.test.ts` + `cloud_agent_pi_wire_contract_test.go` 可作模板，应覆盖全部 12 个内部端点 |
| **M-27** | SDK 测试在原生 FS 或容器内跑 | `/mnt/d` 上冷启动 75.6s，会被误判为性能问题 |

---

## 三、M-03（压缩）的可行性结论 —— 已核对，可执行

新手册（06:59）把压缩策略改成"Go 结构化语义压缩为唯一生产摘要"。**我核对了它的可行性，结论是可行，且不需要任何私有手段**：

| 计划引用 | 实际存在 |
| --- | --- |
| `agentcontext.BuildPrompt` / `Parse` | `internal/agentcontext/checkpoint.go:80` / `:114`（另有 `ParseFrame:151`） |
| `cloudAgentFallbackCheckpoint` / `BoundCheckpoint` / `CompleteTurnTail` | `cloud_agent_context_compaction.go:295` / `:361` / `:393` |
| 压缩任务 op | `:18` `cloud_agent_context_compaction` |

SDK 接入点确实支持"提供自定义压缩结果"：

- `SessionBeforeCompactResult = { cancel?: boolean; **compaction?: CompactionResult** }`
- 事件携带 `branchEntries: SessionEntry[]`（即 Pi 活动分支投影）
- `agent-session.js:1913-1920`：**给了 `compaction` 就直接用，`else` 才走 `_runDefaultCompaction()`** → 默认摘要的模型调用被完全跳过，`fromExtension = true`
- `CompactionResult.details` 注释原文即 *"version markers for **structured compaction**"*

**唯一硬骨头**：`CompactionResult.firstKeptEntryId` 必须是 **Pi entry id**，而 Go 的 `cloudAgentCompleteTurnTail` 在 **Go canonical messages** 上算"保留最近 N 个完整轮次"——canonical 没有 Pi entry id。
→ 建议把 M-03 的第一步定为：**用真实 SDK 探针专门验证"Go 轮次边界 → Pi entry id"的映射能否精确保留完整轮次**；不能则按手册退到受控 context edit，禁止静默换算法。

---

## 四、建议执行顺序

```
✅ M-08 / M-09 / M-10（终态与清理）—— 阶段 1 已完成
✅ M-11（看图占位符）—— 定论为迁移回归并已补线
  ↓
【阶段 2】统一首步合同：Go 建 run 与不可变快照，Node 领取后发起首步
   ├─ ✅ 提示合成（Node）：服务端策略永远在最前，SYSTEM.md 不能替换它（原断言已按需求改写）
   ├─ ✅ 提示准入（Go）：PiModelStep 校验提交的 system prompt 仍含服务端策略（4 用例）
   ├─ ✅ 预授权定案：**(a) 不可执行的占位任务**（Codex 二次评审改判；见阶段状态 §2.8）
   ├─ ✅ 兼容读路径：`cloudAgentRunRefFor`（旧路径优先 + 执行记录回退），4 个新用例覆盖；`CloudAgentRun` 已切换
   ├─ ✅ 控制面入口：`CancelCloudAgent` / `InterjectCloudAgent` 改走同一解析（此前无根任务运行**无法取消**），5 个新用例
   ├─ ✅ 提示合同固化：`harnessHash` 首步钉死、漂移明确拒绝（Node 7 用例 + Go 5 用例）
   ├─ ⏳ 合同版本 + `awaiting_first_step` 阶段 + 服务端策略/Harness/schema **快照内容持久化**
   └─ ⏳ 不可领取占位任务 + 首步原子换单 + 终态退款清扫（含 cleanup 对新状态的覆盖）
       └─ 当前 hash 只能检测漂移，不能恢复旧 Harness；完成后首步才可由 Node 最终提示驱动
  ↓
【阶段 3】Pi v3 session/entry/operation 持久层 + lease epoch fencing → 生产 runner 换 createAgentSession（M-14～M-18）
  ↓
【阶段 4】Canvas Provider → Go 模型任务链；真实 usage/stop/能力/计费（M-01、M-02、M-05、M-06、M-20）
  ↓
【阶段 5】SDK 工具装配、完整 schema 准入、画布/视觉/媒体/审批/插话、并发与唤醒（M-04、M-07、M-12、M-13、M-19）
  ↓
【阶段 6】M-03：探针验证最近完整轮次到 firstKeptEntryId 的映射 → Go 原有结构化语义压缩接回
  ↓
【阶段 7】M-21/M-22：旧驱动引用清零；7 个既有失败逐项归因，不 skip、不放宽断言
  ↓
【阶段 8】M-23/M-24/M-26：SQLite + PostgreSQL 故障注入、真实浏览器、跨进程线格式与 SSE 验收
```

完整退出条件与三端测试矩阵见 [`PI_CODING_AGENT_MIGRATION_ROADMAP.md`](./PI_CODING_AGENT_MIGRATION_ROADMAP.md) §5–6。M-01 可与阶段 2、3 独立开发，但 M-03 的生产接入还依赖 Pi v3 持久化、原提示快照和完整轮次边界映射；无上游 usage 时也要用保守估算触发预算闸门。

**依赖提示**：M-01 是上游 usage 传输缺口；M-02～M-07 还涉及旧 Go 驱动内的上下文治理、控制流与视觉入口。应逐项在 Pi 路径建立可达调用和专项测试，之后才能删除旧驱动。看图引用 M-11 已补线，不代表视觉观察账本 M-07 已恢复。
