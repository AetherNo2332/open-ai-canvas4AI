# 导演台（预演台）Agent 修复设计 — A 路线：服务端后台渲染 + 上游工作站绑定

> 状态：待评审（设计基线，非执行计划；评审通过后另出分阶段执行计划）。
> 基线分支：`feat/agent-skill-defaults` @ `0bfa0a38`（已含 origin/main 4c589c94）。
> 对照基线：upstream `ddcat-ai/open-ai-canvas` main @ `37b44444`（v1.6.4）。
> 分叉事实：fork canary / upstream main 双向分叉 546 / 161 提交，merge-base 2026-09-26。

## 1. 一句话结论

保留 fork 自研的「服务端后台白模渲染 + 服务端回写」（A 路线），从上游移植「创建场景时绑定可打开工作台的 video 工作站节点」（dd52588a）与「图片→预演台 Agent 入口 + 角色绑定语义修复」（c0a9250c），把服务端渲染回写对齐浏览器链的节点语义，并修复成功回执 / 工具描述 / 系统策略 / 技能文案与收尾事实核对，使「Agent 在画布创建可打开的导演台节点」成立且结果可核验、不可虚报。

## 2. 问题定义（症状 → 根因，含代码锚点）

三个用户可见症状：

1. Agent 声称创建了导演台/预演台节点，画布上没有节点（`previs_scene_create/apply_patch` 只写 `previsScenes`，见 `backend/internal/app/cloud_agent_previs_mutation.go:766-782`）；
2. Agent 最终答复虚报成功（记分在服务端、回执口径误导，见 §4 G3/R3/R4）;
3. 后台预演渲染后出现「PREVIS / 3D · 可编辑」卡片，但按钮 / 双击 / 进入预演台均无反应（回写节点缺 `workflowKind`，见 `backend/internal/app/task_previs_render.go:602-618`；守卫静默返回，见 `web/src/pages/canvas/use-canvas-previs.ts:111-113`）。

根因链（静态走查核实，锚点均为 fork HEAD）：

- **R1 节点配方无人能写**：可打开的预演台节点 = `type=video` + `metadata.workflowKind="shot"` + `metadata.previsSceneId`（+`previsShotId`），三者缺一不可；入口看 `previsSceneId`（`web/src/pages/canvas/project.tsx:2393-2405`、`web/src/components/canvas/canvas-node.tsx:342-346`），守卫看 `workflowKind==="shot"`（`use-canvas-previs.ts:113`）。Agent 侧：场景写入不建节点（mutation.go:766-782）；`add_node` 无 metadata 通道（`cloud_agent_tools.go:1479-1500`，metadata 由 `capability.Metadata(content)` 生成，`cloud_agent_approval_preview.go:185`）；能力注册表无 previs/director/shot 类型（`capabilities/capability/registry.go` Types() 排除变体，18 个可创建类型）；`update_node` 受 PatchFields 合同限制（`capability/descriptor.go:119-131`）。唯一完整创建路径是浏览器 `createPrevisShot`（`use-canvas-previs.ts:82-109`）。
- **R2 服务端渲染回写产出死节点**：`commitPrevisOutput` 新建的 video/image 节点带 `previsSceneId` 但不写 `workflowKind`（`task_previs_render.go:539-645`）→ 卡片走 `canvas-previs-node-panel.tsx:26-66` 渲染、点击全静默。浏览器产物链的对照语义：构图帧/白模视频是 `workflowKind="reference_set"/"reference_video"` 的产物节点，工作台节点身份不变（`use-canvas-previs.ts:143-205`）。
- **R3 成功回执口径错误**：previs 写入回执 `summary` 直接复用**审批前**人面审批卡文案「Agent 准备修改预演场景《X》。批准后才会写入当前画布。」（mutation.go:1767 `plan.Preview.Description` ← 849 行），且无 `nodeId`；canvas 写回执才有 `applied/outcome`（`cloud_agent_runtime.go:1332-1368`），previs 三工具不在 `cloudAgentCanvasWriteTool` 集合（`cloud_agent_tools.go:864-880`），整包原样穿透。
- **R4 模型上下文无「场景 ≠ 节点」区分**：工具描述各一行（`backend/internal/app/agent-tool-descriptions.md:459-473`）；系统策略全文无预演/导演/previs 章节（`backend/internal/prompts/agent-system-policy.md`）；技能反写「后台预演**无需打开导演台或画布网页**」（`backend/builtin/影策短剧制作工作流/SKILL.md:18`）并规定等待真实回写（L120-124）——两句话互相矛盾地混在模型上下文里。
- **R5 核对防线缺失**：收尾闸门只核 plan 对账/子代理/插话/截断，不核声明真伪（`cloud_agent_completion.go:65-90`）；事实复核函数 `verifyCanvasOperations` 仅测试引用（`web/src/lib/canvas/canvas-operation-contract.ts:78`，`web/src` 零调用，仅 `web/test` 使用）；UI 在 `canvas_updated` 事件到达即渲染「画布操作已完成」，不等浏览器实际应用（`canvas-cloud-agent-panel.tsx:2115-2120`；应用场景为 `web/src/services/agent-canvas-sync.ts:27-64`，previs 变更走 `requiresRefresh` 全量刷新，`cloud_agent_canvas_events.go:114-118, 236-254`）。

