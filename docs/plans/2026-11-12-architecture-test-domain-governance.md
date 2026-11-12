# 架构守卫、测试分层与业务职责拆分开发计划

> 状态：PR134 包含 T0–T8、审查修复及 T7 实际门禁切换；T9/T10 的实现单独保留，按用户要求在 PR134 CI 全绿后推送后续 PR。本轮以 CI 复核本地 Windows/网络阻断的验收项，见 [PR134 修复与拆分记录](./2026-11-12-pr134-review-fixes.md)。勾选表示实现步骤已执行，不表示失败或跳过的测试通过。
> 执行约定：开始实现时使用 executing-plans，按任务与验收条件推进；如采用子代理，需遵守当时的用户授权及仓库规则，各自在独立工作区工作。本文不授权部署或生产操作。

**Goal：** 建立“业务职责迁移、行为测试迁移、架构约束、验证范围选择”同一交付闭环，降低局部修改的反馈成本，并维持关键业务不变量。

**Architecture：** app 保留组合根、跨域编排与适配；先迁出记忆文档和异步轮询，再处理计费、终态、授权与重复预演规则。架构守卫维持依赖方向与基线单向下降；本地与 CI 共用验证计划，先观察，后逐步启用选择执行。

**Tech Stack：** 当前 backend/go.mod 要求 Go 1.25.0；沿用现有 Go AST、Gin/GORM、SQLite/PostgreSQL/Redis、Node/Bun 和 GitHub Actions，不为本轮升级依赖或引入测试平台。

**Spec：** 本文第 1–4 节固化本次讨论的范围与设计，第 5 节为实施任务，第 6–8 节为交付和验收。所有文件路径相对仓库根。

## 1. 源码依据与实施前置

- 已 fetch 的 upstream4ai/main：9fc248c34；本地分析 HEAD：b5f7f65ee。
- 两者 internal/app、internal/architecture、verify-before-push.mjs 无差异；远端 quality.yml 另有 docs 门禁，实施必须基于最新目标分支，保留它。
- 当前 app：503 个生产 Go 文件、414 个测试文件、130366 生产物理行；Agent 前缀组为 180 文件、44672 行。数字仅为静态统计，不作为强制下降指标。
- Service 在 backend/internal/app/service.go 组合多域能力；task_worker/task_lifecycle 仍持完整 Service；task_billing 已使用窄 repository 合同。
- generation.Engine 尚未接管执行；generation/types.go 与 app/provider.go 的类型字段已经漂移，不得直接替换。
- 数据库迁移依赖 agentmemory 根包；repository 又依赖 database。禁止在 agentmemory 根包新增 repository 依赖。
- 当前工作区已有用户修改及未跟踪文件。实现时从最新目标分支建立独立工作区，禁止宽范围暂存、覆盖或清理当前工作区。
- 每个任务开始时复核相关源码、规则和工具版本。若已有后续实现，按实际差异调整，不重复建设。

## 2. Global Constraints

1. 保留外部 API、错误码、权限、数据归属和业务语义；迁包与行为变更分开提交。
2. 不引入新微服务、新数据库表或数据迁移；如后续发现必须变更，另行形成数据库方案，不夹带。
3. 规模上限保持文件 600 行、函数 120 行、复杂度 20；依赖违规不可豁免。
4. 不新增或提高存量基线额度；合法迁移需显式一对一映射，不能以重建基线接纳新问题。
5. 新域不得 import app/service，也不得经 context、大接口、反射或回调集合取得完整 Service。
6. 不把共享纯规则包改成依赖 repository 的业务包；特别保护 agentmemory 与 database 的依赖方向。
7. 测试优先迁移现有行为覆盖，新增测试只用于新增规则、遗漏行为与关键负例，不为透传方法逐个补镜像测试。
8. 不启动开发服务器、不访问生产、不使用真实供应商凭据。轮询与错误验证采用本地确定性服务/回调。
9. 当前推送要求在任务 T7 正式切换前保持有效；迭代用专项测试，推送前按当前规则执行完整相关门禁。
10. 不取消完整回归入口；不把跳过、环境缺失或选择器故障记为“验证通过”。
11. 文档及 CLI 输出不得暴露 URL 凭据、Token、Cookie、用户内容；仅记录路径、规则、测试名和耗时。
12. 每个迁移单元同时交付实现、测试、边界规则、验证映射、代码地图与待验证说明。

