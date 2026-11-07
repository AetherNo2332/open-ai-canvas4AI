# Pi Agent 全业务服务实施计划

> 当前执行合同为[Pi Agent 服务与总控设计对齐](../design/2026-11-07-pi-agent-service-alignment.md)。
> C0–C6 是运行架构对齐分片，承接 PR #131 和本地 B 阶段实现。验收项均待实施。
> 执行者按 `executing-plans` 逐片推进，将真实 Pi Worker 跨进程运行作为验收组成部分。

## 目标和基线

总控、画布、短剧和电商通过现有画布 Agent 同源的 Pi Runtime 执行，Go 提供会话、消息、权限、任务、计费、工具宿主和业务服务。

代码基线为 `codex/agent-supervisor-b0b6@4ae426d88`。实施前核对实际分支、已有修改、工具链和上游提交。原 A0 的 R-A0-1、R-A0-3 与旧 B 计划中的 Go Agent 循环选择已经被当前用户确认的 Pi 架构取代。

## PR #131 的处理与交付

2026-11-07 核对时，PR #130 与 #131 均为 OPEN。PR #131 的目标为 PR #130 的 `feat/creation-platform-sdk`，head 为 `ba85fc6c3`，当前尚非 Draft。

建议继续由 PR #131 承载架构对齐后的全业务 Pi Agent 服务与总控 Session 协作，实施阶段将其标记为 Draft，描述中说明当前正在进行 Pi 架构整改。最终标题围绕同源 Pi Runtime、通用业务 Session 和总控消息协作表达。

已有本地 10 个 B 阶段提交作为开发基础，在 `codex/agent-supervisor-b0b6` 上完成 C0–C6 及审查问题修复。通过新合同验收后，以正常追加提交将最终分支更新到 PR #131 的 `feat/agent-service-sdk`，沿用已有 PR 与审查记录。

Session、消息、工作、授权、预算、审批、业务适配器和 SDK 的有效实现进入最终提交。Go Agent 循环、自建摘要与画布单独接入的安排按本文迁入同源 Pi 服务，总控使用受控消息工具联系业务 Session。

发布与合并顺序为 PR #130 完成自身验收并合并，PR #131 核对已合入基础 SDK 的 main 后更新目标与基线，完成最终审查再合并。PR #131 的完成依据为 C0–C6 共 28 项、真实 Go/Node-Pi 跨进程证据及上一轮审查问题的修复回归。

上述 PR 状态调整、标题描述更新和远端提交属于交付步骤，实际执行依照用户对远端操作的明确授权。

## 分片总览

| 分片 | 交付 |
| --- | --- |
| C0 合同与迁移清单 | 固定通用 Pi 身份、Session 消息和执行路由合同 |
| C1 通用 Pi Worker 协议 | 非画布 Session 可以实际领取、心跳、恢复和持久化 |
| C2 同源 Pi Runtime 与工具 | 四类 Agent 通过同一 Pi 启动、循环、压缩和工具回调机制运行 |
| C3 总控 Session 消息协作 | 总控通过受控消息工具驱动业务 Session，用户可进入同一会话 |
| C4 授权、计费与业务状态 | 每个 Pi 运行和业务操作绑定有效授权、回执与完成条件 |
| C5 统一前端会话 | 会话目录、实际业务 Session、工作、审批和费用一致呈现 |
| C6 切换与真实链路验收 | 在途恢复、执行路径切换、三业务回归及跨进程证据 |

## C0 合同与迁移清单

读取实际 `agent/src/runner.ts`、`bridge.ts`、`session-tools.ts`、`worker-runtime.ts` 和 Go 的 Pi Session、运行、消息入口。将画布专属上下文与通用执行机制列成迁移表。

固化以下合同。

- 通用 Snapshot 包含 owner、Session、Definition、Role、Scope、Run、触发 Message、Pi revision、lease epoch、有效工具、模型限制、提示及技能快照。
- 受信任执行绑定包含 WorkItem、Assignment ID/epoch、BudgetGrant 和 AuthorizationVersion，由 Go 从授权记录解析。
- Session 消息包含目标 Session、来源、幂等 MessageID、正文、授权资源引用及业务工作关联。
- 待执行消息只选择一个已冻结执行协议，切换记录与任务、费用、操作回执可追踪。

验收如下。

