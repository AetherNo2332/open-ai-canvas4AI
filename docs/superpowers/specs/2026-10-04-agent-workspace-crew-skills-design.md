# Agent Workspace、剧组与分层技能默认配置设计

## 状态

- 日期：2026-10-04
- 目标分支：`canary`
- 依据版本：`3b2901b7`
- 状态：设计稿，尚未进入实现
- 修订：2026-10-04 追加两项 UI/信息架构需求（见文末"补充需求"节）：
  1. 画布 Agent 面板输入区上方增加"活动子智能体列表"（圆形 SVG 头像 + 悬停展开名称的位移微交互）。
  2. 管理员"Agent 默认技能"配置收纳进 `/admin/settings/agent`（Agent（beta）页），并移除该页"运行状态"分区。

## 背景与目标

当前 Agent 运行以 `CanvasProject` / `canvasId` 为主要上下文，已经具备 Pi v3 持久化会话、运行树、技能版本快照、native skill runtime、工具批次幂等、审批、画布快照和运行观测能力。当前技能选择由用户在会话中手动开启，前端把本轮启用数量限制为 8 个；场景 preset 也把技能数量限制为 1–8 个。后端请求校验本身不应继续依赖固定的 8 个数量上限。

本设计同时引入三个产品能力：

1. 一个画布项目对应一个 Agent Workspace。
2. Workspace 内可以配置 Crew（剧组），由 Coordinator 和多个角色 Agent 协作。
3. 管理员可以配置全局默认技能；用户、Workspace 和 Crew 可以在全局默认基础上继续追加技能，取消固定的 8 个技能数量上限。

目标是让多个 Agent 共享同一份项目规则和画布上下文，同时保持会话隔离、权限可审计、技能内容可冻结、画布写入可审批和可回滚。

## 非目标

- 第一版不允许多个子 Agent 直接并行写同一张画布。
- 第一版不把仓库协作使用的根 `AGENTS.md` 当成用户 Workspace 的业务提示。
- 第一版不允许用户私有技能进入管理员全局默认配置。
- 第一版不实现任意 Agent 之间的自由聊天网络；成员通信使用结构化任务和结果消息。
- 取消的是“最多 8 个技能”的固定数量限制，不取消单文件、技能包、总文本、上下文窗口、积分和生成任务预算限制。

## 现状与约束

### Agent 会话

`CloudAgentPiSession` 是一个 conversation 对应的一棵 Pi v3 entry 树，使用 `ActiveRunID`、revision、租约和 fencing 保证同一会话不并发写入。不同 conversation 可以并行。`CloudAgentExecution.ParentID` 表示运行链路关系，不能单独承担 Crew 成员配置。

因此每个 Crew 成员必须使用独立的 `conversationId` / `piSessionId`。Coordinator 与成员共享 Workspace snapshot，但不共享同一个 Pi Session。

### 提示词与技能

Node runtime 已负责装配 Harness 文件和 native skill；Go 在创建运行时解析技能、冻结版本和内容 hash，并通过内部 Pi wire 传递快照。运行期间不能重新读取当前技能版本或每步重新读取 Workspace 文件。

### 画布写入

已有画布 revision、snapshot hash、Cloud Agent mutation、审批和 undo 合同。多 Agent 并行提案必须先由 Coordinator 汇总，再由用户审批，最后通过单一写入边界提交。

### 资源与额度

最新 canary 已修复存储准入与 Agent 审批锁的自锁问题，但 Crew 会增加并发运行、媒体生成、视觉识别和技能上下文带来的资源压力。Crew 需要独立的并发和预算聚合检查。

## 核心概念

### Workspace

Workspace 是 `CanvasProject` 的一对一 Agent 业务扩展，不新增第二个画布身份。它保存项目级 Agent 规则、技能追加项、Crew 配置入口和运行上下文版本。