## 3. 事实基线与采信边界（用户拍板）

### 3.1 上游演进（逐 commit 核实）

| 上游 commit | 日期 | 该段逻辑状态 |
|---|---|---|
| 产品期 #101/#305/#324/#333、5bda548f | 08-03~09-26 | 3D 导演台纯浏览器产品；Agent 未接入 |
| **b4111a87** 新增导演工具 | 09-27 | 仅 `director_scene_read` + `director_preview`，preview = **`execution:"browser_director_viewport"`（浏览器视口渲染）**；不能建场景/节点 |
| **11291658** 预演台重构 | 10-06 | director→previs 全量改名 + Agent 写能力（`previs_scene_create` 仅场景 / `previs_apply_patch` 全套语义补丁）；preview 仍 `browser_previs_viewport`。**fork 拉取的快照（fork 63e0fc06 同日）** |
| **dd52588a** 预演工作站 | 10-08 | **根因修复**：create/patch 绑定/修复唯一 video 工作站（完整 §4 G1 语义）；旧场景补丁自愈；冲突拒绝；回执加 `nodeId/shotId`；schema 加 `nodeId/nodeTitle`；+200 行测试 |
| 29537465 质量重构 | 10-08 | 模块改名、前端 CanvasViewport 重构等；previs Agent 逻辑本体未动（previs hunk 仅 import 改名） |
| **c0a9250c** 图片生成预演台 | 10-09 | 图片工具栏「生成预演台」+ 看图模型复现指令 + 对话节点卡片；修 `characterName` 单独出现误触发角色卡绑定（mutation.go 两处）；e2e+单测 111 行 |

### 3.2 fork 特有功能（`git log --diff-filter=A` 核实上游全历史从未存在）

- `40774e0e`（09-22）收尾闸门 `cloud_agent_completion.go`；
- `e5b7deff`（09-28）、`914f9c95` 等 Pi worker 与 `agent/harness/TOOL_SCHEMA.json` 制品链（上游 Pi 运行时在 `backend/agent-runtime/pi`，形态不同）；
- **`af149b9d`（10-08）服务端白模渲染链**：`task_previs_render.go`（757 行 + 572 测试）、`cloud_agent_previs_render.go`（234 + 185 测试）+ 恢复链路；SKILL.md「无需打开画布网页」承诺即此 commit 写入。

### 3.3 后台等价性验证（路线裁决依据）

