# 画布节点功能验收与错误记录（2026-10-26）

> 本文保留修复前的复现记录。随后已修复 N01–N10，并将操作诊断扩为 131 个场景接入 `Canvas node acceptance`：129 个产生有效命令，2 个被首帧校验正常拦下。后端联测验证真实生产者输出、持久化、重开读回和幂等回执；远端状态及业务验收见待测试记录。原 JSON 为修复前证据，不替换为通过结果。

## 结论与测试口径

本轮检查覆盖全部 **18 类内置节点**的创建、改名/移动/尺寸/锁定、剪贴板复制粘贴、删除，并补充分镜、批量表、预演、分组、画风及展示节点的配置入口。

源码基线为 `b48cd5f7a`。本机 Docker 使用隔离项目 `canvas-node-audit-1026`，页面端口 `3067`；沿用上一轮本机镜像中的运行时代码，对应 `353e6112d`，与基线之间仅 `VERSION`、`CHANGELOG.md` 不同。后端为 SQLite 临时内存数据目录，测试不涉及生产数据或磁盘断电持久性。

本轮结果：

- 现有 150 个相关专项测试文件，最终 965 项通过。首次隔离执行为 964 通过、1 超时；`canvas-edit-ui.test.ts` 在默认 5 秒限制下超时，单独以 30 秒限制复跑约 9.6 秒通过。该项记为测试时限问题，未记为产品缺陷。
- 新增诊断运行 107 个操作场景：77 个产生可转换命令，28 个转换失败，2 个被缺少首帧校验拦下且没有修改。后两项不能记为“生成成功”。
- 77 个可转换场景在真实后端执行并读取新快照，全部接受；另核对 221 项节点类型、普通字段值及删除结果，全部一致。
- 浏览器逐一打开 18 类默认节点及文件夹，未捕获页面异常。浏览器额外复现分镜图创建、转换方式切换、分镜表剪贴板粘贴的保存失败；参数变体创建实测保存成功。

**28 个失败场景归为下列 9 类保存问题，另发现 1 类镜头编号展示错误。没有修改产品逻辑。**

诊断脚本为 [`web/scripts/audit-canvas-node-save.tsx`](../../web/scripts/audit-canvas-node-save.tsx)，精简结果为 [`canvas-node-function-audit-2026-10-26.json`](./canvas-node-function-audit-2026-10-26.json)。脚本调用真实工厂、编辑 hook、复制 helper 和命令转换器；弹窗确认、模型配置就绪检查和生成排队回调采用受控实现，**不调用模型或 Worker**。因此生成相关结果只证明草稿构造/保存阶段，不证明计费、供应商生成或生成完成落地。

## 已确认的问题

### N01 · 分镜图及分镜分组写入工作流标记后无法保存 · P1

复现：创建分镜表，填写画面描述，在“更多操作”点击“创建 N 个图片节点”。

实际页面先提示“已创建 N 个图片节点”，随后显示“修改未保存”。错误与用户报告一致：

```text
canvas_editor_unmapped:node:<image-id>:metadata.shotIndex,metadata.workflowKind,metadata.workflowTitle
```

浏览器创建 3 个分镜图后，Agent 区显示本地共有 4 个节点；后端快照仍仅有原来的 1 个分镜表。该次创建没有提交为云端命令，不能按创建提示判定持久化成功。后续未删除问题节点或修复字段前，其他编辑也可能被同一批转换失败阻塞。

同类入口：生成未完成分镜图的草稿创建、已存在图片节点同步提示词、多图创建分镜组。分镜组实测首先在图片节点的 `shotIndex / workflowKind` 处失败。

来源：`use-canvas-storyboard.ts` 的 `ensureScriptImageNodes`（约 103–150 行）、`use-canvas-node-operations.ts` 的 `createStoryboardGroup`。能力目录没有对应图片字段；`canvas-editor-commands.ts` 严格拒绝未声明变化。

### N02 · 视频操作模式、首帧及镜头归属字段未接入保存 · P1

已执行的失败入口：

- “创建 N 个视频节点”。
- 直接生成镜头视频的草稿构造。
- 首帧模式下构造视频草稿。
- 将分镜行连接到已有视频，自动补齐提示词、时长和镜头归属。
- 普通视频配置中写入 `videoEditOperation`。

错误字段除 N01 的三项外，还包括 `videoEditOperation`；首帧路径另包含 `videoStartFrameNodeId`。这也说明只补 `workflowKind / workflowTitle / shotIndex` 不足以完成视频路径修复。

来源：`use-canvas-storyboard.ts` 的 `createScriptVideoNodes / createAndGenerateScriptVideos / generateScriptVideos`，`canvas-project-domain.ts` 的分镜行附着视频逻辑。

### N03 · 动作拆分 12 宫格草稿无法保存 · P1

