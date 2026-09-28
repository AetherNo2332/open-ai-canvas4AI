# Pi Coding Agent 移植日记（2026-09-28）

## 工作区基线

- Repo：`D:\13537\open-ai-canvas-canary`
- 上一份记录中的 `645d1011` 和 104 项工作树计数已过期。本轮续接时实际检出分支 `canary`、HEAD `605e4243`，工作树混有此前迁移与 DeepSeek 改动；没有清理或覆盖它们。
- 本轮使用本机 `canvas-canary-3000` 做验收，仅重建 backend，保留 web、agent 与 SQLite 命名卷；未访问生产环境。

## 本日实现

1. Go `PiAgentSnapshot` 附带由服务端渠道/模型能力解析出的上下文窗和最大输出 token。Node Pi 模型不再填 1,000,000 / 32,768 假值；限额缺失时以 `FatalWorkerError` 失败关闭。
2. 新增内部 Pi 语义压缩开始、查询、提交协议。开始操作基于 Pi session、run、源 revision/leaf/digest 和压缩意图生成稳定 operation ID；重试复用同一 Go 计费任务，模型压缩失败使用既有服务端 fallback checkpoint。
3. Pi 的 `session_before_compact` hook 等待 Go 的结构化 checkpoint，并返回自定义 `CompactionResult`。Go commit 校验 summary/checkpoint、源 branch 身份和 session revision；事务内追加 Pi 压缩 entry，同时改写 Go canonical checkpoint 和事件。重复提交幂等；任一写入失败整笔回滚。
4. Pi 默认摘要不会在 Go 压缩失败后悄悄运行：hook 返回 cancel；Node 在下一次模型请求前检查并传播提交错误。
5. `645d1011` 已提交 Pi projector 的 `FirstKeptIndex` 保留字段。上述新压缩、模型限额与 Go route 测试代码仍在未提交工作树。

## 验证记录

- `node node_modules/typescript/bin/tsc -p tsconfig.json`（`agent/`）：通过。
- `node --test --test-timeout=20000 --test-force-exit dist/test/*.test.js`（`agent/`）：**64/64 通过**。
- 隔离测试容器：
  `docker run --rm --network none -v "D:\13537\open-ai-canvas-canary\backend:/workspace" -w /workspace -v canvas-gocache:/root/.cache/go-build -e CGO_ENABLED=1 open-ai-canvas-backend-test:sticky-tools go test ./internal/app ./internal/handler ./internal/service -count=1 -timeout 180s -run 'TestPi(ContextCompaction|AgentSnapshotCarriesGoResolvedModelLimits)|TestInternalAgentContextCompactionRoutesAreMounted'`
  结果：app、handler 通过；service 无测试文件。
- 单独复现 6 个旧 Go 测试的命令：
  `docker run --rm --network none -v "D:\13537\open-ai-canvas-canary\backend:/workspace" -w /workspace -v canvas-gocache:/root/.cache/go-build -e CGO_ENABLED=1 open-ai-canvas-backend-test:sticky-tools go test ./internal/app -count=1 -timeout 180s -run 'TestCloudAgentCompactionRequestsWhenTokenLineReached|TestCloudAgentCompactionFallsBackToBytesWithoutModelWindow|TestCloudAgentCancelledRunFinalizesInterruptedCompaction|TestCloudAgentCompactionCanRecoverContextFrameBudgetFailure|TestCloudAgentRunSurvivesTaskInputCompaction|TestCloudAgentLegacyRunSurvivesTaskInputCompaction'`
- 上述 6 项全部失败。其中 4 项 `TestCloudAgentCompaction*` 调用旧 Go 推进路径，要求旧式 `running` 状态/压缩暂停；Pi 首步合同此时保持 `queued/awaiting_first_step`。将这几项改成 Pi hook 和 Go operation 的行为验收，不要修改生产状态机迎合旧测试。
- `TestCloudAgentRunSurvivesTaskInputCompaction` 与 `TestCloudAgentLegacyRunSurvivesTaskInputCompaction` 将当前 holding 占位 task 手动设成已成功，并只填 task `result_json`，没有构造 Pi transcript / assistant final event；因此它们没有命中真实续聊数据形状。这两项不是通过删除断言解决：要建立真正旧版 `cloud_agent` 根任务夹具，验证历史结果导入；另建当前 Pi session continuation fixture，验证 assistant final entry 被带入下一轮。
- 本日没有运行全量 Go `internal/app` 套件；此前记录中的全量超时/基线失败仍然有效。本日也未验证 PostgreSQL、真实 provider、跨进程中断恢复、浏览器、SSE 断线重连。