- **执行层统一管理**：全部任务类型共用后端 `taskWorkerCoordinator`（全局 slot 租约 + Redis 协调、优先级、计费、stage），任务运行与浏览器存活无关（`backend/internal/app/task_worker.go:25-176`）；失败已产出媒体保留可恢复（`task_output.go:113` `canRecoverMedia`）。
- **Agent 生成线**：任务终态由**服务端主动**回写画布（`cloud_agent_media.go:912` `commitAddedNode`）——浏览器事后回来看得到，零操作。
- **用户直接生成线**：执行在服务端；结果进节点靠浏览器订阅（`use-canvas-generation.ts:253-270` `bindGenerationTask`），浏览器缺席时页面加载由 `recoverInterruptedGenerationTasks` **自动恢复补录**（`use-canvas-generation.ts:471-490`，数据源为服务端 `queryGenerationTask`），兜底为任务中心。
- **上游 preview 的后台缺口**：`previs:preview-requested` 事件唯一监听器注册在工作台组件内（upstream `canvas-previs-workbench.tsx:1247-1248`）；用户离开/未开工作台 → 渲染永不发生，而回执仍报「已请求」。**上游后台语义下不成立**。

**结论（用户拍板）**：fork 的「不开画布网页、后台生成白模并回写」与 Agent 生成任务同构，成立且是产品差异化（影策短剧工作流依赖）→ **A 路线：保留服务端渲染链，移植上游工作站绑定与图片入口，修回写语义与回执/策略/技能口径。**

## 4. 目标

### G1 工作站绑定（移植 dd52588a，适配 fork 文件；`git show dd52588a` 可直接对照）

- `previs_scene_create`：
  - schema 新增可选参数 `nodeId`（maxLength 80）、`nodeTitle`（160）；省略时 nodeId 派生 `previs-<sceneId>`（超长按 sceneId 哈希截断，上游算法）；
  - 创建时 `cloudAgentPrevisEnsureWorkstationNode(doc, scene, nodeId, nodeTitle)` 放置/修复**唯一** video 工作站：520×340，位置取现有最右节点右侧（上游算法），metadata 完整写入 `workflowKind:"shot"`、`workflowTitle`、`shotIndex`、`generationMode:"video"`、`videoEditOperation:"text_to_video"`、`composerContent`、`previsSceneId`、`previsShotId`、`status:"idle"`；
  - 冲突规则（BadAuthRequest，沿用上游文案）：场景已绑定**非 video** 节点「预演场景已绑定非视频节点，无法修复工作站」；绑定**多个**工作站「预演场景绑定了多个工作站节点，无法自动选择」；指定 nodeId 已被非视频占用/已绑其他场景/场景已有其他工作站 → 对应拒绝；
  - 审批卡 details 追加「绑定预演视频工作站：<nodeId>」。
- `previs_apply_patch`：同一函数；无工作站的旧场景**补丁时自愈**，details 追加「创建并绑定预演视频工作站：<nodeId>」。
- 回执（mutation.go:1767）补 `nodeId`、`shotId` 字段；plan 结构补 `NodeID`/`ShotID`。
- 语义承诺：模型从此知道「创建场景 = 画布上出现一个可打开的工作站节点」（与 G3 文案配套）。

### G2 服务端渲染回写对齐浏览器语义（修 af149b9d 偏差）

以浏览器 `applyPrevisOutput` 为语义基准（`use-canvas-previs.ts:143-205`）：

- `commitPrevisOutput`（`task_previs_render.go:539-645`）回写时**必须先定位场景工作站节点**（G1 后保证存在）：
  - 若场景无工作站节点（存量脏数据）→ 任务以显式错误失败（fail-loud，不静默创建裸节点、不静默降级），错误信息指明 sceneId 与修复途径；
  - 白模视频产物节点：metadata 补 `workflowKind:"reference_video"`、`assetTags:["预演台白膜", "镜头:<title>"]`，保留现有 `storageKey/resourceId/taskId/previsSceneId/previsShotId/previsSourceHash/previsIndependent/previsRepairTaskId/duration` 等；
  - 构图帧产物节点：`workflowKind:"reference_set"`、`assetTags:["预演台构图", "镜头:<title>"]`；
  - 现有 linked/independent 模式与场景 shot `previewNodeId/clayVideoNodeId` 回写**保留不变**（这部分本就与浏览器一致）；
  - 不再产生「只有 previsSceneId、无 workflowKind」的裸节点形态。
