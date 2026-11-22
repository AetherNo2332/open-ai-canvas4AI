# shortplay 画布内核与 submap 移植施工报告

## 基线与进度

2026-11-22 从远端核实 4AI `main`，fetch 后以同一提交建立分支 `codex/shortplay-canvas-port`。目标基线 `036b9190e56158e8fbe12912469bf59c2a8e2226`，供体固定为 `13731aae6c4cefadc2a1719e3e7e59b22e249f94`。[4AI 源码](https://github.com/AetherNo2332/open-ai-canvas4AI/tree/036b9190e56158e8fbe12912469bf59c2a8e2226) · [shortplay 源码](https://gitee.com/yyvan1/open-ai-canvas/tree/13731aae6c4cefadc2a1719e3e7e59b22e249f94)

当前完成分支准备、S01 可重放基线、S02 文档/能力/关系投影和 S03 项目存储单一写入器。实施验收 **3/14**。每行代表一个可独立评审的分片。推进时更新状态为「进行中」「待验证」「通过」或「阻塞」。证据栏填写实现提交、测试命令和结果链接，阻塞项填写失败样本、负责人和恢复条件。每次交接在末尾追加日期及下一片。完成数以通过行数计算。

| 片号 | 交付 | 前置 | 状态 | 实现提交与验证证据 |
|---|---|---|---|---|
| S01 | 供体行为与目标基线夹具 | 可开始 | 通过 | 实现 `37906500c`。供体固定提交运行采集；4AI 旧档经真实 SQLite repository 写入回读；ZIP v3/v4 重建样本经 Bun 读取。详见 [S01 证据](../../testdata/shortplay-canvas-port/README.md) 与下方「S01 实测记录」。 |
| S02 | 文档、节点能力及关系投影 | S01 | 通过 | 实现 `7cafd662f`。字段声明、node-json 文档、submap/短剧资产能力、字段关系投影、语义分列和 Go/Web 共用关系黄金夹具落地。详见下方「S02 实测记录」。 |
| S03 | 版本、操作、鉴权与单一写入器 | S02 | 通过 | 实现 `49b788a41`。文档格式门禁、客户端能力、revision CAS、receipt 重放、事务回滚和 `UpsertCanvasProject` 单一写入路径落地。详见下方「S03 实测记录」。 |
| S04 | 正式画布路由与完整节点编辑器 | S03 | 待施工 | 待记录 |
| S05 | 内置镜头创建、复制和子树操作 | S04 | 待施工 | 待记录 |
| S06 | 多端同步、历史、撤销与重放 | S05 | 待施工 | 待记录 |
| S07 | 用户预设持久化与跨画布插入 | S05 | 待施工 | 待记录 |
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
