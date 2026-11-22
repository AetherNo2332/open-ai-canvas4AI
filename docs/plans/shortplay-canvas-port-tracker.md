# shortplay 画布内核与 submap 移植施工报告

## 基线与进度

2026-11-22 从远端核实 4AI `main`，fetch 后以同一提交建立分支 `codex/shortplay-canvas-port`。目标基线 `036b9190e56158e8fbe12912469bf59c2a8e2226`，供体固定为 `13731aae6c4cefadc2a1719e3e7e59b22e249f94`。[4AI 源码](https://github.com/AetherNo2332/open-ai-canvas4AI/tree/036b9190e56158e8fbe12912469bf59c2a8e2226) · [shortplay 源码](https://gitee.com/yyvan1/open-ai-canvas/tree/13731aae6c4cefadc2a1719e3e7e59b22e249f94)

当前完成分支准备、S01 可重放基线、S02 文档/能力/关系投影、S03 项目存储单一写入器、S04 正式画布子画布编辑器、S05 内置 storyboard 镜头、S06 多端同步、历史、撤销与重放，以及 S07 用户预设持久化与跨画布插入。实施验收 **7/14**。每行代表一个可独立评审的分片。推进时更新状态为「进行中」「待验证」「通过」或「阻塞」。证据栏填写实现提交、测试命令和结果链接，阻塞项填写失败样本、负责人和恢复条件。每次交接在末尾追加日期及下一片。完成数以通过行数计算。

| 片号 | 交付 | 前置 | 状态 | 实现提交与验证证据 |
|---|---|---|---|---|
| S01 | 供体行为与目标基线夹具 | 可开始 | 通过 | 实现 `37906500c`。供体固定提交运行采集；4AI 旧档经真实 SQLite repository 写入回读；ZIP v3/v4 重建样本经 Bun 读取。详见 [S01 证据](../../testdata/shortplay-canvas-port/README.md) 与下方「S01 实测记录」。 |
| S02 | 文档、节点能力及关系投影 | S01 | 通过 | 实现 `7cafd662f`。字段声明、node-json 文档、submap/短剧资产能力、字段关系投影、语义分列和 Go/Web 共用关系黄金夹具落地。详见下方「S02 实测记录」。 |
| S03 | 版本、操作、鉴权与单一写入器 | S02 | 通过 | 实现 `49b788a41`。文档格式门禁、客户端能力、revision CAS、receipt 重放、事务回滚和 `UpsertCanvasProject` 单一写入路径落地。详见下方「S03 实测记录」。 |
| S04 | 正式画布路由与完整节点编辑器 | S03 | 通过 | 实现 `bd7fdae81`。顺序 ID、submap 容器/小地图/resize、子画布内创建与复制粘贴、级联删除落地；专项 44 PASS，typecheck/lint/build PASS，正式页面完成创建、编辑、折叠、复制、粘贴和重开。详见下方「S04 实测记录」。 |
| S05 | 内置镜头创建、复制和子树操作 | S04 | 通过 | 实现 `e7c47f6f0`。storyboard 预置、六节点实例化、字段引用投影、整组复制引用重映射和远端回包投影归一化落地；专项 142 PASS，typecheck/lint/build PASS，正式页面完成提示词/首帧编辑、复制与重开。详见下方「S05 实测记录」。 |
| S06 | 多端同步、历史、撤销与重放 | S05 | 通过 | 实现 `65f4ef7ad`。浏览器 operations 自动历史与回滚、历史列表/预览/CAS 恢复接口、`before_restore` 留档、字段引用恢复、`idCounters` 高水位合并和版本历史抽屉落地；专项 42 PASS，Go/Web 构建检查 PASS，正式页面完成双端编辑、删除、撤销、冲突与恢复。详见下方「S06 实测记录」。 |
| S07 | 用户预设持久化与跨画布插入 | S05 | 通过 | 实现 `b842557ec`。Schema 111、按用户隔离的模板 API、局部 ID 快照、引用重映射、保存入口和跨画布插入落地；专项 37 PASS，Go/Web 构建检查 PASS，正式页面两次插入均引用各自子节点。详见下方「S07 实测记录」。 |
| S08 | 分享、导出、资源与导入 | S05 | 待施工 | 待记录 |
| S09 | Agent 画布命令与审批 | S03、S05 | 待施工 | 待记录 |
| S10 | 生成输入、真实任务与回写 | S09 | 待施工 | 待记录 |
| S11 | 旧档只读预演与转换器 | S01、S02 | 待施工 | 待记录 |
| S12 | 分镜、批量、预演、短剧和电商入口 | S05、S10、S11 | 待施工 | 待记录 |
| S13 | 旧项目切换与恢复 | S06、S08、S11、S12 | 待施工 | 待记录 |
| S14 | 集成验收、灰度及旧写入器退役 | S07、S13 | 待施工 | 待记录 |

## 源码决定的施工边界

上一轮基线 `2507eea29` 后，4AI 已合并后端领域拆分批次 1 至 7。当前 `backend/internal/app/canvas_bridge.go` 在组合根装配服务，`backend/internal/canvas/host.go` 提供资源交付、存储锁、配额与活动记录。供体中位于 `backend/internal/app` 的逻辑要按调用方归属接到目标模块。落地时保留目标 `yingce/backend` 模块路径，核对 Go embed、TS 动态加载、工具注册、生成物、翻译和样式。S01 逐项登记供体原样移入文件、适配文件、保留的 4AI 能力、授权说明与对应测试，后续升级按供体提交比较。

目标的 `backend/internal/canvas/operations.go` 持久化 `connections`，`commands/native_connections.go` 用分镜行和批量文字列端口定位业务对象。供体的 `relation/projection.go` 与 `web/src/lib/canvas/canvas-node-inputs.ts` 按节点字段还原输入。格式标记、客户端能力和文档 revision 共同控制写入。所有旧格式入口在导入边界转换，运行时重放以当前文档字段为准。

## 分片工作单

### S01 实测记录

- **供体规则**：按 `13731aae6c4cefadc2a1719e3e7e59b22e249f94` 在独立工作树采集。采集器为 `scripts/shortplay-port/capture_donor_baseline.py`，临时注入 `backend/internal/app` 后运行 `TestS01CaptureDonorBehavior` 并清理文件；供体结束后仍为 detached、干净状态。
- **供体创建**：storyboard 生成 1 个 `submap-1` 容器和 6 个子节点，顺序为 `text-1` 至 `text-4`、`image-1`、`video-1`。子节点有 `scopeId=submap-1`、无 `parentId`；`video-1` 的 `referenceTextNodeIds` 顺序为文本 1、2、3，`videoStartFrameNodeId=submap-1__image-1`，`seconds=4`。
- **编辑与保存**：通过字段通道写入图片提示词、首帧字段和视频参考列表。写入后持久文档不含 `connections` 边，关系由字段和读取投影产生；`payloadJson` 与重开摘要一致。
- **复制**：`dup ./submap-1 镜头2` 生成 `submap-2` 与 6 个子节点；副本首帧为 `submap-2__image-1`，文本引用全部重映射到 `submap-2__*`，外部 `image-1` / `text-1` 未复制，回执报告 2 个外部引用未复制。
- **4AI 旧档**：`legacy/payload-with-extensions.json` 含两行分镜、跨镜头首帧、已有视频、素材绑定、两列批量文字、显式空 cell、`row:<id>` / `batch-text:<columnId>` 端口及未知字段；测试通过目标 `UpsertCanvasProject` 写入隔离 SQLite 后读回，文档经 `reflect.DeepEqual` 与输入一致，资源 owner 查询通过。标记为按真实 schema 重建的样本，不是生产历史档案。
- **ZIP**：`zip/projects-v3.json` 与 `zip/projects-v4.json` 覆盖 `infinite-canvas/3` 与 `yingce/4`、drawing document v1/v2、engine 差异、媒体路径和 drawing 预览/渲染路径；`archive-entries.json` 提供可解包的媒体字节。样本标记为按 exporter 历史重建，不是真实下载包。
- **验证命令与结果**：供体 `go test -count=1 -run 'TestS01CaptureDonorBehavior' -v ./internal/app` PASS（局部测试库缺少 `shortplay_episodes` 只产生既有 WARN）；目标 `go test -count=1 -run 'TestShortplayS01' -v ./internal/canvas` PASS；`go test -count=1 ./internal/canvas/...` PASS；`bun test test/shortplay-canvas-baseline.test.ts` 3 PASS。
- **耗时基线**：200 节点、79,382 字节文档、10 轮，隔离 SQLite，Windows 本机实测 `open_avg=209.35µs`、`open_max=528.4µs`、`save_avg=3.05581ms`、`save_max=3.6029ms`。单机样本，不作为 SLA。
- **既有失败**：供体固定提交的 `go test ./internal/canvas/document` 在 `audio` / `image` / `video` 的 render/parse round-trip 上失败，表现为读到 `logical:样例文本` 而非 `样例文本`；与本次采集和移植无关，后续不得误报为新回归。
- **未覆盖项**：供体已登记 registry 清单、类型清单和字段清单，但没有逐个注册类型跑创建/编辑序列；逐类型编辑矩阵放到 S02。没有可用的真实历史 ZIP 或生产 SQLite 档案，因此旧档与 ZIP 不能冒充生产样本。S04 起补正式页面实测，S05 补镜头里程碑的真实页面证据。

### S01 采集两端基线

用各自锁文件和启动脚本建立隔离开发库。在供体正式路由采集空画布、内置镜头、复制子树、预设实例和全部注册节点的操作序列。在 4AI 采集旧数据库文档及 ZIP v3/v4 代表样本。记录初末文档、revision、字段顺序、生成请求、任务绑定及页面结果，脱敏后入测试夹具。逐个核对节点注册表、工具注册、项目入口和支持命令。记录既有失败与代表性文档规模的打开、保存耗时。交付可重放夹具清单及功能覆盖矩阵。

### S02 移植文档与关系

迁入供体 `backend/internal/canvas/{capability,contract,document,relation,layout}` 中功能闭包及配套测试，前端同步节点契约和 `canvas-relation-edge-id.ts`、`canvas-edge-presentation.ts`。文档包还包含 Agent 文本寻址语法。保持 `scopeId`、`parentId`、ID 高水位、媒体引用顺序与首尾帧角色。用同一输入对照 Go 与 Web 的语义投影，验证普通参考兼首帧、文本换序、锚点变化和重复投影。

### S02 实测记录

- **后端能力**：新增 `Field`/`Fields` 声明与旧 `PatchField` 派生桥，保留 4AI 旧命令面；注册 `submap`、`shortplay-asset`，`frame` 标记为视觉节点并从 Agent 文档发现面排除。`locked` 在新文档面只读，旧编辑器命令仍可写。
- **文档与寻址**：新增 `internal/canvas/document`，实现 node-json v1 打印/解析、裸换行、CRLF/BOM/末尾换行规范化、重复键/未知字段/类型/值域/节点身份错误码、字段块区间、模板，以及 `submap-N/localId` 地址解析。全部 Agent 类型逐个执行了打印→解析→再打印的字节稳定往返。
- **关系投影**：`relation.Project` 只读节点字段，按“目标节点顺序 × 字段数组顺序”产出 R1、首尾帧和文本参考；R1 与首帧重叠时保留 R1，结果按边身份幂等去重。布局 `Edges` 改用该投影，旧持久化 `connections` 只作为渲染缓存，不再作为依赖真源。
- **作用域**：`submap` 子节点使用顶层 `scopeId` 归属，不写 `parentId`；布局节点快照单独保留 `ScopeID`，背板 `parentId` 语义不变。复制后原文与副本分别引用各自子节点，外部共享引用不复制。
- **语义分列**：新增 `ColumnGap`、`ColumnRowGap`、`ColumnPrefix`、`AutoColumnGroups`、`Columns`、自然序与稳定列序。分列夹具位于 `backend/internal/canvas/layout/testdata/column-cases.json`，覆盖真实画布列、双轴分列、显式列序、溢出不合并和空输入。
- **Web 契约**：补齐 `CanvasNodeType.Submap`、顶层 `scopeId`、参考字段和 `collapsed`；新增字段关系投影、输入成员解析、边身份和锚点展示纯函数。Go 与 Web 直接读取同一份 `testdata/shortplay-canvas-port/relation-projection.json`，覆盖普通参考兼首帧、文本换序、首尾帧角色、复制 submap 和重复投影。
- **验证命令与结果**：`go test -count=1 ./internal/canvas/... ./internal/architecture` PASS；相关 app 用例 `TestCloudAgentNodeTypesExposeExecutableAllowList`、`TestCloudAgentToolsFollowCanvasCapabilityRegistry` PASS；`bun run typecheck` PASS；`bun test test/shortplay-relation-projection.test.ts test/canvas-node-copy.test.ts` 16 PASS。全量 `go test ./...` 的其它包通过，`internal/app` 首次运行暴露新增节点类型清单未同步；修正后已跑对应用例通过。
- **未覆盖项**：S02 没有接正式项目存储或浏览器编辑路径，留待 S03/S04；Web 只迁移关系读取与边身份，写回投影和媒体引用归一化随 S04/S05 接入；没有把本次 Go/Web 同源投影当作真实页面验收。全量 `internal/app` 在修正后尚未重跑完整 575 秒套件，推送前必须补跑。

### S03 接入项目存储

从两端 `operations.go`、`user_asset_document.go`、`edit_leases.go` 追到 `app/canvas_bridge.go` 和正式项目接口。按格式分派整档保存、增量命令、离线队列与 Agent 写入，使用 4AI 的授权、配额、事务和变更通知。增量 schema 迁移沿用目标登记顺序。验收相同操作重试、过期 revision、旧客户端写新文档、越权写、事务中途失败。每个负例应保住原文档及 revision。

### S03 实测记录

- **写入器与格式**：`ApplyUserCanvasOperations` 拆到 `backend/internal/canvas/operations_apply.go`，保留单一事务、revision CAS、receipt 重放、配额、历史/Agent 投影与 `UpsertCanvasProject`。新增 `document_format.go`，旧客户端写 `referenceFieldsVersion: 1` 返回 `canvas_client_capability_required`；字段引用文档拒绝连接写操作，旧格式文档保持兼容。
- **客户端能力**：新增 `canvas-reference-fields/v1`，不计入 mutation digest；Web 自动附加能力。`create_canvas` 仅在携带能力时创建字段引用文档，`set_field` 不能修改格式版本。创建期携带旧节点/连线时先走旧图规范化，显式版本 1 无法无冲突规范化时返回 `canvas_document_format_conflict`，画布与 receipt 均不落库。
- **持久化与投影**：`idCounters`、`edgePresentation` 已随项目文档落库；字段引用文档的差分不再生成持久连接操作，Web 不再把投影连线写回。OpenAPI、`canvas-operations-sync.mdx` 和 `http-api.mdx` 已同步。`user_data.go` 架构基线条目删除且未增长。
- **负例覆盖**：旧客户端写新格式、能力升级后 receipt 重放、错误格式版本、连接写拒绝、越权写、过期 revision、mutationId 复用、事务中途失败回滚、`idCounters` / `edgePresentation` 落库、字段引用差分、创建期旧引用规范化成功与失败。
- **验证命令与结果**：`go test -count=1 ./internal/canvas/... ./internal/architecture` PASS；`go test -count=1 ./internal/handler -run 'TestOpenAPI'` PASS；`go test -count=1 ./internal/app -run 'Test.*Canvas.*(Create|Operation|Reference)|TestCloudAgent.*Canvas'` 14.5 秒 PASS；`bun run typecheck` PASS；`bun test test/canvas-operations-build.test.ts test/canvas-sync-progress.test.ts` 76 PASS、0 FAIL；`go test -count=1 -json -skip '^TestSettingsEncryptionKey' ./internal/app` 504.194 秒 PASS，FAIL 0、SKIP 25。
- **未覆盖项**：S03 没有进入正式页面完成保存/重开验收，留给 S04；完整 app 套件刻意跳过 `TestSettingsEncryptionKeyPublication`、`TestSettingsEncryptionKeyLinkFallback`、`TestSettingsEncryptionKeyConcurrentFirstUse`，它们在本机因 Windows 与 POSIX `0600` 权限语义差异返回 `key permissions = 666`，与画布改动无关；正式 PostgreSQL 与多端并发留给 S06/S14。

### S04 实测记录

- **实现范围**：画布节点 ID 改为 `${prefix}-${N}` 顺序发号，`idCounters` 随项目文档持久化且只增不减；submap 子节点使用 `${scopeId}__${type}-N`，保留 `scopeId`、`localId` 与展示路径，不写 `parentId`。`canvas-submap-node*` 提供标题栏、内容区、折叠小地图和四角 resize；子画布内创建、落点、容器撑开、拖动夹紧、折叠/展开由正式画布接入。
- **复制与删除**：复制 submap 时先发新容器号，再按新容器发子节点号；内部引用重映射到副本，外部引用与外部入边不复制。单次复制和剪贴板粘贴统一用 `isCopiedSubmapTreeNode` 判定容器作用域，修复了混用两种复制路径时作用域不一致的问题。删除容器按 v1 语义级联删除子节点。
- **专项验证**：`bun test test/canvas-node-id.test.ts test/canvas-submap.test.ts test/canvas-submap-minimap.test.ts test/canvas-node-copy.test.ts test/canvas-node-lod.test.ts test/canvas-storyboard-panel.test.ts` → **44 pass，0 fail，176 expectations**。`bun run typecheck` PASS；`bun run lint` PASS；`bun run build` PASS（20.67s，仅既存 dynamic import / chunk size 警告）。
- **全量回归**：`bun run test` → **3512 pass、7 skip、11 fail，3530 tests / 521 files，241.51s**。11 条失败均为 Windows CRLF 下按源码文本提取失败、CSS 文本断言或 `welcome-title` 独立 harness 问题，分布在 `canvas-keyboard`、`canvas-media-performance`、`canvas-resource-mention-editor`、`director-template-mode-wiring`、`settings-preference-spacing`、`welcome-title`、`workspace-menu-density`、`workspace-sidebar-checkin` 等既有文件；无 submap/ID/复制相关断言失败。
- **正式页面证据**：后端 `127.0.0.1:18080`、前端 `127.0.0.1:13000`，数据目录 `.local/project-workbench-debug`，本地一次性管理员 `s04_acceptance_20261122`。画布 `loNVFAmxzNzZmj8zDqrFJ` 中创建 `submap-1`、`submap-1__text-1`、`submap-1__image-1`；把图片提示词改为 `S04 acceptance shot image prompt`。拖动容器时容器与两个子节点同量位移；拖动子节点越界只夹紧 x。折叠后显示「2 个子节点」和小地图，重开后折叠状态保留。右键创建副本 `submap-2`，复制粘贴整组生成 `submap-3`；最终 9 节点、0 连线，刷新后仍为 9 节点。SQLite 回读 revision `17`、payload `4382` bytes；`idCounters` 为 `{"submap":3,"submap-1__text":1,"submap-1__image":1,"submap-2__text":1,"submap-2__image":1,"submap-3__text":1,"submap-3__image":1}`。
- **未覆盖项**：四角 resize 手柄已渲染，但未取得尺寸实际变化的页面记录；删除父容器级联删除子节点只有纯函数测试，未在正式页面记录前后节点清单；外部引用不复制只有单测，未额外构造页面样本。上述三项留到 S05/S06 的真实镜头链路一并复验。

### S05 实测记录

- **实现范围**：后端新增 `backend/internal/canvas/submapworkflow`，用 `testdata/storyboard.json` 声明六节点镜头并校验版本、ID、localId 顺序、文本入边槽位、`@文本N`、`seconds` 与 `startFrame`。Web 新增同构 `submap-presets.json` / `canvas-submap-presets.ts`，由 parity 测试守住后端作者契约；submap 标题栏接入「子画布形式」下拉，切换时删除旧子树、重置该 scope 高水位、按 manifest 顺序实例化 `text-1..4`、`image-1`、`video-1`，并写入提示词、`seconds=4`、首帧和文本引用顺序。
- **复制与字段真源**：复制 submap 时重映射 `generationSpec.referenceBindings` 与 `inputBindings` 的 `nodeId`，原件与副本分别引用各自子节点。字段引用文档只把 5 条文本/首帧关系投影为编辑器连线，持久 `connections` 仍为 0；导出与重开由 `referenceFieldsVersion=1` 的字段关系恢复。
- **保存回包修复**：正式页面首次复现「编辑文本保存后 7 节点 / 5 连线变成 7 / 0，刷新恢复 5」。原因是服务端 raw 文档 `connections=[]` 进入 `acknowledged` 后直接替换了本地字段投影。新增 `projectCanvasReferenceConnections`，在快照、打开、Agent 刷新、冲突对齐和保存回包进入 store/哈希前统一投影；新增“远端 connections 为空、本地有投影线，保存和重开后仍保留”的同步回归测试，并保留 `remoteContentHash` 与投影后内容一致。
- **专项验证**：`go test -count=1 ./internal/canvas/submapworkflow ./internal/architecture` PASS；`go test -count=1 ./internal/canvas/... ./internal/architecture` PASS。Web 相关 10 文件专项 **142 pass，0 fail，648 expectations**；`bun run typecheck` PASS；`bun run lint` PASS；`bun run build` PASS（1m 5s，仅既存 dynamic import / chunk size 警告）；Prettier 全部改动文件 PASS。
- **全量回归**：`bun run test` → **3527 pass、7 skip、11 fail，3545 tests / 522 files，159.18s**，日志 `.local/s05-full-test.log`。11 条仍是既有 Windows CRLF 源文本/CSS 文本断言与 `welcome-title` 独立 harness 问题；无 submap、字段投影或同步保存相关失败。
- **正式页面证据**：后端 `127.0.0.1:18080`、前端 `127.0.0.1:13000`，数据目录 `.local/project-workbench-debug`，一次性管理员 `s04_acceptance_20261122`，在干净画布 `Deruu2tqUnR5O7AGgKb_g` 完整重放。应用 storyboard 后为 7 节点 / 5 连线；文本改为 `S05 干净验收：雨夜天台的回眸`，首帧提示词改为 `S05 干净验收首帧：雨夜天台，人物回眸，霓虹反光`，视频提示词改为 `S05 干净验收视频：雨夜天台回眸，霓虹闪烁，镜头缓慢推近`，关闭编辑器后仍为 7 / 5。点击容器标题空白选中后执行「创建子画布副本」得 `submap-2` 及 6 个子节点，共 14 节点 / 10 连线，等待「已同步」后刷新重开仍为 14 / 10。先前画布 `lOt4kAlARvkpQaA1xFDSl` 因误点空白新增 `script-1` 污染样本，已弃用且不作为通过证据。
- **SQLite 回读**：`canvas_projects.revision=8`、payload `8738` bytes、14 nodes、持久 `connections=[]`、`referenceFieldsVersion=1`；`idCounters={"submap":2,"submap-1__text":4,"submap-1__image":1,"submap-1__video":1,"submap-2__text":4,"submap-2__image":1,"submap-2__video":1}`。`submap-1__image-1.metadata.referenceTextNodeIds=["submap-1__text-1"]`；`submap-1__video-1.metadata.referenceTextNodeIds=["submap-1__text-1","submap-1__text-2","submap-1__text-3"]` 且 `videoStartFrameNodeId="submap-1__image-1"`；副本对应字段全部指向 `submap-2__*`，视频 `seconds="4"`、`generationSpec.options.durationSeconds=4`。复现命令：`sqlite3 -readonly .local/project-workbench-debug/open_ai_canvas.db "SELECT revision, length(CAST(payload_json AS BLOB)) FROM canvas_projects WHERE id='Deruu2tqUnR5O7AGgKb_g';"`。
- **未覆盖项**：首帧通过修改首帧节点提示词验证，未新增第二张图片在页面改选首帧；删除容器与撤销/重做仍只有纯函数测试，正式页面未执行破坏性删除样本；四角 resize 本次仍未取得尺寸实际变化；外部共享资源复制页面样本未构造；用户预设持久化留到 S07，真实生成任务回写留到 S10。副本 `generationSpec.referenceBindings[].id` 仍保留 `node:submap-1__*` 形式，但 `nodeId` 已重映射到 `submap-2__*`；当前页面和 SQLite 回读的引用归属正确，binding `id` 去源化未在 S05 处理。

### S06 实测记录

- **实现范围**：`ApplyUserCanvasOperations` 在 revision 更新后、同一事务内调用 `CaptureDocumentHistory`，真实浏览器保存因此生成 `canvas_snapshots`；快照写入失败会连同画布和 receipt 一起回滚。历史前像只保护仍存在且 ready 的资源，当前文档引用仍严格校验。新增历史列表、快照预览和 CAS 恢复接口；恢复前强制写入 `before_restore`，字段引用文档恢复后保留 `referenceFieldsVersion=1` 和字段绑定，持久 `connections` 置空，由读取投影重新画出。
- **前端同步**：`canvas-sync-rebase` 对 `idCounters` 按 key 取高水位，避免并发 rebase 把已发出的编号还回去；rebase 和撤销快照恢复都重新按字段投影引用线并剔除悬空边。版本历史抽屉提供版本号、节点/连线数、时间和恢复操作，恢复前先保存当前编辑，恢复后读取服务端并重置撤销基线。历史恢复和引用迁移复用现有画布写入限流与通知刷新。
- **专项验证**：`go test -count=1 ./internal/canvas/... ./internal/handler ./internal/architecture` PASS（canvas 23.974s、commands 28.857s、handler 180.217s、architecture 7.301s）；Web `bun run typecheck`、`bun run lint`、`bun run build` PASS（build 1m 13s，仅既存 dynamic import / chunk size 警告）；`bun test test/canvas-sync-rebase.test.ts test/canvas-reference-roundtrip.test.ts test/canvas-submap.test.ts test/editor-history.test.ts` **42 pass、0 fail、113 expectations**。
- **全量回归**：`bun run test` → **3468 pass、7 skip、12 fail，3487 tests / 522 files，182.68s**。失败仍落在 S05 已登记的 11 组 Windows CRLF 源文本/CSS 文本断言与 `welcome` 独立 harness 问题上（本次 `welcome` 文件产生两个失败项）；没有 submap、rebase、历史捕获、历史恢复或字段投影相关失败。
- **正式页面证据**：后端 `127.0.0.1:18080`、前端 `127.0.0.1:13000`，数据目录 `.local/project-workbench-debug`，画布 `Deruu2tqUnR5O7AGgKb_g`。修复前真实浏览器保存后 `canvas_snapshots=0`；修复后首次保存生成自动版本 V11（14 节点、0 条持久连线），抽屉正常显示版本、节点数、连线数和时间。恢复 V11/V12/V19 均成功，每次恢复前生成 `before_restore`；首次恢复暴露了把旧读取投影线写回持久文档的问题，修复后从 V19 恢复得到 revision 14、14 节点、`connections=[]`，刷新页面仍为 14 节点和 10 条字段投影线。
- **双端与删除**：两个窗口分别修改 `submap-1__text-3` 和 `submap-2__text-4`，两边均落库且互不覆盖。删除 `submap-1__text-2` 后页面为 13 节点 / 9 连线，保存后仍为 13 节点，删除节点没有复活；撤销/重做恢复 `submap-1__text-3`，`idCounters` 保持 `{"submap":2,"submap-1__text":4,"submap-1__image":1,"submap-1__video":1,"submap-2__text":4,"submap-2__image":1,"submap-2__video":1}`，没有回退。对 revision 19 发送旧字段写请求返回 409：`reason=canvas_field_conflict`、revision 仍为 19，错误文案指出节点 `submap-1__text-3` 的 `metadata.content` 已被其他写入修改。
- **最终 SQLite 回读**：从 V19 恢复并清掉恢复产生的重复副本文本后，当前 revision 24、14 节点、持久 `connections=[]`、`referenceFieldsVersion=1`，页面仍投影 10 条引用线。关键历史行包含 `rev22 before_restore 13/0`、`rev19 automatic 14/0`、`rev13 before_restore 14/10`（修复前旧样本）、`rev12 before_restore 14/0`、`rev11 automatic 14/0`。复现命令：`sqlite3 -readonly .local/project-workbench-debug/open_ai_canvas.db "SELECT revision, json_array_length(json_extract(payload_json,'$.nodes')), json_array_length(json_extract(payload_json,'$.connections')) FROM canvas_projects WHERE id='Deruu2tqUnR5O7AGgKb_g';"`。
- **最终字段样本**：`submap-1__text-1=S06 历史快照保存A：文本1`，`submap-1__text-2=S06 窗口B：文本2 独立编辑`，`submap-1__text-3=S06 并发子树A：submap-1 台词`，`submap-2__text-1=S06 断线后：文本3 由窗口A写入`，`submap-2__text-2=S05 干净验收视频：雨夜天台回眸，霓虹闪烁，镜头缓慢推近`，`submap-2__text-4=S06 并发子树B：submap-2 备注`；`submap-1__text-4` 与 `submap-2__text-3` 为显式空值。
- **未覆盖项**：没有多人实时协同，仍是同账号不同窗口各自的 operations 自动重放；正式页面未用 DevTools 断网窗口演练完整离线队列，断线重放依据现有 journal/receipt 同步测试。历史抽屉展示摘要与恢复，不展示完整快照内容预览；预览接口由 HTTP contract 测试覆盖。没有在恢复过程中制造在途生成任务，留给 S13；只在 SQLite 隔离库验证，正式 PostgreSQL 留给 S14。历史前像中已清理资源不阻断保存已有实现路径，未额外构造真实资源清理页面样本。

### S07 实测记录

- **实现范围**：数据库升到 Schema 111，新增 `user_submap_presets` 纯新表与 `(user_id, updated_at)` 复合索引，不修改画布、历史或既有账户数据。`backend/internal/canvas/submappreset` 定义局部化快照和通用校验；repository 的所有查询都带 `user_id`，跨用户更新或删除返回 404。新增 `GET/POST /api/user-submap-presets` 与 `PATCH/DELETE /api/user-submap-presets/:id`，用户身份只取登录态，响应带 `Cache-Control: private, no-store`。
- **契约边界**：每份模板最多 200 个节点、512 KiB、名称最多 80 个字，每个用户最多 200 个模板。显式错误版本整份拒绝；节点类型必须在当前注册表；`localId` 唯一且匹配 `[a-z0-9][a-z0-9_-]{0,63}`。`referenceNodeIds`、`referenceTextNodeIds`、`videoStartFrameNodeId`、`videoEndFrameNodeId`、`generationSpec.referenceBindings` 和 `inputBindings` 的悬空、非字符串或畸形结构整份拒绝。实测补充了供体只做宽松解析时的类型错误路径。
- **快照与插入**：保存时把 `scopeId__globalId` 去掉作用域前缀成 `localId`，完整保留 metadata，并裁掉子树外的引用；插入时为目标 submap 重新发号，同步重映射文本、首帧、尾帧、生成合同绑定和 `inputBindings`。同一快照插入两次，两套子树分别引用自己的新节点，不复用原画布身份。子画布标题栏保存按钮、右键「另存为用户模板」、命名弹窗和六语言文案已接入；模板列表进入子画布形式下拉。
- **专项验证**：`go test -count=1 ./internal/canvas/submappreset ./internal/canvas ./internal/database ./internal/handler ./internal/architecture` PASS；`go test -count=1 ./internal/repository` PASS。Web `bun test test/canvas-submap-snapshots.test.ts test/canvas-submap-presets.test.ts test/canvas-submap.test.ts test/canvas-submap-minimap.test.ts` **37 pass、0 fail、169 expectations**；`bun run typecheck`、`bun run lint`、`bun run build`、Prettier 检查和 `docs` 的 `bun run types:check` 均 PASS。另修复 S06 引入 `canvas-node-id` 后 `canvas-sync-progress` 隔离测试漏登记依赖的问题，提交 `470f08d02`，单文件 **61 pass**。
- **全量回归**：`bun run test` → **3539 pass、7 skip、11 fail，3557 tests / 523 files，167.86s**，日志 `.local/s07-full-web-test.log`。11 条失败仍为 S05/S06 已登记的 Windows CRLF 源文本/CSS 文本断言与独立 welcome harness 问题；没有 submap、用户模板、快照、引用重映射或同步进度相关失败。修复前该全量运行还暴露 `canvas-sync-progress` 的隔离依赖初始化错误，已由 `470f08d02` 消除并单独复跑通过。
- **正式页面证据**：后端 `127.0.0.1:18080`、前端 `127.0.0.1:13000`，隔离数据目录 `.local/project-workbench-debug`，升级前备份 `.local/s07-pre-v111-20261123.db`，健康检查返回 schema `111/111 ready`。源画布 `RvxiKhDPMbIFKf6mHWFH9` 应用内置 storyboard，把首帧文本改为 `S07 模板验收：雨夜天台，人物回眸，霓虹反光`，保存模板 `S07 雨夜镜头模板`；模板 ID `98fcad84597cf47136aa4f7703d08b75`，6 节点、2173 bytes、revision 1。
- **跨画布与重开**：目标画布 `Wh1oWm3cW0QlhMUKVKHnx` 创建 `submap-1`、`submap-2`，分别插入同一模板。页面刷新重开后仍为 **14 节点 / 10 条字段投影边**，两个子画布均显示 `S07 雨夜镜头模板` 且各有 6 个子节点。SQLite 回读 revision `6`、14 nodes、持久 `connections=[]`、`referenceFieldsVersion=1`；`submap-1__*` 与 `submap-2__*` 的文本引用、首尾帧和 `generationSpec.referenceBindings` 全部分别指向各自前缀，`idCounters={"submap":2,"submap-1__text":4,"submap-1__image":1,"submap-1__video":1,"submap-2__text":4,"submap-2__image":1,"submap-2__video":1}`。
- **复现命令**：`sqlite3 -readonly .local/project-workbench-debug/open_ai_canvas.db "SELECT revision, json_array_length(json_extract(payload_json,'$.nodes')), json_array_length(json_extract(payload_json,'$.connections')) FROM canvas_projects WHERE id='Wh1oWm3cW0QlhMUKVKHnx';"`，以及按模板 ID 查询 `user_submap_presets` 的名称、字节数和 revision。页面验收只改了首帧文本节点的提示词并通过 `@文本1` 影响首帧，没有额外构造第二张图片后在页面重选首帧。
- **未覆盖项**：正式 PostgreSQL 未验收；本地只验证 SQLite 空库和 110→111 旧库副本。每用户 200 模板上限是 service 先计数再写入，并发创建同时接近上限时可能超限，尚无数据库级约束。真实历史账号、旧库模板列表分页和正式迁移仍待 S14。页面只提供保存、列出和插入，更新/删除仅由 HTTP contract 覆盖；未在页面验证第二个图片节点改选首帧。验收中 Vite HMR 曾因热更新改变 hook 顺序出现一次错误，干净刷新后未复现，未计入通过证据。

### S04 接入编辑器

以供体 `web/src/pages/canvas/project.tsx`、`use-canvas-project-lifecycle.ts` 为起点移入编辑器、store、节点渲染、工具注册、图层、媒体操作、样式及必要资源。4AI 接项目身份、资源读取、上传和任务服务。用正式页面逐项勾验 S01 注册表，覆盖创建、移动、缩放、快捷键、粘贴、节点编辑、图层拆分与提层、保存与重开。每个供体和目标特有节点记录对应结果。

### S05 打通内置镜头

复用 `submapworkflow/testdata/storyboard.json`、`cloud_agent_canvas_new_submap.go`、`cloud_agent_canvas_dup_submap.go` 以及 Web 的 `canvas-submap*.ts`。按供体 v1 保持单层作用域，子节点位于顶层 `nodes`。验证正式页面创建六节点镜头、子画布内落点、折叠小地图、复制、删除、撤销和重开。复制后首帧及文本指向各自副本，外部共享资源按原合同读取。首个可演示版本在此交付。

### S06 协作与历史

对接 `canvas_history.go`、`realtime.go` 和 Web `canvas-sync-rebase.ts`。两端分别改字段与子树，断线后重放，再执行删除、撤销、恢复历史版本。验证已断开的边不会被旧投影复活，过期写有明确冲突结果。保留两客户端事件、revision 序列和最终文档作为证据。

### S07 用户预设

供体 `submappreset/snapshot.go` 定义局部 ID 快照。迁入目标 repository、handler、迁移登记、`web/src/services/api/submap-presets.ts` 和预设面板。按供体现行 200 节点、512 KiB、80 字符名称测试边界与超限。跨画布插入两次，各自重映射内部引用，另验证跨用户访问、错误版本与悬空引用。数据库升版在空库和旧库副本上复验。

### S08 分享、导入导出

从 `canvas_share.go`、`canvas-import-save.ts`、`web/src/lib/canvas/canvas-export.ts` 接到 4AI 实际资源交付。覆盖 ZIP、历史分享、外部导入、绘图快照、预览和媒体缺失。用户 A 分享给用户 B 时逐项检查可见范围和资源授权。导出再导入的文档须保持引用、子树、绘图可编辑内容和顺序。旧格式识别交给 S11。

### S09 Agent 命令

以供体 `cloud_agent_canvas_shell.go`、`cloud_agent_canvas_field_write.go` 及 submap 寻址测试为入口，迁入读写命令、工具声明、策略片段和编译生成物，对接 4AI 控制面和当前检查点结构。逐项核对供体命令清单。以 Agent 修改一个镜头、用户修改另一个镜头为跨端例，复验锁定、拒绝审批、批准重放、续轮恢复和 UI 同步。

### S10 生成与结果回填

移入供体 `cloud_agent_generation_defaults.go` 的逐字段默认值和 `run` 输入解析。接 4AI `canvas/tasks`、生成轮询与前端任务消费者。对照文本顺序、角色资源版本、尺寸、模型、首尾帧和报价。受控渠道测试成功、失败、取消、重复回调和晚到回调；在获准的实际渠道验证图像和视频请求。记录结果写入正确子节点和费用流水，验证副本与原件任务隔离。

### S11 存档预演

复用 4AI `reference_migration.go` 的预览、冲突选择和 revision 检查模式。分别从数据库 `PayloadJSON` 和 ZIP `projects.json` 读入，共用后端转换规则。第一遍建立旧节点、行、列和资源身份映射；第二遍重写引用与作用域。先解释 `row:<id>`、`batch-text:<columnId>`，再调用一般参考推断。输出候选文档、逐字段差异、资源清单和阻断原因。验收重复预演一致、整表公共素材、跨镜头首帧、已有视频、绘图和显式空列覆盖。待裁决档案保留原件。

### S12 迁移业务入口

从 `handler/drama_canvas_routes.go`、`handler/ecommerce_agent_canvas_routes.go`、`canvas/commands/native_connections.go`、`canvas/previs` 及 Web `canvas-storyboard-materializer.ts` 检查每个读写方。分镜行映射为镜头 submap，保留资产版本、场景关联和结果读取；批量列保留列 ID、行覆盖、输入次序与重试快照。对短剧、电商、批量和预演各从正式入口创建项目、执行、重开并读回结果。发现供体无法表达的业务行为时在本片登记样本、适配归属和验收结果。

### S13 切换历史项目

以 S11 的候选文档、源 revision 和 S12 规则为输入，检查在途任务，留存旧快照，事务内切换格式与活动文档。对源版本变化的候选重新预演。验收中断恢复、重复切换、历史版本恢复、资源复用、旧标签页及任务回调。三种回退分别演练为新建路由恢复、尚无新版编辑时还原旧指针、已有新版编辑时保全增量并修复或反向转换。记录历史样本总量、通过量和具体阻断项。

### S14 集成、灰度与退役

在隔离环境按 S01 功能矩阵逐项复验，比较相同规模文档的打开、保存与重放耗时。覆盖 4AI 支持的数据库与项目 CI。内部新项目先行，存量按 S13 结果分批启用。跟踪保存失败、冲突、孤立引用、晚到任务和重复扣费。越权、丢数据或错误扣费时暂停切换并按 S13 恢复。全部活动写入口、旧草稿和历史档案均有明确归属后退役旧写入器，保留历史快照的读取与转换能力。涉及生产操作时单列影响项目、备份、恢复演练结果与操作窗口。

## 验证与接手

Go 在 `backend` 下运行 `go test ./internal/canvas/...`，按分片追加实际涉及的 app、handler、database 与 repository 专项。Web 在 `web` 下运行 `bun run typecheck`，迁入后显式执行 `bun test test/canvas-relation-persistence.test.ts test/canvas-sync-rebase.test.ts test/canvas-submap.test.ts test/canvas-submap-snapshots.test.ts`。S04 至 S10 附对应正式页面或真实接口证据。集成按根目录 `bun scripts/verify-before-push.mjs --base 036b9190e56158e8fbe12912469bf59c2a8e2226 --mode full` 运行；记录实际退出状态、失败样本和与基线的差异。

**交接日志**

| 日期 | 完成与证据 | 下一项 |
|---|---|---|
| 2026-11-22 | 核对远端 main 与 fetch 结果，建立独立分支，核对供体源码、目标组合根、迁移 API、业务入口和测试位置，完成施工拆片 | S01 建立两端可重放行为夹具。运行基线、转换覆盖率与功能验收均待采集 |
| 2026-11-22 | S01 通过：提交 `37906500c`，供体 storyboard 创建/编辑/复制/重开运行时采集，4AI 旧档真实 SQLite 回读、ZIP v3/v4 重建样本、目标 200 节点耗时基线和两端专项测试均通过；供体既有 document 失败已登记 | S02 移植 `canvas/{capability,contract,document,relation,layout}` 功能闭包，补齐逐注册类型和 Go/Web 语义投影对照 |
| 2026-11-22 | S02 通过：提交 `7cafd662f`，文档、能力、关系投影、语义分列与 Go/Web 同源黄金夹具落地；相关 Go/Web 专项测试通过 | S03 接入项目存储、格式门禁、revision CAS 与单一写入器 |
| 2026-11-22 | S03 通过：提交 `49b788a41`，字段引用文档格式门禁、客户端能力、单一事务写入器、receipt 重放和回滚测试落地；完整 `internal/app` 504.194 秒回归 0 失败、25 跳过 | S04 接入正式画布编辑器与 submap 容器、图层、ID 分配、拖拽/折叠和右键创建 |
| 2026-11-22 | S04 通过：提交 `bd7fdae81`，顺序 ID、submap 容器/小地图/resize、子画布内创建与复制粘贴、级联删除落地；专项 44 PASS，typecheck/lint/build PASS，全量 3512 PASS / 11 条既有环境失败，正式页面完成创建、编辑、折叠、复制、粘贴和重开 | S05 打通内置 storyboard 镜头：六节点模板、提示词与首帧、整组复制后的引用归属 |
| 2026-11-22 | S05 通过：提交 `e7c47f6f0`，storyboard 预置、六节点实例化、字段引用投影、复制重映射与远端回包投影归一路径落地；专项 142 PASS，typecheck/lint/build PASS，全量 3527 PASS / 11 条既有环境失败，正式页面完成提示词/首帧编辑、整组复制、保存和重开 | S06 对接协作与历史：两端改字段/子树、断线重放、删除撤销与历史恢复 |
| 2026-11-23 | S06 通过：提交 `65f4ef7ad`，operations 同事务自动历史、历史/CAS 恢复接口、恢复前留档、字段引用恢复、`idCounters` 高水位合并和版本历史抽屉落地；专项 42 PASS，Go/Web 构建检查 PASS，全量 3468 PASS / 12 条原始环境失败（11 组已知问题），正式页面完成双端编辑、删除、撤销、冲突和恢复 | S07 用户预设：局部 ID 快照、repository/handler/迁移、跨画布插入与边界验收 |
| 2026-11-23 | S07 通过：提交 `b842557ec`，Schema 111 用户模板表、按用户隔离的 CRUD、自包含快照校验、引用重映射、保存入口和跨画布插入落地；专项 37 PASS，Go/Web 构建检查 PASS，全量 3539 PASS / 11 条既有环境失败，正式页面连续插入两次并重开验证 14 节点 / 10 条字段投影边 | S08 分享、导出、资源与导入：分享授权、ZIP/历史分享、外部导入、绘图快照和媒体缺失路径 |