Workspace 的 `AGENTS.md` 是数据库中的用户业务文档。创建运行时，Go 根据用户权限生成不可变 snapshot，Node 将 snapshot 追加到 system prompt。Workspace 文档不能覆盖服务端策略、工具披露、权限模式、额度或 SSRF 安全边界。

Workspace 文档修改只影响新建 Run；运行中和已创建的 Run 继续使用其冻结 snapshot。

### Crew

Crew 是 Workspace 内一组角色 Agent 的配置，不是一个共享 Pi 会话。Crew 包含一个 Coordinator 成员和零个或多个普通成员。成员可以绑定模型配置、权限模式、焦点节点、技能和预算。

### Crew Run

Crew Run 是一次 Coordinator 驱动的协作执行。它有一个父 Coordinator Run，并为每个成员创建独立 Member Run。成员通过结构化消息领取任务、返回结果或报告失败；Coordinator 决定是否重试、合并或请求用户审批。

## 技能分层与合并规则

最终技能集合由以下四层并集组成：

```text
GlobalDefaults
  + WorkspaceSkills
  + CrewMemberSkills
  + UserConversationSkills
```

所有层按 `skillId` 去重。版本冲突的优先级为：

```text
UserConversation > CrewMember > Workspace > GlobalDefault
```

优先级只解决同一技能的版本选择，不允许用户移除全局默认技能。全局默认技能始终追加到新会话；用户显式选择只能继续增加技能。

已创建的 conversation 保存最终技能来源和版本快照。管理员修改全局默认、Workspace 修改技能或成员配置变化，都不改变已有 conversation 和已运行任务。

技能集合还必须经过容量准入：

- 不设置固定技能数量上限；
- 保留单文件和技能包现有大小限制；
- 限制单次运行的总文件数；
- 限制单次运行的总文本字节数；
- 按当前模型的可用输入预算检查编译后的 system/context 大小；
- 继续执行积分、生成任务、视频秒数和 Agent step 预算；
- 任何超限都返回机器可读错误，不静默截断技能内容。

## 数据模型

### Workspace

建议新增：

```text
agent_workspaces
- id
- user_id
- canvas_id              unique
- agents_md              text
- agents_md_hash         sha256
- revision
- created_at
- updated_at
```

### Workspace 技能

```text
agent_workspace_skills
- workspace_id
- skill_id
- skill_version_id
- position
- enabled
- created_at
- updated_at
```

唯一键为 `(workspace_id, skill_id)`。保存时必须校验技能版本属于技能、技能可用且归属 Workspace 用户。

### 全局默认技能

```text
agent_skill_defaults
- id
- scope                 global
- skill_id
- skill_version_id
- position
- enabled
- revision
- updated_by
- created_at
- updated_at
```

全局默认只允许管理员管理，且不能引用用户私有技能。保存通过 revision CAS 防止两个管理员页面互相覆盖。

### Crew

```text
agent_crews
- id
- user_id
- workspace_id
- name
- description
- status
- coordinator_member_id
- revision
- created_at
- updated_at

agent_crew_members
- id
- crew_id
- role
- name
- model_config_json
- permission_mode
- focus_node_ids_json
- budget_json
- enabled
- position
- created_at
- updated_at

agent_crew_member_skills
- member_id
- skill_id
- skill_version_id
- position
- enabled
```

一个 Crew 必须有且仅有一个启用的 Coordinator。普通成员可以按角色和权限配置不同的技能集合。

### Conversation 技能

优先新增独立表，保留来源和版本：

```text
agent_conversation_skills
- conversation_id
- skill_id
- skill_version_id
- source                 global | workspace | crew_member | user
- position
- created_at
```

如果当前 Pi conversation 表尚未独立存在，可先将该集合放入 conversation/session header，但必须在后续迁移中保持来源和版本可审计。

## Agent Run 数据流

创建普通 Agent Run：