## 3. 目标职责与验证模型

### 3.1 业务归属

| 单元 | 归属 | app 保留内容 |
| --- | --- | --- |
| 长期记忆文档 | internal/agentmemory/document | run 快照冻结/注入、工具事件适配 |
| 上游异步轮询 | internal/generation/polling | 默认配置、日志/进度通知适配、错误转换 |
| 任务计费与终态 | internal/taskruntime | 构造装配、跨域 replay/outputs/日志适配、worker 编排 |
| Worker 授权 | internal/identity/worker | 环境配置读取、管理入口与启动接线 |
| 预演纯编辑规则 | internal/canvas/previs | 两端审批展示、事务、CAS、回执与事件 |
| 生成完整执行、资源生命周期、项目删除 | 后续专项设计 | 本轮不整体搬迁 |

新包是建议的实施命名；若最新源码已有同职责包，合并到已有归属，禁止平行实现。已有 service 再导出和 app 对外入口保留。

### 3.2 三种验证模式

- fast：开发反馈；运行变更单元测试和必要接线测试，显示未覆盖内容，不能作为正式交付证明。
- affected：按受影响模块、调用方、合同和风险规则选择；T7 之前仅用于影子对照。
- full：完整相关门禁；主干、发布及共享核心/未知影响变化使用，始终可显式调用。

新包变化可能使 app 重新编译。目标是减少无关测试执行与夹具初始化，不承诺消除依赖编译。

### 3.3 统一验证计划合同

新增 scripts/verification-plan.mjs，导出纯函数：
- buildVerificationPlan(input) -> plan。
- input：mode、base/head、changes（status/path/oldPath）、goPackages（importPath/imports/testImports/xTestImports）、policy。
- plan：schemaVersion=1、mode、base/head、selectedChecks[]、reasons[]、unclassifiedPaths[]、notCovered[]。
- selectedChecks 每项包含唯一 id、cwd、cmd、args、依赖任务 ID；runner 使用 argv 执行，不拼 shell。
- CLI：bun scripts/verification-plan.mjs --base <ref> [--head <ref>] --mode fast|affected|full --json；CI 显式提供 --head，省略时收集当前工作区。
- CLI 默认收集已提交差异、已跟踪工作区修改和未忽略新文件；CI 使用明确 base/head 的干净检出，不混入工作区。
- 删除和重命名同时考虑旧路径；处理 Git 路径使用零字节分隔。
- Git 引用缺失、包图读取失败、未分类代码改动均扩大到对应完整检查并说明原因；无法建立可信 diff 时返回失败，不返回空计划。
- 纯文档仅缩小普通说明改动；API、部署、权限、生成配置型 MDX/脚本按合同映射扩大检查。
- workflow、选择器、公共构造、go.mod/go.sum、数据库、共享任务状态等变化默认触发广覆盖。仅在有明确规则后细化。
- 不使用一套泛化正则猜 app 内所有影响。app 仅对经过审核的有限文件集与测试组做精确映射，其他 app 变化保留全量 app 测试。

新增 scripts/verification-policy.mjs 存放上述模块归属、关键路径、组合测试组和默认扩大规则；它只存声明，不执行测试。

## 4. Review Focus

1. 重命名或删除导致漏选：同时读取旧/新路径；T2 的 RenameAndDeleteSelectBothOwners 测试覆盖。
2. 基线迁移被当成扩额通道：一对一、旧债务消失、目标实际存在、同规则且不增额；T1 的迁移负例覆盖。
3. 记忆幂等与审批越权：区分 user/run/call、容量按整批结果计算、撤销不得覆盖后续编辑；T4 迁移原测试并补未覆盖的 CAS 场景。
4. 轮询取消/终态被误重试：维持现有错误分类、重试上限、Retry-After、deadline；T5 迁移现有测试。
5. CI 将 skip 或依赖服务缺失显示为成功：汇总任务验证“计划任务集合=实际成功集合”；T7 的缺任务、缺服务和失败传播测试覆盖。

## 5. 实施任务

### T0：建立独立工作区与可复核记录

**依赖：** 无。**交付：** 不修改业务的开发基线。

**涉及：** AGENTS.md、backend/go.mod、scripts/verify-before-push.mjs、.github/workflows/quality.yml、本文。