- **兼容前端既有渲染**：产物节点加 `workflowKind` 后不得被 `CanvasPrevisNodePanel` 门（`project.tsx:2393` 仅看 `previsSceneId`）误渲染为工作台卡——执行计划需核对渲染分支优先级（产物节点应走视频/图片正常渲染）并补回归测试。
- **存量数据修复**：懒迁移（我的决定 D3：不做启动全表扫描）——在服务端预演读/写主路径（`cloudAgentPrevisCanvas` 读与 apply 路径）对文档内匹配特征的旧节点（`previsRepairTaskId`/`previsSourceHash` 存在且无 `workflowKind` 的 video/image 节点）做一次幂等修复：改挂 `reference_video`/`reference_set` 语义并重指 shot 链路；若该场景无任何工作站节点，则把其中最早的 video 节点**提升为工作站**（补 `workflowKind:"shot"` + 缺省 metadata，保留其媒体内容，允许卡片携带既有白模媒体）。修复可重复执行、只增字段不删数据。
- **浏览器监听与服务器任务并存**（我的决定 D6）：验收阶段实测「服务器渲染期间工作台恰好打开」的行为（`canvas-cloud-agent-events.ts` 派发的 `previs:preview-requested` 与服务器任务双跑）；若发生双渲染/产物覆盖 → 保留服务器回写为准，前台监听改为仅刷新展示，不二次渲染。

### G3 虚报通道修复

- **G3a 回执措辞**：previs 两条写工具回执 `summary` 改用**提交后口径**（不再复用 `plan.Preview.Description`）：
  - create：`预演场景《title》已写入画布文档，并绑定预演视频工作站节点 nodeId（打开该节点进入预演台）`；
  - patch：`预演场景《title》已应用 N 项修改，已绑定预演视频工作站节点 nodeId`（自愈时同时说明）。
  - 人面审批卡保留原措辞（给用户的「批准后才会写入」是正确语义）。
- **G3b 工具描述**（`backend/internal/app/agent-tool-descriptions.md:459-473`）：
  - `previs_scene_create`：说明会创建/绑定**画布上的工作台节点**（返回 nodeId）；
  - `previs_apply_patch`：说明旧场景会在补丁时自动补工作站；
  - `previs_preview`：提交**后台**白模渲染任务；必须用 taskId 核验任务终态与画布写回，节点真实出现后才能称已交付，「已请求」不是「已导出」；
  - 同步再生制品：`WRITE_TOOL_SCHEMA=1 go test ./internal/app -run TestAgentToolSchemaArtifactMatchesRuntime` + `node agent/scripts/sync-harness.mjs`（`agent/harness/TOOL_SCHEMA.json` + `TOOL_DESCRIPTIONS.md`，SOURCE.json 哈希链）。
