# Canvas Pi Agent 迁移交接（本轮工作）

> **最新状态（2026-09-28）**：当前工作在 `codex/pi-agent-migration`，提交 `eae5177bca533afc730530bd84bc1604ca799831` 已推送到 PR #58（目标 `canary`，尚未合并）。本机 Compose 3000 已部署此 backend，readiness 200、schema 43/43。Playwright 验证品牌 canary 标记及只读 Agent 端到端运行通过（`canvas_get_state`，0 节点，完成，无页面错误）。本轮没有保留备份，也没有触碰生产。详见 `PLAN.md` 与 `agent/PI_MIGRATION_DAILY_2026-09-28.md`。

> **这份文件是谁写的**：本轮（阶段 1 + 阶段 2 大部分）的执行者。
>
> **为什么不写进 `agent/HANDOFF.md`**：那份文件是另一位协作者的任务交接（框架是"必须消除的合同断点"），
> 并且本任务明确要求**不得删除或覆盖 `HANDOFF.md`**。因此这里另起一份同级交接，
> 两边内容互补、互不覆盖。若需要合并成一份，请明确指示。
>
> 本交接初次撰写时：分支 `canary`，HEAD `7aa99880`，**工作树有大量未提交改动（48 文件 `+3526/−482`、39 未跟踪）**。此为历史快照，当前状态以上方最新状态和 `PLAN.md` 为准。
> 其中既有本轮的，也有他人更早的。**不要清理、不要回滚、不要 `git checkout --`。**

---

## 1. 任务边界（与根 `AGENTS.md`、roadmap 一致）

- **Node（`agent/`）**：Pi 会话、提示装配、工具披露、会话投影、驱动循环。
- **Go（`backend/`）**：鉴权、画布/资源写入、渠道与模型路由、上游请求、计费、审批、
  **原有结构化语义压缩**、数据库、公开 `/api/agent` 与 SSE。
- **Web**：只消费 `/api/agent`，不感知 Pi 内部协议。
- **本任务不部署、不推送、不改生产。** 本地 3000 是 canary（`docker-compose.yml` +
  `docker-compose.local.yml`），生产是 `docker-compose.deploy.yml` —— 后者全程未触碰。

---

## 2. 先读这些（顺序）

1. 仓库根 `AGENTS.md`
2. `agent/PI_MIGRATION_TODO.md` —— **任务清单与阶段进度**（本轮的权威进度来源）
3. `agent/PI_MIGRATION_PHASE_STATUS.md` —— **逐阶段执行记录**，含每轮的命令、结果、被测试纠正的错误假设
4. `agent/PI_CODING_AGENT_MIGRATION_ROADMAP.md`
5. `agent/PI_MIGRATION_P1_SDK_EXTENSION_POINTS.md` —— `pi-coding-agent@0.87.1` 的真实探针结论
6. 本文件

`agent/PI_MIGRATION_DAILY_2026-09-27.md` 与 `PI_MIGRATION_GAPS.md` 是历史材料，
**部分计数已过期**，以代码和最新测试为准。

---

## 3. 已完成什么

### 阶段 1：正确性门槛 —— **完成**

| 编号 | 事项 | 关键点 |
| --- | --- | --- |
| M-08 | `PiFailModelStep` 终态守卫 | 已终态且任务非成功 → **返回 `nil`**（不是错误）。返 4xx 会被 bridge 的致命分类整轮退出，把"已正确终结"变成 worker 报错。"成功任务不得被报失败"的不变量仍排在终态守卫**之前**，照旧 403 |
| M-09 | 看门狗置 `CleanupPending` | 抽了共用的 `sweepPiRuns`；此前只写 `failed`，而 `finishCloudAgentCleanup` 要求 `CleanupPending && 终态`，导致子任务与媒体回写无人收尾 |
| M-10 | 未领取 run 的排队语义 | `StalledPiAgentRuns` 加 `lease_expires_at IS NOT NULL`；新增 `UnclaimedPiAgentRuns` + `SweepUnclaimedPiAgentRuns`（30 分钟、原因 `pi_worker_unavailable`）。**`ClaimPiAgent` 必须继续匹配 NULL 租约**，否则首次领取永不可能（有专门的回归用例锁死） |
| M-11 | 看图后下一步必然失败 | 见下节"真实缺陷" |

