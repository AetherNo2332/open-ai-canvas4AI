# 平台 Agent 服务与总控：实施计划与分片验收

> 给执行者：仅在用户要求开始实现后，使用 `executing-plans` 逐片执行。默认不启用并行代理，不自动提交、推送、安装或部署。

**Goal：** 在首版基础 SDK 上，交付跨业务 Agent 服务、隔离的 Session/Context、统一会话 UI，以及用户可直聊的总控派工能力。

**Architecture：** Go Session/工作服务持有权威状态，Pi Worker 执行模型与工具循环，基础 SDK 承担模型、任务、计费和资源。总控是通过受控命令派工的 Agent 定义，业务 Agent 持有独立 Session，双方经持久输入和报告通信。

**Tech Stack：** 沿用实施分支实际 Go/Gin/GORM、SQLite/PostgreSQL/Redis、Node/Pi、Bun/TypeScript/React；不在本计划升级依赖。

**Spec：** [设计合同](../design/2026-11-05-agent-service-sdk-design.md)。基础依赖：[S0–S8 首版 SDK](2026-11-05-internal-platform-sdk-rollout.md)。三份文档一起阅读。

**源码基线：** `canvas4ai/main@5a0ad2ce674e4fe980a40512ac36be0372532f42`。基础 SDK 当前未实施，本文所有切片、接口、测试和新文件均为规划，不能计作已存在或已通过。

## 1. 前置条件和范围关系

- 本计划 A0–A9 是首版 SDK 之后的 Agent 阶段；不把 Agent 整体加入原 S0–S8。
- A0 先核验基础 S0–S8 的代码与验收证据。基础未完成时，只能继续合同设计、源代码分析和缺口记录，不标记后续实现可交付。
- 基础 SDK 的接口命名由基础 S0 固化；本文接口为 Agent 目标合同，A0 必须给出精确类型映射，不能臆造已存在的包/API。
- P1 结构化模型请求与 P3 层级预算若基础首版缺少，分别在 A4/A3 扩展基础层。普通 prompt→text 和单次费用上限不能替代这两项。
- 首批业务为画布、短剧、电商，总控通过注册能力接入；外部主动接单和第三方执行器不在本期。

## 2. 全局约束

1. 平台计费服务是唯一余额/账单/预算写入方；Agent 只持有授权引用，金额统一为整数 microcredits。
2. Session 的用户、业务、空间和 Agent 定义身份不可变；读取、写入、恢复、工具、审批与缓存都带归属校验。
3. 一个 Session 同时最多一个活跃写入者；一个工作同时最多一个有效执行派工。旧 lease epoch 不能再写入。
4. 总控不复制业务全部历史；业务间以显式交接和报告共享资料，记忆默认会话私有。
5. 不重建 Pi 框架，不旁路基础 SDK，不用假画布适配非画布业务，不放宽临时子代理权限以实现业务直聊。
6. 用户停止工作持久生效，自动重试/事件重放不能复活；用户关闭页面只结束本地订阅。
7. 所有新增持久结构走实际迁移序列；旧运行和账单保留，切换不得双执行、双扣费或删除新数据回退。
8. 新领域包不得 import app/service；handler 按现有稳定 service 导入面调用；遵守当前架构守卫，不提高 baseline。
9. 实施在确认的主线隔离工作区进行，保留用户现有修改；源码分析基线不等于当前功能分支，也不等于线上部署。

## 3. 目标接口与持久结构

下表中的方法名和 DTO 是本计划的统一术语。都采用 Go `(ctx, Principal, Request) → (View/Receipt, error)`；TS 核心通过类型化 client 暴露对应分组，不依赖 React。A0 为每一 DTO 补齐字段表、示例、OpenAPI 和基础 SDK 类型映射后才开始实现。

