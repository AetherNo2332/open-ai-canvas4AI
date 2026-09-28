# Pi Coding Agent 移植：阶段执行与交付报告

> **状态更新提示（2026-09-28）**：本文较早的“当前状态复核”包含过期的 HEAD、工作树数量、前端/Node 测试计数和“未触碰本地 Compose”的记录；接续工作已经更新 Pi 工具披露、修复首次工具批次门禁，并在本机 `canvas-canary-3000` 完成只读 UI 验收。最新证据与限制以 [`PI_MIGRATION_DAILY_2026-09-28.md`](./PI_MIGRATION_DAILY_2026-09-28.md) 末尾“工具披露、真实 UI 调用和本地验收”一节为准。全量 Go 与 Web 测试仍有失败，整体移植未完成。

## 当前状态复核（2026-09-28）

以下为本日复核结果，优先于本文较早的阶段状态快照；详细记录见 [`PI_MIGRATION_DAILY_2026-09-28.md`](./PI_MIGRATION_DAILY_2026-09-28.md)。

- 检出：`D:\13537\open-ai-canvas-canary`，分支 `canary`，HEAD `645d1011`；本地领先 `origin/canary` 9 个提交。工作树 104 项状态记录（58 修改、45 未跟踪、1 删除），没有清理、暂存或覆盖这些内容。
- Agent worker 已用锁定的 `@earendil-works/pi-coding-agent@0.87.1` 运行 Pi session；Go 继续持有模型任务、计费、画布工具授权与执行。Pi v3 session / entries、revision 与 active leaf 持久化和租约字段已在工作树中。Node worker 上限默认 4。
- 本地未提交的压缩切片已把 Go 原结构化语义压缩接入 Pi `session_before_compact`：从 Pi v3 active branch 生成操作身份、重用 Go 计费压缩任务，失败走 Go fallback；提交时在一个事务内追加 Pi checkpoint entry 并更新 Go canonical checkpoint / 事件。Pi SDK 内置摘要在 Go 压缩失败时会取消。Go 快照解析出的 `contextWindow` / `maxTokens` 传给 Pi；缺少限额时 worker 失败关闭。
- 已补 worker 重启时恢复待提交压缩的路径：快照暴露待恢复 operation，Node 以 GET 取回同一 Go 操作，Pi `AgentSession.compact()` 生成 entry 并提交后，才恢复下一模型步；没有重新创建压缩任务。
- 定向验证：TypeScript `tsc` 通过；Node 全套 **67/67** 通过；隔离容器中的 Go 待压缩快照测试通过。详细命令见 Daily 记录。
- 另外复跑了 6 个旧 Go 循环测试，全部失败：4 项直接断言旧 `advanceCloudAgent` 在首步前把 run 置为 `running` 或触发旧式上下文暂停；Pi 合同现为 `queued/awaiting_first_step`，这些测试必须迁成 Pi 生命周期验收，不能通过放宽状态断言来“修绿”。另 2 项续聊测试把 holding 占位任务手工伪造成已完成根模型任务，未构造 Pi transcript / 最终 assistant 事件；需分别补真实旧版根任务导入测试和当前 Pi 会话续聊测试，当前不能据此宣称历史兼容已验收。
- 未完成：旧 Go 驱动仍在代码和测试中；插话、视觉观察账本与模型失败重试、硬步数准入等职责仍需迁入 Pi/Go 服务边界并验收。虽然已测 Node runner 对待压缩快照的恢复，但 PostgreSQL、双 worker 跨进程故障注入、真实 Go 计费压缩任务故障点端到端恢复、浏览器/SSE 断线续传尚未完成。本日没有部署或推送，也没有触碰正在运行的 `canary-3000` 容器。

**不得据以上局部通过结果宣称整体移植完成。** 本日命令、失败原文摘要和后续门槛见 Daily 记录。

> 交接范围：`canary` 工作树，起点 HEAD `7aa998806e2aa46d230274bacdf0a859aaf23788`（`VERSION=v1.5.7.1+7aa9988`）。
> 依据：[`PI_CODING_AGENT_MIGRATION_ROADMAP.md`](./PI_CODING_AGENT_MIGRATION_ROADMAP.md)（P0–P6）。
> 纪律：不改生产、不部署、不回滚他人未提交改动；每项结论附本轮实测命令与结果。
> 本文件只陈述**已发生的事实**；未完成项按"未验证 / 阻断"如实标注，不因局部测试通过而宣称迁移完成。

## 0. 一句话结论

**P0 与 P1 已完成并通过全部退出条件；P3 的一个 `[门槛]`（并发结算）已修复并有故障注入测试；P5 的 `CANVAS_AGENT_ENGINE` 漂移已消除；
同工作区 [`HANDOFF.md`](./HANDOFF.md) 新增的 6 项待办已全部用代码核对（全部成立），其中 3 项已修复。**
P2、P4、P6 未实现，P3 其余项、P5 其余项与 HANDOFF 其余 3 项未完成。
关键前置问题「`pi-coding-agent@0.87.1` 的 Provider / SessionManager 恢复 / 动态工具扩展点是否可用」已用真实 SDK 探针证明**可用**。

**回归信号**：改动前后各跑一次全量 `go test ./internal/app -count=1`，失败集合**完全一致**（同样那 7 个用例，无新增失败、也无意外变绿）。
后端 `handler` / `repository` / `cmd/server` 全绿；前端 `bun run typecheck` 无输出、`bun run build` 成功、专项测试 34/34 —— 并在过程中修掉了一个**既有的前端编译/运行阻断**（§4.7）。
`cd agent && npm test` → **38/38**。

**3000 本地部署与端到端验收已完成（§9）**：滚动到新镜像后跑通真实 Agent 运行（`completed`，含 2 次 `tool_completed`）。
部署过程**暴露并修掉了两个真实阻断**：工具批次线格式不匹配导致整批 400（§9.3），以及确定性错误无限重试、
绕过租约与停滞看门狗导致运行永远停在 `running`（§9.4）。终态对账：非终态运行 0、悬挂账单 0。

## 1. 本轮改动清单

| 文件 | 类型 | 说明 |
| --- | --- | --- |
| `agent/package.json` | 改 | 追加 `"@earendil-works/pi-coding-agent": "0.87.1"`（`--save-exact`） |
| `agent/package-lock.json` | 改 | `+1902` 行；实测解析到 `pi-coding-agent-0.87.1.tgz` |
| `agent/test/pi-sdk-probe.test.ts` | 新增 643 行 | P1 真实 SDK 探针（9 个用例） |
| `agent/PI_MIGRATION_P0_BASELINE_AND_CONTRACTS.md` | 新增 246 行 | P0 交付物 |
| `agent/PI_MIGRATION_P1_SDK_EXTENSION_POINTS.md` | 新增 323 行 | P1 扩展点 ADR |
| `backend/internal/repository/finance.go` | 改 | `SettleBillingOrder` 重写：条件状态转移成为唯一结算闸门 |
| `backend/internal/repository/finance_settlement_gate_test.go` | 新增 252 行 | 并发恰好一次 + 确定性反证 + 退款冲突语义 |
| `backend/cmd/server/main.go` | 改 | 删除 `CANVAS_AGENT_ENGINE` 启动校验，改为 token 缺失可见提示 |
| `docker-compose.yml` | 改 | 删除 `CANVAS_AGENT_ENGINE`（该变量已不再选择引擎） |
| `docs/content/docs/backend/backend-database.mdx` | 改 | 同步引擎开关删除与 worker 启用条件；补 `streaming`/`thinking` 能力字段 |
| `backend/internal/app/cloud_agent_context_budget.go` | 改 | 路由交集改为「逐条算可用输入容量取最小」 |
| `backend/internal/app/cloud_agent_context_budget_test.go` | 改 | 追加 4 个用例（交叉组合 / 未知窗口 / 顺序无关 / 失败关闭） |
| `backend/internal/app/model_capability.go` | 改 | `TextCapabilityConfig` 新增 `thinking`（缺省 = 未声明，不猜测） |
| `backend/internal/app/model_capability_test.go` | 改 | 追加 `thinking` 保留与 JSON 往返用例 |
| `web/src/components/model-capability-editor.tsx` | 改 | 管理端新增「思考 / 推理模式」开关 |
| `docs/content/docs/backend/http-api.mdx` | 改 | 补 `thinking` 合同；**修正**已失效的路由交集描述 |
| `docs/content/docs/progress/pending-test.mdx` | 改 | 追加三项待真实环境验收记录 |
| `backend/internal/app/provider.go` | 改 | `resolveProviderConfig` 附回选中渠道模型的权威能力 |
| `backend/internal/app/provider_capability_authority_test.go` | 新增 81 行 | 权威能力附回 + 损坏配置失败关闭 |
| `web/src/components/canvas/canvas-cloud-agent-panel.tsx` | 改 | **修复既有编译/运行阻断**：`ComposerControls` 缺 `reasoningMode` / `onReasoningModeChange` props |
| `backend/internal/app/cloud_agent_runtime.go` | 改 | `cloudAgentCall` 补齐 `type` / `thoughtSignature`（线格式契约） |
| `backend/internal/app/cloud_agent_pi_wire_contract_test.go` | 新增 83 行 | 复刻路由解码器，锁定 Node 生产方真实 body |
| `agent/src/bridge.ts` | 改 | 确定性 4xx 归类为 `FatalWorkerError`（不再无限重试） |
| `agent/test/bridge-wire-contract.test.ts` | 新增 112 行 | 线格式对拍 + 致命/可重试分类 |

工作树总体（忽略行尾差异）：**47 个文件，+3244 / −450**（起点为 34 个文件，+932 / −363）。
未提交改动与新增文件**全部保留**：全程未使用 `git reset`、`git checkout --` 或宽范围删除。

## 2. 逐阶段状态

### P0 固定基线与协议 —— ✅ 已实现 / 测试通过

交付物 [`PI_MIGRATION_P0_BASELINE_AND_CONTRACTS.md`](./PI_MIGRATION_P0_BASELINE_AND_CONTRACTS.md)。

| 退出条件 | 状态 | 证据 |
| --- | --- | --- |
| 保存 `git status`、HEAD、真实失败测试列表 | ✅ | 2410 项 / 34 文件 +932−363；7 个失败用例逐个命名 |
| 逐一核对 GAPS/Daily | ✅ | 13 行核对表，含 **3 处修正** |
| agent / Go / Web 三端合同 | ✅ | 12 个内部协议端点、10 个公开端点、25 类 SSE 事件 |
| 一张端到端时序图 | ✅ | §4 |
| 「一 run 一 session」与版本策略 | ✅ | ADR-P0-1/2/3 |
| 固定模型信封/事件金样本 | ✅ | §6（4 类样本） |
| 旧循环职责清单 | ✅ | §7（六个被调函数，四个必须保留） |

**核对中修正的历史记录**

1. `advanceCloudAgentByID` 实测 **77 处 / 22 文件**（GAPS 记 20 文件 / 75 处，偏小）。
2. Node 测试数为 **25**（Daily 正确；初稿据中间态镜像误判为 23，已按仓库内权威运行更正）。
3. `CANVAS_AGENT_ENGINE` 在**代码与 Compose 中仍然存在**（GAPS 称已移除）—— 已在本轮消除。

**真实失败基线（本轮实测，`CGO_ENABLED=1 go test ./internal/app/ -count=1`，600 s，FAIL）**

`TestCloudAgentRuntimeEmitsContextPressurePerStep`、`TestCloudAgentAutoMediaSubmitsWithoutApproval`、
`TestCloudAgentCanvasReadArgumentRepairContinuesRun`、`TestCloudAgentSkillMissingAndDistinctPaths`、
`TestCloudAgentEmptyCanvasRepairAcrossCheckpoints`（4 子用例）、
`TestPluginViewIncludesDocumentationForEveryOfficialProtocol`、`TestPurgeAssetsBatchSharedResourcesAndHistory`。
→ 与 GAPS 声称的"7 个既有失败"**完全一致**；归因（阶段 5.5 三分类）**未完成**。

### P1 SDK 可行性探针 —— ✅ 已实现 / 测试通过（**门槛通过**）

交付物 [`PI_MIGRATION_P1_SDK_EXTENSION_POINTS.md`](./PI_MIGRATION_P1_SDK_EXTENSION_POINTS.md) + 探针套件。