- **G3c 系统策略**（`backend/internal/prompts/agent-system-policy.md`）：新增「预演台产物」短节：场景 / 工作站节点 / 产物节点 / 后台渲染任务四概念定义 + 三条铁律（交付必须给出 nodeId 且任务终态为成功；「已请求」不得称「已导出」；场景写入 ≠ 节点出现，按回执 nodeId 核验）。同步进 harness（SOURCE.json）。
- **G3d 技能**（`backend/builtin/影策短剧制作工作流/SKILL.md`）：预演台专项执行链（L120-124）与 L18 对齐新行为：场景创建即在画布出现工作站节点；previs_preview 为后台任务，核验以 taskId 终态 + 回执 nodeId 为准；移除/改写「无需打开导演台」措辞为「无需用户在场，产物经工作站节点查看」。
- **G3e 收尾闸门**（`cloud_agent_completion.go`，我的决定 D4：保守拦）：
  - 执行期在 runtime state 记录本轮真实新增节点 ID 集合（来源：写回执 preview items 的 NodeID，服务端可信）；
  - 新校验：最终答复宣称「创建/新增/写回了节点」，但正文中的节点 ID 与本轮真实新增集合**不相交**且可判定时 → 作为 completion blocker 打回并下达修正指示（复用既有 `completion_blocked` 事件与待定机制，不新增事件类型）；
  - 无法判定的一律放行（保守，避免误伤）；补「应放行」与「应拦截」两类回归用例。

### G4 移植上游 c0a9250c（图片→预演台 Agent 入口 + 角色绑定修复）

- 图片工具栏新增「生成预演台」入口（上游 `canvas-image-toolbar-tools.tsx`/`canvas-node-toolbar.tsx`），自动选用支持看图的文本模型并提交复现指令（升级 fork 现有 `web/src/lib/canvas/image-to-previs-agent.ts`，目前仅提示词模板 + 面板 prefill）；
- 对话内节点卡片「此图」引用（上游 `canvas-cloud-agent-panel.tsx` + `cloud-agent-conversations.ts`）；
- 后端角色绑定语义修复（`cloud_agent_previs_mutation.go` 两处：`cloudAgentPrevisApplyCharacterBinding` 与 `cloudAgentPrevisValidateCharacterBindingInCanvas`——`characterName` 单独出现不再触发角色卡绑定要求，图片演员只写名称即可）；
- 移植上游配套测试：`web/test/image-to-previs-agent.test.ts`（111 行）、`agent-chat-node-links.test.tsx`、`canvas-node-toolbar.test.ts` 增量 + 扩展 `cloud_agent_previs_mutation_test.go`。

### G5 测试与验收（用户拍板：本地 compose 真实 agent 回路 + 脚本与 computer use 双轨）

**G5.0 验收环境（一等阶段，非可选项）**

- `docker-compose.local.yml` 本地构建编排（真前端构建，非源码挂载；隔离 compose 项目名；数据目录按 AGENTS.md §7 用 `.local/project-workbench-debug`，不碰生产/托管库）；
- **完整 agent 回路**：backend + web + Pi worker agent 容器（fork pi profile，914f9c95 引入的部署形态）+ 真实模型渠道（账户、渠道 Key 配入 compose env；看图模型必须可用，场景④依赖）；
- 种子数据：测试账号、画布项目、一个已含"死卡片"形态的存量文档（`previsSceneId` 无 `workflowKind`，供场景⑥迁移验证）、一张用于场景④的图片节点、一个用于场景③的白模渲染场景；
- 健康检查只证入口（AGENTS.md §8）：登录态、SSE 连通、一次真实小任务（如随手 text 生成）通过后才开始场景验收。

**G5.1 单元/制品层（先跑，进 CI 断言）**

| 层 | 覆盖 | 方式 |
|---|---|---|
| 后端单测 | 工作站绑定：create 放置 / patch 自愈 / 三类冲突拒绝 / 幂等 / 存量迁移（提升与改挂两路） | 移植 dd52588a 的 200 行测试 + 扩展；`go test ./internal/app -run 'TestCloudAgentPrevis'` |
| 后端单测 | 渲染回写：产物节点 workflowKind/assetTags/shot 链路/无裸节点；无工作站 fail-loud | `task_previs_render_test.go`（现有 572 行基准上改断言） |
| 后端单测 | 收尾闸门：应放行 / 应拦截用例 | `cloud_agent_completion` 测试扩展 |
| 制品漂移 | TOOL_SCHEMA.json / TOOL_DESCRIPTIONS.md / 策略文件哈希链 | `TestAgentToolSchemaArtifactMatchesRuntime`（WRITE_TOOL_SCHEMA 再生后）+ `node --test agent/test/harness-sync.test.ts` |
| 前端单测 | 图片→预演台提示词/入口/节点卡片；卡片打开回归（workflowKind 守卫与产物节点渲染优先级） | `bun test web/test/image-to-previs-agent.test.ts` 等 + 新增 panel 回归 |

