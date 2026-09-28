# P0 固定基线与合同（实测）

> 本文件是路线图 [`PI_CODING_AGENT_MIGRATION_ROADMAP.md`](./PI_CODING_AGENT_MIGRATION_ROADMAP.md) 阶段 P0 的交付物。
> 所有数字都来自本轮在 `canary` 工作树上的**实际命令输出**，不是历史记录的复制。
> 核对对象：[`PI_MIGRATION_DAILY_2026-09-27.md`](./PI_MIGRATION_DAILY_2026-09-27.md)、[`PI_MIGRATION_GAPS.md`](./PI_MIGRATION_GAPS.md)。

## 1. 基线快照

| 项 | 值 | 来源 |
| --- | --- | --- |
| 仓库 | `D:\13537\open-ai-canvas-canary`（WSL：`/mnt/d/13537/open-ai-canvas-canary`） | — |
| 分支 | `canary` | `git branch` |
| HEAD | `7aa998806e2aa46d230274bacdf0a859aaf23788` | `git rev-parse HEAD` |
| `VERSION` | `v1.5.7.1+7aa9988` | 根 `VERSION` |
| `git status --short` 行数 | **2410**（绝大多数是 CRLF 噪声） | `git status --short \| wc -l` |
| 真实改动（忽略行尾差异） | **34 个文件，+932 / −363** | `git diff --ignore-cr-at-eol --stat` |
| 未跟踪文件 | **20 个**（清单见下） | `git ls-files --others --exclude-standard` |
| Node / npm | `v22.23.2` / `10.9.8` | `node -v` / `npm -v` |
| Go | `go1.25.1 linux/amd64`（`/home/a1/.local-go/go/bin`，**不在默认 PATH**） | `go version` |
| Docker | `29.8.0` | `docker --version` |

**未跟踪文件（20）**：`PLAN.md`；`agent/PI_CODING_AGENT_MIGRATION_ROADMAP.md`、`agent/PI_MIGRATION_DAILY_2026-09-27.md`、`agent/PI_MIGRATION_GAPS.md`；`agent/harness/{SYSTEM_POLICY,MEDIA_POLICY,TOOL_DESCRIPTIONS}.md`、`agent/harness/{SOURCE,TOOL_SCHEMA}.json`；`agent/scripts/sync-harness.mjs`；`agent/src/system-prompt.ts`；`agent/test/{system-prompt,harness-sync}.test.ts`；`backend/internal/app/{cloud_agent_admission_idempotency,cloud_agent_canvas_atomicity,cloud_agent_pi_admission,cloud_agent_pi_bridge,cloud_agent_pi_watchdog,cloud_agent_tool_schema_artifact}_test.go`。

> **未提交改动保护**：本轮全程未使用 `git reset`/`git checkout --`/宽范围删除；新增一律追加。

### 1.1 真实失败测试列表（本轮实测，非引用）

命令：`cd backend && CGO_ENABLED=1 go test ./internal/app/ -count=1` → **FAIL，耗时 600s，7 个失败**：

| # | 失败用例 | 分类（阶段 5.5 待定） |
| --- | --- | --- |
| 1 | `TestCloudAgentRuntimeEmitsContextPressurePerStep` | 待归因 |
| 2 | `TestCloudAgentAutoMediaSubmitsWithoutApproval` | 分层工具披露漂移 |
| 3 | `TestCloudAgentCanvasReadArgumentRepairContinuesRun` | 待归因 |
| 4 | `TestCloudAgentSkillMissingAndDistinctPaths` | 分层工具披露漂移 |
| 5 | `TestCloudAgentEmptyCanvasRepairAcrossCheckpoints`（4 个子用例全失败） | 待归因 |
| 6 | `TestPluginViewIncludesDocumentationForEveryOfficialProtocol` | 与 Pi 迁移无关 |
| 7 | `TestPurgeAssetsBatchSharedResourcesAndHistory` | 与 Pi 迁移无关 |

Node 侧：`agent/` 既有 **25 个**测试全部通过（含 `harness-sync` 的 3 个漂移检测用例）。
本轮新增的 `pi-sdk-probe` 9 个用例加入后，`cd agent && npm test` → **34/34 通过**（98.8 s，其中约 75 s 是 WSL 9p 上的 SDK 导入开销，见 §1.2）。

