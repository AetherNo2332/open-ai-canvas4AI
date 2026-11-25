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
| W04 资产与提取 | W03 | 移植资产读写、提取提示词与结构校验、游标、去重键、租约及调度循环。接入已有 worker 和模型调用。资产库呈现角色、场景、道具及分集绑定 | 连点提取只排一项；中断恢复后同一资产绑定两集，已锁字段按供体合同保留；收件箱能定位失败项 | 待施工 |
| W05 造型和媒体 | W04 | 复用 `derived.go` 的基础资产与衍生造型解析；接模型选择、参考图、音色生成、批量补缺、失败重试和回调 | 两套造型有独立参考图，基础角色改名和音色按合同继承；双 worker、重复回调、延迟回调与配额失败均有可复验结果和费用记录 | 待施工 |
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

**推送状态**　本轮提交 `f0e5bae`（功能开关与门禁修复）与 `d8a4ad0`（版本）只在本机，未推送；待门禁剩余两项给出可复现结论或由 CI 复核后再推送，不绕过门禁。

**未覆盖项（W04 起）**　资产与提取、造型与媒体、分集画布与收录、Agent 工具与审批、正式页面全流程、发布与恢复；真实图像/音频渠道与生产灰度仍未验证。