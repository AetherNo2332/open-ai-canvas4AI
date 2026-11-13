# 架构治理 T0–T3 实施记录

对应计划：`2026-11-12-architecture-test-domain-governance.md`，本批只实现 T0–T3。首期整体仍是 T1–T7；T4–T7 待实施，T8–T10 是后续独立批次。

## T0：开发基线

- 目标为最新 fetch 后的 `upstream4ai/main`，目标与初始 HEAD 都为 `9fc248c34218cf882c9b3a189d9e74c5b54fc1cf`。
- 独立分支 `codex/architecture-t0-t3`，初始工作区干净。主工作区的用户修改及未跟踪文件未复制、覆盖或清理；只复制了用户指定的本计划文档。
- 工具：Node 26.1.0、Bun 1.3.9、Go 1.27.1 windows/amd64；项目要求 Go 1.25.0、Web packageManager bun@1.3.9。未切换或升级依赖；冻结锁文件安装 1450 包，113.87 秒。
- app 现有生产文件 503、测试文件 414。记忆文档仍在 app，轮询仍在 app；未迁移任何业务实现。
- 初始架构守卫：包内耗时 11.150 秒（包含首次扫描/缓存环境影响）；app 空用例编译入口 `go test -run '^$' ./internal/app` 为 0.676 秒。没有同环境 app 完整执行耗时和历史 CI 明细，不能据此宣称提速。
- 无新表、迁移、依赖升级或运行时 API 合同变化；不启动开发服务，不操作生产。

## T1：跨版本基线

- `CompareBaselines` 阻断同 PR 新增 allowance、增长 allowance；删除和下调允许。
- `BaselineMove` 一对一、不链式、不复用，保持规则和额度；命令结合 Git archive 的旧源码扫描和当前扫描，验证旧问题消失、目标存在且实际数值不增长。
- 命令必须提供可信非零 base；PR 用 PR base，push 用 before。手动/被调用 CI 使用检出提交第一父提交，明确打印来源；无法取得时失败。
- 保留现有 workspace 和 stale-baseline 检查，未修改 baseline.json 的额度。
- RED：新增接口不存在时测试编译失败；GREEN：架构包和命令测试通过，命令对实际目标基准通过。

## T2：统一验证计划（影子）

- `buildVerificationPlan` 为纯函数；声明式策略、反向依赖图和有限组合测试组分开。
- `go list -json` 提取 Imports/TestImports/XTestImports；未知代码、共享路径、包图缺失扩大范围。Git 基准或 diff 不可信则直接失败。
- Git 零字节 name-status 同时记录旧新路径；工作区模式包含已提交差异、已跟踪修改和未忽略新文件；显式 head 不混入工作区。
- runner 新增 `--mode`、`--plan-only`，默认 full，保留 jobs。实际门禁仍使用原完整相关执行方式；影子计划不用于跳过 CI。
- 本地保存 JSON，CI 上传 artifact；docs 门禁保留。plan-only 只收集差异和包图，不运行测试。
- RED：模块缺失；GREEN：选择、Git fixture、失败扩大和路径测试通过。

## T3：Web 调度

- 原 pretest 的八文件组：tiptap 版本、admin UI、announcement prompt、password reset、OSS settings、channel model form、price tier form、admin logical models；包含源码合同与真实模块导入回归。历史选择每个文件的具体隔离动机缺乏可复核证据，因此保守保持其共同进程边界，不合并到普通集合。
- canvas-grid 导入 React/SSR 及真实 CanvasViewport，保留原独立进程。
- welcome-title 已通过 harness 子进程隔离；此次保留包装测试。
- `bun run test` 调用唯一调度器；9 个隔离文件从普通集合排除，新增匹配 Bun 命名的文件自动发现。专项 `bun test <文件>` 保持可用。
- RED：调度模块不存在；GREEN：唯一分组、新文件发现、缺隔离文件、失败传播测试通过。

## 设计裁决

- 本次范围是 T0–T3，不以首期 T1–T7 完成作为本批退出条件；若理解错误，会低估后续业务迁移工作。
- Windows 不直接执行 skill 的 Bash 工作流包装，使用同等独立 worktree、测试与本记录；若记录不完整，会增加后续接手成本。
- 未选择/未执行的检查只作为计划或待验收，不记作通过。没有同环境基线，不承诺性能比例。

## 最终验证