> 更正记录：本文件初稿曾写"既有 23 个"，那是原生 FS 镜像（缺少 `backend/`，`harness-sync` 失败）下的读数。
> 以仓库内 `cd agent && npm test` 的权威结果为准：**既有 25 个**。这正是路线图要求的"不用中间态读数替代最终证据"。


### 1.2 环境事实：WSL 9p 挂载使 SDK 冷启动慢 115 倍（**非 SDK 缺陷**）

| 位置 | 文件系统 | `import @earendil-works/pi-coding-agent` |
| --- | --- | --- |
| `/mnt/d/...`（仓库所在） | WSL 9p | **75 624 ms** |
| `/home/a1/...` | ext4 | **659 ms** |

补充实测（ext4）：`import pi-ai` 18 939 ms → 659 ms 环境下的 `ModelRuntime.create()` 仅 **4 ms**。

- `@earendil-works/pi-coding-agent@0.87.1` 解包 23 MB / 1108 文件，`exports` 只允许 `.`、`./rpc-entry`、`./client`（仅 source）、`./experimental/plugin`（仅 source）→ **无法深路径导入绕过 barrel**。
- 结论：容器内（原生 FS）冷启动正常；**在 `/mnt/d` 上直接跑 SDK 测试每次进程要多付 ~95 s**。本轮 Node 侧迭代因此使用原生 FS 镜像（`rsync src/ test/` + 独立 `node_modules`），仓库交付物本身仍在 `/mnt/d`。
- 这不影响生产结论，但必须写进交接，否则会被误判成「SDK 太慢，P1 不可行」。

## 2. 文档 vs 当前代码：逐条核对

| # | 历史记录的主张 | 出处 | 本轮实测 | 判定 |
| --- | --- | --- | --- | --- |
| 1 | 34 文件 +932/−363，2400 项多为 CRLF | Daily §一 | 34 文件 +932/−363；`git status` 2410 行 | ✅ 成立 |
| 2 | `advanceCloudAgentByID` = 20 文件 / 75 处；加 `advanceCloudAgent(` = **22 文件 / 84 处** | GAPS §0.3 | `advanceCloudAgentByID` **77 处 / 22 文件**；`advanceCloudAgent(` **11 处 / 3 文件** | ⚠️ **修正**：调用点 77（非 75）；ByID 波及 22 文件（含定义所在的 `cloud_agent_runtime.go`，即 21 个测试文件） |
| 3 | 既有失败 **7 个** | GAPS §0.2 | 7 个（测试名逐个复现，见 1.1） | ✅ 成立 |
| 4 | Node 测试 **25/25** | Daily §一 | 既有 **25 个**全部通过（仓库内 `npm test`） | ✅ 成立（初稿据镜像误判为 23，已更正） |
| 5 | journal 事件类型 **25 个** | GAPS §SSE | `state.event(...)` 第二参数去重 = **25** | ✅ 成立 |
| 6 | `CANVAS_AGENT_ENGINE` 已移除 | GAPS「开发进度」 | 引擎选择确实不再读它，但 `main.go:79` 启动校验 + `docker-compose.yml:22` **仍在使用** | ⚠️ **漂移仍在**（P5 消项） |
| 7 | `agent/package.json` 只锁 `pi-agent-core`/`pi-ai` | Roadmap §0 | 核对成立 | ✅ 成立（本轮已追加 `pi-coding-agent@0.87.1`） |
| 8 | 旧驱动已标废弃、仅测试在用 | Daily §二.2 | `advanceCloudAgentByID` 定义在 `cloud_agent_runtime.go:894`，`advanceCloudAgent` 在 `:921`，标注存在 | ✅ 成立 |
| 9 | 前端不接触 Pi 内部协议 | GAPS §前端接入核对 | 全仓 `grep engine` 只命中**画布绘图引擎**（excalidraw/tldraw），无 `pi-*`、无 `PiSnapshot`、无 `engine` 运行分支 | ✅ 成立 |
| 10 | `server.ts` 串行 await，一个 run 占住 worker | GAPS §5 / Codex D 矩阵 | `server.ts` 单个 `while` 循环 + 单个 `controller`，`await runCanvasAgent(...)` 期间不领取下一个 | ✅ 成立（P4 目标） |
| 11 | `runner.ts` 跳过 canonical 的 system 消息不丢提示 | GAPS §疑点 1 | `runner.ts:45`、`:72` 两处 `if (role === "system") continue;`，顺序为 `canonical.systemPrompt → assembleSystemPrompt → initialState.systemPrompt` | ✅ 成立（**保留**，勿当冗余删除） |
| 12 | `piToolReceipt` 仅按 `callId` 查 | GAPS §G3 | 尚未改造，仍是运行态扫描 | ✅ 成立（P2 目标） |
| 13 | `SettleBillingOrder` 存在并发双结算风险 | Daily §六.1 | 尚未改造 | ✅ 成立（P3 目标） |

