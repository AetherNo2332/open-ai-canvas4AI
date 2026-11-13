# 文档站独立构建、部署与更新开发计划

> **For agentic workers:** 使用 `executing-plans` 逐项实施；仅在用户明确选择并行执行时使用 `subagent-driven-development`。实现已在 `codex/docs-independent-delivery` 分支落地，过渡提交为 `fae665067`；本地验证与未覆盖边界见下方状态。发布和生产操作仍须单独安排。

实施状态：P0–P4、P6 的代码与配置已完成；P5 已完成真实 amd64 文档镜像/网关 smoke、主页真实路由检查与相邻测试。独立审查两项 Important 已用回归测试修复。尚未执行双平台注册表发布、官方 runner、真实受管实例 A→B 与业务回退演练；这些事项保留在 pending-test，不用 mock 或构建成功替代。

**Goal:** 自动重建 docs 镜像、独立更新 docs 服务；正文修订无需重建业务镜像或发布业务版本，并完成主页顶栏文档入口。

**Architecture:** Git MDX 为内容源，CI 按文档输入变化独立发布 docs 镜像，同域 `/docs` 通过 web 网关访问独立容器。Host Updater 提供独立文档事务，与业务更新共享互斥锁，但分别管理身份、状态及回退历史。

**Tech Stack:** 仓库冻结的 Next.js / Fumadocs / Bun，Node standalone，Nginx，Compose，GHCR / GitHub Actions，Go Host Updater。

**Spec:** 本文件第一至四节为合同。用户已选定独立构建和更新；同域路径、首期命令行管理、过渡发布顺序为计划中的工程选择。本文件替代 `2026-11-13-docs-production-deployment.md`，取消原“四镜像必须同提交、docs 故障回退整套业务”的合同。

## 一、现状、范围和基线

- PR133 合并提交 `9fc248c34218cf882c9b3a189d9e74c5b54fc1cf` 提供文档站源码和构建检查；此前核对远端 main 为 `cee37808a7d6e3af04ee3ae11da1507b2c05c4c8`。本地 HEAD `b5f7f65ee` 尚无完整站点文件。实施前重新核对最新远端，在隔离 `codex/` 工作区开发。
- 根 Dockerfile 只构建 web，`.dockerignore` 排除 docs；现有生产 Compose、安装、镜像发布与更新器围绕三份应用镜像。
- 更新器通过认证 Unix socket 提供 `/v1/status`、`/v1/check`、`/v1/update`、`/v1/rollback`；自更新发生在应用更新成功以后。
- 保留工作区已有修改，包括上一轮六种语言文案；不得夹带其他文档、本地资料或凭据。
- 本期保留本地 MDX 编译，不引入 CMS、远程 MDX、数据库表或新的后台管理页面。
- 实际发布、启用官方自动部署及服务器操作是后续步骤；执行前核对实际编排、挂载、备份和恢复窗口。本轮不授权这些操作。

## 二、独立发布和身份合同

### 2.1 两条交付链路

| 场景 | 行为 |
| --- | --- |
| 文档 PR | 检查、构建、真实站点隔离验证；不提升生产通道、不部署生产 |
| 相关输入合并 main | 自动构建并发布 docs 镜像，不触发业务三镜像构建或产品 Release |
| 官方文档站 | 受保护部署环境配置完整时，自动部署刚发布的 digest；未配置时只发布镜像并明确报告未部署 |
| 自部署实例 | 管理员检查后显式选择更新，不跟随官方仓库自动改服务器 |
| 应用 Release | 固定一份 docs 快照供新装及首次接入使用；复用已验证镜像，独立文档修订无须产品发版 |

- 发布输入包括公开 MDX、meta 导航、公开配图、docs 应用源码、锁文件、Dockerfile、docs 构建工作流和直接构建脚本；内部 plans/design/reports 不触发公开文档构建，也不进入镜像。
- docs-only CI 不以根 VERSION/CHANGELOG 变化为前提。开发 PR 仍遵守内部版本 bump 约定，文档日常交付本身不要求生成产品 Release。
- 镜像为 `ghcr.io/<owner>/open-ai-canvas-docs`，平台为 `linux/amd64` 和 `linux/arm64`。
- 构建标识 `buildId=docs-<完整 SHA>`；不可变标签 `docs-sha-<完整 SHA>`；`docs-main` 仅为发现候选版本的通道指针，不能直接作为运行引用。
- 更新前解析并固定目标 digest，运行变量 `CANVAS_DOCS_IMAGE` 必须为指定仓库的 `@sha256:...`。同一不可变标签若已存在，核对身份后复用，禁止覆盖为不同产物。
- 构建身份由 `DOCS_BUILD_ID`、`DOCS_BUILD_COMMIT`、`DOCS_BUILD_TIME` 注入并固化到产物；健康 JSON 为 `{status:"ok", buildId, commit, builtAt, deploymentContract:1}`，不读取请求提供的版本。
- `deploymentContract=1` 表达端口、路由和健康协议，不等于业务版本。新功能说明标注最低适用产品版本；文档 buildId 与应用版本分别展示。
- 应用 Release 资产 `docs-image.json` 含 `schemaVersion=1`、`deploymentContract=1`、digest、buildId、commit、builtAt；发布后不随通道更新而变动。