| 分组/方法 | 请求核心字段 | 结果与责任 |
| --- | --- | --- |
| `Definitions.List` | Scope | 获准 DefinitionView 列表；服务端过滤业务权限 |
| `Sessions.Create` | DefinitionID、Scope、可选 WorkItemID、IdempotencyKey | SessionView；用户由 Principal 注入 |
| `Sessions.Get/List/Archive` | SessionID 或 Scope＋分页游标；Archive 带 ExpectedRevision | 会话元信息与状态，不返回其他会话上下文 |
| `Messages.Send` | SessionID、MessageID、ExpectedRevision、模式、正文、附件资源引用 | MessageReceipt；来源身份由服务器生成，不允许前端冒充总控 |
| `Runs.Get/Cancel` | RunID、取消请求 ID | RunView；取消单 Run 不自动等于停止整个工作 |
| `Events.Read/Subscribe` | SessionID、RunID（按流类型）、Cursor | AgentEventPage/Stream；游标和基础任务游标隔离 |
| `Approvals.Decide` | ApprovalID、RunID、授权/工作/对象版本、决策、幂等键 | ApprovalReceipt；总控和业务页面消费同一条审批 |
| `Context.Build`（仅内部） | RunID、预期 SessionRevision | ContextSnapshot；包含来源、范围和版本，不接收浏览器拼接的最终上下文 |
| `Work.Start` | 来源 Session/消息、目标、Scope、授权范围、预算引用、幂等键 | WorkReceipt；建工作与初始授权绑定，不一定立即派工 |
| `Work.Delegate` | WorkItemID、ExpectedVersion、AgentDefinitionID、目标 Scope、交接引用、BudgetGrantID、ActionKey | AssignmentReceipt；重试同子项返回原派工 |
| `Work.AppendInput` | WorkItemID、ExpectedVersion、来源消息、目标 Session、输入/引用、幂等键 | WorkInputReceipt；超范围拒绝或请求扩权 |
| `Work.Pause/Resume/Stop` | WorkItemID、ExpectedVersion、原因、幂等键 | WorkReceipt；控制状态和事件同事务提交 |
| `Reports.Submit`（执行器） | AssignmentID、LeaseEpoch、WorkVersion、RunID、ReportID、状态、摘要、资源/问题 | ReportReceipt；旧派工报告不修改当前状态 |
| `Work.Get/ListReports` | 工作 ID、游标 | 进度、报告与费用投影，总控默认读这里 |
| 基础 `BudgetGrants.Create/Allocate/Close/Get` | 账户/父授权、工作引用、整数额度、预期版本、幂等键 | 预算授权视图；任务费用仍走原基础账单 |

新增持久结构：AgentSession、WorkItem、Assignment、WorkAuthorization、WorkInput、WorkReport 与持久待投递事件；BudgetGrant/预算预留由基础层拥有。精确表名、索引、字段迁移在 A0 锁定，不复制现有 Pi 消息/事件表。

关键约束：Scope 不可变；按用户＋源消息＋actionKey 去重派工；按用户＋Session＋MessageID 去重输入；按 Assignment＋ReportID 去重报告；预算单父无环；工作版本与 Session revision 使用条件更新。

## 4. 重点审查与验收归属

| 易遗漏情形 | 必须覆盖的行为 | 所属切片 |
| --- | --- | --- |
| 总控、用户、重放同时给业务 Session 发消息 | 单写者、每条受理一次、不重新接单 | A1、A5、A6 |
| 运行被停止，迟到模型/工具/报告继续返回 | 不再收费提交新任务，不覆盖新结果，不复活工作 | A3、A4、A5、A6 |
| 老用户记忆或压缩摘要含其他项目笔记 | 新会话不自动注入，迁移不把旧笔记全局发布 | A2 |
| 多业务同时申请额度，上游费用待核对 | 分配与预留原子，未知费用不按 0 释放 | A3 |
| 页面切换和 Worker 重启期间事件迟到 | 恢复正确 Scope，旧回调不能写当前会话 | A1、A4、A8、A9 |

## 5. 实施切片

每片按“补真实边界失败测试→确认失败原因→最小充分改造→专项及相邻回归→记录证据”执行。以下测试名称为拟新增名称，不是现有通过记录。

### A0：基础 SDK 接缝与 Agent 合同冻结

**依赖：** S0–S8 验收完成；若未完成，本片只产出差额与接口设计，不标记完成。
**交付：** 基础 SDK 实际代码/版本/验证证据映射，以及本计划精确 DTO、端口、迁移与文件清单。

**读取/调整：** 基础 SDK 实际包；`app/cloud_agent_pi_model_step.go`、`cloud_agent_pi_compaction.go`、`agent_subagent_write.go`、`repository/agent_message_queue.go`；设计合同第 2、3、9 节。路径未带前缀时均在 `backend/internal/` 下。