| 退出条件 | 状态 | 证据 |
| --- | --- | --- |
| 锁 `pi-coding-agent@0.87.1` 及依赖 | ✅ | `added 119 packages, 0 vulnerabilities`；锁文件解析到 0.87.1 |
| 真实 `createAgentSession` + in-memory `SessionManager` | ✅ | 9 个用例全部走真实 SDK，未 mock |
| 受控 `resourceLoader` | ✅ | `DefaultResourceLoader` + 内联扩展 |
| 仅 Canvas 工具 / 危险工具为零 | ✅ | `noTools="all"` 与 `"builtin"` 双模式断言，含负向断言 |
| 模型 → 母工具 → 子工具 → 结果 → 下一步 | ✅ | 三步请求全断言 |
| `agent_settled` / 压缩 hook / 导入导出 | ✅ | 各 1 个专项用例 |
| 扩展点 ADR | ✅ | 4 类扩展点 + 4 条否决路径 + 依赖树风险 |

**权威验证**：`cd agent && npm test` → **34/34 通过**（既有 25 + 探针 9，98.8 s）。

**对路线图的 4 处修正（均已写入 ADR）**

1. `PromptOptions` **没有 `signal`** —— 取消必须用 `session.abort()`；Provider 侧能通过 `options.signal` 观察到中止。
2. `entry_appended` **不是通用追加钩子**（只在边界提交时触发）→ P2 必须用 `getEntries()` 的 id 差集做差异持久化。
3. 声明自定义 `models` 时 **`baseUrl` 必填**，即使 Provider 从不发 HTTP → 用 RFC 2606 保留域名占位。
4. `SessionManager` 构造函数为 `private`，**不能继承覆写 `_persist`**。

**关键正面结论**：`SessionManager.inMemory(cwd, options, entries)` 是**公开方法**，能直接从数据库 entry 重建会话（leaf / context / entry-id 三重往返等价）→ **不需要 JSONL 适配层**，Go 数据库可以保持唯一权威。

**环境发现（非 SDK 缺陷，必须写进交接）**：`import @earendil-works/pi-coding-agent` 在 WSL 9p（`/mnt/d`）上 **75 624 ms**，在原生 ext4 上 **659 ms**（115×）。`exports` 映射禁止深路径导入，无法绕开 barrel。容器内冷启动正常；在 `/mnt/d` 直跑测试要多付约 95 s，不要误判为 SDK 不可用。

### P2 持久层 / 恢复 —— ❌ 未实现（前置已验证）

| 路线图要求 | 状态 |
| --- | --- |
| `cloud_agent_pi_sessions` / `_entries` / `_operations` 三表迁移 | ❌ 未开始 |
| 初始 system/Harness/schema 快照原子固定 | ❌ 未开始（Daily「未完成 #2」仍在） |
| lease epoch fencing | ⚠️ 部分存在（`piAgentLeasedRun` + revision CAS），未做跨进程故障验证 |
| 会话导入 / 恢复 / 旧 run 处置 | ⚠️ **入口已验证**（`inMemory(cwd, opts, entries)`），未接入 |
| 原子事件投影、多 run 并发与等待唤醒 | ❌ 未开始（`server.ts` 仍是单 run 串行 `while`） |

**可立即执行的第一步**：按 ADR-P0-1 建三表 + 迁移，恢复走 `SessionManager.inMemory(..., entries)`，持久化走 `getEntries()` id 差集（**不要**用 `entry_appended`）。

### P3 模型 / 计费 —— ⚠️ 部分实现

| 项 | 状态 | 证据 |
| --- | --- | --- |
| **并发结算 `[门槛]`** | ✅ **已实现并通过** | 见 §3 |
| Canvas Provider 经 Go 任务链 | ❌ 未开始 | `runner.ts` 仍构造 `pi-agent-core` 的 `Agent`；SDK 尚未进入生产路径 |
| 真实模型能力注入 | ❌ 未开始 | Node 仍写死 `contextWindow=1_000_000` / `maxTokens=32768`（HANDOFF P1 项） |
| 所有内部模型调用入账 / 硬预算前置 | ❌ 未开始 | `PiModelStep` 无等价前置流程 |

### P4 画布 / Harness / 上下文 —— ❌ 未实现

- SDK 资源装配、`setActiveToolsByName` 动态披露、压缩 sidecar、插话与续聊**均未接入**。
- P1 已证明**所有需要的扩展点都存在**，并给出驱动方式（初始活跃工具集必须在 `createAgentSession()` 后立刻设置；披露变更放在工具 `execute()` 内才无竞态）。
- 已知真实缺陷（来自同工作区的 [`HANDOFF.md`](./HANDOFF.md) P0 项，本轮**未修复、未验证**）：Pi 路径没有真正的上下文压缩 —— `recordCloudAgentTokenAnchor` / `cloudAgentRequestCompaction` 只从已删除的旧 Go 推进路径调用，而前端已把 85% 读数写成"下一次调用前会压缩"。

### P5 兼容与删旧驱动 —— ⚠️ 部分实现

| 项 | 状态 | 证据 |
| --- | --- | --- |
| 消除 `CANVAS_AGENT_ENGINE` 漂移 | ✅ **已完成** | 见 §4 |
| Web 无内部 Pi 协议依赖 | ✅ 已验证 | 全仓 `grep engine` 仅命中画布绘图引擎（excalidraw/tldraw） |
| `/api/agent` 与公开 SSE 合同保持 | ✅ 未改动 | 25 类事件逐条枚举 |
| 旧驱动引用清零 | ❌ 未开始 | `advanceCloudAgentByID` **77 处 / 22 文件**；`advanceCloudAgent(` **11 处 / 3 文件** |
| 7 个既有失败逐项归因 | ❌ 未开始 | 仅复现，未归入三分类 |

### P6 隔离验收与切换准备 —— ❌ 未开始

无隔离 Compose 运行、无 SQLite/PostgreSQL 双跑、无模型 stub 端到端、无浏览器验收、无故障注入、无回滚演练。
**本路线图不授权部署**，本轮未部署、未改生产环境。

## 3. P3 并发结算 `[门槛]`：实现与证据

**缺陷**（Daily §六.1 / GAPS 阶段 2.3）：`SettleBillingOrder` 先无条件读订单状态，再去更新账户；
两个连接可同时读到非终态并各自推进。账户侧的 `reserved_microcredits >= reserved` 守卫**只在账户恰好没有其他预留时才偶然挡住**第二次结算。

**修法**：把订单的**条件状态转移提升为本事务的第一次写入**，并作为唯一结算闸门：

```go
claim := tx.Model(&model.BillingOrder{}).
    Where("id = ? AND status NOT IN ?", order.ID,
        []model.BillingStatus{model.BillingStatusSettled, model.BillingStatusRefunded}).
    Updates(orderUpdates)          // 含全部结算字段
if claim.RowsAffected != 1 {
    return settleBillingClaimLost(tx, order.ID)   // 并发胜者幂等 no-op；refunded 保持显式错误
}
```

条件集合与修复前的接受集合一致（只拒绝 `settled`/`refunded`），因此不会把未知状态的历史订单从"可结算"变成"冲突"。
账户与流水仍在同一事务内，失败整体回滚。

**测试**（`finance_settlement_gate_test.go`，SQLite 文件库 + WAL + `_busy_timeout`，与生产 DSN 同构）

| 用例 | 断言 | 结果 |
| --- | --- | --- |
| `TestSettleBillingOrderIsExactlyOnceUnderConcurrentSettlement` | 两个真实连接同读非终态订单并发结算 → 预留只扣一次、consume 流水恰好 1 条、订单 settled、available 不变 | ✅ 通过，**loser 重试 0 次**（闸门干净串行化） |
| `TestSettleBillingOrderStaleSnapshotCounterProof` | **反证**：内联复刻修复前的写入顺序（订单状态最后写、无条件）→ 同一夹具下确实扣两次（预留归零、流水 2 条）；同一夹具走真实实现 → 只扣一次 | ✅ 通过 |
| `TestSettleBillingOrderKeepsExplicitRefundConflict` | 输给 `refunded` 的结算必须保持显式错误，不得被报成成功 | ✅ 通过 |

**回归**：`go test ./internal/repository/ -count=1` → ok；`-run TestSettleBillingOrder -count=20` → ok（无抖动）；
`go test ./internal/app -run 'Billing|Settle|Token|Video' -count=1` → ok（26.0 s）；`go build ./...` → 成功。

## 4. P5 `CANVAS_AGENT_ENGINE` 漂移：实现与证据

**漂移实质**：`CreateCloudAgentRun` 已固定 `engine=pi`，代码不再读该变量；但
`docker-compose.yml` 仍以 `${CANVAS_AGENT_ENGINE:-legacy}` 注入，`main.go` 仍用它决定"是否要求内部 token"。
由于默认值是 `legacy`，**那道启动校验永远不会触发** —— 一个跑 Pi 却没有 token 的部署会正常启动，
而 `/internal-agent` 因 token 为空而完全不挂载，于是每个 Cloud Agent 运行都停在 `queued` 且没有任何终态。

**修法**：删除该变量（compose + 启动校验），把可见性建立在**真实启用条件**上 —— token 缺失时打印明确提示；
同步更新 `docs/content/docs/backend/backend-database.mdx` 中"Pi 切换尚未完成，不代表可以启用 `CANVAS_AGENT_ENGINE=pi`"的过时表述。
选择"提示"而非"硬失败"，是为了不破坏未配置 worker 的既有部署编排（本轮不授权改生产行为）。

**验证**：`grep CANVAS_AGENT_ENGINE backend/ docker-compose*.yml .env.example` → 仅剩 `main.go` 中的解释性注释；
`docker-compose.yml` 解析正常（services: agent/backend/web，backend 环境仍有 token，不再有 engine）；
`go build ./...` 成功；`go test ./cmd/server/... -count=1` → ok（9.8 s）。

## 4.5 同工作区 `HANDOFF.md` 的 6 项新增待办：核对与进展

[`HANDOFF.md`](./HANDOFF.md)（另一协作者新增）列出 6 项合同断点。**6 项全部先用代码核对，全部成立**；本轮修复其中 2 项。

| # | 断点 | 核对结果 | 本轮状态 |
| --- | --- | --- | --- |
| 1 | 首步未使用 Node 最终提示（Go 创建 run 时即提交根模型任务） | ✅ 成立 | ❌ 未修复（大改，见 §5-2） |
| 2 | `web/src/lib/model-capabilities.ts` 用 `text.thinking`，Go `TextCapabilityConfig` 无该字段 | ✅ 成立：面板 `canvas-cloud-agent-panel.tsx:196/1512` 据 `text.thinking` 决定是否显示推理选项；Go 结构体确无该字段，且管理端编辑器也没有对应开关 → **推理选项对所有模型恒隐藏** | ✅ **已修复** |
| 3 | `resolveProviderConfig` 未补回选中渠道模型的权威文本能力 | ✅ 成立 | ✅ **已修复**（见 §4.6） |
| 4 | Pi 路径无真实上下文压缩（定锚/压缩只从旧推进路径调用） | ✅ 成立：`recordCloudAgentTokenAnchor` 在 `cloud_agent_runtime.go:1190`、`cloudAgentRequestCompaction` 在 `:1213`/`:1251`，均落在已废弃的旧状态机区间（917–1260） | ❌ 未修复 |
| 5 | `cloudAgentRouteIntersectionBudget` 分别取最小窗口与最小输出预留，可能高估最小窗口路由 | ✅ 成立（已复现具体数值） | ✅ **已修复** |
| 6 | Node `canvasModel()` 写死 1,000,000/32,768；压力兜底读 `state.Canonical.Messages` | ✅ 成立：`agent/src/runner.ts:22`；`cloud_agent_context_pressure.go:169/172` | ❌ 未修复（Node 侧属 P3 Provider 接入范围） |

### 4.5.1 第 5 项：路由交集高估（TDD：先复现再修）

**复现**（新增用例先跑在未修复代码上）：

```
cloud_agent_context_budget_test.go:128: input budget 475520 exceeds the smallest route's real capacity 427520
--- FAIL: TestCloudAgentRouteIntersectionBudgetUsesPerRouteInputCapacity
```

512k/64k 与 1M/16k 两条路由：分别取最小值得到 `512000−16000−20480 = 475520`，而 512k 线路实际只有 `512000−64000−20480 = 427520`。
按 475520 装配的请求落到 512k 路由上会被上游拒绝——正是"路由交集"要防的事。

**为什么既有测试没发现**：`TestCloudAgentContextBudgetUsesLogicalRouteSafeIntersection` 造的两条路由（1M/64k 与 512k/32k）里，
最小窗口与最小输出恰好落在**同一条**路由上，两种算法结果相同。新用例专门构造**交叉**组合。

**修法**：逐条路由算 `window − 输出预留 − overhead`，按输入预算取最小值；`minWindow` 只用于保留原有的"窗口过小即失败关闭"判据。

**结果**：新增 4 用例全过（交叉组合 / 未知窗口不参与 / 顺序无关 / 无可用 text 路由失败关闭），既有 5 个预算用例保持通过，`go build ./...` 成功。

### 4.5.2 第 2 项：渠道模型推理声明（三层打通）