### 2.2 独立构建与官方部署 CI

- 新增 `docs-images.yml`：相关 main 变化与手动触发发布，PR 只检查；双架构和路由 smoke 都通过才提升 `docs-main`。
- 同通道提升及官方部署串行；提升时重新核对 main 的当前相关输入，防止慢任务乱序覆盖新产物。被取消或部分平台失败的任务不得提升通道。
- `publish-images.yml` 保持业务发布链路；发布完整应用 Release 时选定兼容 docs 产物，附加 `docs-image.json`，缺少产物则失败，不用任意 latest 兜底。
- 官方自动部署使用专用受信任 self-hosted runner、受保护 environment 和本机更新器 socket；普通构建 runner 不接触生产 socket/token，部署 runner 不运行 fork PR 或其他不可信任务。
- 提供显式官方部署开关，默认未启用；仓库没有配置时不索取凭据、不虚报部署成功。

## 三、部署和访问合同

| 请求 | 归属 |
| --- | --- |
| `/` 和既有 React 路由 | 原工作台 |
| `/docs`、`/docs/backend/creation-platform-sdk` | 文档入口/正文，支持刷新、直达与客户端导航 |
| `/docs/api/search` | 搜索，结果链接保留正确前缀 |
| `/docs/_next/*`、`/docs/images/*` | 构建资源与公开图片 |
| `/docs/api/health` | 文档健康与构建身份 |
| `/api/*` | Go API，不新增根 `/api/search` 劫持 |

- Next 使用 `basePath:'/docs'`、`output:'standalone'`；现有 `app/docs` 调整为站内正文根，避免 `/docs/docs`。Fumadocs source、Next Link、搜索 fetch 和图片分别按冻结版本核对，只加一次前缀。
- docs 独立构建上下文、多阶段冻结安装，非 root Node 运行，容器监听 `0.0.0.0:3001`；不公开宿主机端口，不挂业务卷和凭据。
- 只打包公开内容和站点必需资源；排除 plans、内部报告、环境文件、数据和开发缓存。Node/Bun 版本按约束与现有 CI 确认，不顺手升级依赖。
- 网关只接管精确 `/docs` 和 `/docs/` 子树，保留前缀；清除发往 docs 的业务 Cookie/Authorization，404/502/503 不回退为 SPA 200。
- docs 未就绪不阻止 Nginx 启动；容器重建换 IP 后网关自动恢复，不重启 web。业务 web 健康不依赖 docs。
- 单容器更新可能短时文档不可用，不承诺零停机；测试更新前打开的页面在切换后的静态资源/导航行为，不能以长期缓存掩盖旧资源缺失。
- 顶栏入口在语言切换左侧，指向同源 `/docs`，新标签页打开；复用当前按钮 token、窄屏图标和无障碍文案。

## 四、独立更新事务和应用升级交互

### 4.1 操作入口与状态

- 新增认证 socket 接口 `GET /v1/docs/status`、`POST /v1/docs/check`、`POST /v1/docs/update`、`POST /v1/docs/rollback`。
- update 请求 `{targetDigest, expectedCurrentDigest}`；rollback 请求 `{expectedCurrentDigest, reason}`。目标仅允许配置的 docs 仓库；current 不匹配返回冲突，contract 不支持则拒绝。
- 强类型 `DocsBuild` 含 digest/buildId/commit/builtAt/deploymentContract；`DocsStatus` 含 supported/current/candidate/previous/operation。check 解析 docs-main，返回固定的候选身份，不接受用户传入任意仓库或 Docker 参数。
- 首期通过宿主机 `scripts/update-docs.sh status|check|update|rollback` 管理，脚本调用 socket；token 仅本机读取且不回显，不增加公网 Docker 管理接口或新的业务 API。
- 复用更新器 StateDir，新增 `docs-state.json`（成功版本/历史）与 `docs-operation.json`（进行中事务）；不得覆盖应用 previous env/Compose 或数据库备份状态。