**G5.2 脚本轨（自动、可重复、留机器证据）**

- **Agent 回路脚本**：登录 → 建画布 → 通过 `/api/agent` 发送六场景指令 → 监听 SSE 全程（approval / tool_completed / canvas_updated / finish）→ 断言：previs 回执含 `nodeId/shotId` 且措辞为提交后口径、plan 对账通过、finish_run 放行/打回符合预期、画布文档终态（节点集合、workflowKind、previsSceneId 绑定、shot 链路）；场景③断言"浏览器会话断开期间任务仍在推进并最终回写"（以两步请求模拟离开/回来，或 SSE 断连重连）；
- **浏览器自动化脚本**：仓库既有 previs chrome e2e 脚本 precedent（`web/scripts/*previs-p0-chrome-e2e.mjs`，`CHROME_BIN` 可指）扩展覆盖：工作站节点在画布出现、卡片点击打开 3D 预演台、双击打开、产物节点回写后可见；截图作为产物归档。

**G5.3 computer use 轨（真实浏览器、情境级验收）**

- 由 computer use（agent 或人，驱动真实浏览器实例 + 已登录会话）执行六场景，**截图 + SSE 日志 + 画布文档快照**三件套作为验收记录；
- **场景③（离开画布期间后台渲染→回来开卡）与场景⑤（虚报回归：最终答复措辞、nodeId 引用、闸门打回表现）指定为 computer use 轨必测**——这两项的情境判断（"结果真的回来了且能打开"、"答复与事实一致"）脚本断言覆盖不全；
- 诚实规则（AGENTS.md §8）：执行报告逐条写明已跑/未跑/降级；若 computer use 通道不可用，该轨降级为"脚本轨 + 截图产物 + 清单交用户执行"，并如实声明，不得把脚本通过写成 computer use 通过。

**六场景清单**：① Agent 建场景→工作站节点出现且可打开；② 对无工作站的旧场景打补丁→自愈；③ 后台渲染→离开浏览器→回到画布看产物且卡片可打开；④ 图片→预演台全链路（看图模型复现→场景/对象→白模）；⑤ 虚报回归（场景创建后核对最终答复措辞与 nodeId、闸门行为）；⑥ 存量脏数据迁移（种子死卡片文档 → 首次预演读/写后卡片可打开/已降级为产物节点，shot 链路重指）。

## 5. 非目标

- 不整体合入 upstream main（546/161 分叉含 yingce 模块改名、CanvasViewport 重构等与本问题无关的变更）；
- 不给 `add_node` 增加通用 metadata 通道（保持「显式工具」设计，`cloud_agent_tools.go:1479` 注释语义不变）；
- 不改审批链（request_approval / prepared flow）与 Pi worker 运行时架构；
- 不引入「previs」节点类型（仍是 场景数据 + 工作站节点 的二元结构）；
- `openPrevisWorkbench` 静默守卫**保持**（与上游一致，我的决定 D5）——死卡片由 G1/G2 的数据补齐消除，不做 UI 提示改造；
- 不在 fork 建上游 CI 的浏览器 E2E 管线（测试 = 后端单测 + 前端单测 + 手工验收清单）。

## 6. 约束