- `node --test scripts/verification-plan.test.mjs web/scripts/run-tests.test.mjs`：12/12 通过，覆盖 Git fixture 和失败传播。
- `go test -count=1 ./internal/architecture ./cmd/check-architecture-baseline`：通过；最后记录包内 1.332 / 1.482 秒。命令测试含真实 Git fixture 的合法迁移、源仍存在拒绝及基准缺失拒绝。
- `go vet ./internal/architecture ./cmd/check-architecture-baseline`：通过；实际目标基准的 `go run ./cmd/check-architecture-baseline --base <目标SHA>` 通过。
- 真实干净检出的显式 head 计划与工作区 plan-only 已执行；后者不启动测试。输出保存于本地缓存，内容只含路径、规则和命令。
- `bun run test` 完整入口已执行一次：隔离八文件组 61 pass / 0 fail，2.73 秒；canvas-grid 4 pass / 0 fail，0.38 秒；普通 512 文件为 3470 pass / 5 skip / 9 fail，268.61 秒。共 521 个发现文件，三个组互斥。
- 失败分类：5 个 ci-image-release 测试 Bash 返回 127，原工作区同一测试也为 7 pass / 5 fail；2 个 http-ownership 测试把 Windows 反斜杠相对路径与正斜杠白名单比较，单独执行仍失败；2 个已有 harness（canvas-bottom-controls、canvas-reference-migration）全量时 exitCode=null，单独重跑通过，进程干扰/资源原因尚未确证。未删除测试、扩大超时或伪造成功。
- 普通集合的 5 skip 需要真实后端，未视为通过。完整 Web 门禁仍未通过，不能作为可推送证明。
- 独立只读评审发现并修复：工作区回改抵消已提交差异的漏选（新增 fixture RED→GREEN）；full 影子计划遗漏部分门禁（补充格式、vet、工具合同、lint、changelog、部署命令等声明）。评审未提出删除或绕过失败测试。
- CI、支付双架构、PostgreSQL/Redis、真实 Agent smoke、浏览器 E2E 和 docs 站完整构建尚未在本批本地覆盖；本批未推送，也未宣称通过完整推送前脚本。

## Web 失败收口（2026-11-12 第二批）

在原 9 个失败不掩盖、不删除测试的前提下修复，全部保留原有断言与覆盖：

1. `ci-image-release.test.mjs` 5 个失败：根因是 `spawnSync("bash")` 裸名解析到 System32 的 WSL bash，无法执行 Windows 路径下的仓库脚本（exit 127）。RED：把 PATH 限定为 System32 后专项复现 7 pass / 5 fail，与交接记录一致。修复：按 PATH 顺序探测 `bash.exe` 并用 `uname -s` 校验 MINGW/MSYS/CYGWIN（拒绝 WSL 入口），再以 Git for Windows 默认安装位置兜底；找不到 POSIX bash 时保留裸名让失败如实出现。GREEN：正常 PATH 与 System32-only PATH 两种环境均 12/12。
2. `http-ownership.test.ts` 2 个失败：`relative()` 在 Windows 返回反斜杠，与正斜杠白名单比较永不匹配。RED 专项复现。修复：新增 `toPosix` 统一规范化后再比较；第三个测试的排除路径同样存在该问题，一并修正（POSIX 下 toPosix 为恒等，不改变原断言）。
3. `canvas-bottom-controls` / `canvas-reference-migration` 全量时 `exitCode=null`（16ms 内返回、子进程无输出）：确证为 Bun 1.3.2–1.3.14 已知缺陷 oven-sh/bun#33932——`spawnSync({timeout})` 的计时器用缓存循环时钟，父进程空闲后有效超时被清零并立即杀掉子进程；本机 bun 1.3.9 实证：空闲 21 秒后 sync 调用 6ms 即被 SIGTERM。专项立即运行不触发，因此单独重跑通过。修复：新增 `web/test/run-bun-harness.ts`（异步 `Bun.spawn` + 手动计时器 + 超时标记），迁移全部 23 个同型调用点；本机实证空闲 21 秒后异步包装正常返回，避免下次全量换成其他文件闪失败。
- 验证：`bunx tsc --noEmit` 通过；改动文件 Prettier 与 eslint 通过；`bun run test` 完整入口 EXIT=0——regressions 8 文件 0.30s、canvas-grid 1 文件 0.12s、普通 512 文件 3479 pass / 5 skip / 0 fail / 190.89s（5 skip 仍需真实后端，不计通过）。上次同集合 268.61s 与本次 190.89s 环境不同，不据此宣称提速。
- 新增 helper `web/test/run-bun-harness.ts` 不匹配测试发现模式，普通集合仍为 512 文件。
- 本批未运行完整推送前 runner；Linux CI、docs 构建、支付双架构等仍未覆盖。待 CI 在 Linux 确认三组与普通集合行为一致。

## T4–T8 实施（2026-11-12 第三批）

在原独立工作区继续，每任务一个提交；未推送，T7 门禁切换未启用。

### T4 长期记忆文档（4ef529122）

- `internal/agentmemory/document`：ApplyOperations/Diff/Summary/Inverse 纯规则与 Service（Ensure/Document/UpdateSettings/Update/Revisions/Undo/Decide/AgentWrite），WriteOutcome 与 CapacityDetail 类型化；容量按整批结果检查、修订保留、call 重放语义保持。
- app 保留 run 快照冻结/注入（snapshotText/nudge/attach）、工具事件与容量回执拼装、管理员跨用户聚合、类型别名；`cloud_agent_memory_document_api.go` 收敛为纯门面（getter 入 service.go、Admin 聚合独立文件）。
- 守卫：agentmemory 根包禁 import repository/database/document（fixture RED→GREEN）；原 app 记忆专项 6 个测试迁入新包（fixture 只建文档/修订/旧经验三模型）。
- 验证：agentmemory/document/architecture 包测试、app 记忆专项 5/5（含 run 冻结组合 2.61s）、handler Memory 专项、baseline unchanged。

