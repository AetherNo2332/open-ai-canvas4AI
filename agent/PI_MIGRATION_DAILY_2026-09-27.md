# Pi Agent 迁移工作日结（2026-09-27）

- 仓库：`D:\13537\open-ai-canvas-canary`（WSL：`/mnt/d/13537/open-ai-canvas-canary`），分支 `canary`
- 起点提交：`7aa99880`，`VERSION=v1.5.7.1+7aa9988`
- 变更规模：**34 个文件，+932 / −363**（用 `git diff --ignore-cr-at-eol` 排除 CRLF 噪声；`git status` 显示的约 2400 项绝大多数只是行尾差异）
- 目标：把画布 Agent 从旧 Go 循环迁移到固定版 Pi 内核（`@earendil-works/pi-agent-core@0.87.1`），Go 保留鉴权、画布写入、模型与计费、持久化与 SSE 合同；**不保留双引擎**；充分测试

---

## 一、今日交付总览

| 类别 | 数量 | 状态 |
|---|---|---|
| 架构迁移（引擎/调度/Harness/schema/事件流） | 8 项 | 完成 |
| 正确性修复（幂等、原子性、终态、恢复顺序） | 9 项 | 完成 |
| 真实线上故障修复（前端、部署、worker） | 6 项 | 完成 |
| 新增测试 | Node 25 个 / Go 16 个（Pi 协议 12 + 看门狗 2 + 画布原子性 1 + admission 幂等 1） | 全绿 |
| 发现但未完成 | 见第五节 | 待办 |

**当前运行基线**：`localhost:3000`（web + backend + agent 三容器，agent 独立且无端口）；
`cd agent && npm test` → **25/25**；`go build ./...` 通过；
`go test ./internal/app -run 'TestPi|TestSweepStalled|TestCanvasWriteRollsBack'` → ok；`go test ./internal/prompts/...` 通过。

---

## 二、架构迁移

### 1. Pi 成为唯一引擎
`CreateCloudAgentRun` 不再读 `CANVAS_AGENT_ENGINE`，新运行一律写 `input["agentEngine"]="pi"`；
删除已无用的 `os` 导入与开关分支。

### 2. 删除旧 Go 调度循环
- 移除 `task_worker` 里每 2 秒 tick 的 `advanceCloudAgents` 扫描与完成后的 `wakeCloudAgentScheduler`
- 删除 `advanceCloudAgents`、`wakeCloudAgentScheduler` 两个函数，以及只服务它们的
  `Service` 字段 `agentSchedulerWake` / `agentSchedulerCursor` / `agentSchedulerMu`
- 删除两处只测旧调度的用例
- 续聊不再由 Go 推进上一轮（去掉 `advanceCloudAgentByID` 调用），改为要求上一轮已终结
- `advanceCloudAgentByID` / `advanceCloudAgent`（旧状态机约 344 行）标注废弃，仍被
  **22 个测试文件、84 处调用**充当驱动器

### 3. Node 独立装配系统提示（Harness 迁移）
- 新增 `agent/src/system-prompt.ts`：按 pi.dev 约定读
  `SYSTEM.md`（替换系统提示）/ `APPEND_SYSTEM.md`（追加）/
  `AGENTS.override.md` > `AGENTS.md` > `CLAUDE.md` / `SOUL.md` / `TOOLS.md`；
  `agentDir` 必须是绝对路径；20 KiB 上限；缺失文件不算错误；`cwd` 作为第二发现目录
- 新增 `agent/scripts/sync-harness.mjs`：把 backend 的策略与工具描述同步进 `agent/harness/`
  （策略文件剥掉 YAML frontmatter），生成 `SOURCE.json`（sha256）
- `server.ts` 启动时读一次 Harness 与 `TOOL_SCHEMA.json`，`runner.ts` 用
  `assembleSystemPrompt(harness, 策略前缀)` 作为 `initialState.systemPrompt`
- **移除 Go 侧 `loadAgentWorkspace` 追加**，装配权完全归 Node（消除重复装配）