- [x] 从最新目标分支建立独立工作区和 codex/ 前缀分支；记录 HEAD、目标 SHA、状态及工具版本。
- [x] 复查 app 统计、现有域归属与用户已有修改，不复制本地数据和配置。
- [x] 利用已有 CI 耗时或执行一次必要基线，记录 app 编译/执行、架构守卫、Web 隔离/普通测试的耗时。没有数据则明确缺失，不虚构提速基准。
- [x] 建立实现范围清单：首期 T1–T7；T8–T10 为后续独立批次。
- [x] 确认不升级依赖、不新增表、不改变运行合同。

### T1：补齐跨版本基线约束

**依赖：** T0。**建议 PR：** PR-A，与必要 CI 接线一起交付。

**创建：**
- backend/internal/architecture/baseline_changes.go
- backend/internal/architecture/baseline_changes_test.go
- backend/cmd/check-architecture-baseline/main.go
- backend/internal/architecture/baseline-moves.json（初始空映射）

**修改：** check.go、README.md、quality.yml、verify-before-push.mjs、AGENTS.md。

**合同：**
- CompareBaselines(previous Baseline, current Baseline, moves []BaselineMove) []Finding。
- BaselineMove：From、To、Reason；一对一，不能链式映射、复用源或目标。
- 命令参数 --base <ref>；从明确基准读取旧基线，以工作区/CI 当前文件为新基线。必要 SHA 由调用方提供，不能回退到空基线。
- 基线比较不单独替代现有 Check；命令同时核对迁移源旧问题和目标当前扫描结果。
- 映射必须经代码审查，不能仅凭映射声明证明语义等价。只应用于本次基准差异，不积累永久豁免。

- [x] 先写失败测试：同 PR 新问题+新增 allowance、代码和额度同时增长、删除/降低允许、重复/链式迁移拒绝。
- [x] 补迁移负例：源仍存在、目标不存在、规则变化、额度增长、一源多目标、基准缺失。
- [x] 实现比较、命令与源码扫描联检；保留现有规模和 stale_baseline 行为。
- [x] 接入本地与 CI 的同一明确基准。PR 使用 PR base；push 使用实际 before；全零或缺失时要求可解释的可信基准或失败。
- [x] 增加路径/符号/规则/数值诊断，不引入自动接纳开关。
- [x] 验证：backend 中 go test -count=1 ./internal/architecture ./cmd/check-architecture-baseline。
- [x] 验收：当前基线通过；所有扩额负例失败；合法迁移可复核，代码增长不可隐藏。

### T2：建立统一验证计划，先影子运行

**依赖：** T0；与 T1 可独立开发，但合并前串行验证。**建议 PR：** PR-B。

**创建：**
- scripts/verification-plan.mjs
- scripts/verification-policy.mjs
- scripts/verification-plan.test.mjs

**修改：** scripts/verify-before-push.mjs、quality.yml、AGENTS.md。

**接口：** 使用第 3.3 节合同；现有 runner 保留 --jobs 和现有检查执行方式，新增 --mode、--plan-only；初始默认 full。

- [x] 先写计划选择测试：普通文档、已映射新包、共享构造、测试文件、workflow、未知 app 文件、混合 Web/Backend 改动。
- [x] 写基准缺失、Git 失败、Go 包图失败、文件名空格、重命名、删除、未跟踪文件测试。
- [x] 通过 go list 提取 Imports/TestImports/XTestImports；图读取错误必须显式处理。复用工具链，不自建 Go import 文本正则分析器。
- [x] 实现反向依赖计算与组合测试映射；命令去重，依赖关系确定，输出稳定。
- [x] 影子计划作为 artifact 保存，实际仍按现有完整门禁执行；暂不自动跳过 CI 任务。
- [x] 验证：node --test scripts/verification-plan.test.mjs；在真实干净检出及临时 Git fixture 上核对 JSON 计划，plan-only 不启动测试。
- [x] 验收：未知/失败路径不会得到空成功计划；本地与 CI 对相同 diff 产生等价任务集合和理由。

### T3：清理 Web 重复测试，并固定隔离语义

**依赖：** T2 的计划合同；可与业务迁移分别提交。**建议 PR：** PR-C。

**创建：** web/scripts/run-tests.mjs、web/scripts/run-tests.test.mjs。
**修改：** web/package.json、scripts/verification-policy.mjs、相关验证说明。