### 4.2 更新顺序与失败恢复

1. 获取与应用升级/回退共用的操作互斥；并发操作返回冲突，启动时先恢复未完成事务。
2. 核对预期 current、目标仓库/架构/contract 和发布身份，只拉取 docs；失败不改现行配置或容器。
3. 持久记录旧/新 digest、buildId、阶段及 docs env 键原始存在性和值，再进入切换。
4. 原子更新 `.env` 的 `CANVAS_DOCS_IMAGE` 单键，保留其他键与文件权限；执行 `compose up -d --no-deps --wait --wait-timeout 180 docs`。
5. 经网关核对目标身份、正文和搜索；成功提交独立 current/previous；失败只恢复旧 docs 配置和容器，并验证恢复结果。
6. 回退失败标记需人工处理，不伪报成功。手工回退使用上一成功 digest，不重新解析通道。

- 以上流程禁止业务服务 stop/start/up、数据库 migrate/restore、`--remove-orphans` 和 web 重启。
- 对 env 已改但容器未改、容器已改但状态未提交等中断，重启根据事务和实际 digest 恢复，不把中间状态当成功版本。
- 更新器进程重启及脚本重复提交不得并发产生两次切换；完成后实际容器、env 和独立状态必须一致。

### 4.3 业务升级和首次接入

- 新装使用目标应用 Release 的 docs-image.json；已有独立 docs 且与目标 contract 兼容则保留，不因应用自带旧快照而降级。
- 没有 docs 时用目标快照；contract 不兼容时，在停服前拒绝该次应用升级并说明需要显式安排文档合同升级，不静默换镜像。
- 应用升级记录操作开始时实际 docs digest；整套应用回退时恢复该值并同步 docs 状态，不使用最初安装快照，也不破坏独立 docs 历史。
- 既有 docs 健康故障单独报告/修复，不触发业务数据库恢复。首次启用 docs 失败时保留已验证业务版本，标明“业务成功，文档接入失败”，不得报告完整部署成功；使用现有 `PhaseManualIntervention` 表达部分失败，记录实际应用版本及 docs 错误，仍完成可安全执行的更新器自身安装，不走数据库回退分支。
- 回退到旧三镜像编排时按旧编排清理 docs 孤儿服务，将 docs 标为不支持；不拉取该旧版本不存在的 docs 镜像。
- 过渡 Release A 保持三镜像 Compose，先交付新更新器能力；完整 Release B 再启用 docs 编排/初装快照/顶栏。新装可直接 B；现有受管部署先 A 后 B。
- 对旧更新器跳过 A 验证停服/迁移前失败，发布说明写明顺序；不通过修改应用版本字段冒充升级。需要单 Release 引导时另行设计并验证独立更新器安装路径。

## Global Constraints

- 本期不改业务协议、数据库 schema 或 Agent 行为；现有 SSE/WebSocket/OAuth 代理与凭据边界保持完整。
- 继续镜像 digest 固定及既有更新器资产校验，不做无意义哈希；写路径强校验，不吞错降级。
- 后端文件不超过 600 行、函数不超过 120 行、复杂度不超过 20；必要拆分旧 manager_ops，只下调/删除 baseline，不增加例外。
- docs/web 使用各自 Bun 锁文件；不覆盖其他依赖，不夹带本地数据或敏感资料。
- 先在隔离 Compose 项目、端口和数据卷验证，不将本机静态检查当作生产验收。

## Review Focus

- docs-only 变化是否仍触发业务构建、业务重启、数据库操作或整套回退。
- 应用升级是否覆盖较新 docs，独立回退是否污染业务回退状态。
- 双平台部分失败、通道乱序提升、current CAS 冲突及不支持 contract。
- 进程中断、并发操作、容器换 IP、旧页面资源版本偏差。
- 正文/导航/搜索是否同一构建，公开产物是否包含内部资料或密钥。

## 五、实施任务

### P0：同步基线和路径合同

**Files:** 最新 AGENTS、docs/package.json、docs/bun.lock、docs/app 与 lib、hostupdate、现有 CI/安装脚本。

**Interfaces:** 最新远端独立工作区，输出 deploymentContract=1 和真实路由回归用例。

- [ ] 建立隔离工作区，核对最新文件地图、冻结依赖、更新器和 CI，逐项移入本任务内容。
- [ ] 核对 source/Link/search 在冻结 Fumadocs 的 API，明确单次加前缀的实现。
- [ ] 对真实 Next 应用添加入口、深链、搜索、图片和 404 测试，先确认当前实现不满足新合同；不新建替代 HTML。

### P1：子路径、身份和生产镜像

