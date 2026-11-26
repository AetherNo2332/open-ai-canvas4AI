# 独立 shortplay 短剧工作台移植计划

**交付目标**　创作者在 4AI 从独立入口建档、分集、提取资产、维护基础角色与衍生造型、生成参考图和音色，进入每集精修画布并将资产收回项目。用户和 Agent 能操作同一批授权资产，重开项目可恢复分集、任务与画布关联。

**基线**　目标工作树 `submap@0a601d2f88c2016d91291b6ce9433631308ae89c`。画布能力及已验收项目见[画布施工表](shortplay-canvas-port-tracker.md)。供体固定为 [`yyvan1/open-ai-canvas@13731aae6`](https://gitee.com/yyvan1/open-ai-canvas/tree/13731aae6)。本计划按独立入口与现有 `drama` 并存设计。存量项目继续按各自类型打开。工作台实施验收 0/9。

## 供体合同

以下路径相对供体 API 路由组。施工时对齐请求体、响应、字段默认值、请求大小、访问控制和错误类型，并登记在目标 OpenAPI。供体可执行路由是核对清单的依据。[路由](https://gitee.com/yyvan1/open-ai-canvas/blob/13731aae6/backend/internal/handler/shortplay_routes.go)

| 页面动作 | HTTP 合同 |
|---|---|
| 列作品、建档、读详情 | `GET /shortplays`、`POST /shortplays`、`GET /shortplays/:id` |
| 保存作品设置、提取资产 | `PATCH /shortplays/:id/settings`、`POST /shortplays/:id/extract` |
| 读资产、创建或更新资产 | `GET /shortplays/:id/assets`、`POST /shortplays/:id/assets` |
| 排队生成参考图和音色 | `POST /shortplays/:id/assets/images`、`POST /shortplays/:id/assets/audio` |
| 将画布资产收录回作品 | `POST /shortplays/:id/adopt` |
| 打开分集画布、由画布查作品、从画布生成单项参考图 | `POST /shortplays/:id/episodes/:episodeId/canvas`、`GET /shortplays/by-canvas/:canvasId`、`POST /shortplays/by-canvas/:canvasId/assets/:assetId/image` |

Web 对齐供体 `web/src/shortplay/pages` 的列表、创建、详情、资产、收件箱和设置，以及 `web/src/services/api/shortplay.ts`。业务规则来自 `backend/internal/shortplay`，画布及 Agent 接线分布于 `backend/internal/app/shortplay_*.go`。目标端由 handler 取身份、service 再导出、app 装配领域服务；数据库写经 repository，画布写经现有 revision、事件投影及命令服务。HTTP 客户端、生成渠道、资源权限、计费和 Worker 身份沿用 4AI。供体目录和分支内其他注册点均纳入 W01 清单。[页面](https://gitee.com/yyvan1/open-ai-canvas/tree/13731aae6/web/src/shortplay/pages) · [领域代码](https://gitee.com/yyvan1/open-ai-canvas/tree/13731aae6/backend/internal/shortplay) · [Agent 接线](https://gitee.com/yyvan1/open-ai-canvas/blob/13731aae6/backend/internal/app/shortplay_agent_tools.go)

## 施工与验收表

每片通过后回填提交、实跑命令和证据位置。有阻塞时写明样本、失败表现与恢复条件。

| 片 | 前置 | 交付内容与实施入口 | 通过条件 | 状态与证据 |
|---|---|---|---|---|
| W01 供体行为 | 可开始 | 从固定提交采集完整源码。追踪路由注册、项目类型、菜单、设置、调度启动、数据库、工具 schema、提示词和画布接线。建立两集共用角色、第二集有衍生造型的脱敏样本 | 登记全部页面动作、13 个接口、后台 job 种类和 Agent 注册名。将源码观察、运行记录、外部服务待测分别标记 | 通过（源码观察 + 样本重放）。见 [W01 清点](shortplay-workbench-port-w01.md)、[证据矩阵](../../testdata/shortplay-workbench-port/README.md)与 `donor/inventory.json`；样本 `sample/two-episodes.json` 同时被仓储回放（`TestShortplayWorkbenchSampleReplay`）与纯规则重放（`TestWorkbenchSampleDerivedLookRules`）消费。已提交 `0857e41`。 |
| W02 数据与权限 | W01 | 移入作品、集、资产、任务、设置和画布绑定的最终模型。登记目标架构包角色，按目标当前数据库序列新增迁移。项目内整数资产 ID 使用项目复合身份 | SQLite、PostgreSQL 空库及含旧 drama 的库升级成功；两个项目可各有资产 1，跨项目读写被拒；并发建档失败可原子回滚 | 通过。迁移 v112 + 六表模型 + `internal/shortplay` 纯规则。v112 注册文件改名为 `migrations_zzzzz_shortplay.go`（排在 v111 之后），修掉 init 追加顺序导致的 `86 after 112` 计划失败；`TestCurrentSchemaVersionMatchesMigrationPlan` PASS。新增 `TestMigrateShortplayWorkbenchUpgradesV111DramaDatabase`：按当前计划真实建到 v111、只跑 v112，旧 drama 项目/制作台/画布行保留、六表可写、重跑幂等。`go test -count=1 ./internal/database` PASS；`./internal/repository -run Shortplay` 4 PASS（含并发同名单赢家，CANVAS_TEST_POSTGRES_DSN 指向一次性隔离容器 `shortplay-w02-verify-20261126`）；`TestMigrateShortplayWorkbenchPostgres` 在同一容器自建 schema 后 DROP，PASS；`./internal/shortplay/...`、`./internal/architecture` PASS。已提交 `0857e41`。 |
| W03 分集工作台 | W02 | 完成菜单与路由、列表、新建、详情、设置，接入分集原文和设置校验。复用目标 `http`、i18n、正式布局和错误处理 | 从导航录入两集原文，列表、深链接和刷新后读到同一作品；设置写失败可重试，权限与项目类型校验通过。此片交付首个可用版本 | 通过。提交 `0857e41`，PR [#154](https://github.com/AetherNo2332/open-ai-canvas4AI/pull/154)。侧栏「短剧工作台」独立入口 `/shortplays`、`/shortplays/new`、`/shortplays/:projectId`；一次建档写作品容器（`type=shortplay`）+ 域配置 + 全部分集；列表/详情/设置整份保存。功能开关改为独立的 `shortplayEnabled`：后端 `/shortplays` 路由组、前端路由、侧栏和「功能开放」面板同口径，与 `shortDramaEnabled` 互不影响（`TestShortplayFeatureGateIsIndependentOfDrama` 覆盖四种组合）。正式页面走通登录→新建两集《雨夜回眸 W03》→保存设置→刷新重开，分集/原文/画布地址/设置保持。门禁环境修复见文末「W03 实测记录」。 |
| W04 资产与提取 | W03 | 移植资产读写、提取提示词与结构校验、游标、去重键、租约及调度循环。接入已有 worker 和模型调用。资产库呈现角色、场景、道具及分集绑定 | 连点提取只排一项；中断恢复后同一资产绑定两集，已锁字段按供体合同保留；收件箱能定位失败项 | 已完成（真实模型渠道未配置）。后端：资产读写路由、提取提示词与结构校验、`PlanExtractApply` 落库计划、job 领取条件更新与去重键、游标续跑、失败写 `last_error`；`ConfigureShortplayExtractExecutorFromEnv` 接 OpenAI 兼容 `chat/completions`，`startShortplayExtractWorker` 常驻每 5s 领取至多 4 条。前端：详情页「资产与提取」资产库、手动增改、提取按钮、收件箱（状态/尝试次数/`last_error`），job 终态自动重读资产。PostgreSQL 隔离 schema 专项覆盖领取单赢家与去重键释放。 |
| W05 造型和媒体 | W04 | 复用 `derived.go` 的基础资产与衍生造型解析；接模型选择、参考图、音色生成、批量补缺、失败重试和回调 | 两套造型有独立参考图，基础角色改名和音色按合同继承；双 worker、重复回调、延迟回调与配额失败均有可复验结果和费用记录 | 部分完成。已落地：衍生造型写入/继承/同角色去重与改名传导；参考图出图 job（入队筛选、去重、锁定跳过、父图未就绪等待、状态机 CAS 回写、迟到回调不覆盖、失败留历史且不换当前图）、页面「生成缺图/出图」与缩略图、`POST /shortplays/:id/assets/images` 与 OpenAPI。未完成：音色生成、批量补缺与失败重试的完整页面、真实图片渠道与费用流水、双 worker/延迟回调的 PostgreSQL 复验。 |
| W06 分集精修与收录 | W03、W04 | 接入每集画布创建、反查、资产投送、单项生图与收录，复用 submap 和现有画布写入服务 | 同一集同时打开两次得到同一画布；两集共享资源身份；画布改动保存重开后可定位资产；收录时校验资源和项目归属 | 待施工 |
| W07 Agent | W05、W06 | 逐个迁入供体资产查询、详情、创建、更新、放置卡片及生图工具的声明、策略、提示词和执行绑定；通过 4AI Worker 调回 Go 并重新生成 harness | 在分集画布中执行查询、修改和受审批生成；无作品绑定、跨租户、拒绝审批、批准重放均有测试；另一客户端能看见最终状态 | 待施工 |
| W08 正式页面全流程 | W05、W06、W07 | 核对全部视图及状态，串联任务收件箱、设置、资产卡、画布返回、刷新恢复与直接进入链接，同时回归既有 drama 工作流 | 浏览器从建档走到第二集复用造型、生成媒体、精修、收录、重开。在许可的真实渠道分别记录图像和音频回执；确定性渠道记录并发、失败和重试 | 待施工 |
| W09 发布与恢复 | W08 | 更新 OpenAPI、数据库文档、功能地图和待验项；按仓库规则运行完整门禁，分批开放创建入口和 scheduler | 在隔离环境演练暂停新派发、处理在途回调、恢复同一批任务。记录迁移版本、备份恢复结果、积压及重复扣费检查，再评估生产窗口 | 待施工 |

## 验证与切换

按变更范围从 `backend` 执行 `go test ./internal/shortplay/...`、`go test -count=1 ./internal/architecture` 以及 handler、repository、database 的专项集成测试。Agent 阶段运行 `go run ./cmd/export-agent-tool-schema` 并校验目标合同。Web 阶段执行 `bun run typecheck`、页面专项和正式路由浏览器测试。推送前按目标 `AGENTS.md` 运行 `bun scripts/verify-before-push.mjs`。逐条记录测试环境、命令、结果和跳过项。当前记录是源码核对，运行证据从 W01 开始累积。

运行中的任务与回调按作品、资产与任务身份绑定。暂停上线时先关闭新建或派发，保存已有作品、队列、资源和账务记录，再由兼容该 schema 的服务完成回调或恢复处理。切库或部署前另行复盘影响范围与回退步骤。

## W01/W02 实测记录

**工作树现状**　分支 `codex/shortplay-workbench-port`，HEAD `c752f8b390dceed0ea75645f2d56e77338c21706`（= `origin/main`；计划所写 `0a601d2f8` 是其祖先，`git merge-base --is-ancestor` 退出码 0）。W01/W02 全部改动**未提交**，等集成确认隔离范围后再提交。

**供体**　固定提交 `13731aae6c4cefadc2a1719e3e7e59b22e249f94` 已用 `git fetch --depth 1` 取到 `.local/donor-open-ai-canvas`，`git rev-parse HEAD` 与固定提交一致。W01 清单与样本由另一条并行采集链（`.local/donor-shortplay`、`scripts/shortplay-port/capture_workbench_donor.py`）产出；本轮把重复的夹具与清单合并到 canonical 路径，避免两份形状。

**W02 实现**　`internal/model/models_shortplay.go` 六表最终模型；`internal/shortplay/{jobs,assets,derived,cursor}.go` 纯规则（四种 job 与去重键、项目内整数资产身份、衍生造型读模型与解析、提取批次）；`internal/repository/repository_shortplay.go`（列表聚合、事务建档、项目内资产读写与 revision CAS、当前图/音投影与衍生过期标记）；`internal/database/migrations_zzzzz_shortplay.go`（v112，`init` 追加保证排在 v111 之后）+ `schema.go` 基线登记；`architecture/package_roles.go` 登记 `shortplay` 为 domain；`cmd/migrate-sqlite-postgres` 六表纳入复制清单。

**验证命令与结果**

| 命令 | 结果 |
|---|---|
| `go test -count=1 ./internal/shortplay` | PASS（canonical 样本纯规则重放：两集共用角色、衍生造型显示名/音色/描述回落、六种造型名解析、去重键形状） |
| `go test -count=1 -run 'TestShortplay' ./internal/repository` | PASS（两项目各有资产 1、跨项目读写落空、revision CAS、同名建档原子回滚、列表按用户隔离、并发建档单赢家） |
| `go test -count=1 -run 'TestMigrateShortplayWorkbench' ./internal/database`（SQLite） | PASS（空库 v112、六表存在、复合主键、旧 drama 库升级保留既有行、幂等） |
| `CANVAS_TEST_POSTGRES_DSN=postgres://canvas:canvas@127.0.0.1:15432/canvas?sslmode=disable` 同上 | PASS（PostgreSQL 17 隔离 schema：空库迁移、复合主键、未结束 job 去重索引、旧 drama 升级、两 goroutine 并发建档 1 胜 1 `ErrShortplayNameTaken`） |
| `go test -count=1 ./internal/architecture` | PASS（新增 domain 包分类、迁移文件行数、复杂度） |
| `go test -count=1 ./internal/repository ./internal/database ./internal/shortplay ./internal/architecture` | PASS（67s / 69s / 1s / 2s） |

隔离 PostgreSQL 为本轮专用容器 `shortplay-w02-pg`（`127.0.0.1:15432`，`canvas/canvas`），不是生产实例；验证只建临时 schema 并 DROP。

**未覆盖项**　HTTP 入口与正式页面（W03 起）；真实模型渠道出图/出音（W05）；Agent 工具与审批（W07）；浏览器全流程（W08）；正式 PostgreSQL 的灰度与恢复演练（W09）。当前所有“已通过”仅指上表命令跑绿，不含页面与真实渠道。

## W03 实测记录

**提交与 PR**　功能 `0857e41`、版本 `8d910e9`；PR [#154](https://github.com/AetherNo2332/open-ai-canvas4AI/pull/154)，head `8d910e9`，base `main`。

**正式页面验收**　本地后端 `127.0.0.1:18080`（`CANVAS_BACKEND_DATA_DIR=../.local/project-workbench-debug`）+ 前端 `127.0.0.1:13000`。账号 `shortplay_w03_20261126`：从侧栏新建两集《雨夜回眸 W03》（9:16，49+31 字）→ 保存设置 → 刷新重开，分集、原文、画布地址与设置保持。SQLite 回读 `projects.type=shortplay`、两行 `shortplay_episodes` 各带唯一 `canvas_id`、`settings_json` 含验收提示词。

**功能开关（本轮修正）**　原实现让 `/shortplays` 落在 `/projects` 的 `shortDramaEnabled` 中间件组里，与「两条产品线互不影响」冲突。现改为独立 `shortplayEnabled`：`platform` 常量/默认值/`FeatureEnabled`/`RequireFeature`、`handler/api.go` 独立路由组、前端 `FeatureKey`/路由/侧栏/「功能开放」面板同步；`TestShortplayFeatureGateIsIndependentOfDrama` 覆盖 (drama, shortplay) 四种组合，`GET /projects` 只看 drama，`GET /shortplays` 只看 shortplay。

**门禁环境修复（本轮）**
- `backend:gofmt`：失败来自工作树 4844 个被系统 gitconfig `core.autocrlf=true` 检出的 CRLF 文件（索引与 blob 均为 LF，`git diff` 为空，`git status` 因 stat 缓存未报）。按字节把工作树归一为 LF 后，Linux `golang:1.25-alpine` 内 `gofmt -l backend` 为 0；干净 `origin/main` 检出同为 0。
- `backend:agent-lint`：本地 Go 1.27 的 export data 版本与 golangci-lint 2.5.0 的 typecheck 不兼容（日志 `export data version 4 is greater than maximum supported version 2`，同时打印 0 issues）。用 `GOTOOLCHAIN=go1.25.0`（与 CI 的 Go 1.25 一致）执行脚本同一命令 `golangci-lint run --config=.golangci.yml ./internal/agentcontext/... ./internal/agentmemory/... ./internal/architecture/...`：0 issues，退出码 0。
- `TestSettingsEncryptionKey*`：Windows 没有 group/other 权限位，`os.FileMode.Perm()` 无法表达 0600。测试改用 `requireKeyPermissions`：Windows 跳过权限位断言，POSIX 仍断言 0600；Windows 复跑 5 个用例 PASS，Linux 容器（`golang:1.25.0-bookworm`，挂载本机模块缓存离线）复跑 PASS（4.7s）。
- `web:test`：修正前 3706 pass / 7 skip / 2 fail；两条失败都是外层 `test(...)` 默认 5s 超时短于内部 harness 15s 预算（`canvas-timeline-save`、`canvas-node-generation-reveal`），与业务代码无关。给两条用例显式 30s 外层超时后单独复跑 14 pass / 0 fail。干净 base 检出在同一环境同样复现这两条超时，作为基线证据。

**本机全量门禁（2026-11-26，`--mode full`）**　修复后 26 项中 24 项通过、2 项受本机环境阻塞：`agent:smoke`（Docker Hub `auth.docker.io` 直连超时；用本机代理时构建通过，但 `compose up --wait` 中 agent 容器 unhealthy）与 `backend:full`（`TestAgentSDKRealHTTPWorkerRestart` 在本机 Windows 上不稳定：同一提交曾 184s 通过、也曾 6.8s 与 99s 失败；失败时 worker 报 `EPERM mkdtemp %TEMP%\canvas-pi-run-*`，本机 Node 26 可写 `%TEMP%`、下载的 Node 22.19.0 对该目录一律 EPERM）。CI 权威环境（Ubuntu + Node 22.19.0 + Go 1.25）在同一代码族上 14 项检查全部通过，见 PR #154。`web:e2e` 在设置 `CHROME_BIN` 后 78/78 通过；`payment:platforms` 在设置 `HTTP(S)_PROXY=http://127.0.0.1:7897` 后通过。

**根因收口（2026-11-26 追加）**
- `agent:smoke` 不是 Hub 网络单因：构建需要容器可达的代理（`http://host.docker.internal:7897`，`127.0.0.1` 在容器内不可达）；此前 unhealthy 的 agent 容器里 `/tmp/canvas-agent-health.json` 不存在或进程未启动（容器因 migrate 条件未满足而 created 未 started）。改用可达代理并把 `NO_PROXY` 覆盖 `backend,agent,web,localhost,127.0.0.1` 后，`python -B scripts/verify-production-agent-smoke.py --output ...` 退出码 0，全部场景 PASS（含审批、SSE、媒体版本）。
- `backend:full` 的 `TestAgentSDKRealHTTPWorkerRestart`：本机 Windows SQLite 报 `database is locked`。该用例支持 `TEST_AGENT_POSTGRES_DSN`；在隔离库 `canvas_event_integration`（本机 `shortplay-w02-pg` 容器）串行运行 `go test -count=1 -run TestAgentSDKRealHTTPWorkerRestart ./internal/handler`，退出码 0（263s）。
- `VERSION` 构建号已从 9 位改为 AGENTS 要求的 7 位。

**推送状态**　此前 4 个本机提交仍未推送；本轮把根因结论与 W04 首个产物一并提交，推送前按最新 HEAD 重新 bump 并复跑全量门禁（含可达代理与 `CHROME_BIN`），只有门禁通过才更新 PR #154。

**未覆盖项（W04 起）**　资产与提取、造型与媒体、分集画布与收录、Agent 工具与审批、正式页面全流程、发布与恢复；真实图像/音频渠道与生产灰度仍未验证。
## W04 实测记录（2026-11-26）

**执行器与常驻调度**　`ConfigureShortplayExtractExecutorFromEnv` 只在 `TEXT_BASE_URL` 与 `TEXT_MODEL` 都存在时注入执行器，请求 `POST {base}/chat/completions`（`TEXT_API_KEY` 可选、`TEXT_TIMEOUT_MS` 默认 120s），从 `choices[0].message.content` 取整段回复，非 2xx 或空内容直接报错。执行器只读部署环境，不下发浏览器。`startShortplayExtractWorker` 随 `StartWorker` 启动，每 5s 领取至多 4 条；未注入执行器时在途任务以「提取执行器未配置」失败并写 `last_error`，不静默空转。

**PostgreSQL 队列专项**　新增 `repository_shortplay_jobs_postgres_test.go`：`CANVAS_TEST_POSTGRES_DSN` 指向隔离实例时建临时 schema，8 个 worker 并发领取 3 条 queued job，每条只被一方拿到；`idx_shortplay_jobs_dedupe_active` 拒绝第二条同键在途 job，`FinishShortplayJob` 后同键可重新入队。SQLite 专项的 DSN 补齐 `_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on`（与 `database.Open` 的生产配置同档），修掉「并发领取把 database is locked 当仓储行为」的假失败。

**前端资产库与收件箱**　详情页新增「资产与提取」：资产按角色/道具/场景分组，显示序号、别名、分集绑定、图/音状态与行版本；「手动添加」与「编辑」走同一条写入合同（revision CAS），可锁定设定；「提取资产」调 `POST /shortplays/{id}/extract`，重复点击提示复用在途任务。收件箱列出 `extract` 任务的状态、尝试次数与 `last_error`；job 状态变化即重读资产，失败不自动重试。

**正式页面验收（浏览器）**　本机后端 `127.0.0.1:18081`（`CANVAS_BACKEND_DATA_DIR=../.local/project-workbench-debug`，`TEXT_*` 指向本机 `.local/stub-text-server.mjs` 的 OpenAI 兼容桩）+ 前端 `127.0.0.1:13100`。账号 `shortplay-w04`：新建两集《雨夜回眸 W04》（9:16，130+111 字）→ 提取资产 → 角色沈青禾/顾行舟、道具油纸伞、场景雨巷客栈落库并绑定第 1/2 集 → 编辑沈青禾设定并锁定，`revision` 0→1 → 刷新重开，设定、锁定与集绑定保持。停掉桩后对第三个作品提取，job 进入 `failed`，收件箱显示 `Post "http://127.0.0.1:18090/v1/chat/completions": ... connection refused`，去重键释放。

**验证命令与结果**

| 命令 | 结果 |
|---|---|
| `go build ./internal/... ./cmd/server` | 退出码 0 |
| `go test -count=1 -run 'TestShortplay\|TestQueueShortplayExtract\|TestRunShortplayExtract\|TestUpsertShortplayAsset\|TestExtract\|TestClaimShortplayJob\|TestDedupeKey\|TestConfigureShortplayExtractExecutor' ./internal/shortplay ./internal/repository ./internal/app ./internal/handler` | 4 包 PASS（1.3s / 1.8s / 14.0s / 11.6s） |
| `CANVAS_TEST_POSTGRES_DSN=postgres://canvas:canvas@127.0.0.1:15432/canvas_event_integration?sslmode=disable go test -count=1 -run TestShortplayJobsClaimAndDedupePostgres ./internal/repository` | PASS（1.45s） |
| `CANVAS_TEST_POSTGRES_DSN=... go test -count=1 -run 'TestMigrateShortplayWorkbenchPostgres\|TestMigrateShortplayWorkbenchOnEmptyDatabase\|TestMigrateShortplayWorkbenchUpgradesOldDramaCopy' ./internal/database` | PASS（11.0s） |
| `node node_modules/typescript/bin/tsc --noEmit --incremental false`（web） | 退出码 0 |
| `eslint src/pages/shortplays/assets-panel.tsx src/pages/shortplays/detail.tsx src/services/api/shortplay.ts` | 退出码 0 |
| `prettier --check` 三个改动文件 | 退出码 0 |

**本机环境说明**　`bun run typecheck` 在本机失败于 `tsconfig.tsbuildinfo` 写入被拒（TypeScript 7 的原生 `tsc.exe` 在当前 Windows 环境写任何 buildinfo 路径都报 `Access is denied`，而 Node 脚本写同一路径成功）。改用 `--incremental false` 复跑同一类型检查，退出码 0；增量缓存只影响速度，不影响诊断。CI 为 Linux，不受影响。

**未覆盖项**　没有配置真实模型渠道：执行器用本机 OpenAI 兼容桩验证了 HTTP 请求形状、落库、收件箱与失败路径，不能宣称真实模型提取通过。真实参考图/音色、集画布、收录与 Agent 工具仍属 W05–W07。

## W05 实测记录（增量，2026-11-26）

**衍生造型（写入与读模型）**　`POST /shortplays/:id/assets` 新增 `derivedFrom + variantName` 分支：父资产必须是同一作品下的基础角色；分类、别名、音色继承父资产，单独指定会被拒绝；同角色形象名归一化去重；派生行显示名 `父名-造型名` 不落库，父资产改名随读模型传导。更新时不能改挂父资产，锁定/设定/外观可改。读资产列表统一走 `resolveShortplayAssetRows`，衍生行按父资产合成显示名与继承值。

**参考图出图 job**　`POST /shortplays/:id/assets/images` 按 `assetIds / kind / onlyMissing / onlyFailed` 筛选入队，锁定资产跳过，同资产已有在途 job 复用（`reused`）。调度器每 tick 领取 `asset_image`：未提交时按设置/目录选图片模型并提交 `canvas_image` 任务（衍生造型以父资产当前图为参考图，父图在换或未就绪则本轮等待），已提交时读任务结果并回写。回写是三条不变量：状态机 CAS 只在 `queued/running` 收口（迟到回调不覆盖新图）、新图落 `shortplay_asset_images` 再推进 `current_image_id` 且 `image_version +1`、失败只落 failed 历史行且保留上一张可用图。画幅不匹配（模型出方图）判失败并说明，不把错图的产物设为当前图。

**页面验收（浏览器）**　本机后端 `127.0.0.1:18081` + 前端 `127.0.0.1:13100`，账号 `shortplay-w04`：《雨夜回眸 W04》资产库出现「生成缺图」与每条资产的「出图」；锁定角色沈青禾的「出图」呈禁用。手动新建衍生造型（父资产 1 + 造型名「战袍」）后列表出现 `#5 沈青禾-战袍`，别名继承「青禾、沈姑娘」，设定继承父资产。给顾行舟点「出图」→ 入队回执「新排队 1，复用 0，跳过 4」；job 进入 `failed`，收件箱显示 `没有可用的图片模型，无法出参考图`——本机未配置任何图片模型渠道，这是真实缺口，不是确定性用例通过。

**验证命令与结果**

| 命令 | 结果 |
|---|---|
| `go test -count=1 -run 'TestCreateShortplayDerivedAsset\|TestUpdateShortplayDerivedAsset\|TestQueueShortplayAssetImages\|TestRunShortplayAssetImageJob' ./internal/app` | PASS |
| `go test -count=1 -run 'TestShortplay\|TestQueueShortplay\|TestRunShortplay\|TestCreateShortplayDerivedAsset\|TestUpdateShortplayDerivedAsset\|TestConfigureShortplayExtractExecutor' ./internal/app ./internal/repository ./internal/handler` | PASS（19.6s / 1.0s / 8.4s） |
| `go build ./internal/... ./cmd/server`；`go test ./internal/architecture` | 退出码 0；PASS |
| `node node_modules/typescript/bin/tsc --noEmit --incremental false`（web）；`eslint`；`prettier --check` | 退出码 0 |

**未覆盖项**　真实图片模型渠道、音色生成、批量补缺的完整页面、费用流水与双 worker/延迟回调的 PostgreSQL 复验仍未完成；冻结「同一集两套造型各自独立参考图」要等真实渠道出图后才能宣称。