- [ ] C0-A 四类 Agent 的 Snapshot、工具、消息和恢复 DTO 有完整字段与失败语义，非画布用例通过完整 Scope 校验。
- [ ] C0-B 当前 canvasId 入口依赖、Pi Session 表约束、调度键、历史与预算绑定均列出明确改动位置。
- [ ] C0-C R-A0 与旧 B 计划的冲突已标记，新的 Go/Pi 责任边界与执行协议版本已记录。
- [ ] C0-D 现有 Session、在途任务、工具回执和 Pi entries 的迁移、恢复及切换步骤可审查。

## C1 通用 Pi Worker 协议

修改 `agent/src/bridge.ts`、`server.ts`、`worker-runtime.ts` 及 Go 的 internal-agent、Pi snapshot、claim、repository/model 对应实现。按现有协议机制增加通用版本或兼容读取分支。

使用稳定 Session 身份组织 Pi entries、active leaf 和写者租约，画布身份作为业务 Scope。Worker 实例凭据、Run 执行凭据和用户业务授权各自校验。

验收如下。

- [ ] C1-A 真实 Node/Pi Worker 从 Go 领取一个短剧 Session，完成心跳与模型步骤，运行身份包含完整 Scope。
- [ ] C1-B 同用户同项目的两 Session 可分别调度，entries、上下文和写者身份互不混用。
- [ ] C1-C 多实例接管后旧 lease epoch 的模型、工具、检查点与终态提交被拒绝。
- [ ] C1-D Worker 不可用时状态与原因可见，Worker 恢复后原消息继续运行。

## C2 同源 Pi Runtime 与工具分发

从现有画布 runner 抽出通用启动与循环，建议文件为 `agent/src/session-runtime.ts`。通过 Host 提供业务上下文和回调，将现有画布入口接到该内核。通用化 session-tools 的 Schema 注册与工具回调，复用现有 native-compaction、session-history、checkpoint 和错误恢复机制。

Go 输出 Definition、角色、Scope、用户权限与工作授权的有效工具交集，并冻结当前运行合同。工具由 Pi 注册和调度，具体业务副作用经 Go 工具网关执行。

验收如下。

- [ ] C2-A 总控、画布、短剧和电商的运行证据均经过同源 `createAgentSession` 和 Pi 模型/工具循环。
- [ ] C2-B 未授权业务工具不出现在模型工具集合，伪造调用在 Go 被拒绝，已撤权工具即时失效。
- [ ] C2-C 当前运行按冻结合同恢复，新授权由新运行合同生效，跨 Session 的 entries、摘要、技能和工具回执被拒绝。
- [ ] C2-D 长会话经过两轮 Pi 原生压缩后保留早期事实及近期用户要求，压缩费用归正确工作且重放幂等。

## C3 总控通过 Session 消息协作

新增或调整 Go 的 Session 通信工具与 SDK 合同，建议文件为 `agent_session_tools.go`。将现有派工、输入、outbox 和报告能力接到统一消息入口。

总控 Pi Session 使用 `agent_sessions_list`、`agent_session_open`、`agent_session_send`、`agent_work_read`、`agent_request_user`。工作关联由 Go 处理，发送后的目标消息唤醒业务 Pi Session。业务显式报告再唤醒总控 Session。

验收如下。

- [ ] C3-A 用户目标触发总控 Pi 运行，总控发送消息后两个业务 Pi Session 自动执行，测试主体通过用户入口推进。
- [ ] C3-B 总控重发同一 MessageID 或消息投递重复 10 次，目标只受理一次，子工作和预算绑定均去重。
- [ ] C3-C 总控向其他用户、其他根工作或未授权空间的 Session 发消息时被拒绝，业务只接收明确发送内容及授权引用。
- [ ] C3-D 用户进入同一业务 Session 直聊，消息与总控输入有序执行，报告唤醒正确总控 Session，等待期间模型调用数保持稳定。

## C4 授权、费用、工具回执和完成条件

复用并修复现有 WorkAuthorization、BudgetGrant、业务适配器、审批和 AgentOperation。模型步和压缩通过 Go 计费网关，工具回调完整携带 Assignment、租约和授权版本。

短剧所有收费触发入口与电商生成、重试持续关联本次授权。镜头和其他写操作通过业务原子命令或持久幂等操作标识提交。业务完成策略核对所需操作、资源和采用状态。

验收如下。

- [ ] C4-A 总控推理、业务推理、Pi 压缩及媒体费用归正确工作树，唯一订单账务对平，待核对占额可核实。
- [ ] C4-B 单次审批、任务提交失败、业务写入后断线和回执失败均可恢复，已提交操作不会覆盖用户后续版本。
- [ ] C4-C 停止、撤权、改派后旧执行凭据无法产生新写入或收费，直接重试短剧项目等入口也受原工作额度约束。
- [ ] C4-D 多任务工作等待全部必要结果，资源交付或采用未达条件时状态准确，模型声明 completed 仍经过业务事实核对。

