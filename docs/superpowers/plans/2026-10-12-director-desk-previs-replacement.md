# DirectorDesk 预演台替换实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox ( - [ ] ) syntax for tracking.

**Goal:** 将 DirectorDesk 的三维导演工作流嵌入影策预演台，保留影策 Agent 权限、审批、任务链路，并实现图片到可编辑 3D 预演场景的一键流程。

**Architecture:** 影策 PrevisScene、画布项目和后端任务系统仍是唯一持久化事实源。DirectorDesk 的模型、采样器、路径、镜头和编辑 UI 放入受控前端集成层，通过双向适配器和 runtime bridge 工作。Agent 保留 previs_* 兼容工具，并新增高层导演工具，由后端编译为受校验的 DirectorDesk 原子操作。

**Tech Stack:** React 19、TypeScript、Vite、Three.js 0.185、Go 1.25、Gin、GORM、现有 Agent/Pi worker、预演 renderer、Bun 测试。

**Spec:** docs/superpowers/specs/2026-10-12-director-desk-previs-replacement-design.md

## Global Constraints

- PrevisScene、画布 previsScenes、Agent 审批/CAS、任务队列和资源写回仍是唯一产品事实源。
- DirectorDesk 的 Electron、MCP host、本地工程文件、内置 AI 和文件系统能力不进入 Web 产品边界。
- 现有 previs_scene_read、previs_scene_create、previs_apply_patch、previs_preview 合同保持兼容。
- 新导演写工具必须经过用户权限、审批、revision、requestId、数量限制和资源校验。
- 图片入口必须先读取真实图片；不支持图片输入的模型必须明确失败，不得生成空场景。
- 不覆盖或清理当前工作树的其他未跟踪改动；每次提交只包含本任务文件。
- 完成开发提交前，按仓库约定更新根目录 VERSION，使用 bump 前 HEAD 的 7 位短 SHA。

## Review Focus

- PrevisScene 与 DirectorDesk Project/SceneDocument 往返后 ID、镜头切换、路径时间和扩展字段稳定；Task 2 覆盖。
- Agent 写入与前端运行时并发时不丢失用户修改；Task 4 覆盖 revision/CAS 与刷新。
- 高层导演工具不能绕过审批、资源归属和工具顺序；Task 5 覆盖。
- 图片输入失败不能创建半成品场景；Task 6 覆盖。
- 渲染取消、失败、重试和后台写回必须幂等；Task 4 覆盖，Task 7 做端到端回归。

---

### Task 1: 集成 DirectorDesk 浏览器核心

**Files:**
- Create: web/src/integrations/director-desk/，保留 browser-safe 的 model、engine、scenes、animation、cinematography、visuals、media、ui 子模块
- Create: web/src/integrations/director-desk/index.ts
- Create: web/src/integrations/director-desk/LICENSES.md
- Modify: web/package.json 与 Bun lockfile
- Test: web/test/director-desk-runtime-import.test.ts

**Interfaces:**
- Produces createDirectorRuntime、disposeDirectorRuntime、版本和许可证元数据。
- 不暴露 Electron、MCP、文件系统和本地工程存储。

- [ ] 写失败测试：核心入口可被 Vite/Bun 解析，runtime 创建/销毁 Three renderer。
- [ ] 运行 cd web && bun test test/director-desk-runtime-import.test.ts，确认失败。
- [ ] 迁移 DirectorDesk browser-safe 模块，移除 Electron/file/MCP 依赖，补齐 cannon-es 与 mediabunny 等必要依赖。
- [ ] 实现 runtime 创建、资源加载、host callbacks、错误边界和 dispose。
- [ ] 运行 cd web && bun run typecheck && bun test test/director-desk-runtime-import.test.ts。
- [ ] 提交 feat(previs): 集成 DirectorDesk 浏览器运行时。

### Task 2: 实现 PrevisScene 双向适配器

**Files:**
- Create: web/src/integrations/director-desk/previs-adapter.ts
- Create: web/src/integrations/director-desk/previs-runtime-types.ts
- Modify: web/src/types/previs.ts，仅补充明确扩展字段
- Test: web/test/director-desk-previs-adapter.test.ts
- Test: web/test/director-desk-previs-roundtrip.test.ts