### 阶段 2：统一首步合同 —— 6/7 子项完成

| 子项 | 状态 |
| --- | --- |
| 提示合成（Node）：服务端策略不可被 `SYSTEM.md` 替换 | ✅ |
| 提示准入（Go）：`PiModelStep` 校验提交的 system prompt 仍含服务端策略 | ✅ |
| 预授权定案：(a) 不可执行的占位任务 | ✅ 设计定案（Codex 二次评审改判，见阶段状态 §2.8） |
| 兼容读路径：`cloudAgentRunRefFor` | ✅ |
| 控制面入口：`CancelCloudAgent` / `InterjectCloudAgent` | ✅ |
| 提示合同固化：`harnessHash` 首步钉死、漂移拒绝 | ✅ |
| 占位任务 + 首步原子换单 + 终态退款清扫 | ⏳ **未做** |
| 合同版本 + `awaiting_first_step` + 快照**内容**持久化 | ⏳ **未做** |

---

## 4. 本轮找到并修掉的**真实缺陷**（都有代码证据，不是推测）

### 4.1 M-11：看过图之后的下一步必然失败（迁移回归）

三段证据链：

1. `cloudAgentReference`（`cloud_agent_media.go:252-255`）**要求** `metadata.storageKey` 以 `resource:` 开头，
   并把同一 key 作为 `reference["storageKey"]` 返回（`:263`）；
2. `cloudAgentImageInspection.ImageURL = reference["storageKey"]`（`cloud_agent_vision.go:261`）
   → `cloudAgentImageContentParts` 写成 `image_url.url`（`:897`）；
3. `cloudAgentFlushPendingImages` 把它追加进 `Canonical.Messages`（`:946`），调用者含
   `advanceCloudAgentTool`（`cloud_agent_runtime.go:1598`）—— **Pi 路径可达**。

而 `PiModelStep` 从不设置 `referenceImages` → `resolveAgentResourcePlaceholders`
（`provider.go:488`）找不到白名单 → `BadAuthRequest("模型协议引用了未获准的图片")`。

**性质**：旧循环在 `cloud_agent_runtime.go:1238/:1251` 一直有这两行（都在已废弃区间内），
dev 分支同样有 —— 所以这是**迁移时漏接的线，不是既有缺陷**。修复即在 `PiModelStep` 补回。

**调用顺序已核对**（修复成立的前提）：`provider.go:471` 的 `hydrateGenerationMedia` 读字节填 `DataURL`，
`:488` 才做占位符解析 —— 顺序正确；`:786` 那个分支正是为 Agent 图片写的，且读的是
`input.Config.CapabilityConfig.Text.References`（即"权威能力附回"那处修复），两处互补。

### 4.2 Harness 漂移会让在途运行静默换提示

`server.ts:19` 在 worker 启动时读一次 `harness`，`:32` 却把它传给**每一条**被领取的运行
（`harness` 在 `while` 循环之外）。改了 `SYSTEM.md`/`AGENTS.md` 再重启 → **所有在途运行换提示**。

**对照组**：工具 schema 早有等价检查 `assertToolSnapshotMatchesSchema`
（`tool-disclosure.ts:44-61`，含逐字参数比较）；**提示层零覆盖**。

修复：Node 算 `harnessHash` 并随模型步发送；Go 首步固化进 `PromptContract`，之后不一致
`Forbidden`。**注意**：`PiModelStepRequest.HarnessHash` **必须在 Go 侧声明**，
路由用 `DisallowUnknownFields`，未声明字段会让整个请求变成**空 400**（阶段 1 踩过同一个坑）。

### 4.3 无根任务的运行**完全无法取消**