**结论**：历史记录的方向判断可靠，但**具体计数必须以本文件为准**；Daily 的 Node 用例数已过期，GAPS 的旧驱动计数偏小 2 处。

## 3. 三端合同（当前实现）

### 3.1 Node ↔ Go 内部协议 `/internal-agent`（`backend/internal/handler/internal_agent.go`）

常量时间 Bearer 比对；未配置 `CANVAS_AGENT_INTERNAL_TOKEN` 时**整组路由不挂载**。每个请求都带 `X-Agent-User-ID` 与 `X-Agent-Worker-ID`，由 service 逐次校验归属。

| # | 方法 | 路径 | Service 入口 | 用途 |
| --- | --- | --- | --- | --- |
| 1 | POST | `/claim` | `ClaimPiAgent` | 领取一个 run（+1 revision，写租约） |
| 2 | GET | `/runs/:id` | `PiAgentSnapshot` | 运行快照 / canonical / 工具快照 |
| 3 | POST | `/runs/:id/renew` | `RenewPiAgentLease` | 续租（45 s 周期） |
| 4 | POST | `/runs/:id/messages` | `PiCheckpointMessage` | 消息检查点（≤1 MiB，`DisallowUnknownFields`） |
| 5 | POST | `/runs/:id/no-tool-turn` | `PiNoToolTurn` | 无工具收尾判定（含 `terminated`） |
| 6 | POST | `/runs/:id/model-steps` | `PiModelStep` | 模型步骤准入（≤2 MiB） |
| 7 | GET | `/runs/:id/model-steps/:taskId` | `PiModelStepView` | 轮询模型任务（`textDraft` 增量来源） |
| 8 | POST | `/runs/:id/model-steps/:taskId/ack` | `PiModelStepAck` | 确认已消费 |
| 9 | POST | `/runs/:id/model-steps/:taskId/fail` | `PiFailModelStep` | 幂等失败上报 |
| 10 | POST | `/runs/:id/tool-batches` | `PiToolBatch` | 整批预检（≤512 KiB） |
| 11 | POST | `/runs/:id/tool-calls/:callId/advance` | `PiToolAdvance` | 顺序推进并取回执 |
| 12 | POST | `/runs/:id/fail` | `PiFailRun` | worker 致命错误可见化（`pi_worker_fatal`） |

**P2 缺口**：缺少 v3 entry/operation 契约与压缩入账入口（`/runs/:id/entries`、`/runs/:id/operations`、`/runs/:id/compact` 均不存在）。这是 P2 要新增的部分，不是现状。

### 3.2 Go ↔ Web 公开合同 `/api/agent`（`web/src/services/api/agent.ts`）

| 方法 | 路径 | 前端导出 |
| --- | --- | --- |
| GET | `/agent/capabilities` | `getAgentCapabilities` |
| GET/PATCH | `/agent/profile` | `getAgentProfile` / `updateAgentProfile` |
| POST | `/agent/runs` | `createAgentRun` |
| POST | `/agent/runs/:id/messages` | `sendAgentMessage` |
| GET | `/agent/runs/:id`（`sinceSeq`、`eventLimit`） | `getAgentRun` |
| POST | `/agent/runs/:id/interjections` | `sendAgentInterjection` |
| POST | `/agent/runs/:id/cancel` | `cancelAgentRun` |
| POST | `/agent/runs/:id/undo` | `undoAgentCanvasRun` |
| POST | `/agent/runs/:id/approvals/:approvalId/decision` | `decideAgentApproval` |
| SSE | 运行事件（`after` 游标、断线续传、`Last-Event-ID`） | `subscribeAgentEvents` |

**硬约束**：P5 必须保持上表签名与信封 `{code,data,msg,reason}` 不变；前端不得新增 `pi` 引擎分支。

### 3.3 公开 SSE journal 事件（25 类，实测枚举）