### 4. 工具 schema 制品化（Go/Node 共用）
- `agent/harness/TOOL_SCHEMA.json`：**31 个工具**，`schemaVersion=cloud-agent-tools/v2`
- 生成方式：`WRITE_TOOL_SCHEMA=1 go test ./internal/app -run TestAgentToolSchemaArtifactMatchesRuntime`
- Go 侧字节级漂移检测；Node 侧结构校验 + worker 启动期交叉校验（快照 ⊆ 制品 **且** 同名工具参数定义一致，键序无关）

### 5. SSE 合同对齐（旧 journal 25 类事件全量枚举后逐条比对）
为 Pi 路径补发两条缺失事件，且与消息检查点**同事务**提交：
- `assistant_message`：含"正文 + 工具调用"的**过程说明**（`final:false`）
- `reasoning_message`：从 Pi 的 `thinking` 块提取，`messageId` 带 `:reasoning` 后缀
  （与旧口径 `truncateRunes(...,8000)` 一致），且**不混入正文事件**

### 6. 真实增量正文事件流
`bridge.modelStep` 轮询 Go 的 `textDraft`，按"已发送前缀"补差 → 真实 `text_delta`，
而不是任务结束后一次性补齐。实测一次运行产出 **28 条 `reasoning_delta`**。

### 7. agent 独立部署
`docker-compose.yml` 的 `agent` 服务（`profiles: ["pi"]`）：独立镜像、无发布端口、
只经 `/internal-agent` 通信；补 Harness 只读挂载、健康检查、`NO_PROXY`（见第四节）。

### 8. 前端接入核对
确认前端只消费 Go 的 `/api/agent` 合同、**不接触 Pi 内部协议**；核对了面板的 20 个事件渲染分支；
`assistant_delta` / `reasoning_delta` / `assistant_snapshot` 是前端适配层自己产生的（不在 journal 里）；
前端代码**没有** `engine` 字段分支。

---

## 三、正确性修复