- [x] A0-A：P1–P5 逐项列出实际接口、缺失字段、处理切片和证据；明确结构化调用、预算树并非默认已存在。
- [x] A0-B：冻结第 3 节每一方法的准确 Go/TS 签名、DTO、分页/错误和事务边界；无自由密钥/执行配置透出。
- [x] A0-C：迁移清单覆盖创建、续轮、队列、插话、模型步、压缩、工具/媒体、审批、历史、子代理、前端和原收费入口。
- [x] A0-D：冻结 Session/工作/授权/派工的索引、唯一约束和迁移顺序；平台与 Agent 没有重复账本或重复消息历史。
- [x] A0-E：列出实际改动文件和测试文件；每个新文件只有明确职责，后续切片使用的类型都有定义。

**证据：** 本文补充的映射表、DTO 样例、迁移清单与基础验证记录链接。A0 是执行前门槛，不用空 SDK 或 mock 合同冒充已具备基础。

**A0 记录：** [A0 接缝映射与合同冻结](2026-11-05-agent-service-sdk-a0-contract.md)，含裁定 R-A0-1/R-A0-2（画布 Agent 首期保留原 Pi 执行链，新 Runtime 服务非画布业务）。

### A1：固定业务 Session 与持久单写者

**依赖：** A0。**交付：** 可创建、查询、归档的通用 Session；用户/业务/空间/定义不可混用。

**新增：** `agentservice/contract.go`、`ports.go`、`registry.go`、`session.go`；`model/agent_session.go`、`repository/agent_session.go`、`app/agent_service_host.go`、`handler/agent_sessions.go` 及对应 `*_test.go`。
**调整：** `model/cloud_agent_pi_session.go`、`repository/cloud_agent_pi_session.go`、`app/cloud_agent_conversations.go`、实际迁移注册、service 别名面。

- [x] A1-A：通过真实 HTTP 创建/查询同用户、同空间两个 Session，历史独立；相同外部短 ID 跨业务不会碰撞。
- [x] A1-B：跨用户、跨业务空间的会话/Run/事件/附件访问被拒绝；伪造 Scope 不获得权限。
- [x] A1-C：20 个并发续轮只有一个活跃写入者，其余得到明确冲突或队列回执；旧 lease epoch 写入被拒绝。
- [x] A1-D：现有画布 conversation 一对一映射新 Session，Pi 存储 ID 和事件序号保留，重复迁移不复制历史。
- [x] A1-E：非画布 Session 不需要空 CanvasID 或假画布；未知 AgentDefinition 在产生模型费用前拒绝。

**用例：** `TestAgentSessionScopeIsolation`、`TestAgentSessionSingleWriter`、`TestAgentSessionLegacyMapping`。此片尚未切新 Runtime，不声称非画布 Agent 已能执行。

**A1 证据：** `TestAgentSessionScopeIsolation`、`TestAgentSessionSingleWriter`（20 并发只 1 个写入者）、`TestAgentSessionLegacyMapping`（4 路并发映射只留 1 行）在 SQLite 与 PostgreSQL 17 下通过；`TestAgentSessionNonCanvasNeedsNoCanvas`；HTTP `TestAgentSessionHTTPScopeAndOwnership`。按 R-A0-1，`cloud_agent_pi_session*` 与 `cloud_agent_conversations.go` 未改：画布执行链不变，映射只新增会话根记录。

### A2：Context、记忆和工具读取隔离

**依赖：** A1。**交付：** 同一 Context Builder 按来源与 Scope 组装；默认没有跨会话记忆共享。

**新增：** `agentservice/context.go` 及测试；按 A0 冻结的 scoped memory 模型/仓储和提供器。
**调整：** `app/agent_workspace.go`、`cloud_agent_run_create.go`、`cloud_agent_context_frame.go`、`cloud_agent_memory_document*.go`、`cloud_agent_tools_memory.go`、`internal/agentcontext/checkpoint.go`、`agentmemory/` 的可复用规则。

- [x] A2-A：短剧与电商各放独有标识，捕获最终模型请求，其他业务标识不出现在消息、摘要、工具结果或附件中。
- [x] A2-B：同空间两 Session 的私有笔记/压缩摘要互不可见；显式发布业务文档后只在指定空间可读。
- [x] A2-C：旧用户级工作记忆保留在 legacy 区，新合同不注入；旧摘要不会作为新 Session 的起点；用户可查看旧记录。
- [x] A2-D：每个上下文块和缓存可追溯 Scope/Session/来源版本；恢复或压缩不换 Scope，缺身份时拒绝而非回退全局。
- [x] A2-E：工具可见不等于可执行；context-only 资料、引用文本和总控摘要不能提升权限或变成收费授权。