```
approval_decided            approval_requested          assistant_message
canvas_undone               canvas_updated              completion_blocked
context_compacted           context_compaction_requested context_images_pruned
context_pressure            context_transition          durable_event
generation_task_created     legacy_event                model_failure_recovered
model_step_stop             plan_updated                reasoning_message
run_cancelled               run_failed                  tool_completed
user_interjection           user_interjection_delivered  user_interjection_dropped
user_question
```

序号不变式（`cloud_agent_runtime.go:316`）：`events[i].Seq == EventSeqBase + i + 1`，`EventID = "<runId>:<seq>"`。前端另外**自行**派生 `assistant_delta`、`reasoning_delta`、`assistant_snapshot`、`run_status`、`error`、`tool_failed`、`progress_summary` —— 这些**不在 Go journal 里**。

## 4. 端到端时序（目标链路）

```
 Web                Go backend                         Node Pi worker           上游模型
  │                     │                                    │                      │
  │ POST /agent/runs    │                                    │                      │
  ├────────────────────>│ 鉴权 + 画布/模型权限                │                      │
  │                     │ 创建 run（engine=pi, rev=1）        │                      │
  │                     │ 写首个用户消息 + 初始合同快照        │                      │
  │<─── {run} ──────────┤                                    │                      │
  │                     │<────── POST /internal-agent/claim ─┤                      │
  │                     │ 校验租约 → +1 rev, lease_expires    │                      │
  │                     ├─────── {run, canonical, tools} ────>│                      │
  │                     │                                    │ ① schema 校验         │
  │                     │<────── POST /runs/:id/renew ────────┤ ② 启动心跳            │
  │                     │                                    │ ③ 检查点初始化        │
  │                     │<────── GET  /runs/:id ──────────────┤ ④ 读 v3 entries       │
  │                     │                                    │ ⑤ SessionManager.inMemory(entries)
  │                     │                                    │ ⑥ 订阅 Pi 事件        │
  │                     │                                    │                      │
  │                     │<── POST /model-steps (幂等准入) ───┤ 创建模型任务          │
  │                     │ 同事务：任务 + 预算预授权 + 检查点   │                      │
  │                     ├──── {taskId} ─────────────────────>│                      │
  │                     │                                    │ Canvas Provider      │
  │                     │                                    │ streamSimple ───────>│
  │                     │<── GET  /model-steps/:taskId ──────┤ 轮询 textDraft        │
  │                     ├──── {status, textDraft, result} ──>│ 差量 → text_delta     │
  │                     │                                    │ Pi 事件 → 检查点      │
  │                     │<── POST /runs/:id/messages ────────┤ assistant/thinking    │
  │                     │ 同事务：消息 + assistant_message/   │                      │
  │                     │        reasoning_message 事件       │                      │
  │                     │<── POST /tool-batches（整批预检）──┤ 工具披露批次          │
  │                     ├──── {accepted} ───────────────────>│                      │
  │                     │<── POST /tool-calls/:id/advance ───┤ 顺序执行              │
  │                     ├──── {receipt|pending|terminated} ─>│                      │
  │  (审批/媒体等待由持久状态表达，不占死 worker)              │                      │
  │                     │<── POST /runs/:id/no-tool-turn ────┤ finish_run / 收尾     │
  │                     ├──── {status} ─────────────────────>│ agent_settled        │
  │                     │                                    │                      │
  │ GET /agent/runs/:id?sinceSeq=N                          │                      │
  ├────────────────────>│ 投影公开状态 + SSE（严格单调序号）  │                      │
  │<─── {run, events} ──┤                                    │                      │
```

## 5. 决策记录

### 5.1 ADR-P0-1：**每个 run 一个 Pi session**

| 项 | 决定 |
| --- | --- |
| 取值 | **一个 run 一个 Pi session**（路线图 §2.2 的首选方案） |
| 理由 | 会话级租约、权限重评估与 prompt 变更边界在本项目里是**逐 run** 的合同；跨 run 复用同一 session 需要额外证明权限不会在会话生命周期内被复用，收益仅为省一次导入 |
| 续聊 | 用前一个**终态 run 的 active-branch 投影**构造新 session 起点，记录 `parent_run_id`、导入来源版本与摘要；不继承跨用户/跨画布数据 |
| 持久化 | `cloud_agent_pi_sessions.run_id` 作主键（见路线图 §2.1 建议表） |
| 版本策略 | `format_version = 3`（`SessionManager.getHeader().version`）。**只接受当前 `CURRENT_SESSION_VERSION`**；读到更旧版本时先 `migrateSessionEntries` 再显式标记来源转换版本，不做静默升级 |