```text
鉴权用户与 CanvasProject 校验
  -> 读取全局默认技能
  -> 读取 Workspace 技能
  -> 读取 conversation 追加技能
  -> 按 skillId 去重并选择版本
  -> 容量和模型上下文准入
  -> 生成 SkillSnapshot 与 WorkspaceSnapshot
  -> 写入 run state / Pi wire
  -> 创建 Pi Session / Run
```

创建 Crew Run：

```text
读取 Workspace snapshot
  -> 创建 Coordinator Run
  -> 为成员计算 Global + Workspace + Member + Conversation 技能
  -> 为每个成员创建独立 Pi Session
  -> Coordinator 发布结构化 delegate_task
  -> 成员并行执行 read_only 或 propose 任务
  -> 成员返回 task_result
  -> Coordinator 汇总结果
  -> 用户审批
  -> Coordinator 单一写入边界提交画布变更
```

成员不得接收其他成员的完整 Pi transcript，只接收 Workspace snapshot、授权的画布/资源上下文和任务输入引用。需要协作时通过 `agent_crew_messages` 传递结构化摘要和 artifact 引用。

## 权限和写入边界

成员权限分三类：

- `read_only`：读取授权的 Workspace、画布和资源。
- `propose`：返回结构化变更提案，不直接写画布。
- `write`：仅在明确授权的单一提交步骤中写入。

第一版默认 Coordinator 为 `propose`，成员为 `read_only` 或 `propose`。Coordinator 只有在用户审批后才能调用画布写入工具。

提交前必须校验：

- 当前 CanvasProject 归属和 revision；
- proposed mutation 的 snapshot hash；
- 工具披露和成员权限；
- 当前 Crew Run 与成员 Run 状态；
- 预算和资源准入；
- operation id 与参数 hash 幂等性。

冲突返回机器可读 `creation_conflict`，由 Coordinator 重新读取上下文并生成新提案；不能静默覆盖用户或其他 Agent 的画布修改。

## API

### 管理员默认技能

```text
GET /api/admin/agent/skill-defaults
PUT /api/admin/agent/skill-defaults
```

`PUT` 接受 `revision` 和有序技能版本列表。返回当前 revision、技能状态、版本、总文件数、总文本大小和预计上下文占用。

### Workspace

```text
GET   /api/agent/workspaces/:canvasId
PATCH /api/agent/workspaces/:canvasId
GET   /api/agent/workspaces/:canvasId/skills
PUT   /api/agent/workspaces/:canvasId/skills
```

Workspace 更新必须带 revision。`PATCH` 修改 `AGENTS.md` 时重新计算 hash；技能更新与文档更新均只影响后续 Run。

### Crew

```text
GET    /api/agent/workspaces/:canvasId/crews
POST   /api/agent/workspaces/:canvasId/crews
PATCH  /api/agent/crews/:crewId
DELETE /api/agent/crews/:crewId
POST   /api/agent/crews/:crewId/members
PATCH  /api/agent/crew-members/:memberId
DELETE /api/agent/crew-members/:memberId
PUT    /api/agent/crew-members/:memberId/skills
```

### Crew Run

```text
POST /api/agent/crews/:crewId/runs
GET  /api/agent/crew-runs/:runId
GET  /api/agent/crew-runs/:runId/events
POST /api/agent/crew-runs/:runId/cancel
POST /api/agent/crew-runs/:runId/approvals/:approvalId/decision
```

公开 SSE 继续以 Go 投影的业务事件为准，不向 Web 暴露 Pi entry 或 Node 内部 wire。新增事件包括：

```text
crew_run_created
member_run_started
member_message
member_run_waiting
member_run_completed
member_run_failed
crew_approval_required
crew_run_completed
```

## 前端

管理员后台新增“Agent 默认技能”页面：

- 搜索和筛选可用技能；
- 选择和排序；
- 显示技能版本、文件数、文本大小和上下文预估；
- 使用 revision 保存；
- 显示无效技能和超限原因。

用户侧技能选择器改为显示：

```text
全局默认 N 个 · Workspace M 个 · 本会话追加 K 个
```

