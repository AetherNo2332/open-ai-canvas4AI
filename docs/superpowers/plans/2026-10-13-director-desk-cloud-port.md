# DirectorDesk 云端完整移植 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 DirectorDesk 0.4.11(@ `f3afacf`)去桌面端能力后**整应用**移植进云端 web 工作台,让原版导演台界面(文件/编辑/场景 菜单、场景·资产·精细模型 面板、双视口、多轨时间轴、属性/动作/走位/外观/物理 检查器)在浏览器可用;并打通两条产品链路:**导出到画布**(白膜视频/构图帧/镜头参数/分镜表写回画布节点与结构化分镜表)与 **AI 操控画布**(影策 Agent 经导演工具改场景→嵌入应用实时可见可撤销;用户在应用内编辑→Agent 可见;图片→可编辑 3D 预演场景),全部写操作保留审批/CAS。

**Architecture:** 上游本身是 Vite Web 应用(`index.html` + `src/main.ts` + `src/ui/*` 原生 DOM),Electron 只是外壳。因此移植 = 以上游源码为独立兄弟应用入库(`director-desk/`),按清单剥离桌面能力并改由显式 host bridge 提供宿主能力,`vite build`(`base=/desk/`)成静态站点由 nginx 同源托管;web 侧以 iframe + 类型化 bridge client 挂进画布预演台,持久化仍走影策 `PrevisScene` + 既有 save coordinator,AI 通道改接影策 Agent。

**Tech Stack:** DirectorDesk 0.4.11(Three.js 0.185、cannon-es、mediabunny、原生 TS+DOM+CSS)、影策 web(Vite/React 19/TS/bun)、Go 后端 `/api/agent` 与 `previs_*` 工具、nginx 同源托管、docker compose 隔离栈 3040。

**Spec:** `docs/superpowers/specs/2026-10-12-director-desk-previs-replacement-design.md`(目标/边界/验收)、`docs/superpowers/retrospectives/2026-10-13-director-desk-previs-retrospective.md`(上一轮为何走偏)

## 现状差距(已核验,勿再假设)

- 现在仓库里只有**引擎编译产物**:`web/src/integrations/director-desk/vendor/director-desk-engine.js` + 手写精简 React 面板(用户截图图1:镜头列表/检查器/时间轴)。
- `web/src/integrations/director-desk/LICENSES.md` 自述:"只取 browser-safe 概念,**Electron、MCP、本地项目存储和内置 AI 适配器都不打包**"——即上一轮**故意排除**了 AI 与完整 UI,这正是图1 与图2 的差距来源。
- 上游规模:386 个 `.ts` / 21,941 行;`src/ui/` 93 模块 / 5,970 行;**`src/` 内无 electron/fs/MCP/child_process 导入**(桌面耦合集中在 `desktop/`、`src/files/desktop-output.ts`、`src/storage/local-library.ts`、`src/ui/{project-save,file-locations,update-panel,ai-mcp}.ts`)。
- 本地检出:`.local/director-desk-upstream` = `f3afacf "Release 0.4.11"`;远端 `https://github.com/mangfufu/director-desk` 可达(main 同 SHA)。

## Global Constraints

- 影策 `PrevisScene`、画布 `previsScenes`、Agent 审批/CAS、任务队列、资源写回**仍是唯一产品事实源**;DirectorDesk 只作嵌入编辑运行时。
- **不进入 web 产品边界**:Electron、electron-updater、MCP host(`@modelcontextprotocol/sdk`)、本地工程文件/磁盘路径、内置直连 AI 密钥。
- 单一入口:预演台只显示一个入口,由现有 `directorDeskPrevisMode`(off/canary/on)fail-closed 门禁;不得形成两套长期产品入口。
- MIT 来源与版本必须可追溯:入库目录带 `NOTICE.md`(上游仓库、SHA `f3afacf`、版本 0.4.11、MIT、修改说明)。
- 同源托管:应用从 `/desk/` 提供,与工作台同源(保证 Cookie 与 `postMessage` 可用),不新开端口、不引入第二套鉴权。
- 每次提交只含本任务文件;根 `VERSION` 在收尾提交前按约定 bump(bump 前 HEAD 7 位);未经用户决策不 push、不动生产。

## Review Focus