`CancelCloudAgent`（`cloud_agent_runtime.go:2751`）与 `InterjectCloudAgent`
（`cloud_agent_interjection.go:102`）都以"查根任务 + `operation == cloud_agent`"作为唯一授权入口。
阶段 2 之后的新 run 没有根任务行 → **取消不了、插话不了**，用户只能等看门狗判停。

修复：两处改走 `cloudAgentRunRefFor`（保留根任务优先的旧语义，读不到才回退执行记录，
归属校验由 `repo.CloudAgent` 的 `user_id` 条件保证，未放宽隔离）。

### 4.4 重复/并发取消返回 CAS 冲突（错误面缺陷）

4 个并发取消 → 2 个报 `creation state changed; reload before continuing`。
**金额侧是安全的**（预留只释放一次，不变量对平）—— 所以这是**错误面**问题：
一次双击/重试会让用户看到"取消失败"，而运行其实已经取消。

根因有两处，都已修：
- `CancelCloudAgent` 自身的 `MutateCloudAgent` 修订号 CAS；
- `finishCloudAgentCleanup`（`cloud_agent_recovery.go:102`）也按修订号 CAS。

修法是**精确**的：只在"重新读取确认已达目标状态"（`cancelled` / `CleanupPending == false`）
时吞掉冲突，其它来源的冲突照旧如实报错，不掩盖真实竞态。

---

## 5. 证据（怎么自己复现）

```bash
cd backend && export PATH=/home/a1/.local-go/go/bin:$PATH
go build ./...
CGO_ENABLED=1 go test ./internal/app -count=1                  # ~530-560s
CGO_ENABLED=1 go test ./internal/repository/ ./internal/handler/ ./cmd/server/... -count=1

cd ../agent && npm test                                        # ~90-100s
```

| 范围 | 最近结果 |
| --- | --- |
| `agent && npm test` | **46/46 通过** |
| `internal/repository` / `handler` / `cmd/server` | 全绿 |
| `internal/app` 全量 | **固定 7 个既有失败**，与本轮之前**完全一致**（无新增、无意外变绿） |

那 7 个既有失败的名称在 `PI_MIGRATION_P0_BASELINE_AND_CONTRACTS.md` 里有记录，
属于迁移前就存在的，**不要为了变绿去放宽断言**（阶段 7 要逐个处理）。

本轮新增测试文件（均在 `backend/internal/app/`）：

- `cloud_agent_pi_lifecycle_gates_test.go`（7）
- `cloud_agent_pi_vision_placeholder_test.go`（2）
- `cloud_agent_pi_vision_wiring_test.go`（4）
- `cloud_agent_pi_policy_gate_test.go`（4）
- `cloud_agent_pi_prompt_contract_test.go`（5）
- `cloud_agent_run_read_path_test.go`（4）
- `cloud_agent_control_plane_read_test.go`（5）
- `cloud_agent_billing_invariant_test.go`（5）

`agent/test/` 下：`prompt-contract.test.ts`（5）、`bridge-wire-contract.test.ts`（+2）。

### 部署状态（仅本地 3000 canary）

阶段 1 曾滚动到 3000：backend 镜像 `29a5bfbf…` → **`79154206…`**，web/agent 未重建。
验证过：三容器 healthy、`/api/health` `ready:true` schema 41/41、真实 Agent 运行 `completed`、
内部协议 **0 条非 200**、终态对账 0 非终态运行 / 0 悬挂账单。

**注意**：阶段 2 的改动（含 `harnessHash`）**尚未部署**。若下次要滚动，
`agent/src/system-prompt.ts`、`bridge.ts`、`runner.ts` 都变了，**agent 镜像也要重建**。

---

## 6. 下一步（按依赖顺序）

1. **占位任务 + 首步原子换单 + 终态退款清扫**（Codex 给了实现边界与验收清单，见阶段状态 §2.8）。
   要点：占位任务用**专门的不可领取状态**（不能只靠未知 `operation`，`ClaimNextTask` 不看它）；
   首步在**同一事务**内重新报价 → 退回占位预留 → 创建真实首步任务；**当前 cleanup 只取消
   `queued/running` 任务，不会自动退款一个新设的占位状态 —— 这是必须补的代码**。