受控确认后执行真实 `createScriptActionBoards`，生成回调只记录排队意图，不发模型任务。构造出的图片节点转换失败：

```text
metadata.actionBoardColumns,metadata.actionBoardRows,metadata.shotIndex,metadata.workflowKind,metadata.workflowTitle
```

来源：`use-canvas-storyboard.ts` 的 `createScriptActionBoards`，实际写入 4 行、3 列及动作板工作流归属。

### N04 · 14 类节点的剪贴板粘贴无法保存 · P1

失败类型：绘图、分镜表、技能、生成配置、背板、Markdown、SVG、HTML、全景、对比、图表、调色、转换、批量创作表。

错误均包含：

```text
metadata.copiedFromNodeId
```

普通图片、文本、视频和音频的该场景通过。转换器的复制来源识别明确只处理这四类，其余类型的剪贴板路径同样写入来源字段，但没有对应保存处理。

真实页面区分验证：

1. 分镜表右键“创建参数变体”：后端从 1 节点变为 2 节点，状态为已保存。
2. 再右键“复制节点”，随后“粘贴”：本地出现第 3 个分镜表，保存失败，报 `metadata.copiedFromNodeId`。

**不能把本项描述为所有复制入口都失败。** 参数变体/直接副本走 `duplicate_node` 命令；剪贴板粘贴在 `pasteCopiedNodes` 中修改本地图，仍经过差异转换。来源：`use-canvas-node-operations.ts`（约 375 行、484 行以后），`canvas-editor-commands.ts`（约 94 行）。

### N05 · 预演镜头创建无法保存 · P1

执行真实 `createPrevisShot('empty')` 后，新视频节点首先在以下字段失败：

```text
metadata.previsSceneId,metadata.previsShotId,metadata.shotIndex,metadata.videoEditOperation,metadata.workflowKind,metadata.workflowTitle
```

本次只确认创建及转换阶段；预演场景编辑、预览渲染和导出结果在此之前已经被保存缺口阻断，未按成功计数。来源：`use-canvas-previs.ts` 的 `createPrevisShot`。

### N06 · 创建引用组无法保存 · P1

选中两张已有图片，执行真实 `createReferenceGroup`，图片被收进新背板并写入 `referenceSetId`，转换首先拒绝：

```text
metadata.referenceSetId
```

背板还会写入工作流归属及 `referenceAssetNodeIds`；本轮未把第一处错误之后的字段全部按“独立失败”计数。修复时需检查完整分组图，而不是只删除首个报错字段。来源：`use-canvas-node-operations.ts` 的 `createReferenceGroup`。

### N07 · 转换节点仅切换处理方式就无法保存 · P1

真实页面新建转换节点，不需要连接素材或执行转换，把“Canny 边缘”改为“灰度图”。本地选项更新，但云端没有 `mediaConversion` 状态，报：

```text
metadata.mediaConversion.schemaVersion,metadata.mediaConversion.status,metadata.mediaConversion.updatedAt
```

能力目录仅映射 `mediaConversion.operation`，实际 `selectOperation → updateState` 会写入完整状态。开始处理、失败、过期及完成回调也使用该状态对象，后续需纳入同一合同审查；本轮没有把这些尚未完整执行的阶段当成已复现结果。

来源：`components/canvas/nodes/media-conversion-node.tsx` 的 `updateState / selectOperation`，`capability/editable.go` 的 `conversionOperation`。

### N08 · 批量创作生成产物草稿无法保存 · P1

在受控模型配置和本地排队回调下执行真实 `generateRows → confirmBatchGenDialog`，构造任务输出图片后，转换失败：

```text
metadata.batchInputNodeIds,metadata.batchOperation,metadata.batchRowId,metadata.batchSourceNodeId,metadata.workflowKind,metadata.workflowTitle
```

批量表本身的新增行、修改行、删除行、并发和全局提示词保存通过；错误发生在创建行产物节点并写入来源关系时。来源：`use-canvas-batch-table.ts` 的 `executeBatchGeneration`（约 189–239 行）。

### N09 · 应用项目画风后样式节点无法保存 · P1

在自由画布上下文中执行真实 `applyCanvasStyleAsync`，该上下文不写业务项目接口。生成样式文本节点后，转换失败：

```text
metadata.stylePresetId,metadata.styleProfileJson,metadata.workflowDescription,metadata.workflowKind,metadata.workflowTitle
```

来源：`use-canvas-style-workflow.ts` 的 `applyCanvasStyle`。关联业务项目时还存在先保存项目、再更新画布的两阶段路径，后续修复需回归二者一致性；本轮未执行关联项目分支，不宣称已经复现项目与画布不一致。

### N10 · 镜头编号显示多加 1 · P2

