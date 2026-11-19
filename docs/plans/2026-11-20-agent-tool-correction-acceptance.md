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