1. **iframe 隔离完整性**:画布快捷键(`data-canvas-no-zoom`、Delete、Cmd/Ctrl 组合)、滚轮缩放、焦点、全屏在嵌入的导演台里不得与工作台互相串扰;关闭后 WebGL/键盘监听必须释放。
2. **桌面能力移除后的降级**:原版 UI 中"打开/保存工程文件、导出到本地、检查更新"等入口必须消失或明确不可用,**不得**调用不存在的宿主、不得静默失败或抛未捕获异常。
3. **场景契约**:多镜头/路径/角色绑定/未知扩展字段的 `PrevisScene` 经 bridge 往返**零丢字段**、ID 稳定;冲突时不静默覆盖。
4. **AI 通道**:MCP 宿主移除后 AI 面板仍可用;断线、取消、审批、工具白名单与权限校验不得被绕过(尤其写操作仍须审批)。
5. **资源/模型缺失降级**:模型/贴图/动作资源缺失或加载失败时编辑器可用(占位 + 明确提示),不白屏、不无限重试。

---

### Task 1: 上游源码入库与来源固化

**Files:**
- Create: `director-desk/`(从 `.local/director-desk-upstream` 复制 `src/ index.html public/ LICENSE skills/ docs/` 与上游 `package.json`/`tsconfig.json`/`vite.config.mjs`;不复制 `desktop/`、`scripts/`、`tests/`、`.git/`、`release/`)
- Create: `director-desk/NOTICE.md`
- Create: `director-desk/README.md`(影策侧说明:来源、剥离清单、构建方式)
- Test: `web/test/director-desk-port-provenance.test.ts`

**Interfaces:**
- Produces: `director-desk/` 作为可独立构建的上游应用;`director-desk/NOTICE.md` 记录 `f3afacf`/0.4.11/MIT。

- [ ] **Step 1: 写失败测试** — `director-desk-port-provenance.test.ts` 断言:`director-desk/NOTICE.md` 含 `f3afacf`、`0.4.11`、`MIT`;`director-desk/package.json` 版本为 `0.4.11`;`director-desk/src/main.ts` 与 `director-desk/src/ui/timeline.ts` 存在;`director-desk/desktop` 不存在;`director-desk/node_modules` 不存在。
- [ ] **Step 2: 跑测试确认失败** — `cd web && bun test test/director-desk-port-provenance.test.ts` → 目录不存在即 FAIL。
- [ ] **Step 3: 入库** — `cp -r` 上游源码到 `director-desk/`(按 Files 白名单),写 `NOTICE.md` + `README.md`。
- [ ] **Step 4: 跑测试确认通过**,并 `cd director-desk && git status --short | wc -l` 记录文件数。
- [ ] **Step 5: 提交** `feat(previs): 导演台 - 入库 DirectorDesk 0.4.11 上游源码(来源与许可固化)`

### Task 2: 剥离桌面能力 + host 引导

**Files:**
- Delete: `director-desk/src/files/desktop-output.ts`、`director-desk/src/storage/local-library.ts`、`director-desk/src/ui/project-save.ts`、`director-desk/src/ui/file-locations.ts`、`director-desk/src/ui/update-panel.ts`、`director-desk/src/ui/update-panel.css`、`director-desk/src/ui/ai-mcp.ts`、`director-desk/src/updates/`
- Create: `director-desk/src/host/desktop-removed.ts`(被删能力的显式不可用桩:渲染"该能力在云端不可用"提示并记录诊断,不抛异常)
- Modify: `director-desk/src/main.ts`(去掉被删模块的 mount 调用,改为 host 引导)、`director-desk/package.json`(移除 `electron`/`electron-builder`/`electron-updater`/`@modelcontextprotocol/sdk`;脚本改为 `build: vite build`)
- Test: `web/test/director-desk-desktop-removal.test.ts`

**Interfaces:**
- Consumes: Task 1 的 `director-desk/`。
- Produces: `desktop-removed.ts` 导出 `mountRemovedCapabilityNotice(container: HTMLElement, capability: "file-save" | "file-open" | "update" | "mcp"): void`;`src/main.ts` 通过 `mountHostBridge()`(Task 3)取得宿主能力。