- [x] 清点 pretest 的 9 个具名文件为何隔离；其中 canvas-grid-viewport 独立进程安排先保留。
- [x] 将脚本声明为唯一测试调度入口，取消 pretest 与默认发现的重叠；普通集合从实际测试文件中排除已隔离集合。
- [x] 保留原隔离组的进程边界，不在本任务顺手合并其执行进程。
- [x] 先写调度测试：每个发现文件恰好归属一个组、无重复/遗漏、子进程失败向上传播、新测试文件默认被发现。
- [x] 保留 bun run test 作为完整 Web 入口；专项测试仍允许直接 bun test <文件>，避免为低风险修改强制运行调度器全量。
- [x] 验证：调度器测试、现有隔离测试、普通完整集合各执行一次；存在干扰时报告并保持隔离，不删除测试掩盖问题。
- [x] 验收：完整入口覆盖原有集合，每个文件仅执行一次；既有独立进程语义保持。
- [x] 同步记录时间，不承诺未经测量的提速比例。

### T4：迁移长期记忆文档，完成首个业务闭环

**依赖：** T1、T2。**建议 PR：** PR-D。

**创建：**
- backend/internal/agentmemory/document/service.go
- backend/internal/agentmemory/document/types.go
- backend/internal/agentmemory/document/edit.go
- backend/internal/agentmemory/document/revisions.go
- backend/internal/agentmemory/document/service_test.go

**修改/迁移：**
- app/cloud_agent_memory_document.go、_api.go、_write.go（移出文档行为，保留 app 适配）
- app/cloud_agent_tools_memory.go、service.go、cloud_agent_memory_document_test.go、cloud_agent_memory_tool_test.go
- architecture 的规则测试、baseline.json 和必要的 baseline-moves.json
- scripts/verification-policy.mjs、backend/code-map.mdx、progress/pending-test.mdx

**合同：**
- document.New(repo *repository.Repository) *document.Service；普通文档操作沿用现有 userID/请求/返回语义。
- 请求与视图沿用现有 AgentMemoryDocumentView、AgentMemoryDocUpdateRequest 等类型名和 JSON 字段，归 document 所有；app 用类型别名保持外部导入合同。
- 写入结果为类型化 WriteOutcome，保留 Status、RevisionID、Summary、Changes、Duplicates、CharCount、Capacity；容量错误返回类型化明细。
- 文档模块不接受 canonicalAgentRequest、CloudAgent runtime 或完整 Service；工具事件 map 的拼装暂留 app。
- 管理员跨用户聚合暂留 app，不迁入 document 的普通用户操作面；管理入口维持现有鉴权合同，不新增绕过入口。普通操作保持 owner 校验和可信 userID 来源。实施时核对 handler 与 app 的管理权限检查；如发现原路径只靠 handler 鉴权，单独列为权限缺口，不宣称迁包已经修复。

- [x] 将原有容量、泄漏/规则覆盖拒绝、旧 lesson 导入、review、撤销冲突、run 内重放测试迁入新包；先确认迁移前行为。
- [x] 新包 fixture 只迁移其实际使用模型：文档、修订、旧 lesson，以及需要的用户；不调用 creationTestService 或 database.Models()。
- [x] 迁移编辑、文档持久化、CAS、修订和审批实现；保留存量事务、重试上限与错误语义。
- [x] app 保留 run 冻结、快照注入、工具可见性和事件适配。逐一核对调用方和导出类型，不批量导出私有 helper。
- [x] 增加守卫正反例：document 不可 import app/service；agentmemory 根不可 import document/repository/database；公开合同不得暴露非类型化 map。
- [x] 更新验证映射：新包变化选中包测试和已声明组合测试；文档合同/app 接线/模型变化扩大范围。
- [x] 验证：go test -count=1 ./internal/agentmemory/... ./internal/architecture；app 专项匹配 ^(TestUserMemory|TestAgentMemory|TestMemory|TestCloudAgentRunFreezesMemory)。
- [x] 验收：所有相关原测试找到迁移后归属；不存在导入环；新增包不初始化完整创作运行；app 快照/工具组合测试通过。

### T5：迁移异步轮询，切断隐藏的 Service 依赖

**依赖：** T1、T2；建议在 T4 证明闭环后实施。**建议 PR：** PR-E。

