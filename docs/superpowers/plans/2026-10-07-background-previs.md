# 后台导演台预演实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用户关闭全部画布网页后，Agent提交的导演台预演仍生成真实MP4与构图帧，服务器将成果写回画布后才给出成功工具回执。

**Architecture:** Go持久任务冻结渲染输入并管理权限、取消、资源和原子回写；独立Node renderer运行无头Chromium并复用真实PrevisViewport。Pi控制面与预演任务分别恢复，稳定task/call/resource/node身份防止重复执行。

**Tech Stack:** Go/GORM、React Three Fiber/Three.js、Node、Chromium、FFmpeg、Docker Compose。

**Spec:** `../../../../artifacts/director-ssh-20261007/后台预演设计.md`（用户已授权开始实现，包含H/C/P异常合同）。

**执行记录（2026-10-08）：** Tasks 1–6已实现并通过相应自动验证。Task 7的真实三镜、真实冲突恢复、必要回归与独立审查已完成；完整人工H/C/P场景及网页重开播放仍待验收，不能据此宣称生产可用。证据报告：`2026-10-07-background-previs-validation.md`。

## Global Constraints

- 首版软件渲染、并发1，不占模型推理GPU；不修改生产3000。
- 任务输入只允许授权资源；浏览器运行时不访问任意URL，默认演员随镜像打包。
- 只有可解码且成功关联画布的产物算成功；保存后回写失败可只修复回写。
- 用户取消优先于迟到结果；Pi故障不取消此前获准的独立预演。
- 保留既有`agent/harness/MEDIA_POLICY.md`和`SYSTEM_POLICY.md`用户改动。

## Review Focus

- 取消与回写提交竞态：旧租约不能提交，成功终态与画布同事务。
- 请求响应丢失、Pi回执缺失：按稳定身份对账，不重复渲染或创建节点。
- 模型载入失败、WebGL恢复、相机缺失：不能把占位/空白画面当成功。
- 目标镜头修改与无关画布修改：前者保留旧产物并报冲突，后者合并最新画布。
- Pi终态、用户拒绝和审批过期：不复活run，不扩大原授权。

### Task 1: 持久预演输入与幂等提交

**Files:** `backend/internal/app/cloud_agent_previs_render.go`、`cloud_agent_runtime.go`、`cloud_agent_pi_bridge.go`、`backend/internal/model/previs_task_type.go`及专项测试。
**Interfaces:** `preparePrevisRender(repo,user,canvas,call) (previsRenderInput,error)`；`enqueueCloudAgentPrevis(repo,current,state,call,policy) error`；task type `previs_render`。
- [x] 测试先验证：真实queued任务、冻结输入、默认值上下限、归属与镜头相机校验；重复提交仅一任务；审批源版本变化不提交。
- [x] 实现任务创建与run检查点同事务，submitted事件不提前tool_completed；等待任务终态后回执，复用MediaTaskID等待合同。
- [x] 运行专项Go测试，全部通过。

### Task 2: 真实只读渲染入口

**Files:** `web/src/components/canvas/previs/previs-viewport.tsx`、`web/src/previs-render.tsx`、`web/previs-render.html`、`web/vite.previs.config.ts`、`web/test/previs-render.test.ts`。
**Interfaces:** `window.previsRenderer.load(scene,shotId)`与`frame(time)`、`state()`；复用viewport，等待模型就绪与显式playhead，返回PNG帧。
- [x] 测试先验证：镜头锁定、隐藏辅助UI、模型失败阻止捕获、帧时间与相机运动。
- [x] 实现专用入口和严格就绪状态；使用已有骨骼/材质/对象/相机动画。
- [x] 运行Bun专项测试与独立Vite构建，通过。

### Task 3: renderer服务与可恢复输出

**Files:** `renderer/package.json`、`renderer/src/*.mjs`、`renderer/test/*.test.mjs`、`renderer/Dockerfile`。
**Interfaces:** 服务鉴权的`POST /jobs`、`GET /jobs/:id`、续期/取消、产物下载；taskId+attempt+leaseOwner fencing。
- [x] 测试先验证：提交幂等、限流、错误输入、取消/过期租约、重启对账、禁止浏览器出站、真实MP4/首帧探测。
- [x] 实现并发1、有限任务/帧/文件大小、受控资源映射、确定性截帧/编码、持久manifest与限期回收；仅基础设施瞬态错误允许额外重渲染1次。
- [x] 运行Node测试及无用户网页的真实演员+体块+机位运动短片；可解码、帧数与时长正确。

### Task 4: 后端执行、产物登记与原子回写

**Files:** `backend/internal/app/task_previs_render.go`、`task_worker.go`、`backend/internal/repository/task_previs.go`与专项测试。
**Interfaces:** `processPrevisRender(task,ctx)`；按租约保存产物事实；`SaveTaskCompletionWithRegistration`回写最新画布。
- [x] 测试先验证：合法资源传输、保存后故障保留索引、无关修改合并、目标变化/删除冲突、取消/旧租约拒写、响应丢失不重复登记。
- [x] 实现HTTP提交/查询/续租/取消，下载后探测；稳定资源与节点身份，成功状态与画布同事务。
- [x] 测试只回写恢复使用原资源且保留失败历史；不调用renderer、不创建重复节点。真实冲突恢复也确认renderAttempts为1。

### Task 5: 人工控制与Pi故障隔离

**Files:** `cloud_agent_recovery.go`、`cloud_agent_previs_render.go`、`cloud_agent_pi_bridge.go`及专项测试。
**Interfaces:** 复用既有插话/审批/取消入口，预演等待不调用模型；按终结原因清理关联任务。
- [x] 测试先验证：Pi失败/看门狗清理状态下任务继续、明确用户取消停止、迟到回执不复活终态、receipt恢复只补事实。
- [x] 实现清理边界与结构化诊断；暂停新投递、审批待答沿用现有持久入口，不伪称逐帧暂停。
- [x] 运行H/P状态与协议专项测试，通过；真实Pi杀进程/模型异常列入待测试。

### Task 6: 产品接线、Compose和文档

**Files:** 前端Agent预演事件接线、skill/工具描述、Compose配置、`.env.example`、专题/待测试文档。
- [x] 去除Agent浏览器录制触发；手动导演台导出保持原路径。
- [x] 内部renderer服务、鉴权、持久卷、资源限制接入隔离Compose；无宿主公开端口。
- [x] 同步真实后台任务、诊断与恢复操作说明，记录首版边界。

### Task 7: 隔离端到端和故障注入

**Files:** `backend/internal/app/task_previs_render_test.go`、`renderer/test/`、本计划与验收报告。
- [x] 运行三镜真实任务：不打开导演台/画布网页，核对三个MP4、资源和视频节点；后端重读画布引用已独立探测可解码媒体。
- [x] 真实目标变化后保留产物、只回写恢复不重渲染；Pi失败清理不取消已获准任务。
- [ ] 完整执行设计H1-H3、C1-C4、P1-P7人工运行场景；已有状态/协议/真实渲染覆盖和剩余项逐条见验收报告。
- [ ] 真实用户网页重开播放、任务中心交互、手动导出验收。
- [x] 运行必要Go/Bun/Node回归和构建，记录命令与结果；完整冻结锁文件生产镜像另列待验证。
- [x] 整体独立代码审查，6项重要问题RED→GREEN修复；生产升级另行风险复盘。
