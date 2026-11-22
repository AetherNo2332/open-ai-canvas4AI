# shortplay 画布内核与 submap 移植施工报告

## 基线与进度

2026-11-22 从远端核实 4AI `main`，fetch 后以同一提交建立分支 `codex/shortplay-canvas-port`。目标基线 `036b9190e56158e8fbe12912469bf59c2a8e2226`，供体固定为 `13731aae6c4cefadc2a1719e3e7e59b22e249f94`。[4AI 源码](https://github.com/AetherNo2332/open-ai-canvas4AI/tree/036b9190e56158e8fbe12912469bf59c2a8e2226) · [shortplay 源码](https://gitee.com/yyvan1/open-ai-canvas/tree/13731aae6c4cefadc2a1719e3e7e59b22e249f94)

当前已通过 S01–S14，覆盖行为基线、文档与关系、项目存储、子画布编辑器、内置镜头、同步与历史、用户预设、分享与 ZIP 往返、Agent 画布命令与审批、生成任务与逐节点回写、旧档只读预演、短剧/电商/批量/预演业务入口、历史项目切换与回退、集成验收与旧写入器审计。实施验收 **14/14**。每行代表一个可独立评审的分片。推进时更新状态为「进行中」「待验证」「通过」或「阻塞」。证据栏填写实现提交、测试命令和结果链接，阻塞项填写失败样本、负责人和恢复条件。每次交接在末尾追加日期及下一片。完成数以通过行数计算。

| 片号 | 交付 | 前置 | 状态 | 实现提交与验证证据 |
|---|---|---|---|---|
| S01 | 供体行为与目标基线夹具 | 可开始 | 通过 | 实现 `37906500c`。供体固定提交运行采集；4AI 旧档经真实 SQLite repository 写入回读；ZIP v3/v4 重建样本经 Bun 读取。详见 [S01 证据](../../testdata/shortplay-canvas-port/README.md) 与下方「S01 实测记录」。 |
| S02 | 文档、节点能力及关系投影 | S01 | 通过 | 实现 `7cafd662f`。字段声明、node-json 文档、submap/短剧资产能力、字段关系投影、语义分列和 Go/Web 共用关系黄金夹具落地。详见下方「S02 实测记录」。 |
| S03 | 版本、操作、鉴权与单一写入器 | S02 | 通过 | 实现 `49b788a41`。文档格式门禁、客户端能力、revision CAS、receipt 重放、事务回滚和 `UpsertCanvasProject` 单一写入路径落地。详见下方「S03 实测记录」。 |
| S04 | 正式画布路由与完整节点编辑器 | S03 | 通过 | 实现 `bd7fdae81`。顺序 ID、submap 容器/小地图/resize、子画布内创建与复制粘贴、级联删除落地；专项 44 PASS，typecheck/lint/build PASS，正式页面完成创建、编辑、折叠、复制、粘贴和重开。详见下方「S04 实测记录」。 |
| S05 | 内置镜头创建、复制和子树操作 | S04 | 通过 | 实现 `e7c47f6f0`。storyboard 预置、六节点实例化、字段引用投影、整组复制引用重映射和远端回包投影归一化落地；专项 142 PASS，typecheck/lint/build PASS，正式页面完成提示词/首帧编辑、复制与重开。详见下方「S05 实测记录」。 |
| S06 | 多端同步、历史、撤销与重放 | S05 | 通过 | 实现 `65f4ef7ad`。浏览器 operations 自动历史与回滚、历史列表/预览/CAS 恢复接口、`before_restore` 留档、字段引用恢复、`idCounters` 高水位合并和版本历史抽屉落地；专项 42 PASS，Go/Web 构建检查 PASS，正式页面完成双端编辑、删除、撤销、冲突与恢复。详见下方「S06 实测记录」。 |
| S07 | 用户预设持久化与跨画布插入 | S05 | 通过 | 实现 `b842557ec`。Schema 111、按用户隔离的模板 API、局部 ID 快照、引用重映射、保存入口和跨画布插入落地；专项 37 PASS，Go/Web 构建检查 PASS，正式页面两次插入均引用各自子节点。详见下方「S07 实测记录」。 |
| S08 | 分享、导出、资源与导入 | S05 | 通过 | 实现 `60e5a7107`。浏览器验证只读分享、子画布 ZIP 导出重入、旧 ZIP 媒体和绘图往返。Go 专项与 Web 29 项通过。详见下方「S08 实测记录」。 |
| S09 | Agent 画布命令与审批 | S03、S05 | 通过 | 实现 `5b4341835`。命令词法/管道、submap 寻址、字段读写、new/dup/mv/rm、审批快照固化与检查点重放接入 4AI 控制面；专项 9 PASS，工具契约与画布/架构门禁 PASS，Agent 203 PASS。详见下方「S09 实测记录」。 |
| S10 | 生成输入、真实任务与回写 | S09 | 通过 | 实现 `a92042b64`。`canvas run` 把节点目录展开为现有 `generate_media_batch`，复用审批、报价、并发、重试、取消、轮询和逐节点回写；文本/角色/节点提及编译、首尾帧、规格与副本任务隔离落地。详见下方「S10 实测记录」。 |
| S11 | 旧档只读预演与转换器 | S01、S02 | 通过 | 实现 `010852610`。DB `PayloadJSON` 与 ZIP `projects.json` 共用转换规则，输出身份映射、候选文档、字段差异、资源清单、阻断/警告和引用冲突；原件保持不变。详见下方「S11 实测记录」。 |
| S12 | 分镜、批量、预演、短剧和电商入口 | S05、S10、S11 | 通过 | 实现 `743afa91b`。短剧投送引用物化、批量表字段重开保护、Agent 批量文字/分镜素材绑定、旧分镜行→镜头 submap 只读映射，以及电商/批量正式保存重开测试落地。详见下方「S12 实测记录」。 |
| S13 | 旧项目切换与恢复 | S06、S08、S11、S12 | 通过 | 实现 `be492e89d`。新增分镜迁移预览/执行/回退接口，复用引用规范化、revision CAS、事务、历史前像和画布变更通知；在途任务阻断，切换后无新编辑可还原前像，有新编辑时生成迁移前副本。详见下方「S13 实测记录」。 |
| S14 | 集成验收、灰度及旧写入器退役 | S07、S13 | 通过 | 实现 `67c880f16`。修复迁移前像被 5 分钟节流吞掉的问题；SQLite/PostgreSQL 迁移专项、正式页面升级/恢复/副本回退、陈旧写入冲突与统一升级提示通过。详见下方「S14 实测记录」。 |

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

### S08 实测记录

- **实现** `60e5a7107`。只读分享按 `scopeId` 渲染子画布，公开快照从生成合同提取画连线需要的节点绑定。整画布 ZIP 保留旧业务连线，子画布工作流 ZIP 保留容器、子节点和内部引用，清理外部引用及媒体生成结果。ZIP 导入保留文档格式和节点编号高水位，旧普通连线可补齐引用字段，包内媒体重新归属导入账号。
- **只读分享** 在隔离 SQLite Schema 111 上打开正式分享页。匿名请求返回 200，页面显示 2 个子画布、14 个节点和 10 条连线。旧快照仅有 `connections` 时仍可显示，专项测试覆盖悬空端点。资源服务测试覆盖跨用户查询、分享链接轮换与资源白名单。浏览器匿名读取真实上传图片返回 200、68 字节，画布外资源返回 404。
- **子画布 ZIP** 从正式画布子画布右键菜单导出。`projects.json` 依次保留容器、4 个文本、图片和视频，共 7 个节点，格式版本为 1。经 `/canvas` 的 ZIP 导入入口创建新画布并完成云端保存。首次导入的失败样本 `6EkKHVtAWoZch_SIMHDLH` 落库 5 条投影边。修复后重导 `SWow1Cr5a7iajYGhH_qOI`，SQLite revision 1 保留 7 个节点和 0 条持久边。重开显示 5 条连线，6 个子节点及文本、首帧和生成绑定均引用自身子树。
- **旧 ZIP 与绘图** 浏览器构造含 `connections`、PNG 和绘图快照的 v4 样本，经正式入口导入。节点重新绑定账号下的 `resource:` 资源和素材，文本引用保留。绘图快照 revision 3、预览和生成渲染均可读，整画布重导出再次包含快照、两张预览文件、源媒体及旧连线。删除包内媒体后导入明确提示缺件。v3/v4 重建夹具由 `shortplay-canvas-baseline.test.ts` 验证。
- **复现与验证** 在隔离目录 `.local/project-workbench-debug` 运行 `go run ./cmd/server`，打开正式画布的子画布右键菜单，选择「导出子画布工作流」，再从 `/canvas` 导入并刷新新画布。用 `sqlite3 -readonly .local/project-workbench-debug/open_ai_canvas.db "SELECT revision, json_array_length(payload_json,'$.nodes'), json_array_length(payload_json,'$.connections') FROM canvas_projects WHERE id='SWow1Cr5a7iajYGhH_qOI';"` 核对持久结果。`go test -count=1 ./internal/canvas ./internal/handler ./internal/database ./internal/architecture` 通过，后端创建路径修正后复跑 `./internal/canvas ./internal/architecture` 通过。`bun test test/canvas-import-normalize.test.ts test/canvas-import-media-remap.test.ts test/canvas-share-export-payload.test.ts test/canvas-submap-export.test.ts test/canvas-import-save.test.ts test/shortplay-canvas-baseline.test.ts` 为 29 通过、0 失败。`bun run typecheck`、`bun run lint` 和 `bun run build` 通过。
- **未覆盖** 旧 ZIP 使用重建样本，绘图验证快照和预览字节往返，未在绘图编辑器操作实际笔划。跨用户权限使用 Go 服务测试，浏览器验证匿名访问。生产历史库、对象存储或 CDN、全量推送门禁和远端 CI 待对应阶段验证。

### S09 Agent 命令

以供体 `cloud_agent_canvas_shell.go`、`cloud_agent_canvas_field_write.go` 及 submap 寻址测试为入口，迁入读写命令、工具声明、策略片段和编译生成物，对接 4AI 控制面和当前检查点结构。逐项核对供体命令清单。以 Agent 修改一个镜头、用户修改另一个镜头为跨端例，复验锁定、拒绝审批、批准重放、续轮恢复和 UI 同步。

### S09 实测记录

- **实现** `5b4341835`。新增 `cloud_agent_canvas_{command,command_copy,command_write,field_write,path,read}.go`，命令表覆盖 pwd/ls/cat/grep/wc/head/tail/find/sed/new/dup/mv/rm/run/edit/log/preset/help；支持 `&&`、`;`、换行、heredoc 和 cat/head/tail 管道，`||` 明确拒绝。`new` 按 `idCounters` 高水位发号并支持 `--in <submap>`；`dup` 复制 submap 子树并重映射内部引用、丢弃外部引用；`rm` 任何权限模式都审批；`edit`/`sed -i`/heredoc 统一走 `Field.Write`/`ApplyPatch`。只读会话在任何执行前拒绝写命令，审批前固化 `snapshotHash`，批准执行与检查点重放使用同一份参数。`preset list/delete` 走 4AI 用户预制件 repository；`preset save` 与 `run` 分别返回 S07 页面入口、S10 生成任务的明确依赖。
- **跨端例** `TestCanvasCommandCrossEndEditKeepsUserShot`：Agent 先按旧快照准备改镜头 A，用户通过 `UpsertCanvasProject` 正式保存路径改镜头 B；旧计划必须 `stale_snapshot` 失败且 A/B 均不被覆盖；重读后 Agent 写入 A，B 保持用户版本且 revision 只增一次；同一调用重放按过期快照拒绝；锁定 A 后 Agent 写入返回 `locked`。`TestCanvasCommandDupChildKeepsScope` 验证 submap 内复制保留 `scopeId` 并可用局部路径重读。
- **验收中修复** 命令分类器的 JSON 解码启用 `DisallowUnknownFields`，原先漏声明 `snapshotHash` 导致只读 `cat` 被误判为写；命令分段/管道扫描按字节转 rune 破坏中文参数；媒体节点旧 `prompt` 字段抢占 `prompt.txt` 别名，读取为空；submap 内复制误删 `scopeId`。
- **验证命令与结果** `go test -count=1 ./internal/app -run 'TestCanvasCommand'` 9 PASS；`TestAgentToolSchemaArtifactMatchesRuntime|TestCloudAgentToolsFollowCanvasCapabilityRegistry|TestCloudAgentNodeTypesExposeExecutableAllowList` 3 PASS；`go test -count=1 ./internal/canvas/... ./internal/architecture` PASS；`npm test`（`agent`）203 PASS、1 SKIP；`node agent/scripts/sync-harness.mjs --tool-descriptions-only` 后 `harness-sync`/`tool-contracts` 6 PASS。
- **未覆盖** 隔离开发库没有配置模型渠道，未运行真实 LLM 驱动的 Agent 会话，因此浏览器审批卡、拒绝审批后的 revision 不变和检查点续轮恢复仍以服务级审批/重放测试为证据；真实页面只读同步留待 S14 集成验收补跑。`run` 的真实任务与结果回填按计划在 S10 完成，`preset save` 仍走 S07 页面入口。

### S10 生成与结果回填

移入供体 `cloud_agent_generation_defaults.go` 的逐字段默认值和 `run` 输入解析。接 4AI `canvas/tasks`、生成轮询与前端任务消费者。对照文本顺序、角色资源版本、尺寸、模型、首尾帧和报价。受控渠道测试成功、失败、取消、重复回调和晚到回调；在获准的实际渠道验证图像和视频请求。记录结果写入正确子节点和费用流水，验证副本与原件任务隔离。

### S10 实测记录

- **实现** `a92042b64`。`canvas run` 不新建任务提交路径：`cloudAgentCanvasRunBatchArgs` 只做目录寻址、快照校验和参数归一化，随后复用现有 `generate_media_batch` 审批与状态机。位置目录与 `--glob` 二选一，同一批只允许 image/video/audio 单一模式，目标去重保序，支持 `--concurrency`、`--max-retries`、`--canary`。
- **生成输入** 模型、规格、时长和首尾帧从节点 `generationSpec` 读取，缺失时回退节点扁平字段；提示词按编辑器语义编译 `@文本N`、`@角色N`、`@[node:id]`，文本块按引用顺序追加，媒体槽位仍由既有 `cloudAgentMediaReferencePrompt` 校验。参考顺序为 `referenceNodeIds` → 首尾帧 → 文本引用；字段引用优先，旧 `connections` 只作补充。
- **审批与回写** `requestCloudAgentMediaBatchApprovalWithArgs` 保留模型实际发出的 canvas 调用身份，审批哈希和检查点重放不变；预览仍列出每个目标、单价和规格。批准后每个目标独立创建任务与计费单，节点绑定各自 `metadata.taskId`，结果按任务绑定围栏逐条写回，因此原镜头与副本不会互相覆盖。
- **默认值适配** 4AI 继续用项目默认模型和模型能力目录解析空模型、空尺寸与清晰度，没有复制供体的字面量 `16:9` / `1k` 兜底。4AI 批次状态机只承载一个批次级模型与规格；节点间模型/规格不一致时 `run` 明确拒绝并要求拆批，这是对供体逐节点默认链的有意适配。
- **验证命令与结果** `go test -count=1 ./internal/app -run 'TestCanvasRun' -v` → 5 PASS（字段引用顺序、节点规格展开、glob/批参数、混命令与过期快照、审批后逐子画布任务与回写）。`go test -count=1 ./internal/app -run 'TestCanvasCommand|TestCloudAgentMediaBatch|TestCloudAgentToolLoopPersistsApprovalAndAppliesCanvasWrite|TestCloudAgentToolTableMatchesRuntimeDispatch|TestAgentToolSchemaArtifactMatchesRuntime|TestCloudAgentToolsFollowCanvasCapabilityRegistry|TestCloudAgentNodeTypesExposeExecutableAllowList|TestCloudAgentMediaCancellation|TestCloudAgentAdmissionMediaQuoteOverBudget'` → PASS。状态机复用证据另跑 `TestCloudAgentMediaBatchMixedOutcomesStayIndependentAndReplaySafe`、`TestCloudAgentMediaBatchOneApprovalSubmitsInParallelAndWritesBack`、`TestCloudAgentMediaCompletionReplaysCommittedResultAfterBackgroundAndCheckpointFailure`、`TestCloudAgentMediaFailedTaskUpdatesNode`、`TestCloudAgentMediaCancellationKeepsGenerationRunning` → 5 PASS，覆盖成功、失败、取消、重复终态投递和取消后的晚到结果。`go test -count=1 ./internal/canvas/... ./internal/architecture` → PASS；`agent` 的 `npm test` → 203 PASS、1 SKIP。
- **验收中修复** `canvas` 已编译进工具 schema 但漏登记 `canvas.default` 的 `BusinessTools`，能力目录在准入阶段拒绝调用；补登记后审批链路通过。`agent/harness/TOOL_SCHEMA.json` 中 S09 遗留的 `preset list` 旧描述已按当前运行时重新生成。
- **未覆盖** 隔离开发库没有可用模型渠道，未发起真实付费图像/视频请求，也未跑真实 LLM 会话和浏览器审批卡；真实页面审批、任务中心展示、前端生成消费者和费用流水结算留待 S14 集成验收。Go 用例只断言任务与计费单数量及节点任务绑定，不代表正式渠道扣费对账完成。真实 PostgreSQL、对象存储和晚到回调的跨进程竞态仍未验证。

### S11 存档预演

复用 4AI `reference_migration.go` 的预览、冲突选择和 revision 检查模式。分别从数据库 `PayloadJSON` 和 ZIP `projects.json` 读入，共用后端转换规则。第一遍建立旧节点、行、列和资源身份映射；第二遍重写引用与作用域。先解释 `row:<id>`、`batch-text:<columnId>`，再调用一般参考推断。输出候选文档、逐字段差异、资源清单和阻断原因。验收重复预演一致、整表公共素材、跨镜头首帧、已有视频、绘图和显式空列覆盖。待裁决档案保留原件。

### S11 实测记录

- **实现** `010852610`。新增 `backend/internal/canvas/legacymigrate`，按“身份映射 → 显式旧句柄解释 → `references.NormalizeLegacy` 一般推断”执行只读预演。DB 入口读取 `PayloadJSON`，ZIP 入口读取 `projects.json` 的 `project`、`files` 和 `drawingDocuments`，两者共用同一转换与差异规则。
- **输出** 预览返回 `candidate`、逐路径 `diff`、`resources`、`blocking`、`warnings`、`conflicts`、节点/行/列 `identity` 和显式 `explicit`。显式映射列出 `row:<id>` 输入/输出、`batch-text:` / `batch-reference:` 列、批量输出、显式空 cell 和首尾帧字段；未解决的字段/连线冲突沿用 4AI 的 `fields` / `connections` 选择。
- **确定性与原件** 预览不修改输入文档；ZIP 项目另外记录文件清单和绘图版本。预览期生成的候选连线/绑定使用 `preview-N` 占位身份，保证同一档案重复预演字节一致；正式切换仍由 S13 分配生产身份。
- **数据库样本** 旧档夹具暴露 1 个真实冲突：`batch-row-2` 的显式 `textNodeIds=["text-2"]` 与列连线的 `text-1` 广播不一致；不选来源时 `ready=false` 且不产出候选，选择 `fields` 后通过。候选保留两行分镜输出、旧视频 `taskId`/`storageKey`、两列批量文字、显式空 `col-description`，并补齐 `batch-row-1.outputNodeId`，消费批量输出旧边。
- **ZIP 与绘图** v3/v4 样本分别保留 drawing document version 1/2、引擎、预览路径和 v4 生成渲染路径；绘图节点 `drawingId`/`drawingEngine`/`drawingRevision` 保留。归档资源清单包含 `image:frame-1` 和 `drawing:1`。
- **跨镜头首帧与空列** 构造样本验证 `videoStartFrameNodeId` 指向另一镜头图片时字段原样保留，并在显式映射中记录 `first-frame`；批量行显式空 cell 仍为 `""`，不被当成缺失值删除。
- **验证命令与结果** `go test -count=1 ./internal/canvas/legacymigrate -v` → 5 PASS（DB 旧档与冲突选择、ZIP v3/v4 绘图、跨镜头首帧与空列、冲突选择、批量输出边消费）。`go test -count=1 ./internal/canvas/... ./internal/architecture` → PASS。
- **未覆盖** 本地没有真实历史数据库或 ZIP，样本仍是按 schema 重建；绘图只验证清单和元数据，不解析 tldraw 快照或预览字节；资源存在性通过注入查表验证，未接正式资源所有权查询；未提供 HTTP/UI 入口，也未写入任何存档。跨镜头首帧目前保留节点字段，未强制写入 `generationSpec.referenceBindings`，需在 S13/S14 用真实生成链路复验。正式 revision CAS、在途任务和回退演练属于 S13。

### S12 迁移业务入口

从 `handler/drama_canvas_routes.go`、`handler/ecommerce_agent_canvas_routes.go`、`canvas/commands/native_connections.go`、`canvas/previs` 及 Web `canvas-storyboard-materializer.ts` 检查每个读写方。分镜行映射为镜头 submap，保留资产版本、场景关联和结果读取；批量列保留列 ID、行覆盖、输入次序与重试快照。对短剧、电商、批量和预演各从正式入口创建项目、执行、重开并读回结果。发现供体无法表达的业务行为时在本片登记样本、适配归属和验收结果。

### S12 实测记录

- **实现** `743afa91b`。短剧投送 `materializeDramaSegmentReferences` 同时写 `referenceNodeIds` / `referenceTextNodeIds` 与 `generationSpec.referenceBindings`；首帧继续由 `videoStartFrameNodeId` 独立表达，不重复进入普通引用。页面接线把 `referenceFieldsVersion` 传入投送 hook，旧格式文档保持原行为。
- **字段真源**：`syncBatchColumnReferences` 在列连线尚未回填时不再清空行 `inputNodeIds`。Agent/命令层 `batch-text:` 连线增删改同步写 `rows[].textNodeIds`，`storyboard-asset-reference` 连线同步增删行 `assetBindings`，避免字段文档重开后只剩显示边。
- **旧分镜行映射**：新增 `backend/internal/canvas/legacymigrate/shotmap.go`，只读输出 `ShotSubmaps`，保留资产版本、场景/预演关联、已有图片/视频结果、行锁和重试字段。角色资产按文档节点顺序解析，素材按优先级稳定排序，重复预演字节一致。
- **正式保存重开**：电商画布保存版本、重开对象与 meta、跨用户拒绝通过；批量表重开后列 ID、行覆盖、文字输入、`referenceColumnInputs`、`generatedInputSignature`、`inputChanged` 和显式 cell 全部保留。短剧投送在字段文档只保存节点时仍恢复引用边。
- **验证命令与结果**：`go test -count=1 ./internal/canvas/... ./internal/architecture` → PASS；`go test -count=1 ./internal/canvas/legacymigrate -v` → 7 PASS；`go test -count=1 ./internal/canvas/commands -run 'TestNativeStoryboardAsset|TestNativeBatchTextColumn' -v` → 4 PASS；`go test -count=1 ./internal/app -run 'TestEcommerceAgentCanvasSaveAndReopen'` → PASS。Web `bun test test/drama-canvas-projection.test.ts test/canvas-batch-column-inheritance.test.ts test/shortplay-relation-projection.test.ts test/canvas-storyboard-references.test.ts test/canvas-submap-presets.test.ts` → 34 pass、0 fail、138 expectations；`bun run typecheck`、`bun run lint`、`bun run build` → PASS。
- **未覆盖项**：本片证据是 Go 服务/正式保存入口与 Web 投影专项，不是四个业务页面逐一人工创建、执行、重开；浏览器逐入口证据留 S14 集成验收。真实模型渠道、正式 PostgreSQL、对象存储、审批和费用流水仍在 S14。`shotmap.go` 只是只读映射，写入切换由 S13 执行。

### S13 切换历史项目

以 S11 的候选文档、源 revision 和 S12 规则为输入，检查在途任务，留存旧快照，事务内切换格式与活动文档。对源版本变化的候选重新预演。验收中断恢复、重复切换、历史版本恢复、资源复用、旧标签页及任务回调。三种回退分别演练为新建路由恢复、尚无新版编辑时还原旧指针、已有新版编辑时保全增量并修复或反向转换。记录历史样本总量、通过量和具体阻断项。

### S13 实测记录

- **实现** `be492e89d`。新增 `GET/POST /canvas-projects/:id/shot-migration` 与 `POST /canvas-projects/:id/shot-migration/rollback`。预览返回 S11 引用冲突、待转换行数、镜头 submap 计划、阻断/警告和当前在途任务；执行前重新读取当前文档，不做旧候选直接写入。
- **切换规则**：旧文档先经 `references.NormalizeLegacy` 统一字段引用；每条分镜行生成一个 `submap` 容器和四个提示词子节点，新图片/视频子节点写入 `generationSpec.referenceBindings`、首帧和引用顺序。已有图片/视频结果保持原节点、原 scope、资源归属和任务绑定，作为镜头的外部结果引用，不强制搬入容器，避免破坏历史资源和在途任务身份。
- **并发与恢复**：服务层先做 revision CAS，再检查数据库 `tasks.project_id = canvasId` 与文档内的 queued/running/loading/pending/submitting 状态；存在在途任务时返回 `shot_migration_tasks_active`。写入复用 `upsertUserCanvasProjectWithHistory`，在事务内保存 `before_shot_migration` 历史前像并重算托管连线；重复执行返回 `shot_migration_not_required`。
- **回退**：`restore` 只在当前 revision 等于迁移前 revision + 1 时还原历史前像；已有新编辑时返回 `shot_migration_has_newer_edits`，必须用 `new_canvas` 把迁移前文档复制成新画布，保留当前画布增量。旧标签页继续靠 revision CAS 拒绝过期写入；没有迁移前快照时明确返回 `shot_migration_rollback_missing`。
- **Web 入口**：正式画布加载时读取分镜迁移预览，仅在存在待转换行时显示「升级分镜」提示；面板展示行数、目标容器数、引用冲突和在途任务。引用冲突时要求先完成现有引用迁移；执行成功后刷新画布 store。回退只提供 API，页面回退入口留 S14。
- **验证命令与结果**：`go test -count=1 ./internal/canvas/... ./internal/architecture` → PASS；`go test -count=1 ./internal/canvas -run 'TestShotMigration' -v` → 2 PASS（切换+还原、在途任务+新编辑新画布回退）；`go test -count=1 ./internal/canvas/legacymigrate -run 'TestApplyShotSubmaps' -v` → PASS；`go test -count=1 ./internal/handler -skip 'TestAgentSDK'` → PASS（112.304s），OpenAPI 路由契约与架构守卫 PASS。Web 专项 36 pass、0 fail、144 expectations；`bun run typecheck`、`bun run lint`、`bun run build` → PASS。
- **既有失败**：`go test -count=1 ./internal/handler` 全量仅两条 Agent SDK 外部进程用例失败：`TestAgentSDKRealHTTPWorkerRestart` 的 worker 重启场景收到 `failed`，`TestAgentSDKNativeSkillsHTTP` 出现 SQLite `database is locked`。两条都不经过分镜迁移、画布保存或历史接口；跳过这两条后处理器其余用例通过。未把它们计作 S13 通过证据。
- **未覆盖项**：没有在浏览器打开真实旧项目执行切换、刷新、另一标签页写入和回退；没有注入“任务刚好在切换瞬间完成”的晚到回调竞态。真实 PostgreSQL、对象存储、历史账号、真实 ZIP/DB 档案、灰度性能与生产恢复演练留 S14。回退的“反向转换修复”没有自动算法，当前用历史前像恢复或迁移前副本保全，需在 S14 明确产品入口。

### S14 集成、灰度与退役

在隔离环境按 S01 功能矩阵逐项复验，比较相同规模文档的打开、保存与重放耗时。覆盖 4AI 支持的数据库与项目 CI。内部新项目先行，存量按 S13 结果分批启用。跟踪保存失败、冲突、孤立引用、晚到任务和重复扣费。越权、丢数据或错误扣费时暂停切换并按 S13 恢复。全部活动写入口、旧草稿和历史档案均有明确归属后退役旧写入器，保留历史快照的读取与转换能力。涉及生产操作时单列影响项目、备份、恢复演练结果与操作窗口。

### S14 实测记录

- **实现** `67c880f16`。补齐迁移回退的页面入口、回退预览元数据和统一升级提示；修复 `before_shot_migration` 被自动快照 5 分钟节流吞掉的问题，并增加 SQLite/PostgreSQL 回归用例。
- **迁移前像修复**：`SaveDocumentWithHistory` 与 `CaptureDocumentHistory` 共用 `canvasHistoryForceCapture`，把 `before_restore`、`before_resource_repair`、`before_shot_migration` 都作为不可重建的强制快照原因。回归用例先产生一条 `automatic` 快照，再在 5 分钟内执行迁移；移除修复时用例稳定失败（`before_shot_migration` 记录不存在），恢复修复后通过。修复前的历史画布 `s14-legacy-3661ac175e01` 只保留缺陷样本，没有回填快照；新画布复验从迁移开始重新取证。
- **正式页面升级**：画布 `s14-verify-24c404acf7a4` 先产生 `automatic` 快照 r1，再在 r4 执行升级；数据库出现 `revision=4, reason=before_shot_migration, node_count=5`，迁移后 r5 为 17 节点、5 条投影连线、2 个镜头 submap。`submap-1__video-1.videoStartFrameNodeId = submap-1__image-1`，`old-image` / `old-video` 保持外层 scope。页面首次执行升级时因本地自动保存把 r3 推进到 r4，返回 `canvas_concurrent_write`；预览重新拉取后第二次成功，未丢数据，但这是一条需要保留的用户可见冲突与重试路径。
- **恢复与副本**：无新编辑时页面菜单出现「分镜升级回退」，面板显示 r4→r5 并执行 `restore`，数据库写入 `revision=5, reason=before_restore, node_count=17`，恢复后 r6 回到 5 节点、旧分镜行 2 条。再次升级得到 r7 的 `before_shot_migration` 前像；随后在页面编辑 `submap-2` 图片提示词为「走廊逆光，霓虹反光」，多次自动保存推进到 r11。打开回退面板时重新拉取预览，显示 r6→r10 且只提供「保留当前并生成迁移前副本」；执行后跳转到新画布 `62893bbea0332cd0087a484b149cbc29`，副本 r1 为 5 节点、0 连线、2 条旧分镜行，原画布 r11 的提示词编辑与新结构保持不变。
- **冲突语义**：正式迁移/整份保存使用 revision CAS；操作接口按最新文档合并，并用字段 `before` 值做冲突检查，`baseRevision` 不是文档级 CAS。实测一次陈旧但字段不冲突的标题写入被合并，较新的提示词编辑保留；一次 `submap-2__text-1.metadata.content` 的陈旧覆盖返回 HTTP 409 `canvas_field_conflict`，revision 保持 r11 且「走廊逆光，霓虹反光」未被覆盖。S13 中“旧标签页靠 revision CAS 拒绝所有过期写入”的表述应收窄为整份保存/迁移路径，操作路径以字段基线冲突为准。
- **统一升级提示**：原两个独立横幅位于绝对定位顶栏下方，会与顶部视图切换控件重叠。新增 `CanvasMigrationNotice` 把「素材引用待迁移」和「旧分镜可升级」合并为一张卡，固定在 `--canvas-topbar-offset` 下方，按引用迁移优先排序并提示依赖关系；窄屏换行，按钮仍可见，专注模式和资产视图不渲染。页面实测两个按钮分别打开「确认画布的素材关联」和「把旧分镜行切换为镜头子画布」，提示卡不再遮挡顶部控件。首轮卡片误用未定义的 `--site-surface`，在画布上呈透明并透出点阵；`cb7e1ec9a` 改用不透明 `--popover`，并补 `motion-safe` 淡入/下滑动画，计算样式背景为 `oklch(1 0 0)`、opacity 1。
- **耗时基线**：同一 200 节点、79,382 字节文档、10 轮、隔离 SQLite。S01 施工前样本 `open_avg=209.35µs`、`save_avg=3.05581ms`；S14 单独运行 `open_avg=281.7–389.84µs`、`open_max≈1.0–1.2ms`、`save_avg=3.41692–3.93453ms`、`save_max=4.5–5.2ms`。一次并行负载下的冷样本为 `open_avg=634.9µs`、`save_avg=4.7782ms`。均为 Windows 本机单机样本，不作为 SLA；PostgreSQL 同规模耗时未测。
- **旧写入器审计**：活动文档写入仍统一落到 `UpsertCanvasProject` / `DeleteCanvasProject`（整份保存、operations、历史恢复、分镜/引用迁移），或由 Agent 命令服务维护版本与事件；项目归属写入和 v80 迁移投影也调用 `syncLegacyCanvasWrite`，不是另一套旧文档存储。字段引用文档拒绝连接写操作，旧 `connections` 只作渲染缓存。保留的旧读路径是 `legacy.PayloadJSON`/`projects.json` 预演、历史快照恢复和未升级旧文档读取；没有可安全删除的独立旧写入实现，按事实登记为“保留旧读、无活动旧写”。
- **验证命令与结果**：`go test -count=1 ./internal/canvas/... ./internal/architecture` → PASS；`go test -count=1 ./internal/canvas -run 'TestShotMigration' -v` → SQLite 3 PASS、无 DSN 时 PostgreSQL 3 SKIP；PostgreSQL 17.12 隔离 schema 专用 `TestShotMigrationPostgres` → 3 PASS（4.88s / 4.44s / 4.98s）；`go test -count=1 ./internal/handler -skip 'TestAgentSDK'` → PASS（162.468s）；`bun test test/canvas-shot-migration.test.ts` → 2 PASS；`bun run typecheck`、`bun run lint`、`bun run build` → PASS。正式页面证据来自 `127.0.0.1:13001` + 重建后的 `127.0.0.1:18080` 后端，隔离 PostgreSQL 为 `canvas_s14`。
- **未覆盖项**：没有真实模型渠道、审批卡、费用流水、对象存储和晚到任务回调，不能宣称真实扣费对账或跨进程竞态通过；没有真实生产历史数据库或 ZIP，旧档样本仍是按 schema 重建；ZIP 导入入口已能导入 v3/v4 项目，导入后可在正式画布执行分镜升级，但 `legacymigrate.PreviewZipProjects` 的逐字段预演仍是库级能力，没有独立 HTTP/UI 预演入口；PostgreSQL 只覆盖迁移专项，未做同规模耗时；没有执行任何生产/SSH/灰度操作，也未 push 或声称 CI 通过。真实账号权限矩阵、资源隔离和计费沿用 4AI 既有服务与处理器测试，未做生产多租户演练。

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
| 2026-11-23 | S08 通过。提交 `60e5a7107`。正式页面分享与子画布 ZIP 重开通过，旧 ZIP 的图片、绘图快照和预览往返通过，匿名资源访问范围已验。Web 专项 29 项通过 | S09 对接 Agent 画布命令、审批与跨端同步 |
| 2026-11-23 | S09 通过。提交 `5b4341835`。Agent 画布命令面、submap 寻址、审批快照固化与重放落地；专项 9 PASS，工具契约、画布/架构门禁和 Agent 203 项通过。跨端冲突、锁定和子画布复制已在服务级复验；真实 LLM 会话待 S14 补跑 | S10 接入生成默认值、`run` 真实任务与结果回填 |
| 2026-11-23 | S10 通过。提交 `a92042b64`。`canvas run` 以节点字段展开为现有批次审批与状态机，完成文本/角色/节点提及编译、首尾帧、规格、逐节点任务绑定和副本隔离；专项 5 PASS，状态机复用 5 PASS，画布/架构门禁与 Agent 203 项通过。真实模型渠道和正式页面审批待 S14 | S11 旧数据库与 ZIP 只读预演、字段级差异和阻断原因转换器 |
| 2026-11-23 | S11 通过。提交 `010852610`。新增旧档只读预演包，DB `PayloadJSON` 与 ZIP `projects.json` 共用身份映射、旧句柄解释、候选文档、差异、资源清单和冲突选择；专项 5 PASS，画布/架构门禁通过。真实历史档案、绘图快照字节和写入切换待 S13/S14 | S12 迁移短剧、电商、批量与预演业务入口，逐入口创建项目并读回结果 |
| 2026-11-23 | S12 通过。提交 `743afa91b`。短剧投送引用同时落字段与生成合同，批量表字段重开不再清空行输入，Agent 批量文字/分镜素材连线同步写行字段，旧分镜行只读映射输出镜头 submap 计划；电商与批量正式保存重开、短剧投影专项通过。服务/HTTP 入口通过，四个业务页面逐入口操作留 S14 | S13 以 S11 候选、源 revision 和 S12 规则切换历史项目，验收在途任务、重复切换、历史恢复与三种回退 |
| 2026-11-23 | S13 通过。提交 `be492e89d`。分镜迁移预览/执行/回退接口、引用规范化、行转镜头 submap、在途任务门禁、revision CAS、历史前像、重复切换和资源复用通过；无新编辑可还原前像，有新编辑用新画布保全。处理器除两条既有 Agent SDK 外部进程用例外通过，Web 专项/类型检查/lint/build 通过。真实页面切换与晚到回调竞态留 S14 | S14 集成验收：正式页面逐入口复验、真实 PostgreSQL/对象存储/模型渠道、灰度与旧写入器退役 |
| 2026-11-23 | S14 通过。提交 `67c880f16`。修复 `before_shot_migration` 被 5 分钟节流吞掉的真实回归并补 SQLite/PostgreSQL 用例；正式页面完成升级、恢复、再次升级、编辑后生成迁移前副本和新画布跳转。陈旧整份迁移返回 `canvas_concurrent_write`，陈旧字段覆盖返回 `canvas_field_conflict` 且不覆盖新编辑。两个迁移横幅合并为顶栏下方统一通知卡，消除与视图切换控件的重叠。画布/架构/处理器（跳过既有 AgentSDK 进程用例）、Web 专项、typecheck/lint/build 通过。真实渠道、扣费、对象存储、生产档案和灰度未执行 | S01–S14 全部通过；后续按推送前门禁验证并另行安排真实渠道/生产灰度演练 |