### T5 异步轮询（b7c5453f7）

- `internal/generation/polling`：Run/Download 循环、分类计数（NotFound/Malformed/Transient）、Retry-After、Translate 适配钩子；稳定错误事实 HTTPStatusError/ResponseDecodeError/RetryableSignal/UnretryableSignal/DownloadError；IsTransientTransport 导出。
- app：defaultVideoPollPolicy 构造 + translateVideoPollError（providerHTTPError→HTTPStatusError、熔断/槽位→RetryableSignal、mediaRecovery→UnretryableSignal）+ notify 日志接线测试（warn/info 写 TaskLog）；原 13 个轮询测试迁 polling 包。
- 实证记录：TestVideoPollContinuesAfterConnectionResetAndEOF 在 Windows 本机失败为迁移前平台差异（a393d137f 同样失败，WSAECONNRESET 与 Linux syscall.ECONNRESET 值不同），Linux CI 不受影响；已记 pending-test。

### T6 职责与协议守卫（fe9fc507b）

- responsibilities.go：facadeFiles 精确登记（当前仅记忆 API 门面），类型别名/直接引用初始化/单 return 透传之外一律报告；handler 禁 import gorm/gorm driver/database/sql。
- handler/ecommerce_agent.go 摘除 gorm：`service.EcommerceAgentNotFound` 归属记录不存在语义，404 响应不变。
- 协议负例：document 包改名文件仍受 map 约束；Box[map[string]any]、点导入、跨文件别名、合法递归类型各建 fixture；确认展开封闭性下递归类型不误报（曾把 seen 环改为报违规导致 5 个真实误报，对照实验后还原原语义）。

### T7 影子对照（本地回放部分）

- 本批真实 diff（9fc248c..HEAD）回放：T2 的 workflow/脚本改动正确触发 global 广覆盖；app 未映射文件（轮询适配层）→ backend:full；新包+接线混合 → full。符合"app 仅对审核过的文件集精确映射，其余全量"。
- 纯函数级验证：纯 document 包改动只选 document 包测试 + 反向闭包（app 记忆专项 pattern、handler、service、cmd 依赖方）+ architecture + baseline，**不含 backend:full**；加一个未映射 app 文件即扩大 full。回放输出存 `.local/cache/verify/replay-t4-t7-plan.json`。
- 未实施（记 pending-test 待独立 PR）：runner 按 selectedChecks 执行、本地默认 affected、quality.yml affected 选择与汇总校验、切换前后同环境数据输出。

### T8 任务计费与终态（6152e2951）

- `internal/taskruntime`：BillingCoordinator（窄持久化合同、ErrBillingReview、原因脱敏/截断）、TerminalCoordinator（导出接口 + capabilityOf/rejectedSubmission 注入 + 可选探测 LeaseReleaser/AudioDurationBilling/VideoProcessingSettlement）、RecordTaskDiagnostic/SafeTaskDiagnosticMessage、RedactUpstreamAddress。
- `generation.TerminalError` 承接 providerTaskTerminalError（app 别名）；`service.EcommerceAgentNotFound` 前置于 T6。
- app：task_billing/task_terminal 原方法名转发；serviceRuntimeAdapter 显式接入文本回放/日志/产物登记/音频视频结算；worker/lifecycle 编排暂留 app。
- 验证：13 个 stub 测试迁 taskruntime 全过；app Task/Worker/Billing 专项 11.3s 通过；generation/architecture 包测试、baseline unchanged、gofmt/vet 通过。

### 推送前门禁与回归修复（f2c8dfd5d）

- 完整门禁 `bun scripts/verify-before-push.mjs --base upstream4ai/main`（full）：13 项中 web 4 项、backend baseline/gofmt/test-rest/list-app/verification:tests 与 app-2 分片通过；app-1/3/4 分片各 1–2 个失败。
- 分片失败共 6 个测试。其一为迁移回归：声明式非视频策略（图片/音频）构造 policy 漏设 Translate，503（providerHTTPError）不被 polling 稳定错误事实识别，fail-fast 提前返回（TestAsyncImagePollRetryLimitAndNotFoundStillFailFast）。修复：declarativeProtocolPollPolicy 补 translateVideoPollError（f2c8dfd5d），该测试恢复。
- 其余 5 个（TestAsyncImagePollRetriesTransientErrorsWhenEnabled、TestVideoPollContinuesAfterConnectionResetAndEOF、SettingsEncryptionKey Publication/LinkFallback/ConcurrentFirstUse）在迁移前基线 a393d137f 逐一实证同样失败：连接中断类为 WSAECONNRESET 与 Linux syscall.ECONNRESET 值不同的平台差异；加密密钥 3 个为 Windows 文件权限语义（666 vs 600）。修复后重跑 4 个 app 分片仅剩这 5 个失败，app-2 全绿。
- 未重跑项：web 4 项与 backend 非 app 检查在门禁中已通过且修复不涉及；按纪律不机械重跑。上述平台差异由 Linux CI 兜底，此处不宣称 CI 已通过。