## 追加：Pi 压缩操作重启恢复（本轮）

- 复审发现：Go 已创建的压缩 task 在 Node worker 重启后没有进入 Pi 快照。恢复 worker 可能把压缩 task 当普通 `PiModelStep` 处理，不能可靠地提交原压缩 entry。
- Go `PiAgentSnapshot` 现在回传待处理 compaction 的 operation ID、源 session revision/leaf 和原触发元数据；Node 对这个明确标识的操作只执行 GET 查询，不会再次 POST 创建计费任务。
- Runner 在处理普通恢复点和发起下一次模型步之前，恢复原 Pi v3 branch，通过锁定的 `AgentSession.compact()` 触发 inline hook，提交原 Go checkpoint 生成的 compaction entry。Go commit 成功后才继续发模型请求；operation/revision/leaf 不匹配时失败关闭。
- 新增真实 `AgentSession` 的 worker 重启恢复测试：断言恢复既有 Go operation、只提交一次，并且之后才有一个模型步；同时新增 bridge wire 和 Go snapshot 测试。
- 验证：`node node_modules/typescript/bin/tsc -p tsconfig.json` 通过；Node 全套 **67/67** 通过。一次性 `golang:1.25-alpine` 容器只读挂载 backend，定向 Go 快照测试通过。宿主机 Go 默认 `CGO_ENABLED=0`，所以 SQLite 测试不在宿主机直接运行；容器在退出时自动移除，没有接触现有 Canvas 容器。
- `npm test` 脚本本身因当前 shell 的 PATH 找不到 `tsc` 而未能启动；用 TypeScript 本地入口显式编译，再显式调用 Node test runner 完成了相同构建与全套测试。
- 本轮仍未验证 PostgreSQL、多 worker 跨进程故障注入、服务端真实压缩任务从 task 创建到提交的端到端费用不重复、浏览器或 SSE 续传。也没有部署或推送。

## 下阶段顺序

1. 先新增 Pi 压缩生命周期集成验收：开始→Go 任务→checkpoint result→Pi tree commit，覆盖取消、失败 fallback、重试、重复提交、revision 冲突与租约接管。
2. 将 4 个旧压缩单测拆为纯 Go 业务策略断言与真实 Pi/Go 编排断言；保留核心预算、轮次保留、fallback 和 terminal 语义。
3. 以真实旧 schema/根任务构造兼容 fixture，完成旧 run 导入/续聊与当前 Pi conversation 多轮续聊验收。
4. 继续迁入插话、视觉观察账本、停止原因与模型失败重试；再移除旧 Go Agent driver 及仅绑定旧实现的测试。
5. 完成 PostgreSQL、双 worker / 提交窗口崩溃注入、媒体/审批收尾、SSE 续传和真实浏览器验收。此阶段完成前不宣称移植完整。

## 追加：工具披露、真实 UI 调用和本地验收（本轮续接）

### 本轮实现与修复

- Pi 工具披露改成平铺注册：新轮开始时，通过 Pi extension 注册本轮满足权限和能力条件的具体工具；工具类别保留作分组元数据，不再充当母工具。工具调用 hook 检查本轮可见性与当前模型步骤实际获准的调用；Go 继续执行最终权限、用户归属、审批、参数、画布版本及批次预检。每轮重新建注册表。Harness schema 为 `cloud-agent-tools/v3`，当前列出 24 个具体工具；工具描述仍由 Markdown 提供。
- 模型步骤把上游可用用量指标沿 Go 任务结果传回 Pi；缺少的指标保持未知，不写成 0。计费权威仍是 Go 持久化的账单与 API 调用记录。
- Canvas bridge 的非成功响应现在会在错误中带上限长、去控制字符的公开 `msg` 字段，帮助区分权限门禁和状态冲突；不会把请求正文或凭证放入错误。
- 首次浏览器验收暴露了工具批次 403。原因是 Pi run 创建时为 `queued`，worker 领取时只取得 lease，但 `PiToolBatch` 要求 `running`。仓储领取事务现将 `queued` 原子转为 `running`，其他状态（特别是 `waiting_approval`）原样保留；新增租约回归测试。