**Files:** 修改 docs/next.config.mjs、app/layout.tsx、app/docs、app/(home)、lib/source.ts、components/mdx.tsx；新增 docs/Dockerfile、docs/.dockerignore、docs/app/api/health/route.ts、scripts/test_docs_deployment.py。

**Interfaces:** 第三节路由及构建变量，健康响应固化 buildId/commit/time/contract。

- [ ] 依 P0 失败用例调整路由、布局、导航、搜索和图片，消除重复前缀。
- [ ] 多阶段 standalone 镜像复制完整 static/public/生成内容，非 root 启动；字体本地化，避免构建临时下载。
- [ ] 冻结安装后运行 `cd docs && bun run types:check && bun run build`；执行 `python -B -m unittest scripts.test_docs_deployment`。
- [ ] 在真实镜像验证身份、中文搜索、深链、资源和 404，检查镜像无 plans/报告/环境文件。

### P2：独立 docs 更新与回退（过渡 A）

**Files:** 新增 backend/internal/hostupdate/docs_types.go、docs_manager.go、docs_state.go、docs_deployment.go、docs_update_test.go；修改 server.go、manager.go、manager_ops.go，按职责拆出 deployment_images.go/deployment_services.go；新增 scripts/update-docs.sh、scripts/test_update_docs.py。

**Interfaces:** 第四节 socket 接口、DocsBuild/DocsStatus、CAS、共享操作锁、独立事务/状态文件；不新增公共后端 API。

- [ ] 先写 mock runner 测试：成功、拉取失败、身份不符、CAS 冲突、不支持 contract、无 docs、更新/回退互斥。
- [ ] 实现受限通道发现、digest 固定、单键 env 修改、仅重建 docs 和独立回退；断言无业务容器命令或数据库命令。
- [ ] 加中断恢复测试覆盖写 env/改容器/提交状态各间隙及回退失败，校验实际容器/env/状态一致。
- [ ] 交叉测试应用升级保留较新 docs、应用回退恢复开始时 digest、退回无 docs 编排；docs 首次接入失败不恢复业务数据库，并报告 PhaseManualIntervention 而不是完整成功。
- [ ] 脚本测试覆盖 socket 缺失、空目标、令牌不回显、含空格路径；A 的生产 Compose 继续三镜像。
- [ ] 运行 `go test -count=1 ./internal/hostupdate ./internal/architecture` 和 `python -B -m unittest scripts.test_update_docs`；按改善下调守卫 baseline。

### P3：编排、网关和安装（完整 B）

**Files:** 修改 docker-compose.deploy/build/local/dev.yml、docker-compose.yml、nginx.conf、.env.example、web/vite.config.ts、scripts/install-server-image.sh、install-server.sh、install-host-updater.sh、scripts/test_production_deployment.py。

**Interfaces:** docs:3001，仅内部端口；新装读取 docs-image.json，生产 CANVAS_DOCS_IMAGE 固定 digest；本地源码/开发分别可用，默认不启动服务器。

- [ ] 先加新装 manifest 固定、非法/缺失拒绝、重复安装保留独立 docs 和旧三镜像安装的回归。
- [ ] 加 docs 服务/健康、独立构建上下文和同源网关；web 不强依赖 docs 健康或启动，容器换 IP 自动恢复。
- [ ] 安装前检查资产与镜像完整；重复安装不覆盖已选择的 docs；源码安装同步构建 docs。
- [ ] 运行 `python -B -m unittest scripts.test_production_deployment scripts.test_docs_deployment scripts.test_update_docs scripts.test_install_agent_worker_id`，以及真实 Compose config/Nginx 配置检查。
- [ ] 隔离验证 docs 停止/重建不影响业务入口，docs 错误不成为 SPA 200，不传业务认证头。

### P4：独立发布和官方部署入口

**Files:** 新增 .github/workflows/docs-images.yml、docs-deploy.yml、scripts/docs-release-metadata.mjs 及 .test.mjs；修改 publish-images.yml、quality.yml、scripts/verify-before-push.mjs；按需补通道提升脚本测试。

**Interfaces:** 第二节不可变标签、docs-main 指针、双平台 manifest、Release docs-image.json；官方部署只提交刚发布的 digest。

- [ ] 触发范围回归：公开正文/导航/主题/锁文件触发，内部计划/报告不触发，docs-only 不执行业务镜像发布。
- [ ] 双平台构建和 smoke 均成功才提升通道；测试慢任务乱序、取消、单平台失败不能推进通道或部署。
- [ ] 正式产品 Release 复用兼容产物并固定快照；独立 docs 发布不进入“应用新版本”候选列表。
- [ ] 官方部署显式开关、专用 runner/environment、本机认证 socket；没有配置时报告跳过，不向自部署实例发起操作。
- [ ] 推送前脚本识别 docs/部署/工作流变化，增加对应检查，不只选择 web/backend 后漏检。