2. **合同版本 + `awaiting_first_step` 阶段 + 快照内容持久化**。
   注意：当前只做**漂移检测**，不存旧 Harness 正文 —— 所以"恢复时用旧提示重建"**仍然做不到**，
   只能"发现变更就停止"。这是诚实的降级，不是完整方案。
3. 之后才是阶段 3（Pi v3 持久层 + lease epoch fencing → 生产 runner 换 `createAgentSession`）、
   阶段 4（Provider 链 / M-01 usage 通路）、阶段 5（业务功能接回）、
   阶段 6（Go 结构化压缩映射 `firstKeptEntryId`）、阶段 7（删旧驱动 + 处理 7 个既有失败）。

---

## 7. 坑与约束（踩过的，别再踩）

- **9p 文件系统极慢**：`/mnt/d` 上 `import @earendil-works/pi-coding-agent` 要 **75 秒**
  （原生 ext4 659ms，115×）。跑 Node 测试请用镜像目录 `/home/a1/agent-mirror`
  （`rsync src/ test/ → 在那里 tsc + npm run build + node --test`），在 `/mnt/d` 上直接跑会很痛。
- **`DisallowUnknownFields`**：`/internal-agent` 路由用它解码，Go 结构体没声明的字段会让
  整个请求变成**空 400**（没有错误信息）。改 wire 必须**两侧同时**改，并在两侧各加 wire 测试。
- **夹具不真实**：`piAgentTestFixture` 造的运行**没有根任务**（它登记的是另一条 `pi-root-task`），
  且 ID 写死为 `pi-run-1` —— 而真实运行的 ID 必须由 `(userID, 幂等键)` 派生
  （`CreateCloudAgentRun` 的 `id := cloudAgentID(userID, req.IdempotencyKey)`）。
  身份自校验会拒绝不匹配的运行。**守卫是对的，夹具不真实** —— 用夹具写断言前先确认它模拟的是哪种形态。
- **`CleanupPending` 语义**：取消会置 `true` 并**同步**跑清理，清理成功时自己清回 `false`
  （`cloud_agent_recovery.go:116`）。所以"取消成功"的观测是 **false**。
  （看门狗路径断言 `true`，因为它只置标志、没有 ctx 去跑清理 —— 两者不矛盾。）
- **`MaxCharge` 是上限不是预留额**：真正预留的是订单报价 `AmountMicrocredits`；
  步进预算 `remaining = floor(MaxCredits×CreditScale) − Σ(本 run 各任务报价)`
  （`cloud_agent_runtime.go:2288-2296`，`CreditScale = 1_000_000`）。
  **不要**把 `MaxCharge` 当已预留金额。
- **财务/清理面全部以任务为入口**：`BillingOrdersByTaskIDs` 按 `task_id IN ?` 查、
  空列表直接返回空（`finance.go:601-605`）；`finishCloudAgentCleanup` 按
  `{run.ID, activeID, mediaID}` 遍历。任何"没有任务归属"的预留都会被这套逻辑漏掉。
- **`advanceCloudAgent`（`cloud_agent_runtime.go:933-1272`）已废弃且无生产调用方**，
  但上下文治理的全部入口（压缩、用量锚点、插话 drain、重试阶梯、预算闸）都只在这个区间里 ——
  接回业务功能时这是主要工作面。

---

## 8. 与 Codex 的协作记录

本轮通过 codex_bridge 让 `gpt-6-sol` 评审了阶段 2 的设计（read-only）。要点：

- 它的**结论一度改判**：第一轮读的是另一份检出、并假设"需要锁住整轮预算"，推荐 (b) run 级预留；
  第二轮按我给的路径读了本工作树后**改判为 (a) 占位任务**，理由是本仓库财务/清理面全部以任务为入口
  （我逐条在树内复核成立）。
