# 分镜表发送到当前 Agent 验收记录

## 代码完成

- 分镜节点不再独立选择文本模型或提交脚本拆镜任务；简洁和专业模式均保留镜头数及单镜时长选择。
- 发送箭头、Ctrl/Cmd+Enter、节点重试与全屏局部重拆统一投递到当前画布 Agent 会话；发送前保存画布并保留草稿。服务端 split/resplit API 与已有历史恢复行为不移除。
- Agent 先读取指定分镜表、项目基调、相关上下文和适用技能，缺少画风/预设时继续规划；先通过现有 ask_user 交付方案。只有用户确认后才用 `storyboard.write_rows` 原子写回原表。
- 新写入动作复用画布写权限、快照冲突、事务、素材校验、任务占用和操作幂等；保留锁定行及原有媒体引用。无绑定末尾行才可删除，不创建替代分镜表。
- 当前会话续聊/插话消息在服务端验证原会话业务和 Agent 归属后继承冻结 Run 配置；只替换用户消息、幂等键和焦点节点。运行时配置不会被分镜组件重选。

## 自动化验证

- `bun test test/storyboard-agent-request.test.ts test/canvas-storyboard-panel.test.ts test/cloud-agent-panel-sync.test.ts test/project-chapter-skill-runtime.test.ts test/agent-sdk.test.ts` 共 25 项通过，覆盖忙时插话、提问待答时排队、保存失败时保留输入且不 POST，以及未知结果安全重试。
- Go 会话继承专项及既有 admission 回归：`TestAgentMessageQueueInheritsCurrentRunConfiguration` 与 `TestAgentMessageQueueStopInvalidatesPreparedAdmission` 通过。新增用例先因服务端拒绝缺少 Run 模型配置失败，再随最小实现通过。
- 真实 Chromium + Go HTTP 服务 + Pi Worker + 临时 SQLite + 确定性模型：`TestAgentSDKRealHTTPWorkerRestart` 通过。验证 3 镜/每镜 10 秒请求、先问后确认、确认前分镜不变、确认后写回同一表，保留锁定镜头和图片关联，且服务端 split 请求/拆镜任务均为 0。
- Go 工具合同和 schema 预算通过，运行时与版本化 Harness 制品一致。Go 架构基线扫描通过，分镜编辑移除一处已解决的复杂度例外。
- 首轮 Windows 门禁中，Web 类型检查、格式和 lint、Worker 测试、文档构建/类型检查通过；普通 Web 测试和 Go 分片有 Windows 环境失败，已在原基线复现。首次 Docker 任务被 Docker Hub 认证连接超时阻断，随后切到本地镜像构建器复验。

## 分层验收状态

- 代码完成：是，仍需合并审查。
- 自动化：上述定向测试及确定性模型浏览器 E2E 通过；受影响全量推送门禁与 PR CI 待最终复核。
- 真实页面：本地 Chromium 验证专业模式的镜头数、时长和确认写回。当前画布页面入口固定为专业模式，简洁模式的控件由组件检查覆盖。忙时插话、排队和失败提示由面板测试覆盖，浏览器手工复核待完成。
- 真实模型：浏览器联调用确定性模型，真实业务模型待验。
- 部署：未部署，本 PR 不构成部署验收。