**用例：** `TestAgentContextScopeCanaries`、`TestAgentMemoryPublicationBoundary`、`TestAgentLegacyMemoryQuarantine`、`TestAgentContextRestoreScope`。以最终请求捕获验证，不能只检查 UI 没显示其他会话。

**A2 证据：** 上述 4 个用例在 SQLite 与 PostgreSQL 下通过，断言对象是 Context Builder 产出的最终上下文快照；领域包 `agentservice/context_test.go` 覆盖跨会话、跨空间、跨用户条目一律 `scope_mismatch`。差额：A2-A 的“最终模型请求”在 A4 接上模型步后再以真实请求复验；A2-E 的“工具可执行校验”在 A4 落地，本片只保证上下文资料不改变工具集合。按 R-A0-1，画布记忆与压缩文件未改。

### A3：基础计费层的层级预算与费用归属

**依赖：** A0、A1，基础 S2 的原子提交端口。**交付：** 多 Agent 共用一套钱包/账单，跨 Session 的累计预算可强校验。

**新增/扩展：** 基础 SDK 预算授权模块、BudgetGrant/预留与关联迁移；实际模型/计费仓储专项测试。
**调整：** 原基础计费准入和结算；`app/agent_subagent_write.go`、`cloud_agent_pi_model_step.go`、`cloud_agent_billing_invariant_test.go` 仅负责适配与回归。

- [x] A3-A：根额度 1,000，给两个子工作各分 600 的并发请求最多成功一次；子额度不能跨账户、成环或多父复用。
- [x] A3-B：20 个并发同操作收费请求只产生一笔授权和账单；父/子用量按唯一账单汇总，子分配不二次扣钱。
- [x] A3-C：分别在任务、账单、预算预留、工作绑定写入处故障，整个提交回滚，Worker 不提前执行；SQLite/PostgreSQL 均验证。
- [x] A3-D：实收、退款、在途、uncertain 分别对平；存在待核对订单的子授权不能完全归还给父继续分配。
- [ ] A3-E：总控、业务模型、压缩和媒体费用都能定位同一工作树；用户直聊原工作不切错预算，独立工作不能偷用原额度。
- [x] A3-F：停止、撤权后不再批准新收费请求；上游在途取消/费用核对真实表达，媒体恢复不重新生成扣费。

**用例：** `TestAgentBudgetConcurrentAllocation`、`TestAgentBudgetChargeRollback`、`TestAgentBudgetUncertainHoldback`、`TestAgentBudgetRollupNoDoubleCount`。

**A3 证据：** 上述 4 个用例与 `TestAgentBudgetChargeAdmission`（20 并发同键只 1 张订单、20 并发异键不超额）、`TestAgentBudgetClosedGrantFencesNewCharges` 在 SQLite 与 PostgreSQL 17 下通过；领域规则 `creationplatform/budget_test.go`。变异核对：去掉事务内准入检查或把未关闭子授权按实际用量占额后，对应用例失败。A3-E 未勾选：总控、业务模型与压缩费用定位工作树依赖 A4 的模型步与 A5 的工作单，届时以真实提交复验。A3-F 的“上游在途取消与对账”沿用基础平台既有行为，本片只保证关闭后不再准入。

### A4：复用 Pi 的通用 Runtime 与基础 SDK 适配

**依赖：** A2、A3。**交付：** 画布 Agent 首先通过新 Session、Context、基础 SDK 跑完整模型/工具循环；Pi 本身不重写。

**新增：** app 中结构化模型/任务端口适配与按定义加载工具的接线，文件职责由 A0 冻结。
**调整：** `cloud_agent_run_create.go`、`cloud_agent_pi_model_step.go`、`cloud_agent_pi_compaction.go`、`cloud_agent_pi_snapshot.go`、`cloud_agent_pi_bridge.go`、`handler/internal_agent.go`；`agent/src/{bridge,runner,session-tools,session-history,native-compaction}.ts` 及对应测试。