| # | 问题 | 修法 | 验证 |
|---|---|---|---|
| 1 | **画布已改、回执却报失败**：`advanceCloudAgentTool` 把所有 `toolErr` 降级成失败回执后照常提交；recorder 失败时画布已保存 | 新增 `cloudAgentToolErrorIsBusiness`：只有已知业务拒绝入回执，基础设施错误**向上返回以回滚事务** | `TestCanvasWriteRollsBackWhenRecorderFails`：注入 recorder 失败 → 断言画布**逐字节未变**；**并反证**吞掉错误时画布确实保留写入 |
| 2 | **Pi 准入幂等**（原以为缺、实为已满足） | 恰好一次由**单事务**保证（任务创建 + 预算预授权 + 运行检查点同在 `MutateCloudAgent`）；`PiModelStep` 在 `ActiveTaskID` 非空时返回既有任务 | `TestPiModelStepAdmissionIsExactlyOnce`：重投返回同一 taskId、任务数=1、预授权数=1 |
| 3 | **终态后仍无限轮询**：`advanceCloudAgentTool` 对非 running/queued 静默 no-op → `PiToolAdvance` 返回 `pending` → `executeTool` 每 900ms 无限轮询，占死 worker 并持续续租 | 三层配合：Go `PiToolReceipt` 新增 `terminated`；`bridge.executeTool` 遇终止立即返回；`runner` 把 `terminated` 映射为 Pi 的 `terminate` | `TestPiToolAdvanceSignalsTerminationInsteadOfPending` 覆盖 rejected/cancelled/failed/completed 四种终态 |
| 4 | **停止集合漏 `rejected`** → 审批被拒后 Pi 循环继续（违反合同） | 统一 `isTerminalRunStatus`（completed/failed/cancelled/**rejected**） | 见上条 |
| 5 | **恢复期间不续租**：心跳创建在 `recoverToolResults` 之后，恢复中等待审批/媒体任务可超 45s 失租 | 顺序改为 **schema 校验 → 启动心跳 → 检查点初始化 → 恢复**（schema 不兼容必须在任何恢复副作用之前被拒） | 代码审查 + 全量测试 |
| 6 | **持久化失败被当成正常结束** | 订阅回调包裹 try/catch 记录 `listenerFailure`，`continue()` 后显式抛出；**模型自身 error/aborted 不抛**（合法业务结果） | `检查点持久化失败必须抛出，不能当成正常结束` |
| 7 | **订阅请求缺 abort signal** | 订阅回调使用 Pi 提供的 `(event, signal)`，用于 checkpoint / startToolBatch / snapshot | `tsc --noEmit` + 全量测试 |
| 8 | **superseded 运行永远停在 running**：续聊路径依赖已删除的旧驱动去归档旧合同运行 | 续聊时**显式归档**（`terminateCloudAgent`，仅当尚未终态） | `TestCloudAgentContinuesAfterContractChange` |
| 9 | **Harness 变更不再阻断续聊**：装配权移走后策略哈希不再覆盖 Harness，丢失合同快照 | 把 Harness 内容**折进哈希但不并入 `system.Text`**（正文装配权仍在 Node） | `TestCloudAgentHarnessChangeBlocksContinuation` |

另：`PiFailModelStep` 改为幂等重投（同一步骤失败上报重放必须成功，否则 Node 把 400 当协议错误整轮退出）。

---

## 四、真实线上故障修复

| # | 现象 | 根因 | 修法 |
|---|---|---|---|
| 1 | 画布点 Agent 图标报 `modelCapabilityConfigFor is not defined` | `canvas-cloud-agent-panel.tsx` 调用但**未 import** | 补 import；全仓扫描确认无第二处 |
| 2 | 接着报 `reasoningSupported is not defined`（面板白屏） | 子组件 `iT`（composer 选择区）引用了**父组件局部变量**，既未从 props 接收也未自算 | 在 `iT` 内用 `useMemo` + try/catch 自行推导；能力查询失败退化为"不支持推理"而非崩面板 |
| 3 | agent 容器持续 `fetch failed` | 宿主 `HTTP_PROXY=127.0.0.1:7897` 被 Node `fetch` 遵守，内部调用被送去代理 | agent 服务置空 `HTTP(S)_PROXY` 并设 `NO_PROXY`（含 `backend` 与私网段）。踩坑：WSL 里大写 `NO_PROXY` 是空串会覆盖 `${VAR:-默认}`，故改为 YAML 写死 |
| 4 | 改了代码但浏览器仍加载旧 chunk | `docker-compose.local.yml` 给 web 挂了 `web-data:/usr/share/nginx/html`，卷盖住镜像产物且从未清理，累积 **3235 个 JS / 3 个版本的面板 chunk** | 删除该卷挂载（3235 → 1481，含该符号的 chunk 3 → 1）；`nginx.conf` 的 `/assets/` 由 `immutable` 一年缓存改为**带 ETag 校验**（同名 chunk 内容变化必须能生效） |
| 5 | **agent 已输出但 UI 一直显示"运行中"** | 见下方专节 | 三处修复（超集制品 + 失败可见 + 看门狗） |
| 6 | 后端镜像构建失败 | `goproxy.cn` TLS 握手超时拉 `golang.org/x/text`（网络问题，非代码） | 重试构建成功 |

### 事故专节：agent 已输出但 UI 一直"运行中"

**根因**：`agent/harness/TOOL_SCHEMA.json` 用**未开视觉**的默认请求生成，只有 27 个工具，
**缺 `canvas_inspect_image`**（该工具只在 `visionEnabled=true` 时暴露）。而 worker 启动期的
交叉校验会把"快照里有、制品里没有"判为失败并抛错，且该校验位于**该运行的任何一次
`/internal-agent` 请求之前** → Pi 检查点没写、`/no-tool-turn` 没调、**没有任何终态写入**；
同时该运行的**第一步模型调用是 Go 侧的根任务本身**（已跑完并把正文流给前端）
→ 正文有了、状态永远 `running`。

*这是我加的一个检查把数据漂移放大成 agent 循环全面不可用。*

**关键证据**：后端访问日志显示该运行在卡住的 3.75 分钟内**零请求**；
worker stderr 为 `server snapshot has tools missing from cloud-agent-tools/v2: canvas_inspect_image`。

**更正一个错误结论**：我最初写"租约过期后自愈"是**错的**。用 revision 对账证明是**无限重试**：
卡住 run `rev=17`（同形状健康 run = 12，差 5 ≈ 3.75min/45s），另一条 `rev=79` ≈ 67 个租约周期
（≈50 分钟）。真正恢复靠的是**修制品 + 重启 worker**。

**三处修复**：

1. **制品改为超集**：用最宽请求生成（`permissionMode=auto` + `VisionEnabled` + `HasMemories` +
   非空 `SkillIDs`），27 → **31 工具**；校验语义为"快照 ⊆ 制品 且 同名工具参数一致"
2. **启动期致命错误"运行可见"**：`FatalWorkerError` → `bridge.failRun` →
   `POST /internal-agent/runs/:id/fail` → `PiFailRun` 写 `failed` + `run_failed(pi_worker_fatal)`；
   只接受租约持有者，已终态幂等
3. **租约看门狗**：`StalledPiAgentRuns` + `SweepStalledPiAgentRuns` + `task_worker` **60 秒 ticker**，
   把"租约过期 6 个周期（4.5 分钟）且无进展"的运行终结为 `failed(pi_worker_stalled)`

---

## 五、外部技术复核（Codex / gpt-6-astra）

把完整进度与四个问题交给 Codex 审阅（它直接读了项目源码并跑了隔离探针）。核心结论：

> **Pi 循环确实在工作；但目前不能宣称移植完成。主要问题不在 `fromCanonical` 跳过 system，
> 而在恢复流程、事务失败语义，以及旧循环承担的业务保障尚未完整接入 Pi。**

**它核验通过的**（与我的独立核验一致）：`runner.ts` 跳过 canonical 的 system 消息**不丢提示**
（Pi 会从 `initialState.systemPrompt` 补入初始 system 消息）；`Agent` 会 `await listener(...)`，
所以订阅回调里的 checkpoint 被等待（探针顺序 `checkpoint-start → checkpoint-end → execute`）。
它同时警告：**不要把那句 `continue` 当修复删掉**。

**它抓到的**（本轮已修前两项）：画布保存与 recorder 的事务失败语义、Pi 准入幂等判断、
恢复流程四个缺陷（心跳晚于恢复、`rejected` 不在停止集合、初始提示未固定、部分 transcript）；
并纠正旧驱动引用数是 **22 文件 / 84 处**（我原先的 20/75 少算了 `advanceCloudAgent(` pattern）。

**它给的切换门槛**（未完成即不可宣称可切换）：
写入原子性、并发账务、恢复期间租约、拒绝/取消终态、硬预算、媒体收尾、上下文因果顺序、
历史 run 处置、旧驱动测试依赖清零 —— **现有一次 `completed` 与正常 SSE 序列只证明短路径可运行。**

---

## 六、未完成（按优先级）

| # | 事项 | 说明 |
|---|---|---|
| 1 | **并发结算**（门槛） | `finance.go:749` `SettleBillingOrder` 先读订单状态再更新账户，不能仅凭事务与顺序重试判定并发安全；需订单锁或条件状态转移 + 账务幂等键 |
| 2 | **初始提示快照** | 初始 system 是构造函数临时补入，未固定为初始化检查点 → 重启可能用新 Harness 重建旧 run 的提示；崩溃后只要 `piMessages.length > 0` 就采用部分 transcript，可能丢历史 |
| 3 | **`piToolReceipt` 幂等身份作用域** | 现只按 `callId` 查；需明确唯一作用域并测跨步骤重复 callId、同 ID 不同参数、压缩后查询 |
| 4 | **`server.ts` 串行 await** | 一次只处理一个 run，等审批的 run 会占住 worker，影响其他 run |
| 5 | **删除旧状态机** | 按职责分层迁移 22 文件 / 84 处调用；业务规则留 Go、编排交真实 Pi、跨进程故障用少量严格集成测试；**不要 mock 掉 Agent** |
| 6 | **7 个既有失败用例归类** | 必须落入三分类之一（迁移中修复的业务缺陷 / 被新测试替代的旧断言 / 独立已有缺陷），**不得靠 skip 或放宽断言变绿** |
| 7 | **历史 run 处置** | 不要统一改 `engine="pi"`：Pi run 按检查点恢复；空 engine 旧 run 默认受控终止；`CleanupPending` 交独立幂等清理 |
| 8 | 阶段 4/5 其余 | 硬预算前置、压缩因果顺序、媒体收尾、SSE 断线续传、看图/压缩实测 |

---

## 七、今日教训

1. **启动期硬失败只适用于"必然一致"的合同。** 工具集合会随权限/视觉/技能/记忆开关变化，
   用单一配置的制品做全等校验，等于给 agent 循环埋一个"一改配置就全停"的开关。
   现在制品是超集、校验是包含关系，且失败**显式写进运行**而不是只进 stderr。
2. **"同一个事务"不等于"原子失败"。** 基础设施错误不能按业务拒绝吞掉，否则会出现
   "画布已改 + 失败回执"。
3. **`finally` 与 `try` 是兄弟作用域**，try 内声明的 `const` 在 finally 里不可见。
4. **给静态产物目录挂持久卷会让镜像更新对用户不可见**；同名 chunk 内容变化后
   `immutable` 缓存会让浏览器一直用旧代码（表现为随机的 `X is not defined`）。
5. **可选参数 + 静默降级 = 接线遗漏不会被测试发现**：`runCanvasAgent(..., harness?, toolSchema?)`
   测试直接传参所以绿，真实入口没传所以不生效 —— 是 Codex 而非测试发现的。
6. **不要用叙述代替证据**：本日两次纠正都来自对账（revision 差值、日志零请求），
   而不是"看起来应该自愈"。

---

## 八、相关文件

- 迁移进度与执行清单：[`agent/PI_MIGRATION_GAPS.md`](./PI_MIGRATION_GAPS.md)
- 迁移交接与硬边界：`AGENT.md` / `AGENTS.md`
- 本日新增测试：
  - Go（16 个用例）：`cloud_agent_pi_bridge_test.go`（Pi 协议 8）、
    `cloud_agent_pi_admission_test.go`（准入幂等 1）、`cloud_agent_canvas_atomicity_test.go`（画布原子性 1）、
    `cloud_agent_pi_watchdog_test.go`（看门狗 2）、`cloud_agent_tool_schema_artifact_test.go`（schema 漂移 1）、
    `cloud_agent_admission_idempotency_test.go`（任务幂等 1），另有既有的 `TestPi*` 用例
  - Node：`system-prompt.test.ts`、`harness-sync.test.ts`、`pi-stream.test.ts`、`runner.test.ts`
- 本日新增 Node 模块：`agent/src/system-prompt.ts`、`agent/scripts/sync-harness.mjs`

---

## 九、工作区最新复核（2026-09-27 23:06 Asia/Shanghai）

本节以当前 canary 工作树和本轮实跑为准，修正前文旧快照中的测试数与会话进度。当前 HEAD 为 `31157460`，本地 `canary` 领先 `origin/canary` 两个提交；迁移工作区仍有大量未提交改动，未推送、未部署。

- Node 生产 `runner.ts` 已以锁定的 `@earendil-works/pi-coding-agent@0.87.1` `createAgentSession` 执行 Pi 循环，并从 Go 恢复 conversation 级 Pi v3 session ID、header、entries 与 active leaf。TypeScript 编译通过，Node 全套 **56/56 PASS**。
- Go 已有用户/会话归属的 Pi v3 session 与 append-only entries 表、active leaf/revision CAS、session lease/epoch；holding 预留与首步原子换单也已实现。SQLite 定向测试通过：`go test ./internal/app ./internal/repository ./internal/database -count=1 -timeout 300s -run 'TestPi|TestCloudAgentHarnessBodyDigest|TestCloudAgentPreflight'`，app 155.339s，repository 与 database 通过。
- 旧 Go 编排仍未删除：三个主要定义仍在，`backend/internal` 范围 `rg` 命中 27 个文件、95 处（包含注释与定义）。Pi session operation ledger、旧 canonical 对话历史导入、Web 会话列表、PostgreSQL 与跨进程/浏览器验收尚未完成。
- `server.ts` 仍串行领取和等待；审批/媒体等待会占据 worker。生产 runner 还未把 Go 结构化语义压缩接到 Pi `session_before_compact`，Pi 生命周期探针通过不等于生产压缩接通。
- 完整 `internal/app` 套件此前运行到 600s 超时，卡在 `TestBannerAnnouncementTitleRunsWithEmoji` 的插件文件 `fsync` 路径；本轮只确认上述定向 Go 用例，不宣称全量套件通过。

---

## 十、并行执行切片（2026-09-27 23:24 Asia/Shanghai）

已将 Pi agent 服务从单一 claim loop 改为有界 worker pool，并提交 `ebe0bc39`（`feat(agent): run bounded concurrent Pi workers`）。每个 worker 使用独立 worker ID 和 Go claim 请求；默认并发 4，可通过 `CANVAS_AGENT_CONCURRENCY` 配置 1–16。Compose 配置解析出 `agent` 服务并确认并发值为 4。

- `node node_modules/typescript/bin/tsc -p tsconfig.json` 通过；Node 全套测试更新为 **59/59 PASS**，覆盖独立 run 并发执行及确定性错误上报。
- 容器内 Go `TestSettleBillingOrderIsExactlyOnceUnderConcurrentSettlement` 与 `TestPiSession` 仓储用例通过；Compose `config --services` 通过。
- 不同 conversation 可占用不同 worker 同时执行；同一 conversation 的写入仍由 Go session lease/epoch 串行化。
- 审批/媒体等待仍会占据单个 worker，未实现持久挂起后释放槽位；没有做压力测试、PostgreSQL 测试、跨进程恢复或实际部署。

## 十一、Pi 用量锚点恢复（2026-09-27 23:45 Asia/Shanghai）

提交 `cf0b9207 fix(agent): anchor Pi usage at assistant checkpoint`：Go 在 Pi assistant 消息检查点确认成功模型任务时，从同一数据库事务读取当前用户、该 task、成功 text 调用且供应商明确上报的 usage；可采信的 token anchor 与 assistant transcript/Pi session 检查点一并持久化。上下文压力下一步可使用真实输入用量作为估算锚点。没有上游 usage 时继续使用本地估算。Node 上报的 usage 不参与这个权威读数。

- 新增 `cloud_agent_pi_usage_anchor_test.go`：覆盖 Pi 检查点下成功 usage 入锚、下一步压力读取 provider 锚点、usage 不可用时不造锚；复用了原有按用户、状态、能力、渠道校验的锚点逻辑。
- 容器内定向 Go 用例通过：`TestPiAssistantCheckpointPersistsProviderUsageAnchor`、`TestPiCheckpointCommitsMessageAndConversationEntryAtomically`、`TestCloudAgentTokenAnchorRequiresProviderReportedUsage`、`TestCloudAgentTokenAnchorOnlyUsesOwnedSuccessfulTextCall`；另单独重跑 Pi 新增用量测试通过。
- 只读复核确认：Pi 生产 runner 尚无 Go 结构化压缩 hook；Go 压缩任务仍只由已停用的旧循环触发。Pi session v3 活动分支映射、摘要 operation 幂等记录、压缩 entry 即时持久化需要先设计并实现，不能把探针通过算作接通。
- 仍未迁入 Pi 的旧循环责任包括插话投递、视觉观察账本与部分模型失败/截断重试；旧驱动虽不在生产调度中，但仍有 22 个 Go 测试文件依赖，移除前要迁移其业务断言。
- 此切片没有做 PostgreSQL、进程崩溃/恢复、真实上游模型或浏览器验收；未部署、未推送。当前工作区仍含先前未提交的迁移改动。