### 测试和运行证据

- `cd agent && npm test`：**75/75 通过**（包含 TypeScript 编译）。
- `cd web && bun test`：**2086 通过、17 失败**，共 2103 项 / 270 文件。失败分布在 UI 样式与 DOM 合同、上传占位、提交工作流校验、钱包布局等既存前端测试；本轮没有改 web 产品代码，未把这些失败记为通过。
- `cd backend && go test ./internal/app -run '^TestPi' -count=1`：通过。另一次完整 `go test ./internal/app -count=1 -json` 返回 84 个失败事件：多数使用已退役 Go Agent loop 的旧 fixture/断言；已核到插件目录数量（99 vs 101）和资源清理断言等非 Pi 项。完整失败清单没有保存为文件，故不把 84 项都归类为迁移问题或都视为旧测试噪声。
- 修复后本机 `canvas-canary-3000` 的 `/api/health/ready` 为 HTTP 200，`ready=true`，schema `42/42`。构建版本 `v1.5.7.1+7aa9988`；该镜像编译元数据仍是 `commit=unknown`、`buildTime=unknown`。
- 浏览器端新建对话并实际请求 `canvas_get_state`：`POST /tool-batches` 与工具 `advance` 均返回 200；界面显示完成、画布 0 节点、只读且没有修改，并回显测试标记 `[PI-CANARY-FIXED]`。只读验收截图已在本轮留存于对话中。

### 本地 Compose 操作范围

- 仅重建并重启了 `canvas-canary-3000` 的 backend。`web` 与 `agent` 容器没有重建；SQLite 命名卷 `canvas-canary-3000_backend-data` 保持原挂载并保留数据，没有执行 `down` 或卷操作。
- 当前分支为 `canary`。本轮 Pi queued→running 修复、回归测试和状态记录已在本地提交，未推送；共享工作树其他迁移和 DeepSeek 修改保持原样、仍未提交。

### 仍未完成 / 未验证

- 旧 Go Agent 驱动和与之绑定的测试仍在仓库中；全量 `internal/app` 测试未通过。需要逐个把旧 loop 业务断言迁成 Pi 协议行为测试，保留真正旧版本数据导入兼容测试，并修复与 Agent 无关的基线失败。
- 本轮只通过一个配置的测试模型与一个只读画布工具做浏览器端到端验收；并未覆盖全部 24 个工具、真实生产上游渠道、媒体计费/审批路径。
- PostgreSQL、多账号并发、双 worker 抢占、跨进程故障注入、SSE 断线续传，以及完整历史 session/assistant entry 转换仍需验收。整体 Pi 移植尚未完成。

## 追加：前端复测与旧 Go 测试夹具迁移（续接）

- 本机 PowerShell 的 `PATH` 未包含 Bun，但已有 Bun 1.3.9 安装在 `C:\Users\13537\.bun\bin\bun.exe`；直接使用该已安装程序，无需下载或另装运行时。
- 前端 agent 面板相关测试 48/48 通过；`bun run typecheck` 与 `bun run build` 通过。完整 `bun test` 重跑仍为 2086 通过、17 失败（2103 项 / 270 文件）。失败涉及聊天样式、登录媒体样式、图片上传占位、Canvas 快捷键/视频渲染、部署镜像脚本、业务 API 请求约束、钱包布局和 Select 样式。当前未证明这 17 项都属于旧基线，需另行逐项核验；它们没有阻断 agent 定向用例或生产构建。
- 修正 Pi 续聊测试夹具：不再把 holding 预留任务的 `ResultJSON` 冒充助手答复；改为持久化 assistant final 事件与 canonical 消息。15 轮续聊用例现在校验前一轮的 user/assistant 正文，以及 30 条 Pi session entries。
- 修正旧兼容测试：读取与取消测试显式构造 `operation=cloud_agent` 的历史根任务；工具分派 AST 守卫指向现行 `executeCloudAgentToolCall`，并将仅供历史 Go loop 使用的类别分支排除在 Pi 具体工具 schema 集合之外。
- 定向 Go 验收：`TestCloudAgentToolTableMatchesRuntimeDispatch`、legacy run read/cancel、两项继续对话测试和 15 轮 Pi session 测试共 6 项通过。没有重跑耗时的全量 `internal/app` 套件；之前记录的全包 84 个失败仍未完成逐项分类。
- 本轮新增提交 `4bb800a6` 将第一版 Pi session 历史测试放入 canary；后续本节提及的 fixture 与守卫修复仍待单独审查和提交。本地分支未推送，未执行 Compose 部署。

