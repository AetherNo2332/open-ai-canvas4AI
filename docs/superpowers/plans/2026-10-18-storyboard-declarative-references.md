# 分镜引用字段与画布连接托管 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 使用 superpowers:executing-plans 逐任务执行；用户明确选择子代理方式时使用 superpowers:subagent-driven-development。用下面的复选框跟踪进度。本次交付仅为开发计划。

**Goal:** Agent 通过字段声明素材引用与分镜产物归属，画布服务统一校验、保存并派生连接；Agent 不再直接连接、重连或删除连接。

**Architecture:** 分镜继续使用 assetBindings／表级 referenceNodeIds，媒体草稿使用现有 generationSpec.referenceBindings。画布领域层负责解析引用和协调连接，原生命令、Agent 写入和任务落库调用同一个领域实现，在持久化差异与历史记录之前完成同步。前端将素材选择、拖线和断线转换为业务字段修改，接收服务端权威结果。

**Tech Stack:** Go 1.25、现有 Gin/GORM 与画布 command/task 服务；React 19、TypeScript、Bun、Zustand；沿用当前测试工具和协议生成器，不新增运行时依赖。

**Spec:** [可行性评估](</C:/Users/13537/Documents/ChatGPT/openai canvas/outputs/storyboard-declarative-references-feasibility-2026-10-18.md>)。本计划补充的接口名称、排序、作用域和迁移规则是实现选择，并非已实现能力。

