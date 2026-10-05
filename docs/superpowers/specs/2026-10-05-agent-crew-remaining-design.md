# Agent Crew 剩余开发设计

## 状态

- 日期：2026-10-05
- 基线分支：`codex/crew-admin-switch`
- 目标：补齐 Crew 从画布配置到实际运行、审批提交和本地 Compose 验收的端到端闭环。
- 范围确认：采用垂直切片；本设计只覆盖当前源码中尚未接通或尚未验证的部分，不重复已存在的 Crew 模型、Repository、服务和 API。

## 当前基线

后端已经具备 Crew/Member/MemberSkill 模型、Workspace/Crew 快照、独立成员 Agent/Pi session、`delegate_task`、`task_result`、`crew_wait`、`crew_propose`、SSE、取消、恢复、预算、审批和画布提交接口。前端已经具备 `agent-crew.ts` API 定义、`CrewSettings`、`CrewRunCard` 和 Crew 事件解析器，但这些组件没有接入画布 Agent 设置或发送流程。

当前缺口集中在四处：

1. `canvas-cloud-agent-settings.tsx` 没有 Crew 配置分区，用户无法查看或编辑当前画布的 Crew。
2. `canvas-cloud-agent-panel.tsx` 提交消息仍固定调用 `createAgentRun`/`sendAgentMessage`，不会调用 `createCrewRun`。
3. Crew Run 卡片没有被挂载到画布面板，也没有真实的 SSE 生命周期、审批和提交回调。
4. 普通 Agent 的能力声明可能包含 Crew 专属工具名，而工具注册实际要求 Crew runtime；能力列表、UI 提示和真实运行需要统一。

## 产品语义

- Crew 配置按 `canvasId` 隔离；“各画布的 Crew 配置独立管理”必须在画布 Agent 设置中成为可见、可操作的入口。
- 一个启用的 Crew 必须恰好有一个启用 Coordinator；成员使用独立 conversation/Pi session。
- Crew 成员只能通过结构化任务和结果通信。成员不能直接写画布；Coordinator 汇总后进入一次用户审批，再通过单一画布提交边界写入。
- Crew 配置、Workspace snapshot、成员技能和运行状态只影响新 Run；已经创建的 Run 使用冻结快照。
- 全局开关关闭时，Crew 配置和 Crew Run 路由都不可用；普通 Agent 继续可用且不应看到 Crew 专属工具。
- 版本冲突、权限错误、预算超限、画布 snapshot 冲突必须显示机器可读错误原因，不能静默降级为普通 Agent。

## 方案与取舍

采用垂直切片，而不是一次性重做完整后台。第一片先统一运行时能力和状态推进，第二片把最小配置闭环接入画布，第三片接入 Crew Run 和 SSE/审批，第四片完成跨层验收。这样每片都有独立可运行的结果，前端不会在后端生命周期尚未稳定时绑定错误事件。

不在本期增加自由成员聊天、多人同时直接写画布、独立 Workspace 身份或新的模型供应商协议。继续复用当前 CanvasProject/Workspace、Cloud Agent run、Pi session、CanvasMutation、审批、revision/CAS 和 SSE 基础设施。

## 架构与数据流

```text
Canvas Agent settings
  -> listCrews(canvasId)
  -> create/update/delete Crew + member/skill mutations
  -> revision conflict recovery

Canvas Agent submit
  -> mode=agent: createAgentRun/sendAgentMessage
  -> mode=crew: createCrewRun
  -> CrewRunCard subscribes crew SSE
  -> member events / waiting approval
  -> approve or reject
  -> commit with expected snapshot hash + idempotency key
```

运行时能力必须由请求上下文决定：普通请求只返回普通工具；Coordinator 请求返回 `delegate_task`、`crew_wait`、`crew_propose`；Member 请求只返回 `task_result`。能力查询和实际注册必须共享同一判定函数，避免“列表显示可用但运行时报未授权”。

## 切片设计

### Slice 1：运行时闭环和能力口径

- 抽取或修正 Crew runtime 的工具能力判定，使 `CloudAgentSupportedToolNames`、工具 schema 和真实注册保持一致。
- 明确 Coordinator 创建后为 queued/running，成员收到 `delegate_task` 后从 waiting/queued 进入执行，成员完成后写入结构化结果；重复相同参数幂等，改变参数返回 conflict。
- 覆盖 Coordinator、Member、普通 Agent 三种工具集合；覆盖成员失败、Coordinator 等待、Crew 终止后的拒绝和恢复租约。
- 交付：后端服务级测试与必要的 runtime 小改动；不改公开 API 形状。