## 追加：全量 Go 复测与 Pi 收尾协议验收

- 补齐 `CGO_ENABLED=1` 后重跑 `go test ./internal/app -count=1 -json`，完整用时 440.707 秒：**1805 项通过，73 个唯一测试名失败**（JSON 中 74 条 fail 记录包含包级汇总）。失败并非 CGO 环境缺失。
- 失败主因是旧测试仍调用已退役的 `advanceCloudAgentByID`，但新建运行以 Pi holding operation/lease 驱动；其中 50 个失败测试输出 `Agent 运行不存在`。另有旧 fixture 违反 Pi first-step checkpoint 状态约束、缺少 continuation model task 等问题。`TestPluginViewIncludesDocumentationForEveryOfficialProtocol` 和 `TestPurgeAssetsBatchSharedResourcesAndHistory` 属于 Agent 以外失败；没有把剩余失败统称为旧基线。
- 新增真实 Pi 协议收尾测试：`TestPiFinishRunPublishesOneFinalReply` 经 `PiToolBatch → PiToolAdvance` 验证 finish_run 仅发布一次 final；`TestPiFinishRunIsBlockedByPendingPlan` 验证待办未对账时工具回执包含 `completionBlocked`/`reconcile_plan`，运行保持中、只写一次阻塞事件且不发布 final。
- 新增的两项收尾协议测试通过；完整 `go test ./internal/app -run '^TestPi' -count=1` 也通过，用时 13.940 秒。
- 前端 agent 定向测试 **48/48**、typecheck 和 build 均通过；完整前端测试 **2086 通过、17 失败**（2103 项 / 270 文件）。17 项尚未逐一确认基线来源，不阻断 agent 专项测试和构建。
- 此轮未部署、未推送；共享工作区的其他迁移与 DeepSeek 修改仍保持未提交。当前 canary 本地提交包括 `4bb800a6`、`4bca2d8a`、`272c9789`；本节新增测试与记录需单独审查提交。

## 追加：全量 App 回归复测与 3000 本机 canary 部署

### 代码与提交

- 将旧 Go Agent loop 依赖的 app 测试夹具迁成 Pi step、tool receipt、`finish_run` 与 checkpoint 行为；补充模型失败恢复/取消路径断言、媒体目录默认值解析和多路由上下文预算交集测试。
- 本轮新增提交 `9a5ccc51 fix(agent): complete Pi app regression migration`，包含 18 个 Pi/app 文件；提交使用仓库最近提交的 `Codex Agent <codex@localhost>` 身份，仅通过单次 git 命令指定，没有修改全局 Git 配置。没有推送。
- 提交后仍有其他迁移与 DeepSeek 工作区改动未提交，保持原状。

### 验证

- `go test ./internal/app -count=1 -timeout 12m`：通过，用时 470.877 秒。
- 完整 `go test ./... -count=1` 上一轮中除 app 外各 Go 包通过；Windows 上 `internal/hostupdate` 的 POSIX 文件 mode 断言不成立（Windows 报 666、期望 640）。用既有 Linux 测试镜像重跑 `go test ./internal/hostupdate -count=1` 通过，确认该项是平台文件权限差异。CGO=1 与 GCC 已配置，当前不再是缺环境阻塞。
- web：完整 `bun test` **2107/2107 通过**、typecheck 通过、Vite production build 通过（约 21.87 秒；有现存大 chunk 警告）。
- Agent：`bun run test` 的 TypeScript build 与 Node tests **77/77 通过**。
- 三张本机 Docker 镜像 `open-ai-canvas-backend:local`、`open-ai-canvas-agent:local`、`open-ai-canvas-web:local` 全部成功构建。