- 制品同步链为硬门禁：动 `cloud_agent_tools.go`/工具描述/策略文件 → 必跑 schema 漂移测试 + `sync-harness.mjs` + agent 侧漂移测试，三者全绿才算完成；
- 当前会话环境无 Go 工具链（Node/bun 可用）：执行阶段须先解决 Go 1.25（我的决定 D7：优先 docker `golang:1.25` 容器或安装工具链；均不可行时按 AGENTS.md §8 如实声明后端测试未运行并附命令清单）；
- 验收 compose 环境（G5.0）：隔离项目名 + 本地数据目录，不指向生产/托管数据库；浏览器验收尊重浏览器权限规则（AGENTS.md §8：权限拒绝时不得用 CDP 或间接执行绕过，可继续不依赖浏览器的验证并如实声明）；模型渠道 Key 只进 compose env / `.env`（仓库忽略项），不进代码与文档。
- 数据迁移无兼容层（AGENTS.md §1）：一次性、幂等、只增不删；
- 动 SKILL.md / 策略 / 工具描述需同步对应 harness 制品与 `docs/content/docs/progress/pending-test.mdx`；
- 分支与提交（我的决定 D2）：新分支 `feat/previs-agent-parity`（自 0bfa0a38 切出），G1/G2/G3/G4 各自独立可回退提交；dev PR 按仓库约定 bump `VERSION` 并更新根版本；
- 中文注释/文案沿用仓库既有语言习惯；注释只写非直观原因（AGENTS.md §2）。

## 7. 留给执行计划决定

- 分支/提交粒度备用方案与 PR 切分（默认按 D2）；
- 存量懒迁移的挂载点精确位置（`cloudAgentPrevisCanvas` 读路径 vs apply 路径 vs 两者）与并发写冲突处理（复用既有 snapshotHash 机制）；
- 收尾闸门「宣称节点」的判定正则/关键词表与放行阈值（默认保守 D4）；
- G2 渲染分支优先级回归的具体用例（产物节点 + previsSceneId 时不得渲染为工作台卡）；
- 手工验收清单操作步骤、测试数据（含存量脏数据 fixture）与端口/环境准备;
- Go 环境落地方式（D7）；
- 验收报告的 `pending-test.mdx` 条目措辞。

## 8. 决定记录（评审时请重点核对）

### 用户拍板（已采入设计）

- **U1** A 路线：保留服务端后台渲染链，移植工作站绑定 + 图片入口，真 agent 回路后台语义成立（§3.3）；
- **U2** 验收必须包含**真实 agent 回路**测试，跑在**本地 compose**；验收形式 = **脚本轨 + computer use 轨**（G5.0–G5.3，场景③⑤为 computer use 必测）。

### 我的决定

- **D1** 文档放仓库根 `PREVIS_AGENT_FIX_PLAN.md`（对齐 PREVIS_REFACTOR_PLAN.md 惯例）；**先不 commit**，评审通过后随首个执行提交入库（偏离 superpowers 默认的「写完即 commit」）。

- **D1** 文档放仓库根 `PREVIS_AGENT_FIX_PLAN.md`（对齐 PREVIS_REFACTOR_PLAN.md 惯例）；**先不 commit**，评审通过后随首个执行提交入库（偏离 superpowers 默认的「写完即 commit」）。
- **D2** 新分支 `feat/previs-agent-parity`，G 组独立提交。
- **D3** 存量修复走懒迁移（预演读/写主路径幂等修复），不做启动全表扫描。
- **D4** 收尾闸门保守拦：仅「宣称节点 ID 与真实新增集合明确不相交」时拦截，不可判定放行。
- **D5** 保留 `openPrevisWorkbench` 静默守卫（上游同构），靠数据补齐消除死卡片。
- **D6** 浏览器监听与服务器渲染并存：先实测，冲突时服务器回写为准、前台只刷新展示。
- **D7** Go 验证环境优先 docker golang:1.25；不可行则如实声明后端测试未运行。
- **D10**（我的推断，可推翻）交付范围**不含真实实例部署**：按 U2（本地 compose 验收），懒迁移用本地种子数据 + fixtures 验证；若 canary/托管实例的存量数据也在范围内，说明即可，计划会追加部署阶段（实例、备份、回滚预案、真实数据副本验证）。