**创建：**
- backend/internal/generation/polling/poll.go
- backend/internal/generation/polling/errors.go
- backend/internal/generation/polling/poll_test.go

**修改/迁移：** app/provider_video_polling.go、_test.go、provider_http_client.go、provider_errors.go；verification-policy.mjs、代码地图和待验证说明。

**合同：**
- 导出 PollPolicy/PollEvent/PollOutcome；Run(ctx, taskID, policy, query) 返回与现有轮询等价的结果和错误。
- 保留初始间隔 30 秒、MaxNotFoundMisses=3、MaxMalformedResponses=3、MaxDownloadTries=3，以及现有 RetryTransient/TransientRetryLimit 语义。
- query、Sleep、Notify 使用原有回调语义；app 构造默认策略、记录日志和转换当前 HTTP 错误。polling 不读取 providerAnalyticsContext.Service。
- 不使用尚未统一的 generation/types.go 替换整份 provider 输入，也不创建第二条生成提交路径。

- [x] 迁移现有 TestVideoPoll* / TestVideoDownload* 测试；维持实际 Retry-After、取消、错误计数重置和重试耗尽断言。
- [x] 明确 HTTP/JSON/断流错误分类的归属：polling 拥有所需的稳定错误事实，app 适配现有错误，不解析展示文案来恢复业务类别。
- [x] 迁移循环、等待和下载重试；保留错误链及 deadline 传播。
- [x] 增加 app 接线测试：通知抵达现有日志/进度入口，模块不需要 Service context。
- [x] 更新守卫和验证映射；新包不得 import app/service/handler。
- [x] 验证：go test -count=1 ./internal/generation/... ./internal/architecture；app 中匹配 ^(TestVideoPoll|TestVideoDownload) 及新接线测试。
- [x] 验收：取消、鉴权失败和重试耗尽均不多发请求；通知和错误链保持；新包独立测试不依赖 app。

### T6：补职责与协议守卫，保持规则精确

**依赖：** T1；配合 T4/T5 收口。**建议 PR：** PR-F。

**创建：** architecture/responsibilities.go、responsibilities_test.go。
**修改：** architecture/ast.go、check.go、相关测试、README、AGENTS.md；app/ecommerce_agent.go 的持久化错误适配。

- [x] service 声明规则：允许现有类型别名、常量和直接再导出变量；拒绝业务函数体、init、带副作用的初始化表达式。
- [x] handler 禁止 ORM/SQL 持久化依赖；先把 ecommerce_agent 中 gorm.ErrRecordNotFound 转为稳定业务错误，保持 404 响应语义。
- [x] 协议规则覆盖新记忆/授权合同归属；补同一协议改文件名后仍受约束的负例，不强制禁止全部内部 map。
- [x] 为 Box[any]、跨文件别名、点导入、递归类型建立负例；仅在证实 AST 无法正确处理的规则上引入局部类型解析，不整体加载 app。
- [x] 保持明确规则表，未知包先报告分类；不立即禁止所有跨域 import。（`package_roles.go`；未知顶层 internal 包必须先登记角色，现有跨域依赖政策不变。）
- [x] 验证：go test -count=1 ./internal/architecture；handler 对应错误响应专项；现有 workspace scan 不新增依赖违规。
- [x] 验收：合法别名门面可通过；新增职责违规被阻断；规则无法判断时明确报告，不静默当成合法。

### T7：完成影子对照，切换本地和 PR 门禁

**依赖：** T2、T3、T4、T5、T6。**建议 PR：** PR-G，独立审查策略变化。

**修改：** verification-plan/policy、verify-before-push、quality.yml、AGENTS.md、pending-test.mdx。