### 5.2 ADR-P0-2：**Pi transcript 是编排事实来源，Go canonical 是投影**

- Pi 的 v3 entry 树（`SessionManager`）是模型上下文的唯一事实来源。
- Go 的 `canonical` / `CloudAgentMessageRecord` 只是**投影**，供前端展示与旧合同兼容。
- Go **不得**再用 `trimCloudAgentTextHistory` 或 context compaction 独立改写同一段对话（路线图 §4.3）。
- 恢复时**只能**用 `SessionManager.inMemory(cwd, options, entries)` 重建（P1 已验证），不得直接赋值 `session.agent.state.messages`。

### 5.3 ADR-P0-3：**提示与 schema 版本在首个 Pi 消息前原子固定**

- 初始 system/Harness/工具 schema 快照必须在第一个 Pi 消息之前写入并不可变。
- 重启读取**原快照**，不得用磁盘上当前 Harness 重算旧 run（Daily「未完成 #2」的根因）。
- 提示变更只影响新 run；续聊前校验合同兼容，否则明确新建会话或阻断。

## 6. 金样本（固定信封与事件）

供 P2–P6 做「新旧模型信封与公开结果」逐条对比的基准。**样本必须用 stub 模型生成**，不含真实密钥与用户数据。

| 样本 | 内容 | 落点 |
| --- | --- | --- |
| `model-envelope` | 一次 Provider 请求的 `systemPrompt`、工具声明名序列、消息 role 序列、usage、stop reason | `agent/test/pi-sdk-probe.test.ts` 的 `Observed`（已实现） |
| `tool-disclosure` | 第 1 步只见母工具 → 打开类别后第 2 步见子工具 | 同上 `P1 chain` 用例 |
| `journal-events` | 25 类事件名 + `(runId, sequence)` 单调不变式 | 本文件 §3.3 |
| `checkpoint` | 一次 assistant 检查点同事务写出的消息 + `assistant_message(final:false)` + `reasoning_message(:reasoning)` | `backend/internal/app/cloud_agent_pi_bridge_test.go` |

`TestAgentToolSchemaArtifactMatchesRuntime` 是 schema 制品的字节级金样本，重建方式：
`WRITE_TOOL_SCHEMA=1 go test ./internal/app -run TestAgentToolSchemaArtifactMatchesRuntime`。

## 7. 旧循环职责清单（删除前置条件，阶段 4）

`advanceCloudAgent`（`cloud_agent_runtime.go:921`，约 344 行）只调用六个函数，其中四个 Pi 也在用，**不能一起删**：

| 被调用 | 行号 | 归属 | 处理 |
| --- | --- | --- | --- |
| `advanceCloudAgentTool` | `:1859` | 共享（`PiToolAdvance` 也调） | **保留** |
| `advanceCloudAgentContextCompaction` | `cloud_agent_context_compaction.go:469` | 共享 | **保留** |
| `failCloudAgent` / `terminateCloudAgent` | — | 共享事件与失败路径 | **保留** |
| `advanceCloudAgentMedia` | `:2517` | 共享（媒体收尾） | **保留** |
| `advanceCloudAgentReadBatch` | `:1769` | 仅旧循环 | 随状态机删 |
| `advanceCloudAgent`（自递归） | `:921` | 仅旧循环 | 随状态机删 |

**删除门槛**：`advanceCloudAgentByID`（77 处 / 22 文件）与 `advanceCloudAgent(`（11 处 / 3 文件）引用清零，且其业务职责（步数/金额/媒体硬预算、插话消费、图片投递与观察、完成规则、损坏状态、cleanup）已在 Pi bridge 有等价窄入口与新测试。

## 8. P0 退出条件核对

| 退出条件（路线图 §5） | 状态 |
| --- | --- |
| 保存 `git status`、HEAD、真实失败测试列表 | ✅ §1 / §1.1 |
| 逐一核对 GAPS/Daily | ✅ §2（13 条，含 3 处修正） |
| 写 agent/、Go、Web 合同 | ✅ §3 |
| 一张端到端时序图 | ✅ §4 |
| 确定「一 run 一 session」与版本策略 | ✅ §5.1 |
| 建立固定模型信封/事件金样本 | ✅ §6 |
| 当前代码与文档矛盾已标注 | ✅ §2 |
| 无覆盖未提交文件 | ✅ 全程未回滚 |
| 旧循环职责清单齐全 | ✅ §7 |