### P5：真实演练和顶栏入口

**Files:** 新增 scripts/verify-docs-deployment-smoke.py、scripts/test_docs_deployment_smoke.py；调整 verify-production-agent-smoke.py 及相邻测试编排/清理集合；修改 web/src/components/layout/workspace-top-bar.tsx 与六种语言文案。

**Interfaces:** 真实网关、实际 docs 产物、隔离 Compose 项目；入口在语言切换左侧，打开 /docs 新标签页。

- [ ] 从正文 A 更新 B，证明正文/搜索/buildId 同步、仅 docs 容器变化，业务容器 ID/版本和任务执行保持正常。
- [ ] 演练拉取失败、错误身份、健康/正文/搜索失败，仅保留源版本或回退 docs。
- [ ] 演练 A→B 接入、独立 docs 更新后应用升级保留它、应用回退到开始时 docs、退到旧三镜像编排。
- [ ] 浏览器验证真实入口、中文搜索跳转、深链刷新、明暗主题、窄屏、键盘焦点和更新前旧页面的资源行为。
- [ ] 执行部署 smoke、`bun test test/i18n-catalogs.test.ts` 和受影响 web 类型检查；双架构构建与实际启动覆盖分别记录，不能互相替代。

### P6：文档与交付

**Files:** 更新 docs/README.md、根 README/AGENTS、docs/index.md；专题 overview/docker.mdx、features.mdx，backend/production-deployment.mdx、system-update.mdx、local-development.mdx、code-map.mdx，progress/pending-test.mdx；按需精确放行 .gitignore。

- [ ] 写清官方自动部署与自部署选择更新的差异、A→B 顺序、文档单独更新/回退、应用回退交互、失败恢复及适用产品版本标注。
- [ ] 根文档保留摘要与专题链接；不复制长篇操作到 README，不公开内部报告/账号/敏感机器路径。
- [ ] 待验收表区分静态、真实容器、浏览器、双平台及线上证据；未执行不勾选。
- [ ] 各 dev PR 依约运行 changelog 脚本后改写影响、bump VERSION；日常 docs 发布不要求产品发版。
- [ ] 推送前 fetch/rebase 最新远端且工作区干净，运行默认验证范围，当前布局为 `bun scripts/verify-before-push.mjs --base upstream4ai/main`（实施时确认远端名）；保留总结行/未覆盖项并确认 CI。

## 六、验收标准与排除项

- [ ] 文档合并自动发布双架构 docs，不触发产品发版；官方部署可显式配置，自部署不被自动修改。
- [ ] 更新/回退只改变 docs，失败没有业务停机、数据库恢复或应用版本变化。
- [ ] 应用升级保留兼容较新 docs，回退状态与实际镜像一致；并发和进程中断可恢复。
- [ ] 新装、过渡升级、正文/搜索/资源、双平台构建及真实浏览器均有对应证据。
- [ ] 主页顶栏文档入口实际可用。
- [ ] 本期不提供 CMS、后台文档编辑器、零停机多副本切换或全部用户服务器的自动更新。
- [ ] 完成真实发布和受管部署验收后，再逐项确认上述完整交付标准；本地验证不等于已上线。

## 七、资料依据

- [PR133](https://github.com/AetherNo2332/open-ai-canvas4AI/pull/133)、[已核对的 docs 源码](https://github.com/AetherNo2332/open-ai-canvas4AI/tree/cee37808a7d6e3af04ee3ae11da1507b2c05c4c8/docs)。
- [Next basePath](https://nextjs.org/docs/app/api-reference/config/next-config-js/basePath)、[standalone](https://nextjs.org/docs/app/api-reference/config/next-config-js/output)、[Fumadocs 搜索](https://www.fumadocs.dev/docs/ui/search)；具体实现按冻结版本核对。
- [Docusaurus 构建发布](https://www.docusaurus.io/docs/deployment)、[MkDocs 自动发布](https://github.com/squidfunk/mkdocs-material/blob/master/docs/publishing-your-site.md)、[单仓独立构建示例](https://vercel.com/docs/monorepos)；仅作维护方式依据，不新增这些框架或托管依赖。
- 当前控制面证据：backend/internal/hostupdate/server.go、manager.go、manager_ops.go；Unix socket 客户端为 backend/internal/updaterclient/client.go。