**根因链**：前端类型有 `text.thinking` → 面板据它显示推理选项 → 但 Go 结构体没有该字段、管理端编辑器也没有开关
→ 字段永远不会出现 → `Boolean(undefined) === false` → **推理选项对每一个模型都隐藏**。

**改动三层**：

1. **Go 存储/合同**：`TextCapabilityConfig` 新增 `Thinking *bool`（`json:"thinking,omitempty"`）。
   **缺省语义是"未声明"，绝不默认 true**——把未声明当支持会让不支持推理的上游收到 reasoning 字段而报错。
   `NormalizeModelCapabilityConfigForModel` 做结构体拷贝，字段自动保留；校验函数不受影响。
2. **管理端**：`model-capability-editor.tsx` 的"协议"区新增「能力声明 → 思考 / 推理模式」开关，
   `supported={profile.thinking === true}`（与 SSE 那个默认打开的开关相反，这里默认关闭）。
3. **面板**：**未改动**——它原本就读 `text.thinking`，字段一通就生效。

**证据**：`go test ./internal/app -run TestNormalizeTextCapabilityPreservesThinkingMode` 通过，
覆盖「声明被规范化保留」「JSON 往返含 `"thinking":true`」「未声明保持 nil 且不落库」三个方向。

**验证状态更新**：该改动当时只有 esbuild 的 TSX **语法**解析（不构成前端验证）。
本轮补齐前端环境后已用 `bun run typecheck`（无输出）与 `bun run build`（`✓ built in 55.90s`）**真正验证通过**（见 §4.7）。
真实浏览器验收仍属 P6，未执行。



### 4.6 第 3 项：执行路径附回权威渠道模型能力（TDD：先复现再修）

**核对**：`task_creation.go:414` 已把客户端 `capabilityConfig` 剔除（正确的信任边界），
但 `provider.go::resolveProviderConfig` 在解析出 `channelModel` 后**没有把它补回**。
下游 `provider.go:407` 的判据是：

```go
supportsStream := input.Config.CapabilityConfig == nil || … || *input.Config.CapabilityConfig.Text.Streaming
```

`nil` 会落到 `true` —— 一个显式配置 `streaming: false` 的渠道模型仍会被发 `stream=true`；
同一分支还会丢掉模型物理输出上限（`CapabilityMaxOutputTokens`）。

**复现**（两个用例先跑在未修复代码上，均因正确原因失败）：

```
provider_capability_authority_test.go:43: authoritative capability was not attached to the resolved config
--- FAIL: TestResolveProviderConfigAttachesAuthoritativeCapability
provider_capability_authority_test.go:79: a corrupt capability config must fail closed, not degrade to nil
--- FAIL: TestResolveProviderConfigFailsClosedOnBrokenCapability
```

**修法**：解析出渠道模型后附上权威能力，并明确划出边界：

- **已声明能力**（`capability_config_json` 非空）→ 一律以 DB 为准；解析/规范化失败**失败关闭**
  （与 `task_creation.go:414` 同一策略），否则"损坏"会被当成"未声明"，正是上面 fail-open 的来源。
- **完全未声明**（空 JSON）→ 不改写，保持原行为。

**为什么保留"未声明不改写"**：第一版无条件失败关闭，立刻打挂了既有用例
`TestResolveProviderConfigMapsSKUToProviderModel`（视频模型、未声明能力）。这条不是"放宽断言"能解决的：
`resolveProviderConfig` 同时被**取消/恢复**路径复用（`provider_task_cancellation.go`、`provider_task_recovery.go`、
`task_media_recovery.go`、`task_worker.go`），对从未声明能力的旧记录在这里新增失败会把任务卡死；
而新建任务入口本就拒绝这类记录。另外 `NormalizeModelCapabilityConfigForModel` 对 text 能力会把
`Streaming` 缺省补成 `true`，所以**只要声明过能力，`streaming` 就一定是显式值** ——
本修复覆盖了所有会被真实创建出来的文本模型。

**验证**：4 个 `TestResolveProviderConfig*` 全过（含既有 SKU 用例）；
`./internal/app` 全量失败集合与基线**完全一致**（同样 7 个）；`./internal/handler`、`./internal/repository`、
`./cmd/server/...` 全绿；`go build ./...` 成功。

### 4.7 前端环境补齐，并修掉一个既有的编译/运行阻断

本轮按要求补齐前端环境：`bun 1.3.13`（`/home/a1/.bun/bin`）+ `web/node_modules`（1443 包，`bun install --frozen-lockfile`，699 s）。
未提交 `package-lock.json`，只用仓库既有的 `bun.lock`（符合根 `AGENTS.md` §7）。

**第一次真正跑 `bun run typecheck` 就发现工作树里有一个既有的前端构建阻断**（7 个错误，全部在
`web/src/components/canvas/canvas-cloud-agent-panel.tsx`，与我在 `model-capability-editor.tsx` 的改动无关）：

- 父组件在 `:1038` 向 `ComposerControls` 传 `reasoningMode` / `reasoningSupported` / `onReasoningModeChange`，
  但该子组件的 props 类型没声明这三个（`:1494`）；
- 子组件在 `:1533`/`:1539`/`:1542` 直接引用 `reasoningMode` 与 `onReasoningModeChange`，二者都不在其作用域内
  → **运行时会抛 `ReferenceError`，面板白屏**。

这与 Daily 记录的事故是**同一类缺陷**：当时修了 `reasoningSupported`（子组件内 `useMemo` 自行推导、失败退化为"不支持"），
但同一处的另外两个变量没补。工作树 diff 里能直接看到那次半成品修复的注释。

**修法**（最小、不推翻既有推导）：给 `ComposerControls` 补上 `reasoningMode` / `onReasoningModeChange` 两个 props 并使用；
调用点去掉子组件并不接收的 `reasoningSupported`（子组件保留自行推导，维持"能力查不到不崩面板"的既有取舍）。

**验证**：

| 命令 | 结果 |
| --- | --- |
| `cd web && bun run typecheck` | ✅ 无输出（修复前 7 个错误） |
| `cd web && bun run build` | ✅ `✓ built in 55.90s` |
| `cd web && bun test test/admin-ui-regressions.test.ts test/channel-model-editor-form.test.ts test/admin-logical-models.test.ts` | ✅ **34 pass / 0 fail** |

这同时把上一轮遗留的"前端改动未做类型检查/构建"从**未验证**升级为**已验证**：
`model-capability-editor.tsx` 的「思考 / 推理模式」开关随本次 `tsc --noEmit` 与 `vite build` 一起通过。


## 5. 未解决阻断与未验证项（按优先级）

| # | 项 | 类型 | 说明 |
| --- | --- | --- | --- |
| 1 | Pi v3 entry/operation 持久层（P2） | **阻断** | 三表 + 迁移未实现；`piToolReceipt` 仍按 `callId` 在运行态扫描，未改独立操作账本 |
| 2 | 首步提示合同不一致（HANDOFF P0-1） | **阻断** | Go 在创建 run 时提交根模型任务，Node 领取后才装配 Harness → 首步未使用 Node 最终提示；且 `SYSTEM_POLICY.md`/`MEDIA_POLICY.md` 与 Go 已编译策略可能重复 |
| 3 | Pi 路径无真实上下文压缩（HANDOFF P0-4） | **阻断** | `recordCloudAgentTokenAnchor` / `cloudAgentRequestCompaction` 只从已删除的旧推进路径调用；前端文案已声称会压缩 |
| 4 | 真实浏览器验收（面板渲染 / 刷新 / 断线续传） | **未验证** | 类型检查、构建、专项测试与 API 级端到端均已过（§4.7、§9）；无浏览器自动化环境，未做真实页面验收 |
| 5 | 旧驱动引用清零（P5） | 未完成 | `advanceCloudAgentByID` 77 处 / 22 文件；`advanceCloudAgent(` 11 处 / 3 文件 |
| 6 | 7 个既有失败归因（P5） | 未完成 | 已复现并逐个命名，未归入三分类，**未使用 skip 或放宽断言** |
| 7 | 硬预算前置 / 媒体收尾 / 拒绝取消终态跨进程验证 | 未完成 | P3/P4 门槛项 |
| 8 | PostgreSQL 一侧 + 故障注入（P6） | 部分 | 本地 SQLite 编排与真实端到端已验（§9）；Postgres、提交窗口崩溃、双 worker 抢租约、媒体收尾均未执行 |
| 9 | Node 侧模型能力占位（HANDOFF P1-6） | 未完成 | `agent/src/runner.ts:22` 仍写死 1,000,000 / 32,768；属 P3 Provider 接入范围 |
| 10 | 双模块实例风险 | 已记录 | `pi-ai` / `pi-agent-core` 各存在顶层与 SDK 内嵌两份（同版本 0.87.1）。探针证明跨实例可用；约定见 ADR §3.6 |
| 11 | `/mnt/d` 上 SDK 导入慢 115× | 环境 | 需在原生 FS 或容器内跑 SDK 测试，否则会被误判为冷启动问题 |

## 6. 复现命令

```bash
# P0 基线
cd backend && CGO_ENABLED=1 go test ./internal/app/ -count=1        # 7 个既有失败（600s）

# P1 探针（权威）
cd agent && npm test                                                # 34/34（98.8s，含 ~75s 导入开销）

# P3 并发结算门槛
cd backend && go build ./...
CGO_ENABLED=1 go test ./internal/repository/ -run TestSettleBillingOrder -count=20 -v
CGO_ENABLED=1 go test ./internal/repository/ -count=1
CGO_ENABLED=1 go test ./internal/app -run 'Billing|Settle|Token|Video' -count=1

# P5 漂移
grep -rn CANVAS_AGENT_ENGINE backend/ docker-compose*.yml .env.example
CGO_ENABLED=1 go test ./cmd/server/... -count=1

# HANDOFF 第 5 项：路由交集输入预算（4 个新用例）
CGO_ENABLED=1 go test ./internal/app -run 'TestCloudAgentRouteIntersectionBudget|TestCloudAgentContextBudget' -count=1 -v

# HANDOFF 第 2 项：渠道模型 thinking 能力声明
CGO_ENABLED=1 go test ./internal/app -run TestNormalizeTextCapabilityPreservesThinkingMode -count=1 -v

# HANDOFF 第 3 项：执行路径权威能力（2 个新用例 + 既有 SKU 用例）
CGO_ENABLED=1 go test ./internal/app -run 'TestResolveProviderConfig' -count=1 -v

# 其他后端包
CGO_ENABLED=1 go test ./internal/handler/ ./internal/repository/ ./cmd/server/... -count=1
```

前端（环境：`export PATH=/home/a1/.bun/bin:$PATH`；依赖用 `bun install --frozen-lockfile` 装，禁 npm/pnpm）：

```bash
cd web && bun run typecheck      # 无输出
cd web && bun run build          # ✓ built in 55.90s
cd web && bun test test/admin-ui-regressions.test.ts test/channel-model-editor-form.test.ts test/admin-logical-models.test.ts
                                 # 34 pass / 0 fail
```

Go 需要显式 PATH：`export PATH=/home/a1/.local-go/go/bin:$PATH`（`go1.25.1` 不在默认 PATH）。

## 7. 共享工作区提示

本轮执行期间，同一工作树出现了**其他协作者新增的** `agent/HANDOFF.md`（时间戳晚于本次开工，内容引用了本报告作者新写的 P0/P1 两份文档）。
本轮**未修改、未删除**该文件，只把它列为额外需求来源（§5 第 2–3 项即来自它的 P0 发现）。
继续工作前请重新执行 `git status --short`，以最新工作树为准。

**需要留意的交叉改动**：本轮为了修掉前端构建阻断，改了
`web/src/components/canvas/canvas-cloud-agent-panel.tsx` —— 该文件含**其他协作者未提交的改动**（含那次半成品修复的注释）。
改动是**追加式**的：只补 `ComposerControls` 缺失的两个 props 类型与解构，调用点去掉子组件不接收的 `reasoningSupported`，
未重写他人的逻辑，也没删除那条解释性注释。若该协作者同时在编辑此文件，合并时请以类型检查与构建为准（两项目前均通过）。

## 8. 明确未做

- **未触及生产环境**：未构建或推送任何生产镜像，未改动 `docker-compose.deploy.yml` 或任何生产配置。
  本地 3000 canary 编排的滚动与验收见 §9（该编排明确不是生产）。
- **未启用** Pi 生产路径：`agent/src/runner.ts` 仍使用 `pi-agent-core` 的 `Agent` 构造流程；SDK 目前只被探针使用。
- **未做**数据库迁移，未改动任何表结构。
- **未用** skip、放宽断言或恢复旧调度器让任何失败测试变绿。

## 8.5 复现本轮部署验收