- [x] A4-A：结构化 messages、工具调用/结果、多模态引用经基础 SDK 保真；不得用合并 prompt 替代；模型与压缩用量可结算。
- [ ] A4-B：首步冻结配置、提示与工具合同；Worker 重启恢复原合同；新模型步骤经过 Go 权限与预算准入。
- [x] A4-C：模型返回后、检查点确认前注入崩溃，同步骤重投取回原任务；工具重放返回原回执，不重复写业务或收费。
- [ ] A4-D：Pi 恢复时跨 Session/Scope 的 entry、摘要、历史回执和技能快照被拒绝；宿主机默认工具/全局上下文发现保持关闭。
- [x] A4-E：旧在途画布 Run 走旧合同完成，新 Session 走新路径；同一请求不能被旧/新实现各执行一次。

**用例：** `TestAgentPlatformStructuredModelStep`、`TestAgentPlatformCompactionBilling`、`TestAgentRuntimeResumeAfterCheckpointGap`；回归原 Pi 合同与 Wire 测试。

**A4 证据（按 R-A0-1/R-A0-3 收敛）：** 基础层新增受信任 Go 入口 `SubmitStructuredText` / `StructuredResult`（不开放 HTTP），把结构化请求转成 worker 既有的 canonical Agent 请求，工具调用与工具结果以真实 tool 消息到达上游；准入、计费、幂等与回放沿用平台提交。Go Runtime `startAgentTurn` / `completeAgentTurn`：取会话写入者、记用户消息、冻结上下文、在会话预算下提交模型步，回复按消息 ID 幂等写回。`TestAgentPlatformStructuredModelStep`、`TestAgentRuntimeResumeAfterCheckpointGap` 在 SQLite 与 PostgreSQL 下通过；`TestAgentPlatformStructuredRejectsBeforeCost`、`TestAgentRuntimeRejectsForeignScope`、`TestAgentStructuredStepNotRetriableFromTaskCenter`。变异核对：把工具结果改成普通用户消息后用例失败。

未勾选：A4-B 的“Worker 重启恢复原合同”与 A4-D 的“Pi 恢复拒绝跨 Session 条目”针对 Pi 画布执行链，按 R-A0-1 首期不改；新 Runtime 的等价保证（同轮重投取回原任务、跨用户会话与预算在建任务前拒绝）已由上述用例覆盖。`TestAgentPlatformCompactionBilling` 未实现：新 Runtime 首期按字节预算截断历史，不调用压缩模型，没有压缩费用；模型步工具执行循环在 A6/A7 接入业务工具后补齐。

### A5：持久工作单、派工与总控报告

**依赖：** A4。**交付：** 总控通过确定性服务派出工作，父 Run 结束后业务工作继续存在。

**新增：** `agentorchestration/{contract,ports,work,assignment,inbox,report}.go`；`model/agent_work.go`、`repository/agent_work.go`、`app/agent_orchestration_host.go`、`handler/agent_work.go` 及迁移/测试。
**调整：** 注册总控工具与定义，复用现有事件/Worker 唤醒设施，不复用 parentRunID 作为工作生命周期。

- [ ] A5-A：同源消息、同 actionKey 重放只创建一个子工作、派工、执行 Session 和预算分配；合法分解多个子项可分别追踪。
- [ ] A5-B：总控 Run 完成或前端关闭后，子工作继续；重启从持久待派发记录恢复，不要求总控循环轮询模型。
- [ ] A5-C：报告状态、工作投影和待投递通知同事务提交；重复投递去重，旧 Assignment/旧工作版本报告不覆盖当前结果。
- [ ] A5-D：未授权业务/空间不能派工；总控收到报告不自动获得完整业务历史和写权限。
- [ ] A5-E：停止工作使排队派工与自动唤醒失效；新到结果保留审计但不能自动重新派工或恢复预算。

**用例：** `TestAgentWorkDelegationIdempotency`、`TestAgentWorkOutlivesSupervisorRun`、`TestAgentReportOutboxAndStaleAssignment`、`TestAgentStoppedWorkDoesNotRevive`。

### A6：用户直聊与总控输入的统一队列

**依赖：** A5。**交付：** 用户可直接进入总控派出的业务 Session，消息与控制不会被两次执行。

**调整：** `model/agent_message_queue.go`、`repository/agent_message_queue.go`、`app/agent_message_queue.go`、`agent_message_dispatch.go`、`handler/agent_message_queue.go`；对接 A5 的 WorkInput 与授权版本。