- [ ] **Step 1: 写失败测试** — 断言:被删文件与 `src/updates/` 不存在;`director-desk/src` 下**没有**匹配 `from "electron"`/`require("electron")`/`@modelcontextprotocol/`/`node:fs`/`child_process` 的文件;`package.json` 的 `dependencies`+`devDependencies` 不含上述四项;`desktop-removed.ts` 存在且导出 `mountRemovedCapabilityNotice`。
- [ ] **Step 2: 跑测试确认失败** — `cd web && bun test test/director-desk-desktop-removal.test.ts` → FAIL。
- [ ] **Step 3: 删除并替换** — 按 Files 删除;新增 `desktop-removed.ts` 桩;`main.ts` 去掉 `mountUpdates`/`mountFileLocations`/`saveProjectFile`/`mountAIMcp` 调用点并以桩替代。
- [ ] **Step 4: 跑测试 + 类型检查** — `cd web && bun test test/director-desk-desktop-removal.test.ts` → PASS;`cd director-desk && ../web/node_modules/.bin/tsc --noEmit -p tsconfig.json`(或该目录 `npx tsc --noEmit`)→ 0 错误(依赖需先安装,见 Step 5)。
- [ ] **Step 5: 安装依赖并构建** — 在 `director-desk/` 用镜像源安装 `three`/`cannon-es`/`mediabunny` 等运行时依赖,`vite build` 产出 `dist/`;确保产物中无被删模块引用。
- [ ] **Step 6: 提交** `feat(previs): 导演台 - 剥离桌面端能力(文件/更新/MCP 桩化并接入 host 引导)`

### Task 3: Host bridge 契约与 codec

**Files:**
- Create: `director-desk/src/host/contract.ts`(契约单源:PROTOCOL_VERSION、消息类型联合、payload 类型)
- Create: `director-desk/src/host/bridge.ts`(`createHostBridge(window)`/`createAppBridge(window)`:发送/接收/请求-响应超时/未知类型忽略)
- Create: `web/src/integrations/director-desk/bridge-contract.ts`(web 侧镜像)
- Test: `web/test/director-desk-bridge-contract.test.ts`、`web/test/director-desk-bridge-client.test.ts`

**Interfaces:**
- Produces: `PROTOCOL_VERSION = 1`;app→host 消息 `desk.ready | desk.scene.changed | desk.save.request | desk.asset.request | desk.export.request | desk.ai.send | desk.ai.cancel | desk.ai.approval`;host→app 消息 `host.scene.load | host.save.result | host.asset.result | host.export.result | host.ai.event | host.ai.state`;`createHostBridge` 返回 `{ request(type, payload, timeoutMs?), on(type, handler), dispose() }`。
- 漂移守卫:两边 `PROTOCOL_VERSION` 与消息 type 集合必须一致。

- [ ] **Step 1: 写失败测试** — (a) 契约漂移:解析两个源码文件,断言 `PROTOCOL_VERSION` 相同且 type 集合逐一相同;(b) 行为:合法消息触发 handler;未知 type 被忽略且不抛;请求超时 reject;`dispose()` 后不再派发。
- [ ] **Step 2: 跑测试确认失败** — `cd web && bun test test/director-desk-bridge-*.test.ts` → FAIL(模块不存在)。
- [ ] **Step 3: 实现契约与 bridge** — 按 Interfaces 实现两个文件(用 `MessageEvent` + `structuredClone` 安全载荷;校验 `event.origin === window.location.origin`)。
- [ ] **Step 4: 跑测试确认通过** — 两个测试文件全 PASS。
- [ ] **Step 5: 提交** `feat(previs): 导演台 - host bridge 契约 v1(场景/保存/资源/导出/AI 通道)`

### Task 4: 静态构建与 `/desk/` 同源托管

**Files:**
- Modify: `director-desk/vite.config.mjs`(`base: "/desk/"`、`build.outDir: "dist"`)
- Modify: `Dockerfile`(新增 `desk-build` 阶段构建 `director-desk/`,把 `dist` 拷入 web 运行镜像的 `/usr/share/nginx/html/desk/`)
- Modify: `nginx.conf`(确认 `/desk/` 静态命中、SPA fallback 不吞 `/desk/assets/`)
- Test: `web/test/director-desk-build-artifacts.test.ts`(产物契约)+ 扩展 `.local/compose-accept/acceptance-3040.sh`(`/desk/` 200 + 入口 JS 指纹)

**Interfaces:**
- Consumes: Task 2/3 的可构建应用与 bridge。
- Produces: 运行时 URL `http://<host>/desk/` 返回原版应用;`/desk/assets/*` 命中静态资源。

