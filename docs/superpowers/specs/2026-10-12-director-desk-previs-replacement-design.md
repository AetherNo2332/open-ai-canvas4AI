# DirectorDesk 预演台替换设计

## 目标

用 `mangfufu/director-desk` 的三维场景编辑、走位、镜头和时间轴能力替换当前预演台的编辑运行时，同时保留影策现有的 Agent 控制合同、审批与持久化链路，并保留“图片 → 可编辑 3D 预演场景/镜头”的一键入口。

## 已确认范围

- 图片入口的目标是图片到可编辑 3D 预演场景/镜头（A），不是只生成一张静态分镜图。
- 画布、项目、资源、Agent 运行和任务队列仍由影策负责。
- DirectorDesk 作为嵌入式编辑/采样运行时，不引入第二套产品级 Agent、权限或持久化体系。
- 第一阶段覆盖演员、道具、摄影机、路径、时间轴、镜头切换、白膜视频/构图帧导出。
- DirectorDesk 专属的物理、插件、深度视频、资源包和完整工程文件交付不作为第一阶段持久化合同；需要时通过后续版本扩展。

## 现状与约束

当前影策预演台的持久化事实源是 `PrevisScene`，写入画布项目的 `previsScenes`，并通过 `previs_scene_read`、`previs_scene_create`、`previs_apply_patch`、`previs_preview` 被 Agent 访问。后台渲染由现有预演 renderer 和任务服务完成，产物再写回画布资源节点。

DirectorDesk 以 `Project`/`SceneDocument` 为模型，核心能力在 `src/model.ts`、`src/engine.ts`、`src/scenes/*`、`src/animation/*`、`src/cinematography/*` 和 `src/ui/*`；其 Electron 文件层、MCP host、工程文件保存和内置 AI 不应直接进入影策产品边界。

## 架构

### 1. 三层边界

1. **影策域层**：继续拥有 `PrevisScene`、画布节点、资源引用、用户权限、审批、快照版本、任务队列和 renderer API。
2. **适配层**：新增 DirectorDesk 适配模块，负责 `PrevisScene ↔ Project/SceneDocument` 转换、ID 稳定性、字段降级、增量刷新和导出帧采集。
3. **DirectorDesk 运行时层**：复用其模型、场景采样、动画/路径、摄影机和视口 UI；通过显式 `AppContext`/runtime bridge 接受影策保存和资源服务，不直接读写影策数据库或 localStorage。

### 2. 编辑生命周期

- 打开预演节点：影策读取当前 `PrevisScene`，适配为 DirectorDesk `SceneDocument`，初始化运行时。
- 用户编辑：DirectorDesk 先在本地 history 中变更，提交时转换回 `PrevisScene`，进入影策现有 `commit / save coordinator / onChange` 链路。
- Agent 修改：后端通过现有工具合同修改 `PrevisScene` 并返回新 snapshot；前端收到 Agent 事件后重新适配当前场景，保持编辑器视图和选择状态可恢复。
- 关闭预演台：先完成保存检查；必要时生成缩略图/构图帧，再释放 DirectorDesk runtime。
- 发生快照冲突：拒绝静默覆盖，提示重新读取并按现有 Agent/用户冲突策略处理。

### 3. 渲染与资源

DirectorDesk 的采样器用于生成预览帧和导出输入；实际后台白膜任务继续走影策 `previs_preview` → task queue → renderer → resource store → canvas writeback。DirectorDesk 不直接创建影策资源记录，不绕过配额、权限、任务状态和诊断记录。

## 数据适配

新增纯函数适配器，至少提供：

- `previsSceneToDirectorDocument(scene: PrevisScene): SceneDocument`
- `directorDocumentToPrevisScene(document: SceneDocument, base: PrevisScene): PrevisScene`
- `directorPreviewInput(document, shotId): PrevisRenderInput`
- `directorImageReferenceToPrevisAsset(reference): PrevisObject/PrevisEnvironment`

映射规则：

- `PrevisObject actor/model/billboard` 映射为 DirectorDesk `Entity actor/prop`；保留稳定 ID、名称、颜色、位置、旋转、缩放和可见性。
- `motionPath` 映射为 DirectorDesk `MotionPath`；点按距离/时间保持可逆，缺少 DirectorDesk 专属字段时使用默认值。
- `PrevisCamera` 映射为 `Entity kind=camera`；焦段、目标、位置、路径和 POV 模式保留。
- `PrevisShot` 映射为 duration、fps、aspect、cuts；多个 shot 组成 `SceneDocument.scenes` 或等价的单文档戏段结构，适配器必须保持 shot ID 稳定。
- 影策角色卡绑定保留在 `characterBinding` 扩展字段；DirectorDesk 只消费可渲染的资产/模型引用。
- DirectorDesk 无法表达的字段（资源节点 ID、影策任务产物、审批元数据、快照哈希）留在影策基线对象或扩展元数据中，不能丢失。

## Agent 控制：导演工作流层

Agent 不再只按“读取/修改场景 JSON”的 CRUD 顺序工作，而是按导演任务顺序运行：

1. 理解图片、剧本段落、当前戏段和可用资产。
2. 建立场面调度：人物、道具、空间关系、走位和动作。
3. 设计镜头覆盖：摄影机、焦段、景别、机位、运镜和切镜。
4. 验证连续性：路径表面、空间关系、遮挡和接拍状态。
5. 生成白膜预演并等待任务终态。
6. 交付镜头参数和视频生成提示词。