**Interfaces:**
- Consumes PrevisScene、DirectorDesk Project/SceneDocument。
- Produces previsSceneToDirectorDocument、directorDocumentToPrevisScene、directorPreviewInput 和稳定 ID 映射。

- [ ] 写 actor、prop、camera、motionPath、cuts、characterBinding、多 shot 和未知扩展字段 fixture 测试。
- [ ] 运行两个适配器测试，确认入口不存在导致失败。
- [ ] 实现实体、摄影机、路径、镜头、cuts 和角色绑定映射；保持 ID 稳定。
- [ ] 保存资源节点 ID、任务产物、审批元数据和 snapshot hash 到影策扩展区。
- [ ] 验证 round-trip 不丢数据，再提交 feat(previs): 添加 DirectorDesk 与 PrevisScene 适配器。

### Task 3: 接入统一预演台工作区

**Files:**
- Create: web/src/components/canvas/previs/director-desk-previs-workbench.tsx
- Create: web/src/components/canvas/previs/director-desk-previs-workbench.css
- Create: web/src/components/canvas/previs/use-director-desk-previs-runtime.ts
- Modify: web/src/pages/canvas/project.tsx
- Modify: web/src/pages/canvas/use-canvas-previs.ts
- Modify: web/src/components/canvas/previs/canvas-previs-node-panel.tsx
- Test: web/test/director-desk-previs-workbench.test.tsx
- Test: web/test/director-desk-previs-lifecycle.test.ts

**Interfaces:**
- Consumes Task 1 runtime、Task 2 adapter、现有 useCanvasPrevis 和 usePrevisSaveCoordinator。
- Produces 与旧 CanvasPrevisWorkbench 相同的 open/close/onChange/onApply 生命周期。

- [ ] 写初始化一次、Agent 更新、关闭 flush、取消关闭和 dispose 测试。
- [ ] 构建 DirectorDesk 视口/时间轴/检查器外壳，保留影策保存状态、任务状态和 Agent floating panel。
- [ ] 将 DirectorDesk commit 转成 PrevisScene，进入现有 save coordinator；禁止直接写 localStorage。
- [ ] 接入 Agent canvas sync，按 scene ID/revision 刷新并恢复选择状态。
- [ ] 加入单一 feature flag；默认只显示一个预演台入口。
- [ ] 运行 focused tests 与 cd web && bun run typecheck，提交 feat(previs): 使用 DirectorDesk 作为预演台编辑器。

### Task 4: 接入资源、渲染和后台写回

**Files:**
- Create: web/src/integrations/director-desk/asset-loader.ts
- Create: web/src/integrations/director-desk/render-bridge.ts
- Modify: web/src/previs-render.tsx
- Modify: web/src/pages/canvas/use-canvas-previs.ts
- Modify: backend/internal/app/task_previs_render.go
- Modify: backend/internal/app/desktop_canvas_tasks.go
- Test: web/test/director-desk-asset-loader.test.ts
- Test: web/test/director-desk-render-bridge.test.ts
- Test: backend/internal/app/task_previs_render_test.go

**Interfaces:**
- Consumes directorPreviewInput、授权资源 URL/Blob 和现有 renderer HTTP 合同。
- Produces DirectorDesk 采样帧和导出输入；影策任务继续产生资源记录、构图帧、视频节点和写回诊断。

- [ ] 写资源归属、缺失模型、取消、重复提交、renderer 失败重试和幂等写回测试。
- [ ] 实现受控 asset loader，只接受影策授权引用，不暴露 storage key、机器路径和 token。
- [ ] 实现 render bridge；后台继续走 previs_preview、task queue 和 renderer。
- [ ] 保持任务状态机、配额、资源记录和 canvas metadata 不变。
- [ ] 运行 web focused tests 与 cd backend && go test ./internal/app -run Previs -count=1，提交 feat(previs): 接入 DirectorDesk 资源与渲染桥。

### Task 5: 增加导演工作流 Agent 工具

