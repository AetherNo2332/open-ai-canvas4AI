# 平台 Agent 服务与总控：设计合同

状态：设计草案，未实施、未运行验收。本文承接[首版基础 SDK](../plans/2026-11-05-internal-platform-sdk-rollout.md)，实施切片见[Agent 落地计划](../plans/2026-11-05-agent-service-sdk-rollout.md)。不授权提交、推送、部署或生产变更。

## 1. 目标、前提与范围

目标：为短剧、电商、画布提供同一套 Agent 服务和前端会话体验；总控可以在用户授权内派工，用户也可以直接进入业务 Session；会话、上下文和预算按实际归属隔离。

实施前提是基础 SDK S0–S8 已验收，而非仅有计划文档。当前仓库中基础 SDK 仍只有设计计划，因此本文是基于未来已完成基础的后续规划。

- 复用基础 SDK 的模型、任务、计费、媒体资源，不复制价格计算、余额、账单、任务执行器和资源存储。
- 复用现有 Go 控制面＋独立 Node/Pi Worker；不新增通用 Agent 框架或独立微服务。
- 首批支持总控、画布、短剧、电商四种 Agent 定义；“任意业务”指实现服务端适配接口且获用户授权的内部业务。
- 提供 TS 无界面 SDK 与共享 React 会话组件；业务页面只描述可见信息、入口和交互，不承担工具执行、路由授权、预算计算和上下文安全逻辑。
- 首期只接受用户直接请求和已授权工作的后续事件；GitHub/飞书等外部来源、无用户目标的主动接单、第三方 Agent 市场不在本期。
- 不替代短剧制作状态机、画布命令系统或电商批次组织。Agent 调用这些业务服务，不另写业务规则。

## 2. 源码证据与可复用机制

固定基线：`canvas4ai/main@5a0ad2ce674e4fe980a40512ac36be0372532f42`。路径均相对仓库根；以 `git show <基线>:<路径>` 复查。实施时重新核对 main 漂移。

| 路径 | 源码事实 | 复用与改造 |
| --- | --- | --- |
| `backend/internal/app/cloud_agent.go`、`cloud_agent_run_create.go` | 请求和运行保存 CanvasID、ConversationID、预算；按父轮继承历史、冻结 Workspace/技能/记忆 | 保留运行与快照机制，以业务 Session 替代画布作为通用入口 |
| `backend/internal/model/cloud_agent_pi_session.go`、`repository/cloud_agent_pi_session.go` | Pi 存储身份按用户和 conversation；校验画布、活跃 Run、revision 和 lease | 保留 Pi 身份和单写者，增加不可变业务归属，不能只重命名 canvasId |
| `backend/internal/app/agent_message_queue.go`、`agent_message_dispatch.go`、`repository/agent_message_queue.go` | 持久消息、顺序、重投与调度；AgentMessageConversationOwned 明确排除临时子代理 | 业务 Session 获得独立直聊入口；临时子代理限制不能一刀切删除 |
| `backend/internal/app/agent_subagent_runtime.go`、`agent_subagent_write.go` | 临时子代理继承父请求、同画布、限制层数，预算按父 Run 切分 | 继续用于局部协作；不能直接当跨业务工作单与长期派工 |
| `backend/internal/app/cloud_agent_pi_model_step.go`、`cloud_agent_pi_compaction.go` | 模型步携带 canonical 消息/工具；有提示合同、步骤去重和压缩状态 | 经基础 SDK 提交受信任结构化模型请求，保留校验与恢复 |
| `backend/internal/app/cloud_agent_context_frame.go`、`agent_workspace.go`、`internal/agentcontext/checkpoint.go` | 新鲜任务事实、Workspace 快照和检查点合同 | 演进为按 Session 组装和审计的上下文，不重复造检查点系统 |
| `backend/internal/model/models_agent_memory_document.go`、`app/cloud_agent_memory_document.go` | 每用户一份长期记忆，包含用户偏好和工作笔记，每轮注入快照 | 增加作用域和发布机制，旧工作笔记不能默认进入新业务 |
| `backend/internal/handler/agent.go`、`agent_message_queue.go`、`internal_agent.go` | 公共 API、会话队列、Pi 内部领取与工具桥接 | 公共 API 适配通用合同；内部协议按版本迁移，不对浏览器开放 |
| `agent/src/runner.ts`、`bridge.ts`、`session-history.ts`、`session-tools.ts` | Pi Session 恢复、模型/工具回 Go、默认工具关闭、显式技能与历史回执 | 保留执行器，不因业务新增而允许宿主机全局技能或本地工具发现 |
| `web/src/components/canvas/canvas-cloud-agent-panel.tsx`、`services/cloud-agent-conversations.ts`、`stores/use-global-agent-store.ts` | 全局面板仍依赖画布，缓存与投递已有用户/画布保护 | 抽成 Session 控制器与 UI，原画布作为适配器 |