```bash
export PATH=/home/a1/.bun/bin:$PATH   # 前端；Go 用 /home/a1/.local-go/go/bin

# 滚动（本地 canary，非生产）
cd /mnt/d/13537/open-ai-canvas-canary
docker compose --env-file .env.pi -f docker-compose.yml -f docker-compose.local.yml \
  -p canvas-canary-3000 --profile pi build backend web agent
docker compose --env-file .env.pi -f docker-compose.yml -f docker-compose.local.yml \
  -p canvas-canary-3000 --profile pi up -d

# 健康与版本
curl -s http://127.0.0.1:3000/api/health
# 部署产物核对（应各自命中 1 个 chunk）
docker exec canvas-canary-3000-web-1 grep -rl '思考 / 推理模式' /usr/share/nginx/html/assets/
docker exec canvas-canary-3000-web-1 sh -c "grep -rl '深入推理' /usr/share/nginx/html/assets/ | wc -l"   # 1

# 两个缺陷的回归测试
cd backend && CGO_ENABLED=1 go test ./internal/app -run 'TestPiToolBatchAccepts' -count=1 -v
cd agent   && npm test        # 38/38（含 bridge-wire-contract 的 4 个用例）
```


## 9. 3000 本地部署与端到端验收（本轮）

**边界**：3000 是本机 canary 编排（`canvas-canary-3000`，web + backend + agent 三容器），
**不是生产**；生产编排是 `docker-compose.deploy.yml`，本轮完全未触碰。
未构建/未推送任何生产镜像，未改动任何生产配置。

### 9.1 编排与滚动

既有栈在 `05:32` 前已运行（三容器 healthy）。按仓库既有方式滚动：

```bash
docker compose --env-file .env.pi -f docker-compose.yml -f docker-compose.local.yml \
  -p canvas-canary-3000 --profile pi build backend web   # 第一轮
docker compose ... --profile pi up -d
# 修完 §9.3/§9.4 两个缺陷后
docker compose ... --profile pi build backend agent     # 第二轮
docker compose ... --profile pi up -d
```

| 容器 | 滚动前 image id | 滚动后 image id |
| --- | --- | --- |
| backend | `84df5cbb…` | `29a5bfbf…` |
| web | `e05048f5…` | `b904eb36…` |
| agent | `8555c6b4…` | `66c087fe…` |

三容器均 healthy；`GET /api/health` → `ready:true`、`schema 41/41 ready`、`database/runtime/schema` 全 true；
`GET /` → 200。后端启动日志**没有** `CANVAS_AGENT_INTERNAL_TOKEN` 告警 —— 与 §4 的改动一致（token 已配置时不告警）。

### 9.2 部署产物核对（Daily 陈旧 chunk 事故的对照）

| 检查 | 结果 |
| --- | --- |
| `assets/` 文件总数 | 1550 |
| 含本轮新增编辑器文案 `思考 / 推理模式` 的 chunk | `model-capability-editor-BHohi7XP.js`（HTTP 200） |
| 含面板推理文案 `深入推理` 的 chunk | `project-BUafExI3.js`（HTTP 200） |
| 含 `深入推理` 的 chunk 数 | **1**（Daily 事故当时是 3 个新旧混杂版本） |

即：镜像确实由本轮源码构建，且没有陈旧的重复面板 chunk。

> 注意：镜像内的 `vite build` **不跑 `tsc`**（`bun run build` 才跑），所以镜像构建成功不等于类型正确；
> 类型证据来自 §4.7 的 `bun run typecheck`。

### 9.3 缺陷 A（阻断）：工具批次被 400 拒绝，运行永远停在 running

第一次真实端到端运行（`ag3d38a9…`）**卡死在 `running`**：events 冻结在 75，revision 每 ~75s 缓慢 +1。

现场证据：

```
agent 日志:  Pi worker run failed: Canvas bridge HTTP 400 on POST /runs/ag3d38a9…/tool-batches   （反复）
backend 日志: POST /internal-agent/runs/ag3d38a9…/tool-batches 400 43.805µs   ← ~50µs 空响应体
```

~50µs 的空 400 说明请求**在任何业务逻辑之前**就被拒 —— 是 JSON 解码器，不是业务校验。

**根因**：`/tool-batches` 路由用 `decoder.DisallowUnknownFields()`，而 Go 的 `cloudAgentCall` 只声明了
`id` / `function`；Node 侧 `PiToolCall` 由 `callsFromAssistant()` 构造时**始终**带 `type:"function"`
（可选还有 `thoughtSignature`）→ 未知字段 → 整批 400。

**为什么既有测试没发现**：`agent/test/runner.test.ts` 用**假 bridge**（不经 HTTP/JSON），
`cloud_agent_pi_bridge_test.go` 直接调 service（不过 handle 的解码器）。
**没有任何测试覆盖真实的跨进程线格式** —— 这正是路线图 §6.4 要求而此前缺失的那类测试。

**修法**：`cloudAgentCall` 补齐生产方实际发送的字段（`type` 与 `thoughtSignature`，均 `omitempty`，
与 `agent/src/bridge.ts` 的 `PiToolCall` 对齐）。

**测试**（`backend/internal/app/cloud_agent_pi_wire_contract_test.go`，复刻路由的解码器配置
`DisallowUnknownFields` + 尾部 token 检查，并用生产方真实的 body）：

| 用例 | 修复前 | 修复后 |
| --- | --- | --- |
| `TestPiToolBatchAcceptsTheNodeProducerShape` | 编译失败（字段不存在）→ 证明契约缺口 | ✅ 通过 |
| `TestPiToolBatchAcceptsThoughtSignature` | 同上 | ✅ 通过 |

**并有 Node 侧对拍**（`agent/test/bridge-wire-contract.test.ts`）：断言 `startToolBatch` 发出的
call 键集合恰为 Go 声明的子集，防止生产方再次单方面加字段。

### 9.4 缺陷 B（结构性）：确定性错误无限重试，绕过租约与看门狗

即使 400 本身被修，这个缺陷会让**任何**未来的契约不匹配重新变成"运行永远卡住"。

**根因**：`server.ts` 只对 `FatalWorkerError` 调 `bridge.failRun`；HTTP 400 是普通 `Error`，
于是外层循环只打日志、等 3s、**重新领取同一个 run**。而 `runCanvasAgent` 在每轮里持续续租
→ 租约永不过期 → 依赖 `lease_expires_at` 超时的停滞看门狗（`SweepStalledPiAgentRuns`）**永不触发**。
现象与 Daily 记录的 `rev=79`、以及本轮观测到的 `rev=77→81` 完全一致（本轮的卡死运行最终 `rev=86`）。

**修法**：在 `bridge.ts` 里把**确定性** 4xx 分类为 `FatalWorkerError`（400/401/403/404/405/406/410/413/414/415/422），
408/409/425/429 与 5xx、网络错误保持可重试。这样运行会被显式写成 `failed` 并带原因，而不是静默卡住。

**测试**（`agent/test/bridge-wire-contract.test.ts`）：确定性 4xx 必须 `name === "FatalWorkerError"`；
408/409/429/500/502/503 必须保持可重试。4 个用例全过。

### 9.5 端到端复验（修复后）

滚动后新建一次 read_only 运行（`ag295e3a…`）：

| 观测 | 值 |
| --- | --- |
| 终态 | **`completed`** |
| revision / events | 28 / 20 |
| 事件类型 | `context_pressure`×3、`reasoning_delta`×8、`reasoning_message`×3、**`tool_completed`×2**、`assistant_delta`×2、`assistant_message`×2 |
| 工具链路 | `agent_tools_control` 执行成功 → 返回该母类型下的工具清单 → 模型调用 `finish_run` → `assistant_message{final:true}` |
| 耗时 | 约 30 s（轮询 6 次 × 5 s） |

**修复前**同一路径在 `tool-batches` 处无限 400；**修复后**工具批次被接受并执行。

### 9.6 终态对账

| 指标 | 值 |
| --- | --- |
| agent 运行 | `completed` 11、`cancelled` 5、**非终态 0** |
| 账单 | `settled` 18、**悬挂订单 0** |
| agent 容器日志 | 无新增 `tool-batches` 400 |

### 9.7 本轮在本地库里新增的数据（可清理）

为做端到端验收，在**本地** 3000 数据库中**追加**了（未修改任何既有行）：

- 一个测试用户 `deploytest`（id `64f5daa9fe2520dc2675e8de82e9ca8b`，role=user）
  —— 注册接口关闭（email 注册未启用、域名白名单），因此直接用 bcrypt 生成口令并 INSERT；
- 一张画布 `deploytest-canvas-1790459073`；
- 两次 agent 运行（`ag3d38a9…` 已取消、`ag295e3a…` 已完成）与其账单。

既有用户 `admin` / `test` 及其数据**未被触碰**。如需清理，删除上述 user/canvas/run 行即可。

### 9.8 仍未验证

- **未做真实浏览器验收**：无浏览器自动化环境；面板渲染、刷新、断线续传只能靠 API + 静态产物证据。
- **未做 Postgres 一侧**：本地栈是 SQLite。
- **未做故障注入**：提交窗口崩溃、双 worker 抢租约、媒体收尾均未执行（P6）。

---

# 阶段 1 执行记录：正确性门槛（本轮）

> 起点：`canary`，HEAD `7aa99880`。本轮只做用户指定的第 1 阶段，不跳阶段。

## 1.1 已实现项

| 编号 | 事项 | 改动 | 文件 |
| --- | --- | --- | --- |
| M-08 | `PiFailModelStep` 终态守卫 | 新增 `cloudAgentRunTerminal` 幂等分支；"成功任务不得被报失败"的不变量提到最前 | `cloud_agent_pi_bridge.go:373-421` |
| M-09 | 看门狗置 `CleanupPending` | 新增共用的 `sweepPiRuns`，终结时同时置 `CleanupPending` | `cloud_agent_pi_bridge.go` `SweepStalledPiAgentRuns` / `sweepPiRuns` |
| M-10 | 未领取 run 的排队语义 | `StalledPiAgentRuns` 加 `lease_expires_at IS NOT NULL`；新增 `UnclaimedPiAgentRuns` + `SweepUnclaimedPiAgentRuns`（30 分钟阈值、`pi_worker_unavailable`）；worker ticker 一起跑 | `repository/cloud_agent.go`、`cloud_agent_pi_bridge.go`、`task_worker.go` |
| M-11 | 看图后下一步必然失败 | **确认是真缺陷，且是迁移回归**；补回 `cloudAgentImageReferences` 与 `input["referenceImages"]` | `cloud_agent_pi_bridge.go` `PiModelStep` |

### M-08 细节：为什么必须返回 `nil` 而不是错误

`run.Status` 已终态（completed/cancelled/rejected）而 worker 的 `/fail` 仍在途，是**竞态的正常结果**。
`bridge.ts` 会把确定性 4xx 归类为 `FatalWorkerError` 并整轮退出 —— 对一次已经正确终结的运行回错误，
等于把正常收尾变成 worker 报错。所以终态分支返回 `nil`，但"任务已成功"的协议不变量仍然排在它前面、
照旧返回 403。

### M-10 细节：为什么不改 `ClaimPiAgent`

修"未领取被误杀"时唯一的诱惑是把 claim 谓词里的 `lease_expires_at IS NULL` 也删掉 —— 那会让
**首次领取永远不可能发生**。`TestClaimPiAgentStillClaimsNeverLeasedRun` 专门锁死这一点。

同时保留了 `queued` 语义的诚实性：前端把 `queued` 与 `running` 渲染成同一种"运行中"
（`canvas-cloud-agent-panel.tsx:181`），所以"永远排队"就等于重现"永远显示运行中" ——
那正是看门狗存在的理由。因此超时后给的是**明确终态 + 独立原因**，而不是静默排队。

### M-11 细节：三段证据链 + 旧路径对照

1. `cloudAgentReference` 要求节点 `metadata.storageKey` 以 `resource:` 开头
   （`cloud_agent_media.go:252-255`），并把同一个 key 作为 `reference["storageKey"]` 返回（`:263`）；
2. `cloudAgentImageInspection.ImageURL = reference["storageKey"]`（`cloud_agent_vision.go:261`），
   `cloudAgentImageContentParts` 写成 `image_url.url`（`:897`）；
3. `cloudAgentFlushPendingImages` 把这条消息追加进 `state.Canonical.Messages`（`:946`），
   其调用者包含 `advanceCloudAgentTool`（`cloud_agent_runtime.go:1598`）—— **Pi 路径可达**。

于是下一个 `PiModelStep` 送出的 `agentRequests.canonical` 含 `resource:<id>`，而 `PiModelStep`
从不设置 `referenceImages`（grep 为空）→ `resolveAgentResourcePlaceholders`
（`provider.go:488`）找不到白名单条目 → `BadAuthRequest("模型协议引用了未获准的图片")`。