- [x] 对前序 PR 和代表性变更回放计划：普通文档、记忆内部/合同/接线、轮询内部/错误合同、共享初始化、数据库、workflow、跨栈和未知文件。（本地回放完成；CI 对照待切换 PR）
- [x] 逐例人工确认受影响测试属于计划；前序迁移 PR 至少各有一次完整相关门禁通过记录。（本地回放完成；CI 对照待切换 PR）
- [x] 影子期间出现未被选择覆盖、却由全量发现的相关失败时，补映射与回归 fixture，再重新对照。（审查补齐 Agent runtime、合同及锁文件广覆盖，以及 mapped Go 的格式/vet/lint。）
- [x] 启用 PR affected；主干 push、发布、全零基准、关键共享核心变化和未知影响保留 full。workflow 修改默认广覆盖。（代码已接入，真实新工作流尚未运行。）
- [x] CI 计划检查本身始终执行；汇总检查校验实际任务与计划集合，不能因 needs 被 skip 而将计划中必跑项当成功。
- [x] dependency-backed 测试若计划运行，保留现有 PostgreSQL/Redis 未跳过检查；若计划未选中，明确输出计划理由，不伪造该套测试成功。
- [x] 支付双架构、Agent smoke、E2E 按经过审核的入口/共享依赖范围选择；涉及 Dockerfile、Compose、支付运行时、Agent 合同、锁文件等扩大检查。不只看末级目录名。
- [x] 本地默认 affected；fast 明确标为开发反馈，--all 映射 full。旧 --only 参数保持迭代用途，不成为交付绕过。
- [x] 为失败传播、缺任务、缺服务、未分类路径和命令执行失败补 fixture 测试；验证选择器不能返回空成功。
- [ ] 更新 AGENTS 推送前要求，与实际脚本/CI一致。此前仍按旧完整规则验证，策略切换 PR 也跑完整相关门禁。（说明已同步；真实完整门禁尚有 E2E/smoke 待验收。）
- [ ] 验收：同 diff 本地/CI任务集合一致；新包局部变化不执行全部 app 测试；共享变化可靠扩大；完整回归可随时显式执行。
- [ ] 输出切换前后同环境数据：选中测试/进程数量、墙钟时间、未覆盖项；禁止只用并行提速证明少跑了无关测试。

### T8：迁移任务计费与终态（第二期，分两次提交或 PR）

**依赖：** T5、T7；不阻塞首期完成。

**创建：** internal/taskruntime/billing.go、billing_test.go、terminal.go、terminal_test.go。
**修改：** app/task_billing.go、task_terminal.go、task_lifecycle.go、task_worker.go、provider_protocol_result.go、相关测试及验证映射。

- [x] 先将明确上游终态错误集中归属到 generation 的稳定错误类型，保留 TaskID/Status/Message 与 Error() 语义；app 使用别名或适配。
- [x] 收敛原因脱敏/截断归属，避免 taskruntime import app；禁止借此改写失败分类和计费政策。
- [x] 迁移 billing 及现有 stub 测试，保留空订单、uncertain、订单读取失败、已有成功计费调用等行为。
- [x] 再迁终态；app 通过显式 adapter 接入 replay/outputs/日志。跨包接口不能依赖 app 私有方法，也不能扩大成完整 Service 接口。
- [x] 保留 CAS 冲突不得重复退款/结算/finalize，产出注册失败不得重发模型生成等组合测试。
- [x] 验证新 taskruntime 包、架构守卫、^TestTaskBillingCoordinator、^TestTaskTerminal，以及既有任务/账单数据库集成路径。
- [x] 验收：worker/lifecycle 可暂留 app，但不再自行重复决定终态计费策略。

### T9：迁移 Worker 身份与授权（第二期，独立安全重构 PR）

本 PR 不包含该迁移；单独实现已保留，等待 PR134 CI 全绿后提交后续 PR。

**依赖：** T7；与 T8 可按独立路径排期。

**创建：** internal/identity/worker/grants.go、workers.go、grants_test.go。
**修改：** app/agent_worker_auth.go、_test.go、Service 接线、验证映射与安全合同说明。

- [ ] 将 worker 管理、grant 签发/验证与实时租约绑定迁入子包；环境读取留组合根，配置显式注入。
- [ ] 不改变 owner、user、run、epoch、过期与撤销校验；管理员权限与所有权必须在受控业务入口强校验。
- [ ] 将多数测试改为相关 worker/run/lease 夹具；保留真实 claim 后的授权组合测试。
- [ ] 迁移现有 TestAgentRunGrantBoundToLiveLeaseAndWorker、RejectsTerminalAndTakeover、ScopeFiltersBeforeClaim 的等价覆盖。
- [ ] 验证新包、安全负例、app 组合测试、后端 Agent 冒烟及相关 SSE/Worker 合同；合并前跑完整 Agent 相关回归。
- [ ] 验收：合法签名但已撤销、终态或接管失效的 grant 仍拒绝，不依赖完整 Pi fixture 才能验证核心规则。

### T10：收敛预演编辑规则（第三期，先一条垂直切片）