参考 [DSH 领域模型](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/001-domain.md)、[授权](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/002-authorization.md)、[执行](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/003-execution.md)、[架构](https://github.com/deepseek-ai/dsh-superagent-prototype/blob/main/docs/007-architecture.md)：借鉴工作、授权、派工、Session、报告分离，以及用户可以直接与执行器对话。以下计费和多业务合同属于本平台设计，并非声称 DSH 已提供。

## 3. 基础 SDK 的接缝要求

基础 SDK 首版没有自动承诺以下扩展。A0 验证并记录差额；缺失能力在本计划所属切片扩展基础层，不能由 Agent 临时绕回旧计费/供应商入口。

| ID | 必需接缝 | 消费位置 | 缺失时处理 |
| --- | --- | --- | --- |
| P1 | 结构化文本请求：system、messages、工具定义/调用结果、toolChoice、多模态引用、输出上限；结果含工具调用、用量、推理内容（若模型提供） | A4 的模型步与压缩 | 扩展基础 SDK 受信任 Go 文本合同；禁止压成一个 prompt 丢失工具语义 |
| P2 | 原子 Prepare/Commit、统一幂等、费用授权与业务绑定；结构化错误、单次费用上限 | A1、A3、A4 | 依赖基础 S2 的正式合同，不能复制新账户/订单表 |
| P3 | 层级预算授权、并发预留、在途与待核对费用占额、唯一账单归属 | A3 | 在基础计费层增加 BudgetGrant；既有“单次上限”不算已满足 |
| P4 | 任务快照、持久事件、结果恢复、稳定资源 ID、访问鉴权 | A4、A5、A8 | 复用基础 S3/S4；Agent 事件是独立事件流，不能把任务游标混用 |
| P5 | 已确认的 BusinessRef、身份注入、平台错误与模型选择合同 | A0–A9 | 编写 Go/TS 类型映射表；不凭本文名称猜测首版实际签名 |

P1 中 Worker 提交的 canonical 先经过 Go 的作用域、工具和提示合同校验，再转成基础模型请求。浏览器不能直接构造受信任模型步骤。

首期计费验收使用平台管理渠道。自定义渠道沿用基础 D1 决议；外部账单不可观测时标记未知/外部计费，不能当成 0 成本或承诺统一硬预算。

## 4. 核心对象与状态所有权

| 对象 | 身份与数据 | 唯一写入责任 |
| --- | --- | --- |
| AgentDefinition | 服务端注册的 ID/版本、角色、上下文提供器、工具集合、业务动作；不能由模型/前端注册权限 | Agent 注册模块 |
| AgentSession | ID、OwnerID、Role、DefinitionID/版本、Scope、可选工作单、revision；conversationId 为兼容别名，不另建第二份历史 | Agent Session 服务 |
| AgentRun | 一次用户请求或授权唤醒的执行，引用 Session、工作单、派工、预算、冻结合同及 lease epoch | 现有运行协调器的通用化实现 |
| WorkItem | ID、OwnerID、ParentWorkID、目标、版本、授权、来源 Session、业务归属、预算、状态和最终结果引用 | 确定性工作服务 |
| Assignment | ID、WorkItemID、ExecutorSessionID、执行尝试、lease epoch、版本和状态；同工作同时只有一个有效执行派工 | 派工服务 |
| WorkInput / Report | 来源消息 ID、目标工作/Session、交接资料与读写回执；报告含进展、问题、结果、引用、版本 | Inbox 与报告服务 |
| BudgetGrant | 付款账户、父授权、额度、可用额、在途预留、实收及状态；不是第二套钱包 | 基础计费服务 |

WorkItem 状态建议：`pending/running/waiting_user/paused/completed/failed/cancelled`。Assignment 状态：`pending/running/ended`。结束原因区分成功、失败、撤销与被新派工替代。

Session 可有多次 Run；Assignment 可跨多次 Run。总控本轮完成不结束工作单或子派工。停止工作、暂停自动派发、停止单个 Run、关闭前端面板是四个不同操作。

用户独立咨询可先无工作单；首次收费动作前原子建立工作单与预算关联。一个业务执行 Session 绑定一个工作单；另一项独立工作创建新 Session，不把不相关历史继续塞入原会话。重试同一工作可以更换 Assignment，并在授权仍有效时继续原 Session。

## 5. Session、Context、记忆与权限合同

### 5.1 固定范围

`Scope = 用户身份 + application + spaceType + spaceId`。Session 再绑定 AgentDefinition 与 SessionID。总控使用自身工作空间（例如 orchestration/workId），不以“当前用户全部业务”作为默认 Context。

- OwnerID 由受信任身份上下文注入；application/space 由服务端注册适配器验证，Scope 创建后不可变。
- 读取、发送、续轮、恢复、附件、审批、工具、压缩、事件订阅都校验 Session 与 Run 的同一 Scope。
- 路由切换只改变前端 activeSession；不修改在途 Run 的 Scope、授权或费用归属。
- 单 Session 一个活跃写入者；租约换代后旧执行者不能提交模型、回执、采用结果或报告。
- 同用户有权限访问多个空间，不代表一个 Session 自动拥有它们的上下文。跨空间数据必须有具体引用和授权。

### 5.2 Context 来源

模型输入只由平台规则、当前定义/Workspace、该 Session 的历史与摘要、授权业务资料、当前输入附件、该 Run 的结果事实以及显式共享记忆组装。

每个上下文块保存 source、scope、revision、来源消息/资源引用和可见范围。固定身份/配置与按需刷新业务事实分开；资料不是权限，提示词不能扩大权限。

- 同空间两个 Session 可以读同一份已发布业务文档；不能自动读彼此历史、摘要和私有笔记。
- 总控默认收到工作报告、问题与结果引用；查阅执行会话的细节需要独立的授权读取，并产生访问记录。
- 总控交接只包含目标、受限资料引用、验收要求、授权和预算。不能自动复制总控全部聊天历史。
- 上下文压缩、图像摘要、工具回执、恢复快照与缓存均保留 Scope/Session 身份；新 Session 不继承旧 Session 压缩摘要。
- 旧 Session 的既有历史保留原处，不跨业务重标；新业务不会通过旧压缩摘要再次带入未分类记忆。

### 5.3 记忆

引入 `session / business_space / user_preference` 三种明确范围。会话笔记默认私有；业务共享需显式发布到指定空间；用户偏好只接受用户明确允许共享的条目。

旧用户级记忆保留可查看和撤销的原记录，迁为 legacy 未分类；不得自动批量发布成全局偏好。新合同运行默认不注入这些条目。旧在途运行完成原冻结合同，不能在中途更换记忆规则；下一次创建 Run 使用新规则。

写入记忆校验范围和版本；子代理继承最小明确上下文，不自动拥有用户记忆写权限。

### 5.4 工具与审批

最终权限取用户权限、Session 业务范围、工作授权、定义可用工具与本次调用约束的交集。每次执行重新验证；注册/披露工具不等于获准执行。

审批绑定实际调用、执行 Session/Run、工作与授权版本、目标对象版本及费用报价。重复决策幂等；版本变化或撤权后不能拿旧审批继续执行。UI 可以把审批汇总到总控，但显示的是同一个审批 ID，不再创建第二笔任务。

## 6. 消费与预算合同

实际消费只由基础账单记一次：包括总控模型、业务模型、压缩模型、图片/视频/音频生成及明确收费的工具。计划、派工和事件通知本身默认不新增账单。

- 每笔账单可追溯 accountId、budgetGrantId、workItemId、assignmentId（若有）、sessionId、runId、operationId、taskId。业务汇总使用唯一账单 ID，不能把父汇总和子明细再次相加。
- 根授权由用户或已有明确授权的服务端策略创建；总控只在根剩余额度内划拨。对话中一句模型生成的“预算提高”不改变授权。
- 父子授权必须同付款账户、无环、单父节点；不能把同一额度完整复制给多个 Agent。账务写入统一使用整数 microcredits。
- 子额度划拨不冻结第二笔钱包资金。预算预留与账户冻结是不同维度；实际任务准入时在一个事务里检查和记录，结算/退款同步更新对应预留。
- 使用分配模型：父可分配额扣除父直接实收/在途、活跃子额度和已结束子实际花费；子额度未分配部分可继续划拨。不能又扣完整子额度又累计相同子在途金额。
- 工作或派工结束不代表账单全结。仍存在 running/uncertain 订单时保留占额；全部可核对后归还未使用部分。
- 同任务的媒体恢复不创建新生成账单；新一轮真实重生成创建新操作，并受剩余额度检查。
- 用户进入既有工作直聊，沿用原授权；另开独立工作使用独立授权。用户追加目标超出范围或额度时等待扩权/增额，不偷偷转扣其他工作。
- 沿用基础平台对上游已提交任务的取消限制；“停止工作”阻止新提交，在途任务继续协调与对账，不伪称已经退款。

约束例：根额度 1,000，总控保留 100，短剧分配 600，电商分配 300；短剧内生成属于 600 的使用，不另外再向根加收 600。全部金额在 UI 同时区分预算、冻结、实收、退回和待核对。

## 7. 总控派工、直接对话与恢复

总控只能调用确定性命令：列举获准 Agent、创建工作/派工、补充输入、查询报告、暂停/恢复/停止。业务 Agent 调用业务工具与报告接口；模型不得直接写工作单状态和账单。

- 新工作先持久化目标与授权，再创建派工、执行 Session、初始输入和待投递事件；提交后启动 Worker。
- 同一源消息只生成一份受理记录；明确分解为多个子工作时，每个子项有稳定 actionKey，并共享原授权上限。总控重试派工不能重复创建同一子项。
- 用户可直接进入该 Assignment 的业务 Session 补充要求。用户新要求由业务服务验证并更新工作版本，总控收到结构化变更；不重新接单再做一遍。
- 首期不将不相关的历史咨询 Session 自动附加给总控。绑定既有工作需明确选择工作，校验归属和授权，复用状态而不重新提交生成。
- 消息 actorKind 由服务端产生，分 user/supervisor/executor/system；进入一条持久输入队列。用户停止/修改授权由控制命令先落库，再投递通知。
- 超出范围的请求交回总控或用户授权；context-only 资料不成为收费、写入或新开工作的授权。
- 用户停止后，旧队列、定时恢复和总控不能自动复活工作；显式恢复或新建工作需要重新校验授权与预算。
- Report 携带工作版本、派工身份、session/run、状态、摘要、结果资源和问题。旧 Assignment 报告可审计保留，不改变当前工作状态。
- 状态、报告、待投递事件同事务写入；持久 Inbox/Outbox 至少一次投递，幂等接收。总控只在新结果、待人工问题或明确后续工作时唤醒，避免轮询产生模型费用。

## 8. 前端体验与 SDK

同一会话组件展示总控/业务会话。会话列表可以聚合授权目录；模型 Context 仍按 Session 隔离。工作卡片链接到实际业务 Session，不复制出一份可独立执行的假会话。

TS SDK 核心负责客户端协议、控制器、事件游标、状态归并和取消订阅；不依赖 React、画布、配置 store、window。React 层只负责呈现和调用控制器；业务扩展只声明卡片/目标导航等展示能力。

缓存、草稿、附件、选中项、待提交请求、审批和运行事件均以用户＋Scope＋Session 分区。回调捕获原始目标，不能写入“当前会话”；切页不取消服务端工作。不同事件流保存各自游标。

会话状态与工作状态分别展示；费用卡片显示实际执行方、付款归属、预算和费用明细，切换总控/业务视图不能重复计费。

## 9. 目录与接口责任

以下均为拟新增或拟调整，实际文件以 A0 的基线映射表冻结。不能将新目录存在当成功能完成。

| 位置 | 责任 |
| --- | --- |
| `backend/internal/agentservice/{contract,ports,registry,session,context}.go` | 业务无关 DTO、端口、定义注册、Session 与 Context 用例；不 import app/service |
| `backend/internal/agentorchestration/{contract,ports,work,assignment,inbox,report}.go` | 工作、授权检查、派工、输入与报告；不直接调用供应商 |
| `backend/internal/model/agent_session.go`、`agent_work.go` | 持久行；现有 Pi 表和日志继续复用 |
| `backend/internal/repository/agent_session.go`、`agent_work.go` | 归属、唯一约束、条件更新与事务；不放业务策略 |
| 基础 SDK 的预算模块、现有计费仓储与迁移 | BudgetGrant、原子预算准入与账单归属；禁止由 Agent 域另起账本 |
| `backend/internal/app/agent_service_host.go`、`agent_business_{canvas,drama,ecommerce}.go`、`agent_orchestration_host.go` | 组合根接线与业务适配，复用原业务服务 |
| `backend/internal/handler/agent_sessions.go`、`agent_work.go` | HTTP 身份和 DTO，不直接调用 app/repository |
| `agent/src/bridge.ts`、`runner.ts`、`session-tools.ts`、`session-history.ts` | 通用 Scope 与定义快照的版本化协议；保留 Pi 运行循环 |
| `web/src/sdk/agent/{types,client,controller,events,storage}.ts` | 无 UI 客户端与会话控制 |
| `web/src/components/agent/conversation/` | 共用消息、输入、会话目录、工作/费用/审批卡片及 React 接线 |
| 原全局面板、历史服务、画布/短剧/工作台页面 | 按业务接入和导航，逐步移除重复控制逻辑 |

Go 命令统一为 `方法(ctx, Principal, Request) (Receipt/View, error)`。公开 DTO 不透出 GORM、repository、密钥或任意可执行配置。精确请求/返回见实施计划 A0 的接口清单。

## 10. 迁移与完成边界

- 新增 Session 根记录并映射现有 conversation；不改既有 Pi 存储 ID，不把不同用户/业务历史合并。
- 当前在途 Run 保持原合同。按 Session 选择唯一执行实现，排空或暂停后切换；不得同一请求影子双写或两路执行。
- legacy 临时子代理仍标记 temporary，保留原直聊限制；新的 business Session 不受该临时子代理排除规则误伤。
- 新迁移遵循实施分支当前版本序列；旧模型中的必填 CanvasID/画布条件必须逐条适配，不用空画布/假画布绕过校验。
- 若用户仅回退 UI，可继续恢复新 Session；若后端不能理解新作用域或预算记录，禁止降级接收新请求。保留新数据并向前修复，不能删除账单/授权换取旧版启动。
- 新定义首期随代码注册；角色、作用域、预算与工具限制由服务端控制，不建立可执行任意代码的注册入口。

最终验收必须证明：总控管理至少两个不同业务，用户能直聊其中一个并让总控感知变更；Session/Context 隔离；并发预算不超额、账单不重复；崩溃和重放可恢复；新增测试业务无需修改会话核心或总控路由逻辑。
