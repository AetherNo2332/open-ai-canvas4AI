# 摄影机参数 × Agent 生图 × 设备图库补全

> 状态：随实现推进（本文件随首个实现提交入库）。
> 基线：main @ v1.6.0（previs agent 修复已合入）。

## 1. 一句话结论

两条线：(A) Agent 的 `generate_media` 生图继承来源图片节点的摄影机参数（机身/镜头/焦段/光圈编译出的镜头提示词），与用户侧生成链路同构；(B) 摄像机控制面板机身/镜头栏目补齐缺失的真实设备图片（17 台机身 + 17 支镜头），联网搜索后按既有资产约定入库并接线。

## 2. 现状锚点

- 摄像机控制面板：`web/src/components/canvas/canvas-node-camera-dialog.tsx`（确认时回调 `(options, prompt)`，prompt 目前被丢弃）；入口 `canvas-camera-control-popover.tsx` 只持久化 `cameraControl`。
- 参数模型：节点 `metadata.cameraControl = { enabled, camera, lens, focalLength, aperture }`（id 取自 `camera-prompt-library.ts` 的 CAMERA_PROFILES 26 项 / LENS_PROFILES 27 项）。
- 用户侧生图：`use-canvas-generation-executor.ts:147` 客户端用 `buildCanvasVisualGenerationPrompt` 把 cameraControl 编译进最终提示词；服务端不感知。
- Agent 生图：`backend/internal/app/cloud_agent_media.go`（`createCloudAgentMediaNode` L820 `input["prompt"] = a.Prompt`）不读来源节点相机参数。
- 设备图资产：`web/public/camera-controls/`（tapnow HAR 提取 + `source-manifest.json`），`camera-control-assets.ts` 的 `CAMERA_BODY_IMAGES`(9/26) 与 `LENS_IMAGES`(10/27) 有图，其余回退 SVG 插画。

## 3. 目标

### A Agent 生图继承图片节点的摄影机参数

- **A1 前端**：摄像机控制确认时把编译好的镜头提示词持久化到节点 `metadata.cameraPrompt`（`buildCameraControlPrompt` 的产物；禁用时置空）。改 `canvas-camera-control-popover.tsx` 回调签名 + `canvas-config-composer.tsx` / `canvas-node-prompt-panel.tsx` 两个写入点。
- **A2 后端**：`generate_media`（image 与 video 模式）准备阶段读取**来源/参考节点**的 `metadata.cameraPrompt`，追加到任务 prompt 与节点 composer prompt；无 `cameraPrompt` 时用 cameraControl 原始字段拼一句通用兜底（不抛错、不猜目录文案）。
- **A3 语义**：与用户侧一致——cameraControl.enabled=false 不追加；参数属于来源节点，不是 Agent 工具参数（schema 不变，走描述与系统策略说明）。

### B 摄像机控制面板补齐真实设备图片

- **B1 内容**：为 17 台缺失机身 + 17 支缺失镜头联网搜索真实产品图；优先 Wikimedia Commons（许可可追溯），其次厂商官方产品页；下载后用 PIL 统一为「裁边 + 最长边 ≤616px + 透明/白底 PNG」，落盘 `web/public/camera-controls/<kebab-id>.png`，`source-manifest.json` 逐条记录来源 URL 与许可。
- **B2 接线**：`camera-control-assets.ts` 的 `CAMERA_BODY_IMAGES` / `LENS_IMAGES` 补齐映射；`CameraArtwork` 加载失败仍回退 SVG，不改布局与命中区域。
- **B3 诚实边界**：搜索不到可靠来源的条目保持缺失（回退插画），在 manifest 的 `missing` 列表记录；不使用 AI 生成图冒充真实设备照片。

## 4. 非目标

- 不为设备图搜索新增 Agent 运行时工具（这是内容采集 + UI 资产，不是运行时能力）；
- 不改光圈列图片（f 值图形为定制图形，不在「真实机身/镜头」范围）；
- 不做管理员上传设备图的界面；
- 不改 `camera-prompt-library.ts` 的目录与模板（数据源不变）。

## 5. 验证

- 后端：`go test ./internal/app -run 'TestCloudAgentMediaCamera'` 覆盖「有 cameraPrompt 追加 / 禁用不追加 / 原始字段兜底」；全量 `go test ./internal/app`。
- 前端：`bun test web/test/camera-control-metadata.test.ts`（确认回调持久化 cameraPrompt）+ 既有 2274 用例不回归；`bun run typecheck`、`bun run build`。
- 内容：manifest 中 34 个条目逐条有 source URL；图片尺寸统一；`bun test` 增加「映射与磁盘文件一一对应」的守护测试。
- 交付：pending-test.mdx 条目 + VERSION bump + CI 全绿。