现有四个工具继续作为兼容和安全边界：

- `previs_scene_read`：读取影策投影，不暴露 DirectorDesk 原始工程 JSON、URL 或 storage key。
- `previs_scene_create`：创建影策场景和工作站节点，返回稳定 node/scene ID。
- `previs_apply_patch`：在场景 snapshot 校验、审批和 CAS 约束下应用语义操作，再由适配层写入 DirectorDesk。
- `previs_preview`：只提交渲染任务，必须核验任务终态和画布写回后才报告完成。

在兼容层之上新增导演语义工具，由后端编译为 DirectorDesk 原子操作：

- `director_block_scene`：根据图片或剧本建立演员、道具、空间关系和初始走位。
- `director_design_shot`：创建或修改一个镜头的摄影机、景别、焦段、运镜和镜头切换。
- `director_draw_action`：为演员或摄影机生成路径、动作片段和时间范围。
- `director_check_continuity`：执行路径表面、遮挡、空间关系和前后镜头接拍检查。
- `director_preview_shot`：提交白膜视频/构图帧并追踪任务终态。
- `director_handoff_shot`：输出参考视频提示词、纯文本提示词和镜头参数。

这些工具内部使用 DirectorDesk 的 `director_read`、`director_spatial`、`director_apply`、`director_view`、`director_continuity`、`director_path_surface`、`director_scan` 和 `director_job` 能力，但不直接向 Agent 暴露原始工程写入面。每个写工具仍必须经过用户权限、审批、revision、requestId、数量限制和影策资源校验。

## 图片到可编辑预演场景

图片入口继续使用画布资源引用和支持图片输入的文本模型，并改为导演工作流：

1. 前端选择图片节点并创建带资源引用的 Agent 请求。
2. Agent 必须先调用图片检查工具读取真实图像，再调用 `canvas_get_state` 与 `previs_scene_read`。
3. 已有场景则读取并补丁更新；没有则调用 `previs_scene_create`。
4. Agent 调用 `director_block_scene`，根据真实图像建立 actor、道具、环境和空间关系，不把原图伪装成场景对象。
5. Agent 调用 `director_design_shot` 设置主机位、景别、焦段、构图和必要的镜头覆盖。
6. Agent 调用 `director_check_continuity` 检查遮挡、路径和接拍问题；发现问题后再执行一次受控修改。
7. 前端收到场景变更后刷新 DirectorDesk runtime；用户可继续编辑走位、镜头和时间轴。
8. 用户点击“生成白膜”或 Agent 调用 `director_preview_shot`/`previs_preview`，走现有后台任务和写回链路。

此入口不改变画布结构化分镜表的既有 `canvas_create_storyboard` / `canvas_edit_storyboard` 合同。

## 迁移策略

- 不直接覆盖当前桌面端工作树；先以隔离分支接入 DirectorDesk 核心模块。
- 第一阶段按模块迁移：模型与采样 → 适配器 → 视口/时间轴 UI → 导出桥 → Agent 事件刷新。
- 保留旧 `CanvasPrevisWorkbench` 作为可回退实现，直到适配器、保存、渲染和图片入口通过验收；切换由单一 feature flag 控制，不能形成两套长期产品入口。
- DirectorDesk 的依赖版本、许可证和第三方资产清单随迁移记录；MIT 代码可纳入，但第三方动作/模型资源必须按其许可证处理。

## 验收标准

1. 打开已有预演场景，演员、道具、摄影机、路径、镜头切换和时间轴与原场景一致。
2. 用户编辑后关闭并重开，保存结果、撤销/重做和快照版本正确。
3. Agent 可以按导演工作流完成场面调度、镜头设计和连续性检查；底层写入仍受审批和 snapshot 冲突保护。
4. Agent 调用 `previs_preview` 后，后台 renderer 生成白膜视频和构图帧，任务中心、资源节点和预演台状态一致。
5. 从图片触发 A 流程后，经过“场面调度 → 镜头设计 → 连续性检查”得到可编辑 actor/道具/摄影机场景；不支持图片输入的模型会明确失败，不会误生成空场景。
6. 图片生成的预演场景可以继续手动编辑并再次渲染。
7. 现有画布 Agent、结构化分镜表和普通媒体生成回归测试不受影响。
8. 桌面端和 Web 端都不直接暴露 DirectorDesk 的本地工程文件、MCP token 或影策资源内部地址。

## 风险与取舍

- **模型差异**：DirectorDesk 的 `Project` 比 `PrevisScene` 更丰富；第一阶段以影策合同为准，避免为了追平所有字段而重写后端。
- **UI 迁移量**：DirectorDesk 当前是原生 DOM/UI 组合，嵌入 React 需要明确挂载/卸载边界；优先复用核心模块，不把全局 CSS 和菜单直接复制进影策。
- **资源加载**：DirectorDesk 依赖本地模型/动作资源，而影策使用资源服务；先提供受控 URL/Blob loader，再逐步补齐资源包能力。
- **性能**：三维运行时和画布同时存在；预演打开时暂停非必要画布渲染，关闭后释放 WebGL、模型和纹理引用。
- **双引擎一致性**：迁移期间旧预演台只作为回退，不允许 Agent 分别写入两套场景格式。
- **工具过度暴露**：不把 DirectorDesk 全量 `director_apply` 原始合同直接交给模型，优先使用高层导演工具，避免模型绕过导演顺序和业务校验。