- [ ] A6-A：business Session 可直接接收用户消息；temporary 子代理仍保留原限制，不能简单移除 SQL 排除条件。
- [ ] A6-B：用户消息与总控输入并发到达，经一个队列、一个活跃执行者处理；源消息可追踪，重试不重新建工作。
- [ ] A6-C：用户在原工作补充要求，工作版本更新且总控收到一次变更通知；越界/增额需新授权，前端不能伪造 actorKind。
- [ ] A6-D：用户停止或拒绝审批后，总控旧指令、消息重放、恢复调度均不能重新执行；旧授权版本拒绝。
- [ ] A6-E：审批在总控与业务界面指向同一 ID；重复批准不重复收费，目标/报价/权限改变后旧审批失效。

**用例：** `TestAgentBusinessSessionDirectChat`、`TestAgentUserAndSupervisorInputOrdering`、`TestAgentUserStopFencesQueuedDelegation`、`TestAgentApprovalCrossViewIdempotency`。

### A7：短剧与电商业务适配器

**依赖：** A6；画布适配由 A4 起步，此处完成三业务一致合同。**交付：** 总控能管理至少两个不同业务，业务规则仍由原服务执行。

**新增/完善：** `app/agent_business_canvas.go`、`agent_business_drama.go`、`agent_business_ecommerce.go`，提供器与工具专项测试。
**调用而非重写：** `drama_service.go`、`drama_actions.go`、`drama_orchestrator.go`、`drama_canvas.go`、`workbench_bridge.go`、`internal/workbench/tasks.go`、`internal/canvas/commands/`。

- [ ] A7-A：总控授权后分别派出“短剧分镜任务”和“电商营销素材任务”，两业务各在独立 Session 执行并汇报可追踪结果。
- [ ] A7-B：业务 Agent 可读取自己的资料并申请生成；调用既有业务校验，不能绕过短剧审阅/预算和画布版本命令。
- [ ] A7-C：短剧与精修画布交接只传选定资料、目标与资源引用，采用仍为显式动作；旧版本结果不覆盖新采用。
- [ ] A7-D：业务状态机继续后台推进，Agent 不按页面状态反复重提相同生成；平台任务与短剧作业/电商批次能一一追踪。
- [ ] A7-E：注册一个最小测试业务可被总控发现/派工/读报告，不修改总控核心路由或 Session 公共字段。

**用例：** `TestAgentSupervisorDelegatesDramaAndEcommerce`、`TestAgentBusinessToolsPreserveDomainRules`、`TestAgentNewBusinessRegistration`。

### A8：Agent TS SDK 与统一会话组件

**依赖：** A7。**交付：** 同一前端入口支持总控、各业务 Session 和直聊；切换不串状态。

**新增：** `web/src/sdk/agent/{types,client,controller,events,storage}.ts` 及测试；`web/src/components/agent/conversation/` 的共用视图与 React 接线。
**调整：** `components/canvas/canvas-cloud-agent-panel.tsx`、`canvas-cloud-agent-chat-ui.tsx`、`services/cloud-agent-conversations.ts`、`cloud-agent-history.ts`、`stores/use-global-agent-store.ts`、`layouts/user-layout.tsx` 与短剧/工作台真实入口。

- [ ] A8-A：无 window/React/store 环境可导入 SDK 核心；业务页面不执行工具、预算核算和安全上下文拼装。
- [ ] A8-B：草稿、附件、待发请求、缓存、摘要、事件按用户＋Scope＋Session 隔离；切账号、切业务、切会话不复用旧状态。
- [ ] A8-C：总控→业务 A→业务 B 快速切换并注入迟到事件，只更新原目标 Session；断线、刷新可恢复准确游标。
- [ ] A8-D：从工作卡进入真实执行 Session 直聊，总控显示后续变更；关闭面板不取消后台工作，不复制另一份执行会话。
- [ ] A8-E：预算/冻结/实收/退回/待核对显示区分，实际执行方和工作归属清楚；两个视图批准同一审批只执行一次。
- [ ] A8-F：真实页面验证消息、输入、工具/报告卡、审批、错误、窄屏与滚动；同步更新原入口，不创建替代 HTML 验收。

**用例：** `agent-sdk-no-ui-dependencies`、`agent-controller-scope-switch-race`、`agent-controller-cursor-recovery`、`agent-cost-and-approval-shared-view`；加真实路由操作证据。

