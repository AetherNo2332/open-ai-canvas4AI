# Agent 工具纠错修复验收

基线：4AI `codex/agent-capability-budget`，`81e422855`。修复的是画布 Agent 工具，不改变短剧制作调度。

| 验收项 | 证据与状态 |
| --- | --- |
| 批量预检和生成共享 `selectionId` | `TestCloudAgentBatchSelectionCredential`：Schema、模型报价预检与批次审批通过。复用单张生成的签名与归属校验；`TestCloudAgentBatchSelectionRejectsInvalidCredentials` 覆盖混用、跨用户、篡改及格式错误。 |
| 批次审批不跨轮 | `TestCloudAgentBatchControlWithoutApprovalIsNotRepairable`：缺少本轮批次时不给参数重试名额，引导按新生成路径重新准入。 |
| 对准失败对象提示字段 | `TestCloudAgentArgumentFeedbackTargetsFailedObject`、`TestCloudAgentCanvasOpCorrectionGuidance`：画布操作和分镜行只提示对应对象的字段；创建字段、嵌套补丁和精确替换有定向指引。示例只用占位符，并先通过本轮 Schema 校验。 |
| 合同与权限边界 | 工具制品、目录字节预算、架构守卫通过；仅更新新轮次的 Schema。已有 Run 沿用冻结目录，不转移批次审批，不放宽写入权限或分镜原子性。 |
| 真实页面与模型 | 待验：用 `qwen3.8-27b-awq-int4` 完成「多图批次 → 下一轮改单项」「写 20+ 行分镜」和「修改文本节点」，记录纠错次数；确定性冒烟不能替代此项。 |
| 部署 | 以 6060 实例运行版本、readiness 和 Worker 健康检查确认，不以 PR 状态代替。 |

本次不改前端纠错时间线，不自动拆小已确认的分镜批次，不新增跨轮审批状态。

## 续聊历史图片回归

- 6060 原故障：上一轮看过的图片切为历史版本或所属节点被删除后，Pi 仍携带旧引用，Go 在首个模型预检 400。本轮没有创建模型步骤或发给上游。
- 自动化：`go test ./internal/app -run 'TestPiContinuesAfterHistoricalCanvasImageLosesBinding|TestAgentResourceScope|TestPiModelStep|TestAgentToolSchemaArtifactMatchesRuntime' -count=1`。新增用例先复现换图和删节点的预检失败，再核对预检与正式准入通过、仅发送有效图、原始请求和会话记录不变，以及未证明来自旧轮次或本轮图继续被拒绝。架构守卫独立验证。
- 文档站：Agent SDK 增加服务端注册、scoped client、首轮、续聊和事件示例；Agent SDK 和创作平台 SDK 各有最佳实践与验收边界。
- 真实画布、真实模型和部署证据单独记录；Go 准入回归不等同于真实模型通过。