**旧路径是对照组**：`cloud_agent_runtime.go:1238` 调 `cloudAgentImageReferences`、`:1251-1252` 写
`input["referenceImages"]` —— 两者都在已废弃区间（933–1272）内，dev 分支同样有这两行。
**所以这是迁移时漏接的线，不是既有缺陷。**

补线位置与旧路径一致（装配 canonical 之后、发请求之前），并顺带接回"超上限的旧图换成文字占位"的语义。

**调用顺序已被核对**（这是修复成立的前提）：

```
provider.go:471  hydrateGenerationMedia      读资源字节 → media.DataURL
provider.go:488  resolveAgentResourcePlaceholders  把 resource: 换成 data URL
```

`hydrateGenerationMedia` 的 `provider.go:786` 分支正是为 Agent 图片写的
（"仅 Agent 图片走内存字节"），并且它读 `input.Config.CapabilityConfig.Text.References`
—— 也就是本轮早前修好的"权威能力附回"（`provider.go` 的 `resolveProviderConfig`）。
两处修复是互补的：没有权威能力，水合拿不到 MaxImages/MaxImageBytes。

## 1.2 实际测试命令与结果

```bash
cd backend && export PATH=/home/a1/.local-go/go/bin:$PATH
go build ./...                                                  # OK
CGO_ENABLED=1 go test ./internal/app -run 'TestPiFailModelStep|TestSweep|TestClaimPiAgent|TestPiToolAdvance' -count=1
CGO_ENABLED=1 go test ./internal/app -run 'TestPiImageReferences|TestPiModelStepWiring|TestPiModelStepMustPass|TestPiVisionNextStep|TestAgentImageReference' -count=1
CGO_ENABLED=1 go test ./internal/repository/ ./internal/handler/ ./cmd/server/... -count=1
```

| 范围 | 结果 |
| --- | --- |
| `internal/repository` | ✅ ok（0.9s） |
| `internal/handler` | ✅ ok（26.9s） |
| `cmd/server` | ✅ ok（10.8s） |
| `internal/app` 全量 | 见下方"回归"一行；失败集合与基线一致 |

新增测试 **13 个**：

- `cloud_agent_pi_lifecycle_gates_test.go`：终态保持（cancelled/rejected/completed 三个子用例）、
  成功任务在终态运行上仍被拒、看门狗置 `CleanupPending`、stalled 忽略从未领取、
  未领取超时给出 `pi_worker_unavailable`、未领取新运行不误杀、claim 仍能领取 NULL 租约。
- `cloud_agent_pi_vision_placeholder_test.go`：`resource:` 占位符导致下一步被拒（**最小复现**）、
  画布图片只能以 `resource:` 形态下发。
- `cloud_agent_pi_vision_wiring_test.go`：水合产出正确白名单并能通过占位符准入、
  超上限裁剪为文字占位且仍可通过、不可用资源失败关闭、`referenceImages` 为空的后果对照。

> 保留既有用例不动：`TestSweepStalledPiAgentRunsTerminatesAbandonedRun`（只设 `lease_expires_at`，
> 属于"领取后失联"）与 `...LeavesActiveRunAlone` 在新谓词下继续通过。

## 1.3 仍未验证项

| 项 | 说明 |
| --- | --- |
| 看图全链路真实运行 | 本地栈**没有任何渠道模型声明图片输入能力**，且 17 个历史运行里 `"image_url"` 出现 **0 次** —— 图片从未真正进入过模型上下文。本轮只证到"装配与水合正确"，未做一次真实看图运行 |
| 旧循环里另外三处窄入口 | `attachCloudAgentLessons`（每步重挂）、`deliveredImageNodeIDs` + `cloudAgentSettleImageDelivery`（观察账本）、`ForceThinkingOff`/`BoostStepOutputBudget`（重试阶梯）仍未接 —— 属 M-04/M-05/M-07，Stage 5 |
| 阶段 2–7 全部 | 首步合同、Pi v3 持久层与 fencing、Provider 链、业务功能、压缩、删旧驱动 |

## 1.4 当前 Git 差异（工作树，未提交）

工作树总体（忽略行尾差异）：**47 文件 `+3341 / −454`，33 个未跟踪文件**
（阶段 1 之前为 `+3244 / −450`）。

阶段 1 自身：

| 文件 | 变化 |
| --- | --- |
| `backend/internal/app/cloud_agent_pi_bridge.go` | +270 − 部分（终态守卫、`sweepPiRuns`、`SweepUnclaimedPiAgentRuns`、`PiModelStep` 图片水合） |
| `backend/internal/app/task_worker.go` | +23 −（60s ticker 同时跑未领取清扫） |
| `backend/internal/repository/cloud_agent.go` | +33（`StalledPiAgentRuns` 谓词收紧 + 新增 `UnclaimedPiAgentRuns`） |
| `cloud_agent_pi_lifecycle_gates_test.go` | 新增 230 行 |
| `cloud_agent_pi_vision_placeholder_test.go` | 新增 94 行 |
| `cloud_agent_pi_vision_wiring_test.go` | 新增 190 行 |

**未覆盖、未回滚、未清理任何既有改动**：全程未使用 `git reset` / `git checkout --` / 宽范围删除。

## 1.5 下一阶段入口（Stage 2：统一首步合同）

现状：`CreateCloudAgentRun` 在建 run 时就提交**根模型任务**，而 Node 领取后才装配 Harness；
`PiModelStep` 见 `state.ActiveTaskID` 非空即短路返回该任务视图 → **首步用的是 Go 编译的提示，
不是 Node 装配的提示**。阶段 2 的入口就是这条短路：

- 需要先定：由 Node 发起首步（Go 只创建 run + 预授权），还是让根任务使用与 Node 完全一致的
  版本化提示制品；
- 不可变快照要覆盖 prompt / Harness / 工具 schema，并且恢复时不得重读磁盘上的新版 Harness；
- 强制服务端策略不能被 `SYSTEM.md` 替换（`agent/src/system-prompt.ts` 的装配顺序要复核）。

## 1.6 阶段 1 部署记录（本地 3000 canary，非生产）

**边界**：部署目标是 `canvas-canary-3000`（`docker-compose.yml` + `docker-compose.local.yml`），
**不是生产**。生产编排 `docker-compose.deploy.yml` 未触碰，未推送任何镜像。

### 编排与滚动

```bash
docker compose --env-file .env.pi -f docker-compose.yml -f docker-compose.local.yml \
  -p canvas-canary-3000 build backend
docker compose --env-file .env.pi -f docker-compose.yml -f docker-compose.local.yml \
  -p canvas-canary-3000 --profile pi up -d
```

阶段 1 只改了 Go，所以只重建 backend；web 与 agent 镜像未变、容器未重建。

| 容器 | 滚动前 image id | 滚动后 image id |
| --- | --- | --- |
| backend | `29a5bfbf…` | **`79154206…`** |
| web | `b904eb36…` | `b904eb36…`（未变） |
| agent | `66c087fe…` | `66c087fe…`（未变） |

### 验证结果

| 检查 | 结果 |
| --- | --- |
| 三容器 | ✅ 全部 healthy |
| `GET /api/health` | ✅ `ready:true`、schema 41/41、database/runtime/schema 全 true |
| `GET /` | ✅ 200 |
| 后端启动日志 | ✅ 仅迁移探测与健康检查，无错误 |
| **真实 Agent 运行** | ✅ `agc2af10b…` → **`completed`**，43 事件、2 次 `tool_completed`，约 40s |
| **内部协议成功率** | ✅ 重启后 `internal-agent` 请求 **0 条非 200** |
| 终态对账 | ✅ 非终态运行 **0**、悬挂账单 **0**、`cleanup_pending` 残留 **0** |

**为什么要跑真实运行**：`PiModelStep` 是每次模型步的必经路径，本轮往里加了图片水合，
所以必须证明它没有影响无图路径。已核对 `cloudAgentImageReferences` 在
`count == 0` 时直接 `return nil, nil`（`cloud_agent_vision.go:663-665`），
不会触碰图片能力配置；真实运行通过即是该结论的端到端证据。

### 回滚窗口观察（顺带验证了失联恢复）

backend 重建期间（容器 07:59:49 启动、应用约 08:00:36 就绪），agent 日志出现 8 条
`Pi worker run failed: fetch failed`，**在 08:00:36 之后完全停止**，worker 自行恢复，
无需重启。这与 `server.ts` 的设计一致（"leave the lease to expire"），
也说明重建窗口不会让运行卡死 —— 终态对账里非终态运行数为 0 正是这一点的证据。

### 本轮部署未做的事

- 未做 PostgreSQL 一侧（本地栈是 SQLite）。
- 未做故障注入（双 worker 抢租约、提交窗口崩溃、媒体收尾）——属阶段 6。
- 未做浏览器端验收；本次只有 API 级端到端。
- **看图全链路仍未真实跑过**：本地渠道模型没有声明图片输入能力，所以 M-11 的修复
  只有单测与代码级证据，没有真实看图运行。

---

# 阶段 2 设计记录：统一首步合同（进行中）

## 2.1 现状（已核对，含行号）

| 事实 | 证据 |
| --- | --- |
| 建 run 时 Go 就提交**根模型任务** | `cloud_agent.go:514-533`：`Operation="cloud_agent"`、`admission.ID=runID`、`MaxCharge=floor(MaxCredits*CreditScale)`，input 带 `textHistory` + `agentRequests.canonical`（Go 用 `compileCloudAgentPolicies` 编译的提示） |
| Go worker 会**立刻执行**它 | `ClaimNextTask`（`repository/repository.go:359-376`）谓词只看 status/lease/next_poll_at，**不看 operation** |
| Node 因此拿不到首步 | `PiModelStep` 第一件事 `if state.ActiveTaskID != "" { return PiModelStepView(...) }`（`cloud_agent_pi_bridge.go:288-304`）—— 领取时 ActiveTaskID 就是根任务 |
| 预留与任务同事务 | `CreateTask` → `CreateTaskWithCreditReservation`（`repository/finance.go:440-453`） |

**结论**：首步用的提示是 Go 编译的那份，"Node 装配的 Harness 对首步不生效"这点成立。

## 2.2 预算语义（这条决定了 Q1 怎么选）

核对后确认**当前没有"已预留被重复预留"**：

- `taskAdmission.MaxCharge` 是**上限校验**，不是预留额：
  `if billingOrder.AmountMicrocredits > req.admission.MaxCharge { 拒绝 }`（`task_creation.go:143-145`）；
- 真正的预留额是**报价** `billingOrder.AmountMicrocredits`；
- 步进预算 `remaining` 是"总额 − 本 run 各任务报价之和"：
  `remaining = floor(MaxCredits*CreditScale); for order := range BillingOrdersByTaskIDs(state.TaskIDs) { remaining -= order.AmountMicrocredits }`
  （`cloud_agent_runtime.go:2288-2296`）。

**由此得到阶段 2 的硬约束**：若把 run 创建时的预留改成"不建任务的 run 级预留"（Q1 的 (b)），
它**不在 `state.TaskIDs` 里**，`remaining` 就不会减掉它 → 一个 run 可以花掉"预算 + 预授权"两份额度。
选 (b) 必须同时改 `enqueueCloudAgentTask` 的记账口径。
反过来，若保留一个承载预留的任务行（Q1 的 (a)），报价自动进入 `TaskIDs`，`remaining` 天然正确。

## 2.3 Q3 已实施：服务端策略不可被 `SYSTEM.md` 替换

**改动**：`agent/src/system-prompt.ts` 的 `renderSystemPrompt`。原来是
`const prefix = parts.system !== undefined ? parts.system : base` —— 工作区放一个 `SYSTEM.md`
就能整段顶掉服务端策略（工具权限、能力边界、安全规则都在策略里）。现在服务端策略**永远在最前**，
`SYSTEM.md` 只能追加自己的工作区规则；只有 `base` 为空时 `SYSTEM.md` 才单独充当系统提示。

**测试变更（显式记录，不是放宽断言）**：`test/system-prompt.test.ts` 原第 12 条断言的是**旧语义**
（"SYSTEM.md 替换服务端前缀"且 `!rendered.includes("服务端策略前缀")`）。新合同与该断言直接冲突，
因此**按需求改写**为正向断言："策略仍在最前 + SYSTEM.md 内容仍在 + 顺序正确"，并新增一条
"没有服务端策略时 SYSTEM.md 单独生效"。旧断言的删除是需求变更的结果，已在此留痕。

结果：`node --test dist/test/system-prompt.test.js` → **9/9 通过**。