本 PR 不包含该迁移；scene_update 首切片在后续 PR 单独审查和验收。

**依赖：** 首期稳定；单独审查两端语义差异。

**创建：** internal/canvas/previs 的 types.go、validate.go、apply.go、apply_test.go。
**修改：** app/cloud_agent_previs_mutation.go、canvas/commands/upstream_pure.go、对应规则/组合测试与验证映射。

- [ ] 逐项核对两端 operation、scene 校验、ID、默认值、错误、排序与摘要差异；把预期差异写入适配说明。（首切片为 scene_update。）
- [ ] 先建立两端相同输入结果的 parity 测试，再抽取纯规则；不直接删除仍有调用的 app 路径。
- [ ] 共享类型化编辑结果，两端各自转换审批摘要/OperationSummary。
- [ ] 权限、事务、CAS、事件和回执留在现有受控入口；不另建画布写路径。
- [ ] 迁移重复规则细节测试，保留两端事务/审批恢复集成测试。
- [ ] 验证共享规则、commands、app 预演专项及相关真实路由/E2E；按当前验证计划执行完整关联门禁。
- [ ] 验收：规则单一来源，行为差异明确，直接写入与事务不变量未改变。

## 6. PR 顺序与阶段退出

建议顺序：PR-A → PR-B → PR-C / PR-D → PR-E → PR-F → PR-G；随后第二期 T8/T9、第三期 T10。斜线表示可独立排期，不表示授权同时修改同一工作区。

每个 PR 只承载一个可审查交付：
- 基础 PR 不顺手迁业务；
- 迁包 PR 同时迁测试与映射；
- 策略切换单独 PR，不能夹带未完成业务重构。
- 若任务实现偏大，可按已列出的可独立验收点拆分，但不得只提交空目录、未使用接口或第二套实现。

首期完成条件是 T1–T7 全部满足验收，不以创建包目录或脚本存在为完成。T8–T10 未实施时必须明确标为后续，不能声称整体 app 治理结束。

## 7. 每个 PR 的交付与验证纪律

- [ ] 检查改动聚焦、调用方/类型同步、权限和错误语义、事务及生命周期。
- [ ] 迁移前列出原测试 → 新测试/保留组合测试的对应表，删除重复前确认覆盖等价。
- [ ] 迭代只跑受影响专项；同一版本已通过且无新风险，不重复运行。
- [ ] 准备推送时 fetch/rebase 到最新目标分支，在干净工作区按当时 AGENTS 运行 bun scripts/verify-before-push.mjs。
- [ ] 按项目约定先运行 changelog 预览，再写用户可见条目与 VERSION；不在本文硬编码未来提交 SHA。
- [ ] 交付列出实际执行命令、总结行、未覆盖项和 CI 结果，不把静态审查或计划命令写成已通过。
- [ ] 代码地图更新归属；已实现但尚未由用户确认的变化写入 pending-test。不改动非本次产生的用户内容。
- [ ] 任意同类失败连续三次，停止盲试，记录现象、已排除项和新假设后换路径。

## 8. 衡量、回退与后续范围

### 衡量

记录相同环境与改动类别下：
- 选中测试文件/包/子进程数量和选择理由；
- 构建/测试墙钟时间及可取得的执行耗时；
- 完整回归发现的相关漏选；
- 新包需要的表、配置与后台能力；
- 基线数量与最大债务变化，仅作辅助，不以行数下降代替职责改善。

不预设“提速多少倍”。首期必须证明两个业务试点能独立测试、局部变更不执行全部 app 测试，并且共享改动会扩大覆盖。

### 回退

- 选择器出错：通过 full 模式恢复完整门禁，保留失败计划和日志；不得直接放行。
- 某模块迁移出现回归：按迁移 PR 定点 revert，重新执行关联验证；不回滚用户无关修改。
- 本轮无 schema 变更，不需要数据回滚；不得将这个前提扩展到后续未经设计的资源/项目拆分。
- 新包职责发现过宽：先收窄迁移切片，不用反向 import app 或完整 Service 接口赶进度。

### 不在本轮直接实施

完整 Agent 调度迁包、所有 provider 统一执行、资源交付/删除整体重构、项目跨域删除、完整财务系统重构。后续依据实际调用链、事务和计时另写计划，不把它们隐藏在 T8–T10。