### 本机 Compose 3000

- 仅更新 Compose 项目 `canvas-canary-3000` 的 backend、agent、web；未触碰 3030/prod、未删除容器/卷。保留原 `canvas-canary-3000_backend-data` 命名卷。
- backend 停止前将 `/data` 全量复制至 `C:\Users\13537\AppData\Local\Temp\canvas-canary-3000-predeploy-20260928`；SQLite backup API 生成的 `open_ai_canvas.predeploy.sqlite3` 执行 `PRAGMA integrity_check` 返回 `ok`，SHA-256 为 `fc79a73509d3800f4569bd6192c1cd0def9c253bce25ee1d5abb8fafd8c834c1`。备份含本机 canary 数据，留在本机临时目录。
- Compose 重建后 backend、web 健康，Agent 容器运行；`/api/health/ready` HTTP 200、`ready=true`、schema 42/42、active worker tasks 0。Web `/` HTTP 200。镜像 build metadata 显示版本 `v1.5.7.1+7aa9988`，commit/buildTime 仍为 `unknown`。
- admin 测试登录、认证 session、`/api/agent/capabilities` 均 HTTP 200；仅验证账号身份、角色和 capabilities，不启动模型请求或付费任务。
- Computer Use 的 GUI 浏览器验收此前因无法确认目标浏览器 URL 被安全机制拦截。本轮没有改用 Playwright 或其他 UI 自动化绕过；所以本地 API/首页 smoke test 已做，完整 GUI 与画布交互验收仍未完成。

### 剩余迁移验收

- 全量 browser/SSE 断线续传和多账号跨用户 UI 验收仍待完成；此轮无真实上游模型/图片/视频任务，因此供应商 usage、媒体计费与审批链路尚未实测。
- PostgreSQL、多 worker 跨进程 lease 抢占/故障注入、重放 exactly-once 组合证明，以及历史 Pi session 转换仍未完成。
- 旧 Go Agent scheduler/driver 仍未移除；必须先替换并验收其业务职责（审批、画布写入、预算/计费、媒体结算、取消、压缩、恢复与清理），不能以新 Pi loop 已运行代替这个退出门槛。
- 本机 Compose 使用的镜像元数据 commit/buildTime 为 unknown；虽然镜像按当前 canary 工作区构建，仍需补可追溯的镜像标签后再做发布验收。

## 追加：步骤级工具回执修复、品牌 Canary 标记与本地复部署

### 本轮修复与行为

- 修复 provider 重复使用 `tool_call_id` 时跨模型步骤误命中旧回执的问题。Pi 工具批次现在必须携带当前模型 task ID；Go 校验该 ID 与最近模型步骤一致，并将已接收批次绑定到步骤。恢复、推进和错误事件也带步骤 ID，历史回执只从最近一批 assistant tool-call 后读取。相同调用 ID 在另一模型步骤出现时不会复用上一轮结果；为旧的无步骤 ID in-flight 批次保留受控恢复兼容。
- Pi 工具仍为扁平注册：运行开始即注册快照中通过权限与能力过滤的所有具体工具，类别仅作说明元数据；Pi `tool_call` hook 检查运行内可见性和 Go 对当前模型批次的准入，Go 执行入口再次校验用户、权限、审批、参数、画布版本及幂等回执。每轮建立新的 registry。当前注册 24 项工具。
- 工作区侧栏品牌名后增加黄色底、黑色字、无衬线字体的 `canary` 标记；测试验证文本位置和样式。
- 移除旧 Go Agent 推进循环入口 `advanceCloudAgentByID`、`advanceCloudAgent`、`advanceCloudAgentReadBatch` 与旧 Go 上下文压缩推进入口。Go 继续保留画布业务、权限、计费、持久化和供 Pi worker 使用的共享运行时功能。

