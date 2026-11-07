# Pi Agent 通用 Worker 协议（C1）

> 本文记录 C1 在当前分支的实现与证据。C1 的跨进程最终验收仍需在 C6 使用真实 Node Worker、确定性模型和部署编排复核。

## 已落地的执行边界

通用业务 Session 现在可以通过 `EnqueueAgentSessionPiRun` 建立 Pi 运行。运行使用稳定的 `agent-session:<sessionId>` 会话键，并将以下身份冻结在 `cloudAgentRuntime.Execution`：

- `pi-execution/v1`、Session、Definition、版本、角色、Scope 和触发 MessageID；
- 工作执行的 WorkItem、Assignment ID/epoch、BudgetGrant 和 AuthorizationVersion；
- Definition 解析出的工具 Schema；
- 工作 Grant 的 microcredits 上限，投影为本轮模型准入预算。

画布运行继续使用原有 canvas policy、工具披露和 `canvas-pi-wire/v4` 形态。通用运行在运行时校验、工具刷新和 Pi Snapshot 组装处走独立分支，不能因为缺少 CanvasID 而被当作损坏的画布运行。

Pi Session 创建、CloudAgent 行写入和会话接管在同一仓储事务中完成。通用会话头使用 `agent://<conversationId>`，准入键按 `owner + conversation` 计数；同一用户同一业务空间的不同 Session 仍拥有独立的 Pi Session、entries 和写者租约。

内部 Worker 请求继续携带 `canvas-pi-wire/v4`、Pi SDK 和 Session format 身份，并增加可选的 `X-Agent-Execution-Protocol: pi-execution/v1`。缺失该扩展头仍兼容旧 Worker；非空但不匹配时由后端返回 `426 agent_protocol_mismatch`。运行 Snapshot 的通用执行合同始终由 Go 严格校验。

## C1 回归证据

| 场景 | 证据 |
| --- | --- |
| C1-A 身份、工具、模型准入 | `TestAgentSessionPiRunClaimCarriesGenericIdentity`、`TestAgentSessionPiRunAdmitsModelStep` |
| C1-B 同空间双 Session 隔离 | `TestAgentSessionPiRunIsolatesTwoSessions` |
| C1-C 接管与旧 epoch 栅栏 | `TestAgentSessionPiRunLeaseTakeoverFencesOldEpoch` |
| C1-D 无 Worker 排队 | `TestAgentSessionPiRunStaysQueuedWithoutWorker` |
| Node Bridge 与协议制品 | `cd agent && npm test` |
| Go 运行时与架构守卫 | `go test ./internal/app -run 'TestAgentSessionPiRun|TestPiModelStepAdmissionIsExactlyOnce|TestCloudAgentPi|TestPiAgent' -count=1`；`go test ./internal/architecture -count=1` |

测试记录的关键身份包括 RunID、SessionID、触发 MessageID、Worker、Pi Session lease epoch、Assignment 和 BudgetGrant。模型步骤回归停在 Go 任务已持久化的准入边界，真实 provider 执行与跨进程恢复留给 C6 验收。

## 后续切片

C2 需要把 `agent/src/runner.ts` 的启动与循环抽成通用 Session Runtime，并让总控、画布、短剧和电商都通过同一套 Pi 会话创建、工具注册、检查点和原生压缩路径运行。C1 已提供 Snapshot 身份、冻结工具与模型准入所需的 transport 字段；C2 仍需补齐通用工具回调和业务副作用网关。
