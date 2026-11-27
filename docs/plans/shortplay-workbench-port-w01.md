# W01 供体行为清点（shortplay 独立工作台）

供体固定提交 [`13731aae6c4cefadc2a1719e3e7e59b22e249f94`](https://gitee.com/yyvan1/open-ai-canvas/tree/13731aae6c4cefadc2a1719e3e7e59b22e249f94)，本机克隆 `.local/donor-shortplay`（detached、干净）。
目标工作树基线 `0a601d2f88c2016d91291b6ce9433631308ae89c`，本文件只登记源码观察，未改动目标生产代码。

证据分级：

- **源码观察**：读供体源码得出的合同，可在供体工作树用文件行号复核，不等于目标已实现或已运行。
- **实跑**：本目标或供体实际执行命令的返回结果，附命令与输出。
- **待验**：需要真实模型渠道、真实数据库或浏览器才能确认，本轮未取得。

## 1. 页面动作与路由

路由注册在 `web/src/router.tsx`，导航登记在 `web/src/constant/navigation-tools.ts`（slug `shortplays`，标签键 `nav.tools.shortplays`，六语言「短剧」，与 drama 制作台的「短剧创作」并列）。源码观察。

| 页面 | 路由 | 动作 |
|---|---|---|
| 列表 `pages/list.tsx` | `/shortplays` | 拉列表、删除作品 |
| 新建 `pages/create.tsx` | `/shortplays/new` | 提交名称、画幅、分集原文、默认模型 |
| 详情 `pages/detail.tsx` | `/shortplays/:projectId` | 排队提取、进精修画布（按需建集画布）、删除 |
| 资产库 `pages/cast-view.tsx` | 详情内页签 | 编辑资产、锁定、批量补图、重试失败 |
| 收件箱 `pages/inbox-view.tsx` | 详情内页签 | 重试提取、给失败资产补图 |
| 设置 `pages/settings-view.tsx` | 详情内页签 | 保存设置、重做某类参考图、批量补缺 |

纯逻辑单测入口：`pages/detail-model.ts`、`pages/settings-model.ts`（设置归一、脏检查、模型半截选择判据）。

## 2. 十三个 HTTP 接口（源码观察）

`backend/internal/handler/shortplay_routes.go` 单文件注册，挂在 `handler/project.go:39` 的路由组下；鉴权、请求体上限、统一响应沿用同包 `dramaRoute` / `bindDramaJSON`。

| # | 方法与路径 | 请求体上限 | 语义 |
|---|---|---|---|
| 1 | `GET /shortplays` | — | 列作品（容器行 + 集数） |
| 2 | `POST /shortplays` | 32 MB | 建档：容器行 + 域配置 + N 条集行，不建画布、不入队 |
| 3 | `GET /shortplays/:id` | — | 详情：容器 + 设置 + 全部集 + 最近 job |
| 4 | `GET /shortplays/:id/assets` | — | 资产列表 |
| 5 | `POST /shortplays/:id/assets` | 1 MB | 资产身份+设定整份写（页面与 Agent 共用） |
| 6 | `PATCH /shortplays/:id/settings` | 64 KB | 设置整份提交并归一 |
| 7 | `POST /shortplays/:id/assets/images` | 1 MB | 排队参考图 |
| 8 | `POST /shortplays/:id/assets/audio` | 1 MB | 排队音色样本 |
| 9 | `POST /shortplays/:id/adopt` | 1 MB | 自由画布卡收录进目标集（一次一张、一个事务） |
| 10 | `POST /shortplays/:id/episodes/:episodeId/canvas` | — | 幂等按需建集画布，`created=false` 表示已存在 |
| 11 | `POST /shortplays/:id/extract` | — | 提取入队；在途时返回那一条，`queued=false` |
| 12 | `POST /shortplays/by-canvas/:canvasId/assets/:assetId/image` | 64 KB | 画布生成结果回写（只收 `resourceId`，`applied=false` 不算错） |
| 13 | `GET /shortplays/by-canvas/:canvasId` | — | 画布反查集归属，未命中 `{linked:false}` 而非 404 |

`:id` 同时接受 `shortplayId` 与 `projectId`（服务端两个都反查）。静态段 `by-canvas` 与 `:id` 同层，gin 先静态后回溯参数。

## 3. 后台 job 种类（源码观察）

`backend/internal/shortplay/model.go:11-20` 声明四种唯一执行单位，去重键落在 `shortplay_jobs.dedupe_key`，唯一索引只约束 `queued` / `running`：

| kind | 去重键 | 语义 |
|---|---|---|
| `extract` | `extract\|shortplayId` | 读全部 `scriptText`，产资产行与每集 `assetIdsJSON` |
| `asset_image` | `asset_image\|shortplayId\|assetId` | 按资产设定出图，写回 `image_resource_id` |
| `asset_audio` | `asset_audio\|shortplayId\|assetId` | 按 `voice` 文本出声，写回 `current_audio_id` |
| `asset_sync` | `asset_sync\|canvasId` | 画布节点字段回写失败时的补偿 |

状态机取值：`queued` / `running` / `succeeded` / `failed` / `canceled`。参考图与音色另有独立生命周期 `queued` / `running` / `ready` / `failed`，空串表示「从未生成」，与「生成失败」可区分。

调度器在 `shortplay/orchestrator.go`：`Scheduler.Run` 每 3 秒 tick，单 tick 取 20 部有在途 job 的作品，租约 TTL 2 分钟，并发 2，推进顺序「同步任务结果 → 规划新工作 → 派发」。租约由组合根注入 `platform.Coordinator`（接口注入而非直接 import `platform`，否则 `database → shortplay` 成环）。启动位置 `app/service.go:216-221,248`：`startShortplayScheduler` 排在 `startDramaScheduler` 之后，随 worker ctx 停止。

## 4. Agent 注册（源码观察）

四个工具，全部 `RequiresDeclaration: true` **且** `RequiresShortplay: true`，因此不进基础集前缀，普通画布会话上下文里不出现：

| 工具名 | 种类 | 门控 |
|---|---|---|
| `shortplay_list_assets` | 读 | 需声明 + 需短剧集画布 |
| `shortplay_asset` | 写（create/update） | 需声明 + 需短剧集画布 + 需画布 |
| `shortplay_asset_image` | 媒体（计费，走审批） | 同上 |
| `shortplay_asset_audio` | 媒体（计费，走审批） | 同上 |

登记点三处：`app/cloud_agent_tools.go:420-431`（schema）、`internal/agent/tool/policy.go:244-253,412-420`（Policy 行）、`internal/agent/tool/read_args.go:43-109`（typed args）。实现体在 `app/shortplay_agent_tools.go`（43 KB）。`Scope.Shortplay` 由服务端建 run 时按 `shortplay_episodes.canvasId` 命中判定，与 drama 面是两套独立业务面。

## 5. 数据模型与迁移（源码观察）

| 表 | 主键 / 关键约束 |
|---|---|
| `shortplays` | 域配置行，1:1 `projects` 容器 |
| `shortplay_episodes` | 每行一张集画布；`assetIdsJSON` 存本集资产 int 序号 |
| `shortplay_assets` | `(shortplayId, id)` 复合主键，`id` 为项目内从 1 起 int；`derivedFrom` / `variantName` / `renderRule` |
| `shortplay_asset_images` | `(shortplayId, assetId, version)` 唯一 |
| `shortplay_asset_audios` | `(shortplayId, assetId, version)` 唯一 |
| `shortplay_jobs` | `dedupe_key` 唯一索引只约束未结束行 |

容器判别值 `shortplayProjectType = "shortplay"` 写在 `app/shortplay_service.go:24`，`projects.Type` 列承载；上游资产服务只按真实项目 ID 查，不看类型。

迁移序列：`88 shortplay_tables`、`91 shortplay_asset_voice`、`92 shortplay_asset_int_id`（字符串主键改复合 int 主键、删 `normalized_name`）、`110 shortplay_derived_asset`（资产五列 + 图片/音频历史表）、`111 shortplay_asset_audio_status`。**目标端不得挪用这些编号**，须按目标当前序列新开。

## 6. 提示词与画布接线（源码观察）

- 提取：`shortplay/prompts.go` 的 `ExtractSystemPrompt`、`extractSchema`（角色/道具/场景三类的 JSON 契约，含「年代阶段与性别」「非人实体」硬要求）、`extractAssetListTemplate` / `renderAssetList`（把当前资产清单给模型做增量判断）、`extractRequirements`（角色 ≤24、道具 ≤20、场景 ≤16）、`extractSkillBlock`（用户写作技能注入）。
- 出图：`production.CastImagePrompt(settings.Assets, kind, name, appearance, stylePrompt, "")`；衍生形象追加 `shortplayDefaultRenderRule`（「参考图1是角色既有形象，保持五官、脸型与体态完全一致，只改服装、发型与配饰。」）并把父资产当前图作为 `sourceImageID`。
- 画布接线：`app/shortplay_canvas_inject.go`（把资产卡注入集画布文档、刷新已有卡）、`shortplay_adopt.go`（收录：源卡 → 资产行 + 目标集卡）、`shortplay_asset_sync.go`（画布节点字段回写表 + 失败补偿）、`shortplay_generation.go`（画布生成时把资产提示词注入生成输入、按造型名解析资产行）。
- 节点类型：`shortplay-asset`（独立类型，不是 text 的 workflowKind 变体），内置尺寸 264×352，可作 text/image/video/audio 的输入。

## 7. 衍生造型合同（源码观察）

`shortplay/derived.go` 纯函数，无数据库：

- 显示名 = `{父名}-{形象名}`，总长 ≤ 20 字，**不落库**（父资产改名要自动传导）。形象名不得含分隔符 `-`，不得与角色名或别名归一后同名。
- 读模型回落：`Kind`、`Name`（合成显示名）、`AliasesJSON` 读父资产；`Voice` / `Description` / `RenderRule` 本行为空时回落父资产。
- 造型解析 `ResolveShortplayLook` 第一命中即返回：① 完整显示名 → ② `variantName` → ③ base 名称或别名 → 0 → ④ 「基础造型」词表 → 0 → ⑤ 都不命中 → 0（模型写错造型名静默回落 base，不让整条生成失败）。② 排在 ④ 之前，所以真叫「默认战袍」的衍生形象仍选得中。
- 音色继承：衍生形象以父资产当前样本作参考音；父样本未就绪时本轮**不排队**并给出可执行原因。

## 8. 脱敏样本（已入仓）

`testdata/shortplay-workbench-port/two-episodes.json`：两集剧本、两集共用一个角色（资产 1）、第二集使用衍生造型（资产 2，`derivedFrom=1`、`variantName="夜行装"`），另含一个道具与一个场景。样本按供体合同手工构造，**不是生产档案**，标记 `sanitized-synthetic`。W02 起用它做迁移与复合主键回放，W04 起用它做提取与造型解析断言。

## 9. 证据分级汇总

| 项 | 级别 | 说明 |
|---|---|---|
| 13 接口、4 job 种类、4 Agent 工具、6 表、5 个迁移号 | 源码观察 | 按行号可在 `.local/donor-shortplay` 复核 |
| 调度器 3s/20/2min/并发 2 | 源码观察 | 常量未在目标运行 |
| 页面六个视图与动作 | 源码观察 | 未在目标浏览器打开 |
| 衍生造型显示名/回落/解析顺序 | 源码观察 | 纯函数，W04 起用样本断言 |
| 供体真实提取出图、真实渠道回执 | 待验 | 需模型渠道；本轮未取得 |
| 目标端任何运行结果 | 未开始 | W01 不改目标生产代码 |

## 10. 下一步

W02：按目标数据库序列新开迁移，移入六表最终模型与复合主键；`shortplay-asset` 登记进目标能力注册表；`internal/shortplay` 域包按目标包角色分类登记（新增 internal 顶层包须先在 `architecture/package_roles.go` 分类）。