分镜产物把 `shotIndex` 设为 `row.shotNumber`，预演镜头也从 1 开始；但上下文摘要和节点搜索按 `shotIndex + 1` 展示及建立搜索词。

诊断输入 `shotIndex=1`，上下文标签及搜索说明返回“镜头 2”，搜索“镜头 2”会命中该节点。该项为实际纯函数输出验证，未伪装为整页搜索验收。

来源：`canvas-context-summary.ts:24`、`canvas-node-search.ts:24` 及 `:66`。修复前需统一序号语义，不能只改一处标签。

## 各节点覆盖结果

表中“基础”表示工厂创建、改名/移动/尺寸/锁定、删除均通过转换、后端执行和快照读取；不表示该节点的所有扩展功能均已通过。

| 节点 | 基础 | 剪贴板粘贴 | 本轮附加检查 |
| --- | --- | --- | --- |
| 图片 | 通过 | 通过 | 提示词通过；分镜图、动作板、分组和批量来源失败 |
| 文本 | 通过 | 通过 | 正文/富文本/字号通过；项目画风失败 |
| 视频 | 通过 | 通过 | 操作模式、首帧、分镜产物及预演失败 |
| 音频 | 通过 | 通过 | 提示词/速度字符串配置通过 |
| 绘图 | 通过 | N04 | 本轮未完整操作绘图引擎编辑与导出 |
| 分镜表 | 通过 | N04 | 行新增/修改/删除通过；附加产物功能见 N01–N03 |
| 技能 | 通过 | N04 | 空节点基础合同；技能执行未发起 |
| 生成配置 | 通过 | N04 | 工作流标题/参数配置通过；供应商执行未发起 |
| 背板/文件夹 | 通过 | N04 | 折叠参数及普通文件夹创建通过；引用组失败 |
| Markdown | 通过 | N04 | Markdown 正文保存通过 |
| SVG | 通过 | N04 | SVG 正文保存通过 |
| HTML | 通过 | N04 | HTML 正文保存通过 |
| 全景 | 通过 | N04 | 投影/来源/底部参数保存通过；未验收真实全景素材旋转 |
| 对比 | 通过 | N04 | 默认组件打开；双图拖动比较未完整验收 |
| 图表 | 通过 | N04 | JSON 数据及折线配置保存通过 |
| 调色 | 通过 | N04 | 四项调色参数保存通过；未把参数读回视为图片导出通过 |
| 转换 | 通过 | N04 | 切换算法即失败，见 N07 |
| 批量创作表 | 通过 | N04 | 表格编辑通过；生成行产物草稿失败 |

现有 150 文件回归还覆盖上传/替换、引用与连线、图片工具保存、分镜及批量表、绘图文档、媒体版本、播放、恢复、日志和冲突等相邻实现。其中包含纯函数、组件 harness 和源码规则测试，不能用总通过数替代每个按钮的真实业务验收。

## 修复建议与复测顺序

1. **统一工作流节点的可写字段及归属命令。** 先处理 N01/N02/N03/N05/N08/N09；服务端需要验证镜头、预演和批量来源节点真实存在且归属正确。生成/版本/资源身份仍由服务端重建，避免开放任意 metadata 写入。
2. **把剪贴板粘贴迁入明确复制命令，或补全全部节点类型的来源重建。** 保留新 rowId、绘图文档身份、子节点与引用重映射，回归同画布及跨画布粘贴。参数变体已有命令路径可作参照。
3. **为转换状态建立完整保存协议。** 区分用户选择的算法、浏览器临时进度和需持久化的结果/来源；不能为了通过转换而删除状态或全局忽略未知字段。
4. **补齐分组关系，并统一镜头编号。** 分组需一起检查父子节点、引用字段和派生连线；序号需回归搜索、摘要和排序。
5. 每类修复以真实生产者的输出接入转换器、后端和快照读回，再在页面复验。不要将“本地节点已创建”“任务已排队”当作保存/生成成功。

## 可复跑方法与剩余边界

在 `web/` 中运行：

```bash
bun scripts/audit-canvas-node-save.tsx test/fixtures/canvas-editor-capabilities.json /tmp/node-audit.json
```

优先把第一个参数替换为当前后端 `/api/desktop/capabilities` 返回的 `data`。脚本发现失败操作会返回退出码 1，并保留完整 JSON；当前 28 个失败是诊断结果，不应修改断言把它们变成通过。该脚本没有加入默认测试命令。

剩余业务验收包括：真实供应商生成及计费、角色卡形象与声音资源、完整绘图编辑/导出、本地 AI 深度/姿态/线稿/抠图运行时、视频合并及字幕导出、真实全景/对比素材交互、第三方插件节点、跨物理设备和生产部署。已被保存缺口阻断的后续流程应在修复后继续，当前不能标记“所有节点功能全部通过”。