不显示 `/ 8`，也不因数量达到 8 禁用按钮。超过容量时显示后端返回的具体预算错误。

Workspace 页面包含：

- Agent.md 编辑器；
- Workspace 技能追加配置；
- 当前有效技能来源和版本；
- 生效范围说明：只影响新 Run。

Crew 页面包含：

- 剧组名称和描述；
- Coordinator 选择；
- 成员角色、模型、权限和预算；
- 成员技能追加；
- 成员焦点节点；
- 运行时状态树和消息摘要。

## 迁移与兼容

1. 新增 Workspace 时，为已有 `CanvasProject` 懒创建或在迁移中补齐一对一记录。
2. 已有 Agent conversation 的 `skillIds` 视为 `user` 来源，不自动回填管理员默认技能，避免历史对话行为改变。
3. 新建 conversation 时才读取管理员默认技能。
4. 现有场景 preset 可以继续作为用户追加技能集合，但移除 8 个数量校验；preset 内容仍需经过容量准入。
5. 现有 `CloudAgentPiSession`、`CloudAgentExecution` 和 SSE 合同保持可用。
6. 新增数据库 migration、OpenAPI 文档、后台权限测试和跨账号隔离测试。

## 失败处理与安全边界

- 默认技能不存在、禁用或版本失效：新 Run 创建失败并返回具体技能 ID；不静默丢弃。
- 技能内容在 Run 创建后更新：当前 Run 使用旧快照；新 Run 使用新版本。
- 全局默认配置 revision 冲突：返回 creation conflict，要求管理员重新读取。
- Crew 成员失败：成员 Run 进入 failed；Coordinator 可重试或降级，不自动把错误结果写入画布。
- 成员超时或租约失效：释放成员执行槽，保留 Crew Run 状态，支持安全恢复。
- 用户越权访问其他 Workspace、Crew、Member Run、Pi Session 或 SSE：按 user_id、canvas_id 和资源归属拒绝。
- 技能内容不能覆盖服务端策略、工具权限、额度、模型准入或出站安全规则。
- 日志和事件不得写入 API Key、Cookie、签名 URL 或技能中的敏感凭证。

## 分阶段实施

### P0：技能默认配置

- 新增全局默认技能模型、migration、repository 和管理员 API。
- 后台页面支持配置有序技能版本。
- 新建 conversation 注入全局默认技能。
- 添加版本冻结和跨账号权限测试。

### P1：取消数量上限与容量准入

- 删除前端 `/ 8` 和禁用逻辑。
- 删除 preset 的 1–8 校验。
- 添加总文件数、总文本字节和上下文预算校验。
- 更新技能选择器和 API 错误展示。

### P2：Workspace

- 新增 Workspace 及 Workspace 技能追加。
- 接入 `AGENTS.md` snapshot。
- 新增 Workspace 页面和 revision CAS。

### P3：Crew 配置与只读并行

- 新增 Crew、Member、MemberSkill。
- 每个成员创建独立 Pi Session。
- Coordinator 结构化委派。
- 成员只读或提案执行。

### P4：审批、汇总与写入

- Crew 消息和结果持久化。
- Coordinator 汇总。
- 用户统一审批。
- 复用 CanvasMutation、snapshot hash、undo 和幂等写入。

### P5：恢复、观测和灰度

- Crew 级取消、超时、预算聚合和恢复。
- 增加 crew、member、parentRunId、taskId 观测维度。
- 先开放固定技能角色，再评估自定义 Agent 角色。

## 验收标准

### 技能

- 管理员配置 20 个默认技能，新建 conversation 能完整继承。
- 用户继续追加技能，最终集合是并集。
- 同一技能多层指定不同版本时按优先级选择。
- 全局默认技能不能引用私有用户技能。
- 现有对话不因管理员修改而改变。
- 技能超过 8 个但容量足够时可以运行。
- 数量不限但上下文或文件预算超限时明确拒绝。
- 技能更新后，运行中的 snapshot、hash 和 prompt cache identity 不漂移。