**部署影响**：当前 `agent/harness/` 里**没有** `SYSTEM.md`（只有 `SYSTEM_POLICY.md` 等），
所以线上行为不变；这次改的是"有人放 SYSTEM.md 时会怎样"。agent 镜像需要重建才能带上该修复。

## 2.4 待外部评审确认

已把现状与四个问题（Q1 预授权承载、Q2 `state.TaskIDs` 不变量、Q3 提示合成、Q4 迁移顺序）
提交 Codex（`gpt-6-sol`，read-only）评审，**尚未收到结论**。收到后按结论调整 Q1/Q2/Q4 的实现方案再动代码。

## 2.5 Codex 设计评审结论（`gpt-6-sol`，read-only）

评审对象是阶段 2 的四个问题。**注意**：Codex 读的是它本机的另一份 Canvas 检出
（`C:/Users/13537/Documents/ChatGPT/open-ai canvas/canary`），行号可能与工作树不一致；
它的**机制结论**与我的独立核对一致，具体行号我在本树复核过（见下）。

### 已在本树复核的 Codex 结论

| Codex 结论 | 本树复核 |
| --- | --- |
| `MaxCharge` 只是**报价上限**，真正预留的是计费订单的 `AmountMicrocredits` | ✅ 一致（`task_creation.go:143-145`）。**这条同时纠正了我的一个措辞**：不存在"建 run 时已锁住整轮预算"这回事 |
| 运行状态里有"任务历史不能为空"的校验 | ✅ `cloud_agent_runtime.go:517` `if len(state.TaskIDs) == 0` |
| 本地实现**直接以 runID 查根 `Task`**，移除根任务后这些入口必须改为以 run 为权威 | ✅ `cloud_agent.go:251-267`：`cloudAgentTask` 用 `s.repo.TaskForUser(userID, id)`（id 即 runID）并要求 `task.Operation == cloudAgentOperation`。**这条是本阶段最大的一处结构耦合** |

### Q1 预授权：结论选 (b)，但先纠正预算前提

- **(a) 占位任务：不推荐**。只换 `operation` 挡不住 `ClaimNextTask`；改成不可领取状态又会把
  **财务凭证伪装成可重试、可取消的任务**。
- **(c) 复用根任务：不推荐**。可执行任务一经提交就有领取竞态；要消除就得新增"待激活"生命周期，
  等于绕一圈还是在做 run 级承载。
- **(b) run 级预留：推荐**。与 run、不可变快照**同一事务**提交；每个 `PiModelStep` 仍创建真实任务与订单，
  但报价从 run 池分配，**不再走"从 available 扣款"的老路**；步骤结算只消耗实际费用，未用额度留在池中，
  终态一次性释放。不变量：

  ```
  已结算费用 + 未结算步骤报价 + 池内可分配额度 = 初始预留额
  ```

- **Codex 指出的一个我没想到的前提**：若产品说的"预授权"只是**保持现有的首步报价预留**（较窄的合同），
  应先明确；因为 **Node 完成首步信封之前，Go 无法按现有任务报价逻辑准确预留首步费用**
  （token 计费模型的报价依赖输入内容）。**不能把 `MaxCharge` 当作已预留金额。**

### Q2 状态不变量：给新合同显式版本 + 阶段

引入显式合同版本与 `awaiting_first_step` 阶段：只在该阶段允许 `TaskIDs=[]`、`ActiveTaskID=""`、步数为零，
并要求**持久化快照与 run 预留记录都存在且归属一致**；首个 `PiModelStep` 原子地创建任务、写 `TaskIDs`、
设活动任务并退出该阶段。首步前取消可以零任务终止并释放预留。其他阶段维持原规则。
**旧版继续按旧规则解码；不要用"数组为空"猜版本，也不要改写那 18 条终态 `state_json`。**

### Q3 提示合成：拆快照，避免二次拼接

把快照拆成 `serverPolicy` / `workspaceSystem` / 其他 Harness 部件 / 工具 schema，按固定版本规则合成，
**不要把已合成的 `canonical.systemPrompt` 再拼一次**。首步与后续步只读这份快照。

**Codex 额外指出的、我此前没考虑的加固**：要在 Go 的 `PiModelStep` 边界强制校验
"Node 提交的 system prompt 不能删改服务端策略，工具也不能只按名称放行而接受同名不同 schema"。

### Q4 迁移顺序（四步，本阶段按此执行）

1. **先加兼容读路径与存储结构**：版本化快照、run 预留、零任务初始态；旧 run 仍按根任务读。
   重点核查 `cloudAgentTask`、运行详情、幂等查询与续轮逻辑（它们现在都以 runID 直查根 Task）。
2. **先部署能识别新合同的 Node 与 Go 读端**，保留旧创建流程；领取时按合同版本限制 worker。
   未结束的旧 run 按旧路径跑完，**不中途转换**。
3. **实现并验证 run 创建事务、步骤资金分配与终态释放**，再用开关启用新版创建。
4. **回滚只关开关**；只要已有新版 run，就必须保留新版读取与财务恢复能力。

### 我据此做的取舍

Codex 的 Q1 结论依赖一个尚未确认的产品问题：**"预授权"是"锁住整轮预算"还是"沿用现有首步报价预留"**。
我**没有**在这一轮按 (b) 动 `CreateCloudAgentRun` —— 那会同时改动财务路径，前提未定就动手返工成本太高。

本轮只落地了 Q3 里那条**与 Q1 无关、且有独立价值**的 Go 侧加固（下一节）。

## 2.6 已实施：Go 侧服务端策略准入校验（Codex Q3 加固）

**问题**：Node 装配系统提示后，Go 在 `PiModelStep` 只校验了 `len(SystemPrompt) > 128<<10`。
也就是说一个缺陷或篡改的 worker 只要少发一段 system prompt，就能**解除服务端对工具权限、
能力边界与安全规则的约束**。只修 Node 侧（2.3 那条）等于把强制层交给被校验方自己声明。

**改动**（`cloud_agent_pi_bridge.go`，`PiModelStep` 的长度校验之后）：

```go
if policy := strings.TrimSpace(state.Canonical.SystemPrompt); policy != "" {
    if !strings.Contains(request.Canonical.SystemPrompt, policy) {
        return nil, kernel.Forbidden("Pi 模型请求缺少服务端策略")
    }
}
```

**判据取"包含"而不是"前缀"**：Node 的装配结果确实以策略开头（`renderSystemPrompt` 把 `base`
作为第一段），但按包含校验可以容忍未来在策略前插入版本头之类的合法包装，降低无谓的耦合。
策略为空（旧运行）时跳过 —— 否则会把历史运行全部锁死。

**与 Q3 的关系**：这条**不要求** Go 重新装配提示，Node 依然自由追加 Harness 内容；
Go 只要求"自己下发的那份策略仍然在"。它是 Node 侧修复的服务端兜底，两侧同时成立才形成强制层。

**测试**（`cloud_agent_pi_policy_gate_test.go`，4 个用例）：

| 用例 | 断言 |
| --- | --- |
| `TestPiModelStepRejectsPromptWithoutServerPolicy` | 模拟"装配时丢掉策略、只留工作区文件" → 必须被拒且原因含"服务端策略" |
| `TestPiModelStepAcceptsPolicyWithWorkspaceAppended` | Node 的正常装配（策略 + 追加的 AGENTS.md/SOUL.md）→ 通过并产生任务 |
| `TestPiModelStepAcceptsPolicyBehindVersionHeader` | 策略前带 `<!-- prompt-contract: v2 -->` → 通过（包含语义） |
| `TestPiModelStepSkipsPolicyCheckWhenRunHasNoPolicy` | 旧运行无策略前缀 → 不因该规则被拒 |

既有 `TestPiModelStepAdmissionIsExactlyOnce` 继续通过（它提交的就是运行自身的 canonical，含策略）。

## 2.7 仍未做与下一步

**未做**（等产品前提确认，见 2.5 的"取舍"）：

- Q1 的 run 级预授权 —— 取决于"预授权"是"锁住整轮预算"还是"沿用首步报价预留"；
- Q2 的 `awaiting_first_step` 阶段与合同版本；
- 去 `PiModelStep` 首步短路、不可变快照拆分与恢复不重读磁盘；
- Codex Q3 后半句"工具不能只按名称放行而接受同名不同 schema" —— 当前只校验名字是否在已披露集合内，
  未校验参数 schema 与快照一致（属 M-19 范围）。

**下一步（按 Codex 的四步顺序）**：先做第 1 步 —— 加兼容读路径与存储结构（版本化快照、
run 预留、零任务初始态），旧 run 仍按根任务读；重点是先把 `cloudAgentTask` 这条
"以 runID 直查根 Task"的耦合拆成"run 为权威 + 兼容读旧根任务"。

## 2.8 设计定案（Codex 二次评审后**改判**）

> **注意 §2.5 的 Q1 结论已被本节取代。** Codex 第一轮读的是另一份检出、并假设了"需要锁住整轮预算"，
> 因此推荐 (b)；第二轮它按我给的路径读了**本工作树**后**改判为 (a)**。

### 改判的理由（已在本树逐条复核）

现有财务与清理面**全部以任务为入口**，一个"没有任务归属"的 run 级预留会被这一整套逻辑漏掉：

| Codex 的论点 | 本树复核 |
| --- | --- |
| 订单按任务 ID 查询；`BillingOrdersByTaskIDs` 对空 `TaskIDs` 直接返回空 | ✅ `finance.go:601-605`：`task_id IN ?`，`len(taskIDs)==0` 时返回空 map |
| 管理端对账视图也依赖关联任务状态 | ✅ `finance.go:618` `AdminBillingOrders` |
| 取消与恢复从任务进入 | ✅ `cloud_agent_recovery.go:14-20` 要求 `CleanupPending && 终态`，随后按 `{run.ID, activeID, mediaID}` 遍历 |
| `ClaimNextTask` 不检查 operation | ✅ `repository.go:359-376` |

→ 若选 (b) 且不改财务表，就会出现一张**没有任务归属的预留订单**：查不到、对不上账、退款路径要另建。
所以"不改财务表结构 + 改动最小"这两个约束下，**(a) 占位任务**反而更容易防止悬挂订单。

### 定案：(a) 不可执行的占位任务承载建 run 时的报价预留

**必须的实现边界**（Codex 给出，我认同）：

1. **占位任务用专门的不可领取状态**，不能是 `queued/running`，也**不能只靠未知 `operation`**
   （`ClaimNextTask` 不看 operation）。占位 ID 可以继续承载既有 `runID == 根任务 ID` 的查询关系，
   但必须与真正的首步任务区分，且**不计入模型步骤的 `TaskIDs`**。
2. **首个 `PiModelStep` 在同一数据库事务内**：按实际首步信封重新报价 → 退回占位预留 →
   创建并预留真实首步任务 → 更新 run 状态。事务失败则占位预留保持原样；
   响应丢失后重试只返回已创建的首步任务。后续步骤维持现有按任务报价累计的 `remaining` 口径。
3. **首步前取消 / worker 永久未领取 / 运行失败都必须经可重试 cleanup 退款**。
   **当前 cleanup 只取消 `queued/running` 任务，不会自动退款一个新设的占位状态** ——
   这是 (a) 必须补的代码，不能假定已有清理覆盖。同时要把"终态 run 仍有未结占位订单"纳入扫描与管理端待核对视图。

### 验收方式（Codex 给出，作为阶段 2 的验收清单）

在 **SQLite 与 PostgreSQL** 的事务测试中，对以下五个节点逐点注入失败并重启恢复：
建 run 提交前/后、首步换单事务中、首步提交后响应前、首步前取消、租约失效清扫。
每个节点核对：

- 同一 run **至多一张未结占位订单**；
- **占位任务永不被 Go worker 领取**；
- 真实首步**恰有一张任务订单**，且只走 `PiModelStep`；
- 终态 run **无未结占位订单**；
- 重复请求 / 重复取消**不改变第二次账户余额**；
- 用户 `reserved_microcredits` 与全部未结订单的预留额**对平**；
- 回归查询既有 18 条终态运行。

### 仍然悬空的产品问题

Codex 明确划了取舍界线：**若要求"建 run 就锁住整轮预算、以后各步从中扣"，则改选 (b)，并接受那是
一次财务结算改造。** 当前机制预留的是**首个任务的报价**，不是整轮预算。

我按现有机制与两个约束采纳 (a)。**如果产品要的是"整轮预算锁定"，这条需要被显式推翻** ——
它会影响阶段 2 的整个预授权实现。

## 2.9 已实施：兼容读路径（Codex 四步顺序的第 1 步）

**目标**：把"以 runID 直查根 Task"这条耦合拆成"run 为权威 + 兼容读旧根任务"，
且**严格增量** —— 既有可读运行的行为一个都不变。

### 改了什么