- [ ] **Step 1: 写失败测试** — 断言 `director-desk/vite.config.mjs` 含 `base` 为 `/desk/`;`Dockerfile` 含 `director-desk` 构建阶段与 `desk` 拷贝目标;验收脚本含 `GET /desk/` 与入口指纹检查。
- [ ] **Step 2: 跑测试确认失败** — `cd web && bun test test/director-desk-build-artifacts.test.ts` → FAIL。
- [ ] **Step 3: 实现构建与托管** — 改 `vite.config.mjs`/`Dockerfile`/`nginx.conf`;本地用 `docker build`(不能本机 `vite build`,rolldown win32 缺失)。
- [ ] **Step 4: 验证** — 重建 3040 镜像后 `curl -sI http://127.0.0.1:3040/desk/` → 200;`curl -s http://127.0.0.1:3040/desk/ | grep -o 'assets/[^"]*\.js'` → 命中入口;`bash .local/compose-accept/acceptance-3040.sh` → 新增项 PASS 且总门仍 ALL PASS。
- [ ] **Step 5: 提交** `build(previs): 导演台 - DirectorDesk 静态构建与 /desk/ 同源托管`

### Task 5: 工作台挂载(iframe + bridge client,替换精简面板)

**Files:**
- Create: `web/src/components/canvas/previs/director-desk-app-frame.tsx` + `.css`(iframe `/desk/` + 加载态/失败重试 + 全屏容器)
- Create: `web/src/integrations/director-desk/app-bridge-client.ts`(web 侧 `createHostBridge` 使用方:场景下发、保存请求、资源应答、AI 事件转发)
- Modify: `web/src/components/canvas/previs/director-desk-previs-workbench.tsx`(改为挂载 `DirectorDeskAppFrame`;保留既有保存状态条/任务状态/Agent 浮动面板)
- Modify: `web/src/pages/canvas/use-canvas-previs.ts`(仅在 `directorDeskPrevisMode` 命中时启用新壳)
- Test: `web/test/director-desk-app-frame.test.tsx`

**Interfaces:**
- Consumes: Task 3 契约、Task 4 的 `/desk/`。
- Produces: `<DirectorDeskAppFrame scene={PrevisScene} onSceneChange={(s) => void} onReady={() => void} />`;`app-bridge-client` 导出 `createDirectorDeskHostBridge(options)`。

- [ ] **Step 1: 写失败测试** — 静态渲染断言:未命中开关时不渲染 iframe;命中时渲染 `src="/desk/"` 且带 `title`/`sandbox` 白名单;加载失败渲染重试;卸载不残留监听(bridge `dispose` 被调用)。
- [ ] **Step 2: 跑测试确认失败** — `cd web && bun test test/director-desk-app-frame.test.tsx` → FAIL。
- [ ] **Step 3: 实现** — 按 Interfaces 实现 frame + client,接入 workbench 与开关。
- [ ] **Step 4: 跑测试 + tsc** — 该测试 PASS;`cd web && ./node_modules/typescript/bin/tsc --noEmit` → 0。
- [ ] **Step 5: 真机浏览器验收** — 3040 打开画布 → 预演台,确认图2 外壳出现(菜单栏/场景·资产面板/双视口/多轨时间轴/检查器分页);画布快捷键与滚轮不串台。
- [ ] **Step 6: 提交** `feat(previs): 导演台 - 以原版应用替换精简面板(iframe 挂载 + host bridge)`

### Task 6: 场景往返与保存接线

**Files:**
- Modify: `web/src/integrations/director-desk/previs-adapter.ts`(补齐原版 Project ↔ PrevisScene 的字段覆盖)
- Modify: `web/src/integrations/director-desk/app-bridge-client.ts`(`desk.save.request` → 既有 save coordinator;`host.scene.load` 下发适配结果)
- Test: `web/test/director-desk-app-roundtrip.test.ts`

**Interfaces:**
- Consumes: Task 5 的 bridge client、既有 `use-previs-save-coordinator.ts`。
- Produces: `applySceneToApp(scene: PrevisScene)` / `readSceneFromApp(project: DirectorProject): PrevisScene` 往返函数。

- [ ] **Step 1: 写失败测试** — 多镜头/路径/角色绑定/未知扩展字段往返零丢字段;ID 稳定;`save.request` 失败时保留待写队列且不静默成功;revision 冲突走既有冲突策略。
- [ ] **Step 2: 跑测试确认失败** — `cd web && bun test test/director-desk-app-roundtrip.test.ts` → FAIL。
- [ ] **Step 3: 实现** — 补适配器字段 + bridge 接线(禁止直写 localStorage)。
- [ ] **Step 4: 跑测试 + tsc** — PASS / 0 错误。
- [ ] **Step 5: 提交** `feat(previs): 导演台 - 原版应用场景往返与保存协调器接线`

### Task 7: 导出到画布(白膜视频 / 构图帧 / 镜头参数 / 分镜表写回)