### 验证和本地 Compose 3000

- Agent `bun run build` 通过；`bun run test` 编译并运行产物测试 **78/78 通过**。另一次 source + dist `bun test` **155 通过、0 失败**。
- Web `bun run typecheck` 通过；完整 `bun test` **2108 通过、0 失败**，覆盖 271 个文件、11767 项断言。Canary 标记专用测试通过。
- Go `internal/app` 完整测试 `CGO_ENABLED=1 go test ./internal/app -count=1 -timeout 12m` **通过**，用时 452.941 秒；Pi 定向测试、`internal/handler` 全量测试，以及 `repository`、`protocol`、`service`、`prompts`、`cmd/server`、`database` 包测试通过。CGO/GCC 已在本机可用，之前注释的 `CGO_ENABLED=0` 阻塞已经解除。
- Compose 生产镜像构建成功。停 backend 前将本地 `canvas-canary-3000` 的 `/data` 复制到 `C:\Users\13537\AppData\Local\Temp\canvas-canary-3000-predeploy-20260928-160316`；SQLite backup 文件 `open_ai_canvas.predeploy.sqlite3` 的 `PRAGMA integrity_check` 为 `ok`，SHA-256 为 `12faedc6743938db3b9c2ce5db980bc5964887bee8877fff43ad1de5a74b7e45`。三个旧 image ID 已分别打上 `predeploy-20260928-160316` 标签供本机回滚；重建只作用于 `canvas-canary-3000` 的 backend、agent、web，数据卷保留，没有执行 `down` 或删除卷。
- 重建后运行镜像 ID：backend `sha256:b6e6289fdd2cdd2183174604aaf1052e3f0486d255cda198ab22ad8d4e64aa5f`、agent `sha256:955455dda5f3c66c0d024b90f448d04d1e54ab072f6c4908add06b2b93c95545`、web `sha256:984914f942f64cb732153b1a7e4f21b424d41c834be39ee342a978e1ed89e19f`。backend/web 健康，agent 运行；3000 首页 HTTP 200，`/api/health/ready` 返回 `ready=true`、schema `42/42`、active worker tasks `0`。构建元数据版本为 `v1.5.7.1+7aa9988`，commit/buildTime 仍是 `unknown`。
- 用用户提供的 admin 测试凭据完成登录，输出仅记录角色，不记录密码或会话值：登录/session、`/api/agent/capabilities`（24 工具）与只读 `/api/channels/system` 均 HTTP 200。没有调用模型，不产生上游请求或计费。
- 在实际 nginx 静态目录找到包含 badge 文案的 `user-layout-BRxOkMUD.js` 与样式 `application-BUMHJZ7k.css`。Computer Use 仍报 `nodeRepl.fetch request failed`，所以没有声称已完成浏览器视觉检查或完整画布交互验收；也没有改用 Playwright 绕过。

### 后续验收门槛

- 用 Computer Use 恢复后做登录后的实际 UI 视觉/交互验收，确认品牌标记位置和各主要 Agent 场景。
- 以真实上游渠道验证流式正文/推理、厂商 usage、模型停止原因与重试；覆盖看图、媒体生成、审批与结算。当前仅做了无模型 API smoke test，没有产生付费任务。
- 补 PostgreSQL schema/会话存储、不同账号并行隔离、多 worker lease 抢占、跨进程崩溃恢复、模型步骤/工具写入/计费 exactly-once 组合故障注入，以及 SSE 断线续传。
- 继续验证旧历史数据导入、历史会话转 Pi entries、旧版本完成记录续聊；补带 commit 与 buildTime 的可追溯容器镜像元数据。
- 本轮提交为 `8c58d0ca`，topic branch `codex/pi-agent-migration`，PR [#58](https://github.com/AetherNo2332/open-ai-canvas4AI/pull/58) 目标为 `canary`，GitHub 状态 `OPEN` / mergeable `CLEAN`。提交消息包含 `[skip ci]`，没有启动 CI。PR 包含此前本地迁移提交及本轮的 116 文件增量；未纳入 `.env*`、本机数据/备份或认证材料。