| 改动 | 说明 |
| --- | --- |
| `cloudAgentExecutionOutput(task *model.Task, …)` → `(userID string, identity cloudAgentRunIdentity, …)` | 它此前只用 `task.UserID` 与 `task.ID`；新 run **没有任务行**，不能再要求传 Task |
| 新增 `cloudAgentRunIdentity` + `cloudAgentTaskIdentity(task)` | 把"身份"从 `*model.Task` 收敛成一份最小结构；旧路径的 `task→run` 状态映射（`succeeded`→`completed`）留在 `cloudAgentTaskIdentity` 里，旧视图逐字段不变 |
| `agentRunOutput(task, state)` → `agentRunOutput(identity, state)` | 唯一调用点就是上面那个函数 |
| 新增 `cloudAgentRunRefFor(userID, id)` | **旧路径优先**：根任务还在就完全走旧逻辑（含终态损坏 runtime 的只读降级、幂等键校验、ProjectID 校验）；根任务路径失败时才回退到执行记录 |
| `CloudAgentRun` 改用 `cloudAgentRunRefFor` | 无根任务且无执行记录时仍 404（不凭空造运行）；有执行记录但身份不匹配时拒绝 |

### 为什么顺序是"旧路径优先"而不是"run 优先"

`cloudAgentTask` 的旧路径优先用**任务 InputJSON 里的 `Agent` 字段**，只有解析失败才回退到执行记录；
而执行记录路径是从 runtime 重建 state。两者在被压缩过的历史 run 上**可能不同**。
先跑旧路径可以保证行为零变化；等阶段 2 第 3 步真正去掉根任务时，
只需删掉 `cloudAgentRunRefFor` 里的旧分支，调用方一行都不用动。

### 测试（`cloud_agent_run_read_path_test.go`，4 个用例）

| 用例 | 断言 |
| --- | --- |
| `TestCloudAgentRunReadsRunWithoutRootTask` | 只有执行记录、没有根任务 → **可读**（迁移前必然 404），且状态取执行记录而非任务状态映射 |
| `TestCloudAgentRunStillReadsLegacyRunWithRootTask` | 走 `CreateCloudAgentRun` 的真实旧 run（有 `operation=cloud_agent` 的根任务）→ 仍可读且身份不变 |
| `TestCloudAgentRunRejectsUnknownRun` | 两条路径都不存在 → 仍 404；他人 ID 也读不到 |
| `TestCloudAgentRunRejectsMismatchedRunIdentity` | 执行记录存在但 ID 与 `(userID, 幂等键)` 不一致 → 拒绝（防越权/错挂） |

### 过程中发现的一条重要事实

`piAgentTestFixture` 造出来的运行**没有根任务**（它登记的是另一条 `pi-root-task`，不是 runID 那条）。
也就是说 **Pi bridge 的既有测试一直在跑"未来形态"**，而 `CloudAgentRun` 对这条运行在迁移前**本来就 404**。
我最初按"夹具 = 旧路径运行"写了断言，被测试直接打回 —— 这也说明这条读路径此前**没有任何测试覆盖**，
新增的 4 个用例补上了这块空白。

### 回归

`internal/repository`（0.9s）、`internal/handler`（26.7s）、`cmd/server`（10.1s）全绿；
`internal/app` 全量（524.7s）失败集合与基线**完全一致** —— 仍是那 7 个既有失败，无新增、无意外变绿。
`go build ./...` 通过。

## 2.10 已实施：提示合同固化（"恢复不得重读新版磁盘文件"）

### 先确认的缺陷（不是推测）

`agent/src/server.ts`:

```ts
const harness = await loadHarnessPrompt(harnessDir);   // :19  worker 启动时读一次
...
const run = await bridge.claim(controller.signal);     // while 循环
await runCanvasAgent(bridge, run, controller.signal, harness, toolSchema);  // :32 同一个对象
```

`harness` 在 `while` 循环**之外**，却被传给**每一条**被领取的运行。也就是说：
运维改了 `SYSTEM.md` / `AGENTS.md` / `SOUL.md` 再重启 worker，**所有在途运行会静默换系统提示**。
这同时违反两条阶段 2 的要求：首步与后续步同一份快照、恢复不得重读新版磁盘文件。

**对照组**：工具 schema 早就有等价的漂移检查 —— `assertToolSnapshotMatchesSchema`
（`tool-disclosure.ts:44-61`，含逐字比较参数 schema，漂移就 `FatalWorkerError`）。
**提示层此前完全没有**：`grep 'schemaVersion|harnessHash|promptVersion'` 在 `bridge.ts`/`runner.ts`
里**零命中**，服务端也不知道 worker 用的是哪份 Harness。

### 改动

| 侧 | 改动 |
| --- | --- |
| Node | `system-prompt.ts` 新增 `harnessHash(parts)`：对 system / context（按名） / appendSystem 三层做 sha256 |
| Node | `runner.ts` 用 `harnessHash(harness)` 算出 `promptContract`，经 `bridge.modelStep(..., promptContract)` 发送 |
| Node | `bridge.ts` 在请求体带 `harnessHash`（无 Harness 时不带） |
| Go | `PiModelStepRequest` 新增 `HarnessHash string json:"harnessHash,omitempty"` —— **必须在 Go 侧声明**，路由用 `DisallowUnknownFields`，未声明字段会让整个请求变成空 400（阶段 1 踩过同一个坑） |
| Go | `cloudAgentRuntime` 新增 `PromptContract`；首个模型步固化，之后不一致返回 `Forbidden("本轮运行的提示合同与当前 agent 装配不一致…")` |

**为什么是"拒绝"而不是"用新的"**：Node 只有当前磁盘上的 Harness，**无法重建旧内容**；
静默换提示会让同一轮运行的系统提示中途改变，比明确失败更难排查。这与工具 schema 漂移的
既有处理（也是明确停止）保持一致。

**hash 编码用长度前缀**（`标签:字节长度:` + 内容）而不是分隔符拼接：文件内容本身可能包含任何
分隔符，长度前缀才无歧义。有一条用例专门证明"天真拼接会让两个语义不同的 Harness 撞成同一个 hash"。

### 测试

**Node（`test/prompt-contract.test.ts`，5 个 + `bridge-wire-contract.test.ts` 新增 2 个）**：
稳定性、任意一层改动都改变 hash（含顺序、含缺文件）、缺失与显式空串同身份、长度前缀防碰撞、
sha256 形态；以及 **wire 断言**——`modelStep` 确实发送 `harnessHash`，且无 Harness 时不发送。

**Go（`cloud_agent_pi_prompt_contract_test.go`，5 个）**：首个模型步固化、后续步骤沿用同一 Harness 通过、
**漂移必须被拒且不得污染已固化的合同**、不带字段时保持迁移前行为、以及用与路由相同的
`DisallowUnknownFields` 解码验证 producer 形态能被接受。

### 回归

`agent && npm test` → **46/46**（本轮从 39 起，+7）；
`internal/app` 全量（557.6s）失败集合与基线**完全一致**（仍是那 7 个既有失败）；
`internal/repository` / `handler` / `cmd/server` 全绿；`go build ./...` 通过。

### 这条修复保护的是什么、没保护什么

**保护了**：Harness 漂移不再静默生效 —— 在途运行遇到变更会明确失败，可被运维发现。

**没保护**：它只做**检测**，不做**固化内容**。真正的"恢复时用旧提示重建"需要把 Harness 文本
本身持久化进运行快照（下一步）。当前实现下，改了 Harness 就重启会让在途运行**失败**而非**继续用旧提示** ——
这是诚实的降级，不是完整方案，已记入"仍未做"。

## 2.11 已实施：控制面入口的根任务耦合（取消 / 插话）

### 发现

Codex 在四步顺序的第 1 步里点过"取消与恢复也从任务进入"，我上一轮只修了**读路径**，
控制面没修。这轮把残留的入口全找了一遍（`TaskForUser(userID, id)` + `operation == cloud_agent`）：

| 位置 | 性质 |
| --- | --- |
| `cloud_agent.go:276` | `cloudAgentTask` 自身（**兼容路径保留**，`cloudAgentRunRefFor` 会回退） |
| `cloud_agent_runtime.go:923` | `advanceCloudAgentByID` —— 已废弃区间，无生产调用方 |
| **`cloud_agent_runtime.go:2751`** | **`CancelCloudAgent`** —— 用户可见、控制面 |
| **`cloud_agent_interjection.go:102`** | **`InterjectCloudAgent`** —— 用户可见、控制面 |

后两个是真缺口：阶段 2 之后的新 run 没有根任务行，于是**完全无法取消、也无法插话**。
取消不了不是"读不到"，而是**用户只能看着它跑完，或者等看门狗超时判停** —— 控制面能力缺失。

### 改动

两处授权都改走上一轮建的 `cloudAgentRunRefFor`：

- 它保留"根任务优先"的旧语义（旧 run 行为逐字段不变）；
- 根任务读不到才回退到执行记录，而 `repo.CloudAgent` 自身已做 `user_id` 归属校验；
- 因此归属隔离没有被放宽。

`CancelCloudAgent` 原有的注释（"控制面操作，即使 runtime blob 损坏也必须可用"）与
"先查任务行"其实是矛盾的 —— 任务行恰恰是新形态没有的东西。现在注释与实现一致了。

### 测试（`cloud_agent_control_plane_read_test.go`，5 个）

| 用例 | 断言 |
| --- | --- |
| `TestCancelCloudAgentWorksWithoutRootTask` | 无根任务的运行**可取消**，状态变 `cancelled`，且清理跑完了 |
| `TestCancelCloudAgentStillWorksForLegacyRun` | 有根任务的旧 run 取消行为不变 |
| `TestCancelCloudAgentStillRejectsForeignRun` | 他人运行、不存在的运行仍被拒 |
| `TestInterjectCloudAgentAcceptsRunWithoutRootTask` | 无根任务运行时插话不再在归属校验处 404；他人仍 404 |
| `TestRunRefResolutionUsedByControlPlaneEntries` | 解析结果 `LegacyTask == nil`、身份取执行记录、state 可序列化 |

### 过程中被测试纠正的两个错误假设

1. **夹具不真实**：我最初直接用 `piAgentTestFixture` 的运行当"无根任务运行"，结果被身份自校验拒了 ——
   夹具的 ID 是写死的 `pi-run-1`，而真实运行的 ID 必须由 `(userID, 幂等键)` 派生
   （`CreateCloudAgentRun` 的 `id := cloudAgentID(userID, req.IdempotencyKey)`）。
   **守卫是对的，夹具不真实**。改成按真实规则重建后才有意义 —— 顺带说明前 3 个用例此前
   是在"因为错误的原因通过"，不重建就等于假绿。

2. **`CleanupPending` 的语义我写反了**：取消会置 `CleanupPending = true` 并**同步**跑
   `finishCloudAgentCleanup`，而该函数成功收尾时会自己把它清回 `false`
   （`cloud_agent_recovery.go:116`）。所以"取消成功"的正确观测是 **false**；
   若为 `true` 反而说明清理没跑完。我把断言改成了这个方向。
   （与阶段 1 的 `TestSweepStalledPiAgentRunsSetsCleanupPending` 不冲突：那条断言 true
   是因为看门狗只置标志、没有 ctx 去跑清理。）

### 回归

`internal/repository`（1.0s）/ `handler`（25.5s）/ `cmd/server`（10.3s）全绿；
`internal/app` 全量（533.9s）失败集合与基线**完全一致**（仍是那 7 个既有失败）；
`go build ./...` 通过。

## 2.12 计费不变量基线 + 重复取消幂等性

### 先建基线（改财务代码之前）

`cloud_agent_billing_invariant_test.go`（5 个用例）。核心不变量：

```
账户 reserved_microcredits == Σ(未结订单 [reserved|running|uncertain] 的 AmountMicrocredits)
```

| 用例 | 结论 |
| --- | --- |
| `TestReservationInvariantHoldsAcrossRunLifecycle` | 建 run → 恰 1 张未结预授权；取消 → 0 张；两个节点不变量都对平 ✅ |
| `TestRepeatedRunCreationDoesNotDoubleReserve` | 同一幂等键重投 3 次 → 同一个 run、只 1 张订单 ✅ |
| `TestDifferentIdempotencyKeysReserveIndependently` | 不同幂等键各自预授权；取消其一不影响另一 ✅ |
| `TestConcurrentRunCreationReservesExactlyOnce` | 4 并发同键提交 → 收敛到 1 个 run、1 张订单 ✅ |
| `TestConcurrentCancelReleasesReservationExactlyOnce` | 见下 —— **发现缺陷** |

**基线结论**：顺序路径与并发建 run 的计费都是对的。这一轮的价值主要在于把"改之前是对的"
变成了可回归的证据，而不是口头判断。