**Files:**
- Create: `web/src/integrations/director-desk/export-to-canvas.ts`
- Modify: `web/src/integrations/director-desk/render-bridge.ts`(导出改走 `previs_preview` 任务链路)
- Modify: `director-desk/src/ui/export*`(导出面板/快捷键改走 bridge `desk.export.request`;**删除本地文件下载路径**)
- Modify: `web/src/pages/canvas/use-canvas-previs.ts`(导出完成后刷新画布节点)
- Test: `web/test/director-desk-export-to-canvas.test.ts`;既有 `backend/internal/app/task_previs_render_test.go`、`backend/internal/canvas/tasks/previs.go` 必须保持绿

**Interfaces:**
- Consumes: Task 3 bridge(`desk.export.request` / `host.export.result`)、既有 `DirectorProject`、`canvas_edit_storyboard` 合同。
- Produces:
  - `requestPrevisRender(input: { canvasId: string; sceneId: string; shotId: string; kind: "whitebox-video" | "composition-frame"; render: PrevisRenderInput }): Promise<{ taskId: string }>` — 只提交任务,不落本地文件、不带磁盘路径;
  - `writeBackPrevisArtifacts(input: { canvasId: string; sceneId: string; taskId: string; project: DirectorProject }): Promise<{ nodeIds: string[]; storyboardUpdated: boolean }>` — 幂等:同 taskId 重复调用不产生重复节点;
  - `exportShotTable(project: DirectorProject): Promise<{ rows: number }>` — 镜头表 → 画布结构化分镜表(`canvas_edit_storyboard`)。

- [ ] **Step 1: 写失败测试** — 断言:(a) 导出请求含 `canvasId/sceneId/shotId/kind`,且载荷**不含**任何文件系统路径或 blob 下载;(b) 任务未达终态前不写回、失败/取消不产生资源记录与画布节点;(c) 写回产生可引用节点且**幂等**(同 taskId 两次 → 节点数不变);(d) 镜头表导出生成结构化分镜行(字段与 `canvas_edit_storyboard` 合同一致)。
- [ ] **Step 2: 跑测试确认失败** — `cd web && bun test test/director-desk-export-to-canvas.test.ts` → FAIL。
- [ ] **Step 3: 实现** — bridge 导出请求 → `render-bridge` → 影策任务;产物经资源服务落库后写回画布节点;镜头表走结构化分镜表合同。禁止绕过配额/权限/任务状态。
- [ ] **Step 4: 跑测试与后端回归** — 上述测试 PASS;`cd backend && go test ./internal/app -run 'Previs' -count=1`、`go test ./internal/canvas/... -count=1` → 保持既有结果。
- [ ] **Step 5: 真机验收** — 3040 打开预演台 → 导出白膜视频与构图帧 → 任务中心出现任务 → 完成后画布出现对应节点并可预览;导出镜头表后画布分镜表行数与镜头数一致。
- [ ] **Step 6: 提交** `feat(previs): 导演台 - 导出到画布(白膜/构图帧/镜头参数/分镜表写回)`

### Task 8: AI 操控画布(工具→场景→应用双向 + 审批 + 图片入口)

**Files:**
- Create: `web/src/integrations/director-desk/agent-canvas-bridge.ts`
- Modify: `web/src/integrations/director-desk/scene-sync.ts`、`edit-bridge.ts`(Agent 补丁下发与用户编辑回传)
- Modify: `web/src/lib/canvas/image-to-director-agent.ts`(接到新壳)
- Modify: `web/src/components/canvas/previs/director-desk-app-frame.tsx`(应用内选择状态与补丁的保留/恢复)
- Test: `web/test/director-desk-agent-canvas.test.ts`;既有后端 `cloud_agent_director_contract_test.go`、`cloud_agent_director_continuity_test.go`、`cloud_agent_director_image_test.go`、`cloud_agent_director_staging*_test.go` 必须保持绿

**Interfaces:**
- Consumes: Task 3 bridge(`host.scene.load` / `desk.scene.changed`)、既有后端 6 个导演工具(`director_block_scene`/`design_shot`/`draw_action`/`check_continuity`/`preview_shot`/`handoff_shot`)、既有 `previs_apply_patch` 审批/CAS 链路。
- Produces:
  - `applyAgentScenePatch(input: { sceneId: string; patch: PrevisPatch; revision: string }): Promise<{ appliedRevision: string; selectionPreserved: boolean }>` — 经 bridge 下发给应用,保留当前选择/视图;
  - `publishLocalEdit(input: { sceneId: string; project: DirectorProject }): Promise<{ revision: string }>` — 用户在应用内编辑 → PrevisScene → Agent 可见(`desk.scene.changed`);
  - `startImageToDirectorFlow(input: { canvasId: string; nodeId: string }): Promise<{ runId: string }>` — 图片 → 可编辑 3D 预演场景。