### Slice 2：画布 Crew 配置

- 在 `canvas-cloud-agent-settings.tsx` 增加 Crew 设置入口和独立视图，文案明确“各画布的 Crew 配置独立管理”“只影响新 Run”。
- 通过 `listCrews(canvasId)` 加载；支持创建、更新、删除；成员增删改；Coordinator 选择；权限、模型、预算、焦点节点和成员技能编辑。
- 保存使用服务端返回的 `revision`；收到 `agent_crew_revision_conflict` 时重新加载并保留用户未提交表单，避免静默覆盖。
- 全局开关关闭或 API 返回 feature-disabled 时隐藏入口并清理旧状态。
- 交付：前端组件接入、可访问标签/空态/错误态、组件和 API mock 测试。

### Slice 3：Crew Run、SSE、审批和提交

- 在 `canvas-cloud-agent-panel.tsx` 增加 Agent/Crew 模式选择；只有存在启用 Crew 且全局开关开启时显示 Crew 模式。
- 新建消息在 Crew 模式调用 `createCrewRun`；继续普通 Agent 会话时保持原有 `sendAgentMessage` 行为。
- 挂载 `CrewRunCard`，用 `createCrewStreamParser` 处理 snapshot、业务事件、序号去重、断线重连和终态；显示成员状态、任务摘要、错误和审批预览。
- 审批按钮调用 approve/reject；批准后使用返回的 snapshot hash 和新的幂等键调用 commit；处理 snapshot conflict、重复提交和取消。
- Crew Run 与普通 Agent 的消息、运行中锁、取消和切换画布互不串状态。
- 交付：端到端前端集成测试，至少覆盖创建、SSE、审批批准/拒绝、提交冲突、取消和重连。

### Slice 4：跨层验证与 Compose 3000 验收

- 后端：Crew handler/service/repository 测试、跨账号隔离、feature gate、幂等、恢复、工具 schema、预算聚合和敏感字段脱敏。
- 前端：聚焦测试、typecheck、build；记录 canary 基线已有的 11 个失败，不把继承失败归因于 Crew。
- 浏览器：管理员 Agent(beta) 开关、画布 Crew 设置入口、成员编辑、Crew 模式发起、成员事件、审批和画布提交。
- Compose：使用独立本地项目和持久化目录，在 localhost:3000 构建并检查镜像、`/api/health`、schema、登录后的真实 Crew 流程；不直接改服务器生产容器。
- 交付：验收记录、已知限制和 PR 前验证清单。

## 错误和安全边界

- 所有 Crew 路由继续经过 `FeatureAgentCrew`；服务层再按 user、canvas、workspace、crew、member 归属校验。
- SSE 只投影公开业务事件，不暴露 Pi transcript、API key、Cookie、签名 URL 或内部 wire。
- 断线恢复从最后 sequence 读取；旧事件不可重复改变 UI 状态。
- 未审批提案不触碰 CanvasProject；提交必须校验 expected snapshot hash、权限、状态和 idempotency key。
- 成员失败只标记成员和 Crew 状态，Coordinator 可重试或降级；不自动把失败文本写入画布。
- 全局开关关闭时，已有已完成数据可读性按后端既有策略处理，但新建/修改/运行必须拒绝并在 UI 显示明确原因。

## 验收标准

1. 关闭全局开关：画布不显示 Crew 操作，普通 Agent 工具列表不含 Crew 工具。
2. 开启全局开关：画布设置显示 Crew 配置，能创建一个 Coordinator + 两个成员并保存 revision。
3. 修改成员或技能时发生 CAS 冲突：提示冲突并重新读取，不覆盖另一端修改。
4. 从 Crew 模式发起 Run：Coordinator 与每个成员拥有不同 Agent/Pi session，成员只能看到 `task_result`，Coordinator 只能通过结构化工具委派和汇总。
5. 成员完成/失败、SSE 断线重连、取消都能恢复到一致状态。
6. `crew_propose` 后必须先审批；拒绝不写画布，批准且 hash 未变化才允许提交。
7. 普通 Agent 永远不因全局开关开启而获得 Crew 工具。
8. 本地 Compose 3000 的健康检查、前端构建和真实浏览器路径全部通过，或将阻塞项明确记录为未完成。

## 设计后的计划边界

设计确认后，实施计划按 Slice 1→4 顺序拆成小任务。每个任务包含精确文件、接口、失败测试、通过命令和提交点；不把 PostgreSQL 实库联调、服务器部署或 PR 合并伪装成本地单元测试结果。