**Baseline:** AetherNo2332/open-ai-canvas4AI，main 提交 [a6495805](https://github.com/AetherNo2332/open-ai-canvas4AI/tree/a6495805efc19101b2a8aaaefaded4722a5e7766)。本地源码参考目录为 C:/Users/13537/Documents/ChatGPT/openai canvas/open-ai-canvas-source，固定版本阅读快照为 C:/Users/13537/Documents/ChatGPT/openai canvas/.local/research/upstream-a6495805。执行时项目根 P 必须是该提交或其后已核对差异的可写工作树；下面 P/ 路径均相对于这个真实项目根，快照目录不能作为运行项目。

## Global Constraints

- 先处理既有分镜和生成链路，补齐普通输入节点后，最后关闭 Agent 连线操作。全套任务在一个功能分支集成，未完成切换验收前不发布。
- 每类对象只有一个引用权威字段。connections 是业务关系的派生展示；受托管关系不能再从旧连接反向并集补回引用。
- 分镜最多 100 行；assetBindings 最多 100 项，priority 为 0–100；生成工具当前最多 16 个媒体节点引用，实际模型限制更低时使用模型限制；超限在报价／收费／任务提交前拒绝，不能静默截断。
- role 保持 character、environment、wardrobe、prop、weapon、style、motion、audio；GenerationSpec 的媒体类型及 reference、source-text、first-frame、last-frame、mask 角色沿用现有合同。
- 字段缺失表示不修改，[] 表示清空当前作用域；清空行绑定不取消表级共享引用。
- 行身份使用真实 rowId，重排只改变 shotNumber；Agent 不能直接写 imageNodeId、videoNodeId、taskId、status 或伪造素材 URL。
- 角色卡和绘图通过现有素材适配器解析；不能把 character 错当成 GenerationSpec.mediaType，也不能用角色名称猜素材身份。保留已有资源权限、角色版本、模型能力、费用和审批规则。
- 字段、托管边、读写集合、事件和逆向差异在同一事务内提交；事务内不调用上游模型、不嵌套写事务，事件在提交后发出。
- 已提交任务输入保持冻结；后续编辑只改变草稿／下一次生成。迟到结果不能恢复已删除行、节点或已替换的输出关系。
- 第一版素材库资产先绑定到当前画布节点，再引用 nodeId；不新增跨画布 assetId 引用、自动下单或直接资源地址输入。
- 原有手动自由连接可用于画布展示，Agent 不再维护装饰性自由连接。这里的自由连接不能绕过托管输入合同进入生成请求。
- 遵守项目 AGENTS.md：Go 域包不 import app/service；业务前端请求走 http；前端使用当前皮肤 token。新增协议由 Go 合同生成 TypeScript，禁止手改 generated 文件。
- 不新增数据表；使用现有文档 JSON、ConsumerJSON、任务输入及事件／回执机制。若执行中发现必须增加数据库列，先记录具体缺口并修订计划。
- 本计划不含 SSH、生产文件、容器修改或部署。后续涉及这些操作时按用户要求先复盘方案并评估风险；不调用 Astra，不做无用文件哈希验证。

## Review Focus

1. 行绑定被清空但旧边仍存在：下一次生成不得重新引用该素材；Task 2、3、5 覆盖。
2. 表级、行级、角色卡和关键帧引用同一素材：共享来源解释清楚，提示词编号与提交数组一致；Task 2、4、5 覆盖。
3. 用户改行／换输出时旧任务完成：资源保存，旧关系不复活，收费不重复；Task 4、6 覆盖。
4. 全局关闭 Agent 连接操作：compare、panorama、config、批量表等仍可用字段表达输入；Task 1、3、5、8 覆盖。
5. 旧画布字段与边相互矛盾：一次规范化必须可预览、可恢复，不能静默合并或丢数据；Task 7 覆盖。

## 数据合同与固定行为

| 对象 | 权威字段与操作 |
| --- | --- |
| 分镜共享上下文 | storyboard.referenceNodeIds；canvas_create_storyboard 增加可选顶层 referenceNodeIds，canvas_edit_storyboard 增加 set_references 动作。 |
| 分镜行 | assetBindings；characters 保留人物与表演描述，明确 characterImageNodeId 才参与图片引用。历史 characterAssetId 的节点匹配只允许唯一结果，否则返回选择冲突。 |
| 媒体草稿 | generationSpec.referenceBindings；generate_media 的普通模式仍接受 sourceNodeId／referenceNodeIds，由服务端一次转换为该合同，旧 metadata.referenceNodeIds 只作为镜像。 |
| 其他输入节点 | 给 CanTarget 且无上述专用结构的节点增加 metadata.inputBindings: ReferenceBinding[]；复用同一 ReferenceBinding 类型和字段校验，不新造另一套引用格式。包括 config、svg、html、panorama、compare、chart、colorgrade、media-conversion。 |
| 批量表 | 每行 inputNodeIds、textNodeIds；系统维护 outputNodeId。contentKind=storyboard 时通过现有标准 StoryboardRow 适配器读取 storyboardRows，不按列名重建引用。 |
| 分镜输出 | 增加原生命令 bind_storyboard_output：id=表 ID、rowId、outputMode=image/video、sourceNodeId=现有产物 ID；sourceNodeId 空串表示解除该槽位。Agent 通过 canvas_edit_storyboard 的 bind_output／unbind_output 动作调用语义入口。 |
| 托管边 | 保留 relation、storyboardRowId，增加服务端维护的 managedBy="reference-fields-v1"、referenceOwnerNodeId、referenceBindingId；客户端不能伪造归属标记。 |
| 文档切换标记 | 根 referenceFieldsVersion=1；新画布创建时写入。无标记的历史画布通过 Task 7 一次规范化，切换后不保留长期双读逻辑。 |

有效分镜输入排序固定为：显式 first-frame／last-frame／mask，行 assetBindings 按 priority 降序且同优先级保持数组顺序，未重复的人物图片引用，表级引用按原顺序。普通媒体使用 referenceBindings.order。相同 nodeId+生成角色去重；普通 reference 与同节点的特殊帧角色重合时保留特殊角色，first-frame 与 last-frame 可以各自保留。文本上下文独立处理，不占媒体序号。

storyboardVideoInputMode=direct 时不自动引入该行图片；keyframe 时将该行 imageNodeId 作为 first-frame，图片未就绪则拒绝生成。首尾帧／mask 的作用通过 GenerationSpec 表达，不靠边顺序猜测。角色卡展开与实际模型计数以已有服务端适配器结果为准。

canvas_edit_storyboard 的新动作合同：

- set_references：nodeId、snapshotHash、referenceNodeIds；不传 rowId 或行 patch。
- update／append：沿用现有规则，patch 可写合法 assetBindings；角色和输出状态的保护字段保持不变。
- bind_output／unbind_output：nodeId、snapshotHash、rowId、outputMode；绑定另传 outputNodeId。只改变归属，保留原媒体及资源；活跃任务占用的槽位拒绝修改。
- 原生 edit_storyboard 增加 set_references，通过 patch.referenceNodeIds 执行；新增 bind_storyboard_output 用于输出归属，拒绝用行 patch 直接修改输出 ID。

分镜生成／选模型的参数分两支：

- 普通模式保持原参数。
- 分镜模式同时提供 storyboardNodeId、storyboardRowId、mode；generate_media 仍提供 nodeId、title 和模型选择。prompt、引用及视频时长从行解析，不传 sourceNodeId、referenceNodeIds、referenceTransientIds；提供冲突的副本参数返回 storyboard_input_conflict，不能静默覆盖。
- model_list 接受同一对分镜身份字段，使用相同解析器筛选模型。
- 第一版不提供临时追加分镜素材字段；需要的素材先写入该行绑定。
- 系统在创建／接纳产物节点时写输出归属。已有非空产物不被生成请求隐式覆盖，维持现有重新生成授权与任务准入规则。

## 文件职责

| 范围 | 新增或修改位置（均为 P/） | 职责 |
| --- | --- | --- |
| 共享领域 | backend/internal/canvas/references/{types,resolve,reconcile,migrate}.go | 引用解析、连接协调、一次历史规范化；无数据库和网络副作用。 |
| 能力合同 | backend/internal/canvas/capability/{structured,editable,rows}.go；backend/internal/canvas/contract/generation.go | 复用 ReferenceBinding，公开可编辑字段和引用模式。 |
| 原生命令 | backend/internal/canvas/commands/{contracts,structured,planner,host_mutation,storyboard_result,native_connections}.go | 语义动作、提交前同步、冲突集合、系统输出归属。 |
| Agent 写入 | backend/internal/app/{cloud_agent_storyboard,cloud_agent_tools,cloud_agent_approval_preview,cloud_agent_generation_references,cloud_agent_media,cloud_agent_prepared_media,cloud_agent_generation_contract,cloud_agent_canvas_events}.go | 参数分支、统一领域调用、费用前准入、完整事件。 |
| 文档保存 | backend/internal/canvas/canvas_history.go；backend/internal/app/canvas_bridge.go | 旧写入入口同样执行协调；不另建独立保存逻辑。 |
| 任务生命周期 | backend/internal/canvas/tasks/{contracts,service,structured,materializer,active_target}.go；backend/internal/app/cloud_agent_media_writeback_test.go | 复用 Consumer 和持久任务绑定，控制迟到结果。 |
| 前端输入 | web/src/lib/canvas/{generation-contract,canvas-storyboard-materializer,canvas-editor-commands,canvas-node-copy,canvas-batch-storyboard}.ts | 统一编辑合同，移除生成输入的旧边并集，复制引用。 |
| 前端交互 | web/src/pages/canvas/{use-canvas-storyboard,use-canvas-connection-controller}.ts；web/src/components/canvas/storyboard-assets-cell.tsx | 选择素材、拖线、断线及共享引用来源提示。 |
| 前端新增 | web/src/lib/canvas/canvas-reference-edits.ts；web/src/components/canvas/canvas-reference-migration-panel.tsx | 手势转语义编辑、迁移冲突选择。 |
| 生成消费 | web/src/lib/canvas/canvas-project-generation.ts；web/src/services/{canvas-generation-continuation,desktop/canvas-generation}.ts | 从权威规格生成输入，等待服务端提交／落库回执。 |
| Agent 合同发布 | backend/internal/app/agent-tool-descriptions.md；cloud_agent_tool_schema*.go、agent_tool_snapshots.go；agent/test/tool-snapshot-registration.test.ts | 关闭边操作，更新工具合同和失效的快照。 |
| 文档 | docs/content/docs/backend/{http-api,code-map,backend-database}.mdx；overview/features.mdx；progress/{todo,pending-test}.mdx | 字段、来源、迁移、实现与待验收状态。 |

## Task 1：固定引用合同与共享样例

**依赖：** 无。**Files：** 修改文件职责表中的能力合同、web/src/types/canvas.ts；新增 backend/internal/canvas/references/types.go、backend/internal/canvas/references/testdata/reference-fields-v1.json；测试 backend/internal/canvas/capability/registry_test.go、web/test/canvas-reference-contract.test.ts。

**Interfaces：** 新包 references 定义 Scope{Kind,NodeID,RowID,Mode string}，Kind 为 generation、node、storyboard-context、storyboard-row、batch-row；Resolution{Bindings []contract.ReferenceBinding, DependencyNodeIDs []string, Edges []EdgeIntent}。EdgeIntent 包含 Key、OwnerNodeID、RowID、BindingID、FromNodeID、ToNodeID、FromHandleID、ToHandleID、Relation 字符串字段。Result{Document map[string]any, ReadNodeIDs []string, ReadEdgeIDs []string}。类型供 Task 2–4 使用，包不 import commands/app/service。

- [ ] 检查工作区和 AGENTS.md，按 using-git-worktrees 技能复用合适的工作树或创建隔离工作树；从基线建立 codex/storyboard-reference-fields 分支，保留现有修改。把实际 P 写入执行记录。
- [ ] 添加 TestReferenceFieldsContract 和前端同名合同用例：媒体使用 generationSpec；非生成输入节点使用 inputBindings；frame 不增加输入合同；行 patch 仍拒绝 imageNodeId/status。样例覆盖显式空数组、两个同优先级素材、角色卡、首帧及字段／旧边矛盾。
- [ ] 运行 backend/ 下 go test ./internal/canvas/capability -run 'TestReferenceFields' -count=1；web/ 下 bun test test/canvas-reference-contract.test.ts。预期新行为断言失败。
- [ ] 提取 capability.referenceBindingField() PatchField，在 generationSpecField 和 inputBindings 中复用；保持现有 ReferenceBinding 数据类型。增加文档标记及边的服务端归属类型，定义实际允许的节点字段，读取／审批投影同步公开这些字段。
- [ ] 重跑上述专项并通过；执行 backend/ 下 go generate ./internal/canvas/contract，生成文件无无关变化。提交：feat(canvas): 引用合同 - 统一分镜与节点输入字段。

## Task 2：实现引用解析与托管边协调

**依赖：** Task 1。**Files：** 新增 references/resolve.go、reconcile.go、resolve_test.go、reconcile_test.go；复用 capability 与 connection 领域校验。

**Interfaces：** Resolve(doc map[string]any, scope Scope, registry *capability.Registry) (Resolution,error)；Reconcile(before,candidate map[string]any, registry *capability.Registry, nextID func() string) (Result,error)。不修改输入对象；新增边 ID 仅在缺少业务身份匹配时调用 nextID，重复协调不分配新 ID。

- [ ] 新增 TestResolveIgnoresStaleEdges、TestResolveStableRolesAndOrder、TestResolveKeyframeMode、TestReconcileIsIdempotent、TestReferenceInputPolicies；失败断言示例：

```go
if len(got.Bindings) != 0 { t.Fatal("清空显式绑定后仍从旧边恢复了素材") }
if allocatedOnSecondPass != 0 { t.Fatal("重复协调新增了连接身份") }
if beforeJSON != afterInputJSON { t.Fatal("纯函数修改了输入文档") }
```

- [ ] 在 backend/ 运行 go test ./internal/canvas/references -count=1，确认新用例失败。
- [ ] 依照固定排序解析字段；文本、媒体及角色卡适配需求分开。验证存在性、自引用、类型、重复、环路、节点容量；资源就绪和实际模型能力留在生成准入。TestReferenceInputPolicies 明确覆盖 compare 输入 3 张图、panorama 输入 2 张图及批量行输入 11 张图被拒绝，相应的 2／1／10 张上限被接受。特殊帧和角色卡的绑定与同源普通引用去重，生成前实际展开不得改变已展示编号。
- [ ] 按 EdgeIntent.Key 匹配归属、行、角色和端点，保留未变化边 ID、锚点和扩展字段；删除本功能拥有的过期边，保留其他边。协调覆盖共享上下文、行素材、输出归属、批量行和普通输入节点；共享引用不需要 Agent 复制到每个输出。
- [ ] 重跑专项通过；共享样例验证顺序与重复调用结果完全一致。提交：feat(canvas): 引用解析 - 按业务字段协调托管连接。

## Task 3：接入所有服务端写入与历史事务

**依赖：** Task 2。**Files：** 修改 planner.go、host_mutation.go、structured.go、native_connections.go、storyboard_result.go、canvas_history.go；app/cloud_agent_storyboard.go、cloud_agent_approval_preview.go、cloud_agent_generation_references.go；新增 commands/reference_fields_test.go、app/cloud_agent_reference_fields_test.go。

**Interfaces：** 新增 commands/planner_reference_fields.go 的 reconcileReferenceFields(before,candidate Document, registry *capability.Registry) (references.Result,error)，仅适配类型及 ID 分配。增加 Operation.OutputMode string；bind_storyboard_output 和 edit_storyboard/set_references 的参数按上文合同。app 通过 references 调用同一实现，不导入 planner 私有函数。

- [ ] 写 TestReferenceFieldsCommitAtomically、TestReferenceFieldsUndoRedo、TestReferenceFieldsTrackReferencedVersions、TestCloudAgentHeadlessReferenceEdit：校验单次提交同时改变行与边、回滚二者、无浏览器也生效，以及被引用节点被删／变更时拒绝旧版本写入。
- [ ] backend/ 运行 go test ./internal/canvas/commands ./internal/app -run 'TestReferenceFields|TestCloudAgentHeadlessReferenceEdit' -count=1，确认失败。
- [ ] 在 Plan 与 ExecuteHost 计算 Diff／Inverse 前协调；读集合加入受影响引用、所属表及输出，不能让无关节点移动导致整个画布冲突。增加语义输出绑定，验证类型、行存在和活跃任务占用；禁止直接写系统字段。
- [ ] Agent 预览和真实写入都协调后再生成预览／事件；SaveDocumentWithHistory 作为旧保存入口的一致性防线，采用同一领域实现。拆镜整体替换、删除节点也清理托管关系；不要在前端补写第二次。
- [ ] 更新后端合同生成：backend/ 下 go run ./internal/canvas/commands/generate；重跑专项通过，事件包含实际节点和连接变化，单步撤销／重做可恢复二者。提交：feat(canvas): 引用写入 - 原子提交字段连接与历史。

## Task 4：分镜生成归属、模型筛选与冻结输入

**依赖：** Task 3。**Files：** 修改 app/cloud_agent_media.go、cloud_agent_prepared_media.go、cloud_agent_generation_contract.go、cloud_agent_tools.go；tasks/contracts.go、service.go、structured.go；新增 app/cloud_agent_storyboard_generation.go、cloud_agent_storyboard_generation_test.go。

**Interfaces：** cloudAgentMediaArgs 增加 StoryboardNodeID、StoryboardRowID；新增 resolveCloudAgentStoryboardGeneration(doc map[string]any,a cloudAgentMediaArgs) (cloudAgentMediaArgs,tasks.Consumer,error)。返回的内部参数是唯一解析结果，Consumer{Kind:"storyboard",RowID,Field:"imageNodeId"/"videoNodeId",OutputNodeID:a.NodeID}；持久任务绑定的 TargetNodeID 为分镜表。复用已有 ConsumerJSON，不新增数据库列。

- [ ] 写 TestStoryboardGenerationResolvesDeclaredInputs、TestStoryboardModelListUsesSameInputs、TestStoryboardGenerationRejectsConflictingInputs、TestStoryboardPreparedRowChanged、TestStoryboardReferenceLimitBeforeBilling：验证实际 provider 输入来自行；非法／越权字段在准备前拒绝，模型超限在准入阶段拒绝，二者的 Commit／provider／计费调用次数均为 0；准备后行内容变化必须重新准入。
- [ ] backend/ 运行 go test ./internal/app ./internal/canvas/tasks -run 'TestStoryboardGeneration|TestStoryboardModelList|TestStoryboardPrepared|TestStoryboardReferenceLimit' -count=1，确认失败。
- [ ] model_list 和 generate_media 接受普通／分镜两支参数，分镜分支禁止重复输入来源，行提示词和时长由服务端取得。使用同一 Resolve 结果完成角色／绘图资源、模型限制、顺序、价格和准备输入处理；把行、模式、共享引用与角色版本加入已有依赖检查。
- [ ] 创建草稿／提交任务时由系统设置输出槽位及 Consumer；沿用原有付费准入和持久化事务。明确选择分镜 Consumer 后的任务结果仅走结构化 materializer，不能再通过通用 completeCloudAgentMediaNode 写第二遍。
- [ ] 重跑专项通过；普通模式行为保持，分镜模式不需要任何连线调用。提交：feat(agent): 分镜生成 - 声明镜头归属并冻结引用输入。

## Task 5：前端字段编辑、手动连线与生成统一

**依赖：** Task 3、4。**Files：** 新增 canvas-reference-edits.ts、web/test/canvas-reference-edits.test.ts；修改 use-canvas-storyboard.ts、use-canvas-connection-controller.ts、storyboard-assets-cell.tsx、canvas-storyboard-materializer.ts、canvas-storyboard-operations.ts、canvas-editor-commands.ts、canvas-project-generation.ts、generation-contract.ts 和现有生成消费入口。

**Interfaces：** buildCanvasReferenceEdit(document:CanvasDocument, intent:ReferenceEditIntent): Operation[]。CanvasDocument 从现有 canvas-event-projection.ts 导入，Operation 从 command-contract.generated.ts 导入；新文件导出以下联合类型，供画布 hook 调用：

```ts
type ReferenceEditIntent =
    | { kind: "set_row_assets"; nodeId: string; rowId: string; assetBindings: StoryboardAssetBinding[] }
    | { kind: "set_board_references"; nodeId: string; referenceNodeIds: string[] }
    | { kind: "set_node_inputs"; nodeId: string; bindings: ReferenceBinding[] }
    | { kind: "bind_output"; nodeId: string; rowId: string; outputMode: "image" | "video"; outputNodeId: string }
    | { kind: "unbind_output"; nodeId: string; rowId: string; outputMode: "image" | "video" };
```

StoryboardAssetBinding 从 types/canvas 导入，ReferenceBinding 从 generation-contract.generated.ts 导入，不新增文档或引用的平行类型。前端只产生字段或语义输出操作，不为托管关系产生连接操作；批量表继续写现有行输入字段，原有自由连接走现有手动入口。

- [ ] 添加用例“清空行引用不读旧边”“拖线只发语义操作”“共享引用在行内显示继承来源”“keyframe缺图不能提交”“非生成节点字段保持顺序”；断言：

```ts
expect(ops.some(op => ["connect_nodes", "delete_connection", "update_connection"].includes(op.type))).toBe(false);
expect(update.patch.assetBindings).toEqual([]);
expect(submitCount).toBe(0); // 所需首帧未就绪
```

- [ ] web/ 运行 bun test test/canvas-reference-edits.test.ts test/canvas-storyboard-assets.test.ts test/canvas-editor-commands.test.ts，确认新用例失败。
- [ ] 素材选择与拖线调用统一编辑函数，断线删除相应显式字段；对继承引用引导修改所属表／人物／首帧选择，不静默删除其他作用域引用。托管边显示来源，已提交任务仍显示其冻结来源与状态。
- [ ] 删除 storyboardRowReferenceNodeIds 对旧边的输入并集，前端输出草稿创建通过语义绑定而非手工造边。canvasEditorCommands 识别系统归属字段并避免重复提交服务端派生边；生成入口从 GenerationSpec／行身份组装请求，渲染可继续使用服务端 connections。
- [ ] 重跑专项通过；补齐普通 config／compare／panorama 等节点输入编辑，错误经既有 API 机制显示。提交：feat(canvas): 引用交互 - 素材选择与拖线统一为字段编辑。

## Task 6：复制、并发、删除与任务迟到结果

**依赖：** Task 3–5。**Files：** 修改 canvas-node-copy.ts、commands/native_versions.go、commands/history.go、tasks/materializer.go；扩展 tasks/{references,recovery}_test.go、commands/history_test.go、app/cloud_agent_media_writeback_test.go；新增 web/test/canvas-reference-copy.test.ts。

**Interfaces：** 继续使用现有 task binding、documentEpoch、Consumer.OutputNodeID 和 taskId 比较；结果失配沿用 row_deleted、target_deleted、output_binding_changed 等现有结果状态。复制后的 rowId 与归属标记由目标表重新协调，不继承任务身份。

- [ ] 加入 TestReferenceLateResultDoesNotRebind、TestReferenceCopyRemapsOwnedOutputs、TestReferenceUndoDuringTask、TestReferenceConcurrentSourceDelete；断言资源数保留、原行／节点未恢复、新输出未覆盖、任务及计费次数均未增加。前端测试只复制表与复制整个容器两种引用重映射。
- [ ] backend/ 运行 go test ./internal/canvas/tasks ./internal/canvas/commands ./internal/app -run 'TestReferenceLate|TestReferenceCopy|TestReferenceUndo|TestReferenceConcurrent' -count=1；web/ 运行 bun test test/canvas-reference-copy.test.ts，确认失败。
- [ ] 分镜 Consumer 复用现有 materializer 的行、输出、taskId 与世代检查；结果先落资源，再决定是否回填。修改 next-generation 字段不能改动任务 InputJSON 和已冻结引用版本。
- [ ] 复制清除旧托管标记及活动任务，再根据复制范围重映射字段、产物和边；撤销／重做只应用对应历史差异，不重新生成或重新消费。
- [ ] 重跑专项通过，任务结果重复投递幂等。提交：fix(canvas): 引用生命周期 - 防止复制串联与迟到结果恢复旧关系。

## Task 7：旧画布一次规范化

**依赖：** Task 2、3、5、6。**Files：** 新增 references/migrate.go、migrate_test.go；commands/reference_migration.go、reference_migration_test.go；前端 canvas-reference-migration-panel.tsx；更新 contracts.go、读取投影及文档入口。

**Interfaces：** NormalizeLegacy(doc map[string]any, choices map[string]string, registry *capability.Registry, nextID func()string) (MigrationResult,error)，MigrationResult 嵌入 Result，并增加 Conflicts []MigrationConflict；MigrationConflict{ScopeKey string,FieldNodeIDs []string,EdgeNodeIDs []string}。新增仅画布客户端可用的 migrate_references 命令，id=画布 ID、patch.scopeChoices 为 scopeKey 到 fields／connections 的映射，沿用版本和权限校验，不向 Agent 暴露。

- [ ] 写 TestReferenceMigrationEdgeOnly、TestReferenceMigrationConflictRequiresChoice、TestReferenceMigrationIsIdempotent、TestReferenceMigrationUndoRestoresPreimage；验证空字段与旧边矛盾也列冲突，旧输出节点和不相关边保留。
- [ ] backend/ 运行 go test ./internal/canvas/references ./internal/canvas/commands -run 'TestReferenceMigration' -count=1，确认失败。
- [ ] 预览识别历史行端口、全局上下文、分镜输出、批量表及普通媒体输入。字段缺失且边可唯一解释时补字段；显式字段与边不一致时要求对该作用域选择来源，不静默合并。模式不明的首尾帧不靠普通边猜测。
- [ ] 同一命令保存旧快照、规范化字段和边、写 referenceFieldsVersion=1，并生成迁移回执；重复执行返回同一结果。旧文档可读取、预览和导出，编辑／生成先完成规范化；恢复旧文档后按其标记重新进入该入口，不启用长期双读。
- [ ] 重跑专项通过；测试选择 fields 和 connections 两种结果、无权访问与旧版本提交失败。提交：feat(canvas): 引用迁移 - 一次规范化历史画布关系。

## Task 8：关闭 Agent 连接操作与发布新工具合同

**依赖：** Task 1–7 全部完成。**Files：** 修改 cloud_agent_tools.go、cloud_agent_tool_contracts.go、cloud_agent_approval_preview.go、agent-tool-descriptions.md、cloud_agent_capability_discovery.go、agent_tool_snapshots.go；相邻工具 schema／预算／artifact 测试及 agent/test/tool-snapshot-registration.test.ts。

**Interfaces：** 新增 validateCloudAgentReferenceOperations(ops []agentCanvasOp) error；在审批预览和真实执行入口均调用。connect_nodes、update_connection、delete_connection 统一返回 connection_ops_disabled；新增语义动作继续使用原权限等级和审批流程。

- [ ] 写 TestAgentReferenceOnlyContract、TestAgentRejectsLegacyConnectionOperations、TestAgentReferenceContractSnapshotDrift：模型可见 schema 中无边操作和端口字段；直接构造旧请求也失败；同批包含合法改名和非法连线时整批回滚。
- [ ] backend/ 运行 go test ./internal/app -run 'TestAgentReference|TestAgentRejectsLegacy' -count=1，确认失败。
- [ ] 从 canvas_apply_ops schema 删除边操作及只供其使用的 fromNodeId/toNodeId/端口字段；更新工具说明、能力发现和审批展示为引用／归属语义。旧检查点恢复时按现有工具快照漂移机制停止或重建上下文，不能在新 schema 下暗中执行旧边请求。
- [ ] 保留 commands 的内部连接操作及手动画布能力；Agent 的通用 metadata patch 不能写 connections、归属标记或其他绕过字段。同步快照编译与注册样例，不额外建立工具注册表。
- [ ] 重跑专项及 agent/ 下 npm test；确认新工具注册与实际分派一致。提交：refactor(agent): 引用合同 - 停止模型直接操作画布连接。

## Task 9：集成验收、文档与版本收尾

**依赖：** Task 1–8。**Files：** 新增 backend/internal/app/storyboard_reference_flow_test.go、web/test/canvas-reference-flow.test.ts；更新文件职责表中的专题文档、VERSION、CHANGELOG.md。

**Interfaces：** 集成测试使用现有真实 service／repository、命令回执及模拟 provider；模拟 provider 只记录真实提交参数，不发付费请求。验收记录保存为 docs/content/docs/progress/pending-test.mdx 的本功能条目，区分静态、自动化、页面和真实模型验收状态。

- [ ] 编写一条纵向业务测试：建表和真实素材节点 → 只写 assetBindings → 服务端建线 → model_list 同源筛选 → 生成绑定到行 → 任务成功回填 → 清引用并再次准备时不带旧素材。覆盖无浏览器条件和任务提交请求重试，断言相同 operationId 只产生一个任务。
- [ ] backend/ 运行 go test ./internal/app -run TestStoryboardReferenceFlow -count=1；web/ 运行 bun test test/canvas-reference-flow.test.ts；修复真实缺口后通过。Task 9 不另造一套脱离应用的静态页面。
- [ ] 执行一次最终检查：backend/ 下 go test ./internal/canvas/... ./internal/app；web/ 下 bun run build；agent/ 下 npm test。能力／合同改动已影响全链路，按项目 CI 要求在有 PostgreSQL／Redis 的环境补对应集成门禁，缺失环境须明确记为未验证。
- [ ] 文档写明字段权威、手动断线、共享引用、输出归属、旧画布迁移、资源保留与已提交任务冻结。调用 node scripts/changelog.mjs 预览，再按仓库规则改写日志并更新内部版本；最后 node scripts/changelog.mjs --check。只暂存本任务的明确文件。
- [ ] 如用户另行要求页面验收，在隔离数据目录 .local/project-workbench-debug 和确认的空闲端口启动真实应用；检查素材选择、拖线、断线、撤销／重做、刷新与双客户端一致性。真实模型收费验收需要单独的明确授权，本计划默认使用模拟 provider。没有页面验收时保留待测试状态。
- [ ] 提交：test(canvas): 引用流程 - 补齐分镜到产物的端到端验收。最终报告列出通过项、未验证项和剩余限制；本任务不包含生产部署。

## 里程碑与完成定义

| 里程碑 | 任务 | 可检查产物 |
| --- | --- | --- |
| M1 服务端引用闭环 | 1–3 | 单次字段写入即可持久化一致的引用与边，重复请求、回滚和撤销正确。 |
| M2 创作与任务闭环 | 4–6 | 分镜选模型／生成只用字段及行身份，用户交互和迟到结果处理一致。 |
| M3 切换与交付 | 7–9 | 旧画布可规范化，Agent 无边操作，关键节点有字段替代且集成验收有证据。 |

- [ ] Agent 完成完整分镜制作流程时，调用轨迹中的直接边操作为 0。
- [ ] 分镜、普通媒体、批量表及其他受支持输入节点都可用字段表达实际输入；未支持的插件明确返回能力不支持，不能退回隐藏连线。
- [ ] 对规范化后的文档，服务端引用解析不依赖遗留边；所有托管连接均可由权威字段与系统输出归属重建。
- [ ] 引用与边在无浏览器、重连、撤销、复制、并发和迟到任务下保持一致。
- [ ] 未覆盖页面／真实模型验收的项目明确列入待测试，不写成已验证。

## 计划自查

需求覆盖：评估中的五个缺口对应 Task 2、3、4、5、8；表级引用与输出语义对应 Task 3–4；权限、费用和冻结输入对应 Task 4、6；复制、撤销、并发对应 Task 3、6；历史规范化对应 Task 7。Review Focus 的五类风险均有任务及具名用例。

边界检查：references 只依赖 capability／contract／connection，app 是组合入口；普通节点与分镜字段各自只有一个权威源；任务绑定复用 ConsumerJSON；新迁移命令不暴露给 Agent。计划未包含运行时产品代码、数据库／容器修改、收费生成或生产部署。