- [ ] **Step 1: 写失败测试** — 断言:(a) Agent 补丁经 bridge 下发且**保留选择/视图状态**;(b) **未审批的写操作不得下发**(审批预检失败 → 零 bridge 消息、零场景变更);(c) revision 过期走既有冲突策略(不静默覆盖);(d) 用户编辑回传带 revision 且可被 Agent 读取;(e) 图片入口在不支持图片输入的模型上**明确失败且不创建半成品场景**;工具顺序(block_scene → design_shot → check_continuity)被强制。
- [ ] **Step 2: 跑测试确认失败** — `cd web && bun test test/director-desk-agent-canvas.test.ts` → FAIL。
- [ ] **Step 3: 实现** — 实现 `agent-canvas-bridge` 并接入 `scene-sync`/`edit-bridge`/图片入口;所有写路径继续受权限、审批、revision、requestId、数量限制与资源校验约束。
- [ ] **Step 4: 跑测试 + 后端导演工具回归** — 该测试 PASS;`cd backend && go test ./internal/app -run 'CloudAgentDirector' -count=1` → 保持绿(基线失败项按既有 A/B 口径对照)。
- [ ] **Step 5: 真机验收** — 3040:Agent 指令(如"镜头1 加顶光并调整机位")→ 应用内立即可见且可撤销/重做;画布图片节点 → "根据此图生成 3D 预演台" → 生成可编辑 actor/道具/摄影机场景;不支持图片的模型明确报错且画布不变。
- [ ] **Step 6: 提交** `feat(agent): 导演台 - AI 操控画布双向接入(补丁下发/编辑回传/审批/图片入口)`

---

## 后续计划

**Plan 2 — 皮肤与性能收口**:暗/明皮肤在嵌入应用内的表现、WebGL/资源释放、打开关闭耗时预算、iframe 加载失败降级。
**Plan 3 — 验收与发布**:端到端合同测试(打开旧场景→编辑→Agent 补丁→重开→图片生成→白膜任务→资源写回)、`pending-test`/`features`/许可清单文档、VERSION 与真机 compose 全量验收。

## Self-Review

- **Spec coverage**: 设计文档「三层边界/编辑生命周期/渲染与资源/Agent 控制/图片入口/迁移策略/验收 8 条」分别落在 T1–T6(边界与迁移)、**T7(渲染与资源写回=导出到画布)**、**T8(Agent 控制画布 + 图片入口)**、Plan 2/3(皮肤性能与验收发布);设计第 8 条「不暴露本地工程文件/MCP token/内部地址」落在 T2 守卫测试 + T7 "载荷不含文件路径"断言。
- **已有基础无需重建(已核验)**:后端 6 个导演工具(`backend/internal/app/cloud_agent_director.go`,含契约/连续性/图片/staging 测试)、渲染任务链路(`backend/internal/app/task_previs_render.go`、`backend/internal/canvas/tasks/previs.go`)、图片入口(`web/src/lib/canvas/image-to-director-agent.ts`)、结构化分镜合同(`canvas_create_storyboard`/`canvas_edit_storyboard`)均已存在 → T7/T8 是**接线与验收**,不是重写。
- **Type consistency**: `PROTOCOL_VERSION`/消息 type 在 T3 定义,被 T4(验收指纹)、T5(bridge client)、T6(场景往返)、T7(`desk.export.request`)、T8(`desk.scene.changed`/`host.scene.load`)复用;`mountRemovedCapabilityNotice`、`createHostBridge`、`DirectorDeskAppFrame`、`requestPrevisRender`、`writeBackPrevisArtifacts`、`applyAgentScenePatch`、`publishLocalEdit`、`startImageToDirectorFlow` 名称在各任务间一致。
- **Review Focus 覆盖**:1(iframe 隔离)→T5 Step 1/5;2(桌面能力降级)→T2 Step 1/3 + T7 Step 1(a 载荷无本地路径);3(场景零丢字段)→T6 Step 1;4(AI 不绕过审批)→**T8 Step 1(b/c)**;5(资源缺失降级与 WebGL 释放)→Plan 2。