- 它指出了一条我没想到的加固：**Go 必须在 `PiModelStep` 边界校验 Node 提交的提示没有删改服务端策略**
  —— 只修 Node 等于把强制层交给被校验方自己声明。已实现。
- 它给了四步迁移顺序，**第 1 步（兼容读路径）已完成**。
- 它明确划了取舍界线：**若产品要的是"建 run 就锁住整轮预算、以后各步从中扣"，则改选 (b)
  并接受那是一次财务结算改造。** 当前机制预留的是**首个任务的报价**，不是整轮预算。
  **这个产品问题仍然悬空** —— 若要推翻，会影响阶段 2 剩余的整个预授权实现。

---

## 9. 当前部署与数据状态

- 三容器 healthy：backend（约 1 小时）、agent（约 10 小时）、web（约 10 小时）。
- 本地库终态对账（阶段 1 部署后）：非终态运行 **0**、悬挂账单 **0**、`cleanup_pending` 残留 **0**。
- 测试期间对本地库只做过**追加**，未修改既有行。

## 10. 当前续接状态（2026-09-28）

本节更新并覆盖上文较早的阶段计数与披露方案。完整逐轮记录见 [`PI_MIGRATION_DAILY_2026-09-28.md`](./PI_MIGRATION_DAILY_2026-09-28.md)，现行阶段路线见 [`PI_CODING_AGENT_MIGRATION_ROADMAP.md`](./PI_CODING_AGENT_MIGRATION_ROADMAP.md)。

- 当前架构以 `pi-coding-agent@0.87.1` 的 `createAgentSession` / Pi session 为唯一 Agent 循环。旧 Go scheduler/driver 推进入口已删除；Go 业务服务继续承载身份/权限、画布/媒体、渠道模型、计费、持久化、恢复与清理。
- 工具按用户最新要求平铺注册：每轮开始注册全部满足权限与能力的具体工具（当前 24 个）；类别只是描述元数据。Pi `tool_call` hook 校验运行 registry 与当前模型批次准入，Go 在每次执行时重新校验授权和业务约束。不能恢复成母工具/子工具的层级披露。
- 工具回执按模型步骤 ID 限定；相同 provider call ID 在不同步骤复用时，不会读取或重放上一步 receipt。新增跨步骤重复 ID 与过期步骤拒绝用例。
- 自动化验证：Agent `bun run test` 78/78，另 source+dist 全套 155/155；Web typecheck 与 production build 通过，完整 web 测试 2108/2108（271 文件、11767 assertions）；Go `internal/app` 全量通过（CGO=1，452.941 秒）、Pi 定向测试通过、`internal/handler` 全量通过。
- 已按用户授权仅重建本机 `canvas-canary-3000` 的 backend/agent/web，保留 SQLite 数据卷；重建前备份并验证 `integrity_check=ok`。3000 首页、readiness、管理员登录/session、agent capabilities 和只读渠道接口均成功。没有请求上游模型或产生费用。
- 侧栏品牌名称后已有黄色底黑字无衬线 `canary` badge，专用前端测试通过，生产构建的实际 nginx JS/CSS 中也能找到标记。Computer Use 本轮不可用，因此 GUI 视觉和完整交互尚未验收；不得声称已验证。
- 尚需验收真实供应商流与 usage/stop 语义、媒体和审批后结算、PostgreSQL、不同用户并行/隔离、双 worker lease 抢占与跨进程故障恢复、计费与画布副作用 exactly-once、历史消息转换及 SSE 断线续传。实现接近完整不代表这些外部组合已经通过。
- 已提交 `8c58d0ca` 到 `codex/pi-agent-migration`，并打开 PR [#58](https://github.com/AetherNo2332/open-ai-canvas4AI/pull/58) → `canary`。GitHub 当前显示 `OPEN` / mergeable `CLEAN`，检查项为空（按用户先前要求跳过 CI）。工作区没有未提交文件；后续在此分支新增修复时仍须检查 diff，禁止加入 `.env*`、本机数据/备份或认证材料。