### A9：端到端验收与旧路径收口

**依赖：** A8。**交付：** 同一工作树在并发、故障、重启和用户接管下保持正确费用、权限和结果。

- [ ] A9-A：完整链路：用户授权根工作→总控派两个业务→用户直聊修改一个→新结果回报→根汇总；任务、账单、预算、报告和资源关联完整。
- [ ] A9-B：隔离 SQLite、PostgreSQL＋Redis、真实 Go/Pi 多进程和前端构建分别验收；模型用确定性服务，核对实际调用次数。
- [ ] A9-C：覆盖两用户×三业务×同空间两 Session；最终模型输入、存储、日志、事件和 UI 均无跨范围混入。
- [ ] A9-D：派工落库后崩溃、模型成功后断线、预算写失败、报告投递重复、旧 Worker 返回、未知费用、媒体恢复全部有演练证据。
- [ ] A9-E：原画布/Agent 临时子代理、短剧成片、基础 SDK 三业务路径与自定义渠道例外无未说明回归；清理无调用的旧控制逻辑。
- [ ] A9-F：OpenAPI、数据库说明、代码地图、功能/待测试文档和迁移记录同步；未验证外部供应商/存储不能写成上线验收完成。

## 6. 顺序、测试与回退

默认顺序：`S0–S8 完成 → A0 → A1 → A2 → A3 → A4 → A5 → A6 → A7 → A8 → A9`。不以“基础只差一点”为理由绕过 A0；当前不执行任何实现。

验证分层：领域规则测试→真实仓储事务→HTTP 与认证→Go/Pi/模型桩/存储跨进程→真实前端。mock 返回正确不能证明 Worker、费用和恢复链路成立。

在实施时对本片实际文件执行（新文件未实现前不运行这些命令冒充验收）：

```text
backend/: go test ./internal/agentservice ./internal/agentorchestration -count=1
backend/: go test ./internal/app -run '<本片准确用例>' -count=1
backend/: go test ./internal/repository -run '<本片准确用例>' -count=1
backend/: go test ./internal/handler -run '<本片准确合同/权限用例>' -count=1
backend/: go test ./internal/architecture -count=1
web/: bun test <本片实际测试文件>
web/: bun run typecheck
web/: bun run build
agent/: npm test
```

基线的 Agent `npm test` 会先 build，再执行 `dist/test/*.test.js`；A4/A9 需要该完整回归，其余切片按实际影响运行专项。实施时重新核对 `agent/package.json`；独立 Node Worker 与 web/Bun 分别遵循原锁文件，不混用依赖目录。涉及计费和迁移的测试必须使用隔离账号、数据目录、数据库和资源空间。

每次切换只允许一个收费执行路径。回退优先回退入口/适配，不回退已提交的预算与账单数据；新作用域不能被旧后端安全理解时停止接收并向前修复，不以删除状态恢复服务。用户授权生产操作后另行评估变更、备份和恢复计划。

## 7. 验收记录与总标准

沿用基础计划的证据模板，另补 SessionID、Scope、WorkItemID、AssignmentID、BudgetGrantID、RunID、模型/媒体 TaskID、账单 ID、事件游标和授权版本。证据写入实际验收记录，不预填“通过”。

- [ ] AF1：基础接缝 P1–P5 与 A0–A9 全部必需项通过，无未知依赖被默认当已实现。
- [ ] AF2：总控管理多个业务，用户可以直聊同一执行 Session，并且不会重复接单或重复收费。
- [ ] AF3：会话历史、私有记忆、压缩摘要、工具、附件和缓存按 Scope/Session 隔离。
- [ ] AF4：用户修改、停止、撤权与审批由服务端生效，旧派工/租约/消息不能越过新版本执行。
- [ ] AF5：多 Agent 合计消费受授权上限约束；账单唯一，父子汇总不重复，待核对费用不会按 0 释放。
- [ ] AF6：总控 Run/前端生命周期不决定业务工作生命周期；崩溃、事件重放和网络恢复可正确续接。
- [ ] AF7：新增内部业务通过注册适配器接入，不修改 Session 核心或复制总控实现。
- [ ] AF8：已验证范围与未验证外部链路分别列明；没有通过证据的验收项仍保持未勾选。

当前状态：设计与分片计划已编写；基础 SDK、Agent SDK、层级预算、总控派工、业务迁移和新 UI 均未实施或验收。