**Files:**
- Create: backend/internal/app/cloud_agent_director.go
- Create: backend/internal/app/cloud_agent_director_test.go
- Create: backend/internal/app/cloud_agent_director_contract_test.go
- Modify: backend/internal/app/cloud_agent_tools.go
- Modify: backend/internal/app/cloud_agent_runtime.go
- Modify: backend/internal/app/cloud_agent_tool_categories.go
- Modify: backend/internal/app/cloud_agent_tool_text.go
- Modify: backend/internal/app/agent-tool-descriptions.md
- Modify: agent/harness/TOOL_SCHEMA.json，按现有同步流程更新
- Test: web/test/agent-director-tool-presentation.test.ts

**Interfaces:**
- Produces director_block_scene、director_design_shot、director_draw_action、director_check_continuity、director_preview_shot、director_handoff_shot。
- 每个写工具返回 approval preview、revision、requestId 和机器可读错误。

- [ ] 写工具分类、读写权限、图片能力、stale revision、越界数量、跨用户资源和未审批写入测试。
- [ ] 实现导演摘要投影，不返回 DirectorDesk 原始工程 JSON。
- [ ] 将场面调度和镜头设计编译成受控 previs_apply_patch/适配器操作。
- [ ] 实现连续性检查：路径表面、空间/遮挡、前后镜头接拍；建议修复必须显式审批。
- [ ] preview 复用现有任务状态机；handoff 复用提示词编译器。
- [ ] 同步 harness schema、工具描述、工具卡片和失败分类，不暴露第二套 DirectorDesk MCP 工具。
- [ ] 运行 cd backend && go test ./internal/app -run CloudAgentDirector -count=1 与 web Agent focused tests，提交 feat(agent): 增加导演工作流 Agent 工具。

### Task 6: 接回图片到 3D 预演场景入口

**Files:**
- Create: web/src/lib/canvas/image-to-director-agent.ts
- Modify: web/src/pages/canvas/project.tsx
- Modify: web/src/pages/canvas/use-canvas-previs.ts
- Modify: web/src/services/api/agent.ts
- Modify: backend/internal/app/cloud_agent_vision.go
- Test: web/test/image-to-director-agent.test.ts
- Test: web/test/image-to-previs-flow.test.ts
- Test: backend/internal/app/cloud_agent_director_image_test.go

**Interfaces:**
- Consumes 画布图片引用、图片检查能力和 Task 5 导演工具。
- Produces “根据此图生成 3D 预演台”入口；失败时不创建半成品场景。

- [ ] 写提示词和顺序测试：真实图片检查、block_scene、design_shot、check_continuity。
- [ ] 复用 capability resolver 选择支持图片输入的模型，并使用受控 mention token。
- [ ] 在图片节点上下文菜单/操作栏接入 Agent prefill，沿用现有 Agent UI。
- [ ] 后端验证图片归属、场景绑定、工具顺序和审批；失败/取消保持画布不变。
- [ ] 运行 web 与 backend focused tests，提交 feat(previs): 接入图片到导演场景工作流。

### Task 7: 回归验证、文档和发布收尾

**Files:**
- Modify: web/src/pages/canvas/project.tsx
- Modify: web/src/components/canvas/previs/canvas-previs-workbench.tsx
- Modify: web/src/components/canvas/previs/canvas-previs-node-panel.tsx
- Modify: docs/content/docs/overview/features.mdx
- Modify: docs/content/docs/progress/pending-test.mdx
- Modify: README.md
- Modify: VERSION
- Test: web/test/previs-director-desk-e2e-contract.test.ts
- Test: backend/internal/handler/canvas_tasks_test.go
- Test: renderer/test/render.test.mjs

**Interfaces:**
- Consumes Tasks 1–6，输出默认 DirectorDesk 预演台、可撤销回退开关、文档和验证记录。

- [ ] 写端到端合同测试：旧场景打开、编辑、Agent 补丁、重开、图片生成、白膜任务和资源写回。
- [ ] 运行 cd web && bun run typecheck；相关 bun test；cd backend && go test ./internal/app ./internal/handler；renderer 测试。
- [ ] 验证默认只显示一个入口；回退开关不产生第二套场景格式。
- [ ] 更新功能文档、已知限制、DirectorDesk 许可证和图片入口说明。
- [ ] 按 AGENTS.md 规则更新 VERSION。
- [ ] 运行 git diff --check、git status --short，确认不包含其他未跟踪工作树。
- [ ] 提交 feat(previs): 完成 DirectorDesk 预演台替换。