### Workspace 与 Crew

- 一个 CanvasProject 只能访问自己的 Workspace。
- Workspace Agent.md 在运行中保持冻结。
- 两个 Crew 成员并行时使用不同 Pi Session，互不污染 entry tree。
- 子 Agent 只能访问被授权的画布、资源和技能。
- 成员失败不破坏其他成员和 Coordinator。
- 画布提案冲突不会静默覆盖用户修改。
- 未审批的成员提案不会改变 CanvasProject。
- 账号切换后 Workspace、Crew、SkillSnapshot、Run 和 SSE 缓存不串号。

### 验证命令

- 前端：`cd web && bun test`，相关页面再执行 `bun run build`。
- 后端：`cd backend && go test ./...`，重点覆盖 agent、skills、handler、repository 和 database。
- Agent runtime：`cd agent && npm test` 与 `npx tsc --noEmit`。
- 真实验收：至少验证新建 conversation、旧 conversation、20 个技能、容量超限、两个成员并行、成员失败恢复和审批写入。

## 设计决策总结

采用 `CanvasProject = Workspace`，而不是新增独立 Workspace 身份；采用“全局默认 + Workspace + Crew 成员 + 用户追加”的技能并集；采用每个成员独立 Pi Session；采用容量/上下文预算替代固定技能数量限制；采用 Coordinator 汇总后统一审批写画布。这样可以复用当前 canary 的 Pi 持久化、native skills、工具幂等、审批、画布 revision 和观测基础，同时避免共享 session、无限上下文和并发写画布带来的数据风险。

## 补充需求（2026-10-04 追加）

### 需求 A：画布 Agent 面板的活动子智能体列表

位置：画布 Agent 面板中，消息列表底部与输入框顶部之间的横向空条区域（现该处只有输入框的提示词高度拖拽手柄）。

- 活动子智能体列表横向排开在该区域。
- 每个子智能体显示为圆形 SVG 头像；头像资产可联网获取开源 SVG（如 GitHub avatar 资产、开源图标集）。
- 鼠标悬浮在头像上时，在头像右侧展开该子智能体的名称；位于右侧的头像向右平移为名称腾出空间；鼠标移走后名称收起、被推移的头像复位。
- 展开与复位均为**非线性动画**（缓出曲线，非 linear / 非 step）。
- 微交互遵循 `docs/plans/ui-design-system.mdx`（AGENTS.md 所称 ui-design-system）：只消费三层 token；动效使用既有 `--motion-dur-*` / `--motion-ease-*` token；尊重 `prefers-reduced-motion`（reduce 时直接显隐，不做位移）；键盘可达（`:focus-visible` 触发与 hover 同等的展开）。

第一版数据来源：当前 canary 尚无子智能体运行概念（P3 Crew 才引入 Member Run）。该区域第一版实现为**数据驱动、空态不渲染**的组件：当不存在活动子智能体时不占任何空间，面板外观与现状一致；子智能体数据源接口在 P3 Crew Run 落地时接入，先按最小合同占位。

### 需求 B：技能管理收纳进管理员 Agent（beta）设置页

- 本设计 P0 的"管理员默认技能"配置页不新增独立一级导航，作为分区收纳进 `/admin/settings/agent`（Agent（beta）页），导航描述与页内锚点同步调整。
- 风格与编排遵循 AGENTS.md 及 ui-design-system 的开发阅读约定：沿用该页现有锚点分区模式（原生锚点 + section）、AdminPageFrame 外壳、既有 token，不复制独立 Modal styles，不新增全局 `.ant-*` 覆盖。
- **移除 Agent（beta）页的"运行状态"分区**（含执行器状态指标卡、执行器表格与 5 秒轮询）；调度配置与记忆管理分区保留。后端 `/admin/settings/agent-scheduler/status` 接口本身保留（调度能力仍在使用），仅前端不再消费该接口的页面分区。