## C5 统一会话体验

修改现有 Agent TS SDK、会话组件和工作目录，连接统一 Pi Session 的消息与事件投影。总控、业务、画布共用身份、订阅、草稿和历史控制器，业务操作卡片按角色提供。

验收如下。

- [ ] C5-A 总控工作卡打开实际业务 Session，画布、短剧和电商均可直接对话并展示异步回复。
- [ ] C5-B 切用户、项目或 Session 以及新会话加载失败时，显示、草稿、订阅和发送目标分别绑定正确身份。
- [ ] C5-C 总控和业务视图显示同一审批与账务事实，关闭面板、取消本轮和停止工作的效果可验证。
- [ ] C5-D 真实页面验证消息、工具进度、报告、资源、刷新恢复、窄屏和主题，记录实际路由与运行 ID。

## C6 切换与跨进程验收

逐个执行入口迁入通用 Pi Runtime。根据已冻结执行协议处理既有 Go 在途消息和任务，再将新轮次统一调度到 Pi。完成切换后收拢业务 Go 模型/工具循环和自建摘要的调用入口。

新增表与字段同步数据库模型清单、SQLite/PostgreSQL 迁移及复制工具。历史消息转为 Pi entries 或只读上下文时保留来源、顺序和已执行回执，所有恢复行为核对执行身份。

验收如下。

- [ ] C6-A 真实 Go、Node/Pi、确定性模型与前端完成用户目标、Session 消息派工、用户直聊、自动报告、总控汇总和费用查询。
- [ ] C6-B SQLite 与 PostgreSQL/Redis 分别覆盖停止、改派、实例接管、消息重投、工具回执窗口与媒体恢复，操作和订单各执行一次。
- [ ] C6-C 新旧数据和在途任务切换后可恢复，原画布操作、临时子代理、短剧成片及电商批次回归通过，复制工具包含全部新增表。
- [ ] C6-D 四类 Agent 的最终运行均有 Pi 证据，实际渠道与资源存储样本按授权验证，验收记录列出提交、环境、调用和账务计数。

## 验证与交付

各片先建立对应回归，再修改最小调用链。Go 检查覆盖领域、仓储、handler、迁移、复制和架构守卫，Node/Pi 检查覆盖实际 Worker 运行、工具合同、恢复和压缩，前端检查覆盖真实组件及页面。

每条验收记录 SessionID、RunID、触发 MessageID、Worker、lease epoch、工作与预算关联，以及模型、工具、任务、资源和订单数量。全部 28 项通过后形成全业务 Pi Agent 服务验收记录。

## 可交给其他 Agent 的提示词

```text
将平台总控和业务 Agent 的设计与现有画布 Pi Agent 对齐。

先阅读本文件及同目录 ../design/2026-11-07-pi-agent-service-alignment.md，
并核对实际 checkout 的 AGENTS.md、锁文件和当前源码。

用户已经明确运行架构。
所有 Agent 的模型、工具、压缩、检查点与恢复循环由现有画布同源 Pi Worker 承担。
总控是独立 Pi Session，通过受控消息工具向授权业务 Session 发消息。
用户可直接进入同一业务 Session，消息有序处理，Session 与 Context 隔离。
Go 承接身份、消息、工作、授权、预算、审批、模型与工具网关和业务状态。

基线为本地 codex/agent-supervisor-b0b6，交接 head 为 4ae426d88。
最终由修正后的 PR #131 承载交付，实施分支通过验收后以正常追加提交更新其 head 分支。
从 C0 到 C6 逐片实施，以真实 Go/Node-Pi 跨进程链路证明全部 28 项验收。
复用现有 Pi Session Runtime、工具注册、原生压缩和恢复能力，
保留并接入现有 SDK、Session、工作、预算、业务适配器和 UI 的有效实现。
R-A0-1、R-A0-3 及旧 B 计划中的非画布 Go 循环选择已被当前设计取代。

将上一轮审查的预算传递、跨工作树指令、Assignment 栅栏、
回执原子性、业务完成条件、前端身份隔离和复制清单问题纳入回归。
持续推进到代码、文档、28 项验收和运行证据完整交付。
产品或外部操作所需授权按用户现有要求处理。
```