### 发现：重复/并发取消返回 CAS 冲突（**错误面**缺陷，非计费面）

4 个并发取消 → **2 个**报 `creation state changed; reload before continuing`。

**先把严重性界定清楚**：我把不变量断言排在错误断言**之前**，确认了
**金额侧完全正确**（预留恰好释放一次、不变量对平）。所以问题是：
一次双击 / 客户端重试 / 网络重发会让用户看到"取消失败"，而运行其实已经取消了。

根因两处，都已修：

1. `CancelCloudAgent` 自身的 `MutateCloudAgent` 修订号 CAS；
2. `finishCloudAgentCleanup`（`cloud_agent_recovery.go:102`）也按 run 修订号 CAS ——
   两个并发请求都会走到清理，后到的一方必然撞冲突。

**修法是精确的**：只在"重新读取确认已达目标状态"时吞掉冲突 —— 取消路径要求
`current.Status == "cancelled"`，清理路径要求 `!current.CleanupPending`。
其它来源的冲突照旧如实报错，**不掩盖真实竞态**。

修后 4 并发取消全部成功、金额仍对平。

### 回归

`internal/app` 全量（581.3s）失败集合与基线**完全一致**（仍是那 7 个既有失败）；
`internal/repository`（0.8s）/ `handler`（21.5s）/ `cmd/server`（8.5s）全绿。

### 仍未做

Codex 验收清单里的**五个崩溃节点注入**（建 run 提交前/后、首步换单事务中、首步提交后响应前、
首步前取消、租约失效清扫）目前只覆盖了可无侵入验证的部分（并发与幂等）。
真正的"事务中途失败"需要注入点，等占位任务与原子换单落地时一并做 —— 那时才有换单事务可注入。
**PostgreSQL 一侧仍未做**（本地回归跑的是 SQLite）。

## 2.13 已实施：首步原子换单与提示正文快照（P1.5 收口）

本节是 §2.8 定案（(a) 不可执行的占位任务承载建 run 时的报价预留）的落地记录。
状态标记：**已实现** = 代码在树；**测试通过** = 本轮实跑过；**未验证** = 需要本机没有的环境。

### 改了什么

**Go：建 run 只落"占位 + 快照"，真实首步由首个 `PiModelStep` 换单**

| 文件 | 改动 |
| --- | --- |
| `internal/model/models.go` | 新增 `TaskStatusHolding`（`holding`）：不进队列、`ClaimNextTask` 永不领取、不计入活动任务额度 |
| `internal/app/cloud_agent_contract.go` | 合同快照（策略身份、工具 schema 身份、Harness 正文与哈希、装配后提示身份）；`cloudAgentFreezeFirstStepContract`、`cloudAgentVerifyAssembledPrompt`、`cloudAgentToolSchemaDrift`、`cloudAgentHarnessBodyDigest`；占位任务的事务内读取；首步准入失败的终态 + 退款；清理期退还占位预留 |
| `internal/app/cloud_agent.go` | `CreateCloudAgentRun` 改为提交 v2 运行态（合同版本 + `awaiting_first_step` + 快照 + 占位任务 ID）与不可领取占位任务；幂等判断与父轮解析统一走 `cloudAgentRunRefFor` |
| `internal/app/cloud_agent_runtime.go` | 运行态新增 `contractVersion` / `phase` / `contractSnapshot` / `placeholderTaskId`；校验规则里 `awaiting_first_step` 是唯一允许空任务历史的阶段；`enqueueCloudAgentTask` 在首步走换单并把阶段推进与任务落库放在同一事务 |
| `internal/app/task_creation.go` | `taskAdmission` 支持 `Status` / `Stage`：占位任务是 `holding`，其他任务仍是默认 `queued` |
| `internal/app/storage_quota.go` | `createCloudAgentRunWithinStorageQuota`：配额校验与"占位任务 + 预留 + 执行记录"同一次写入 |
| `internal/repository/finance.go` | `CreateCloudAgentHoldingTask`、`SwapCloudAgentHoldingForFirstStep`、`RefundCloudAgentHoldingOrder`、`PendingCloudAgentCleanups`；`refundBillingOrder` 抽成可在外层事务复用的实现 |
| `internal/repository/repository.go` | 任务列表排除 `holding`：占位行不是用户可理解的任务 |
| `internal/app/cloud_agent_pi_bridge.go` | 首步请求声明并校验收 `harness` 正文（此前 Node 已在发，Go 未声明，`DisallowUnknownFields` 会把首步变成空 400）；快照回发冻结的 Harness（恢复不重读磁盘）；工具**参数结构**与 Go 权威目录比对；首步准入失败当场给终态并退预留 |
| `internal/app/cloud_agent_recovery.go` | 清理事务里退还"从未开始"的运行的占位预留；新增 `DrainPendingPiAgentCleanups` |
| `internal/app/task_worker.go` | 看门狗清扫后补做收尾排空。此前只有 HTTP 取消会调 `finishCloudAgentCleanup`，被清扫的运行会把预留冻在账上 |

**Node：两处真实缺陷 + 判据修正**

| 文件 | 改动 |
| --- | --- |
| `agent/src/runner.ts` | **顺序缺陷**：带 `taskId` 的 assistant 检查点是 Go 侧清 `ActiveTaskID` 的唯一动作，而 `PiToolBatch` 在 `ActiveTaskID` 非空时拒绝整批。原实现把批次先入队，等于每个工具批次都以 403 失败，worker 会当致命错误整轮退出。已改为"检查点 → 批次准入" |
| `agent/src/session-tools.ts` | **行为分叉**：母类型此前要求"至少有一个合格子工具"才披露，Go 的 `cloudAgentVisibleToolsForCategories` 把合格目录里的所有母类型无条件下发。某个类别的子工具全被判不合格时，Node 的工具集会比服务端预检少一个入口 |
| `agent/test/runner.test.ts` | 顺序改用 ops 断言先后（不再断言"assistant 是第一条检查点"）；失败步骤断言"没有 assistant 检查点"；修正读源码的路径（原来指向 `dist/src/runner.ts`）与 C1 判据（`[^)]*` 跨行误报） |
| `agent/test/tool-disclosure.test.ts` | 夹具改为"母类型成功、只有子工具回执失败" |
| `agent/test/prompt-contract.test.ts` | 新增跨语言固定向量，Go 侧断言同一个常量 |
| `agent/package.json` | `test` 加 `--test-timeout=20000 --test-force-exit`：SDK 用例结束时会留活跃句柄，整轮会挂住不退出 |

**新增测试**：`internal/app/cloud_agent_pi_first_step_test.go`（首步换单、正文/哈希不一致、后续步骤合同变更、首步准入失败退款、清扫排空退款、跨语言哈希向量）。

### 实际测试命令与结果（本轮实跑）

Go 侧事务级用例的跑法（宿主 `CGO_ENABLED=0` 且无 C 编译器，必须走容器；镜像已在本地）：

```bash
cd backend && go build ./... && go vet ./internal/app ./internal/repository   # 通过
docker run --rm -v d:/13537/open-ai-canvas-canary:/src -v canvas-gocache:/root/.cache/go-build \
  -w /src/backend open-ai-canvas-backend-test:sticky-tools \
  sh -c "CGO_ENABLED=1 go test ./internal/app -count=1 -timeout 600s -v \
         -run 'TestPiFirstStep|TestPiCleanupDrain|TestCloudAgentHarnessBodyDigest'"
# ok infinite-canvas/backend/internal/app 11.880s（5/5 PASS）
cd agent && node node_modules/typescript/bin/tsc -p tsconfig.json   # 退出码 0
cd agent && node --test --test-timeout=20000 --test-force-exit "dist/test/*.test.js"
# tests 52 / pass 52 / fail 0 / duration 8.70s（wall 8.77s）
```

首步用例逐条结果（容器内，真实 SQLite 事务）：

| 用例 | 结果 | 覆盖 |
| --- | --- | --- |
| `TestPiFirstStepSwapsHoldingReservationForRealTask` | PASS 3.02s | 建 run 只有占位与快照；首个模型步换单；占位预留退、真实首步只留一笔未结；阶段推进与任务同事务；预留额对平 |
| `TestPiFirstStepRequiresPromptBodyMatchingItsHash` | PASS 3.00s | 缺正文、正文与哈希不一致都拒绝，且不动账、不推进阶段 |
| `TestPiFirstStepAdmissionFailureRefundsPlaceholder` | PASS 2.98s | 首步准入失败当场给终态并退预留 |
| `TestPiCleanupDrainRefundsUnstartedRun` | PASS 2.85s | 真实 `SweepUnclaimedPiAgentRuns` + 收尾排空退款 |
| `TestCloudAgentHarnessBodyDigestMatchesNodeVector` | PASS 0.00s | 与 Node 的跨语言哈希固定向量 |

**跑通后暴露并修掉的三个真实缺陷**（都不是测试写错，是产品缺陷）：

1. 首步准入失败发生在换单**之前**时（`CreateTask` 就被拒），走的是 `failCloudAgent`，占位预留不退还 —— 用户看到"失败"而钱一直冻着。现在这条路径与换单事务里的失败走同一处理：同一事务写终态并退款。
2. `failCloudAgent` 不置 `CleanupPending`：任何失败的运行都不会进收尾，子任务不取消、租约不释放、预留不退。现在终态一律带收尾标记（收尾本身幂等）。
3. 收尾退款被 `decodeErr == nil` 挡着：失租/状态不可解码时**退款被跳过**，等于把预留永久冻结。现在退款只看占位行本身（`HoldingTaskForRun`：同一笔事务写下、操作名唯一、幂等），并让"占位行没有预授权"从静默 no-op 变成显式错误。

**Agent 用例基线（迁移前必须知道）**：`-run 'TestCloudAgent|TestPi|TestReservationInvariant|TestAgent'` → **130 PASS / 43 FAIL**，另有一条用例在 420s 超时（后续排查）。43 个失败全部是驱动旧 `advanceCloudAgentByID` 循环的用例：建 run 改成 v2（无根任务）之后它们语义上已经失效，正是 C1 要迁移的那批。


逐文件耗时（同一命令按文件分组跑）：

| 文件 | 结果 | 耗时 |
| --- | --- | --- |
| `prompt-contract` | 6 pass | 0.09s |
| `harness-sync` | 3 pass | 0.11s |
| `bridge-wire-contract` | 6 pass | 0.13s |
| `system-prompt` | 9 pass | 0.18s |
| `tool-disclosure` | 6 pass | 0.43s |
| `pi-stream` | 4 pass | 0.71s |
| `pi-sdk-probe` | 9 pass | 3.84s |
| `runner` | 9 pass | 8.54s |

这就是"整轮跑测试会假死"的原貌：最后两个文件收尾时进程不退出，之前的观测被当成挂起。

### 失败归因（本轮修掉的 6 个红）

1. `runner` 顺序缺陷（见上）与 `tool-disclosure` 母类型分叉（见上）—— 都是产品缺陷，不是测试挑剔。
2. `runner` 读源码路径错误、C1 判据正则跨行误报 —— 判据缺陷。
3. 两处旧断言把"会话引导阶段的 system/user 检查点"当成契约破坏 —— SDK 接线后的正常形状。
4. `tool-disclosure` 错误回执用例的夹具让母类型也失败，测的已经不是原意。

### 未验证与阻断

- **Go 侧测试只能在容器里跑**：宿主机 `CGO_ENABLED=0` 且没有 C 编译器，`mattn/go-sqlite3` 原样报
  `Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work. This is a stub`。
  已在 `open-ai-canvas-backend-test:sticky-tools` 容器内跑通首步用例（命令见上）。
- **整包一次跑不通，但不是代码问题**：`go test ./internal/app` 不跳用例时会卡在依赖外网的用例上
  （容器内无外网；已定位到 `TestAdminStorageListStatsAndPreview`，Agent 子集里另有一条待定位），
  表现为超时而不是失败。迁移期间的验证按文件/用例名过滤逐个跑。
- **跨进程未验证**：Node 与 Go 未在真实 HTTP 上跑过一次首步（含本轮的换单与新校验）。
- **PostgreSQL 未做**；崩溃节点注入未做（换单事务现在可以注入了）。
- **C1 未完成**：`advanceCloudAgentByID` / `advanceCloudAgent` 仍在树里，其旧驱动测试大量以
  "根任务即运行 ID"为前提；本轮把建 run 改成 v2 之后，这些用例在语义上已经失效，
  必须按路线图 §5 P5 迁移而不是放宽断言。这是当前工作树最大的未完成面。
- **SDK 版本未入快照**：路线图 C2 要求 Harness 正文、哈希、SDK 版本与最终信封身份一起固定。
  本轮固定了正文、哈希、策略身份、工具 schema 身份与装配提示身份；SDK 版本待 Node 上报后再补。
