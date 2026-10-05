# Agent 动态子代理实施计划

> **For agentic workers:** 使用 executing-plans 按任务顺序执行；本轮只运行新增链路的定向测试，不重复既有全量套件。

**Goal:** 把固定 Crew 改为父 Agent 自主编排的动态子代理，并让用户只维护一个持续生效的画布开关。

**Architecture:** 先完成持久化策略和显式父子链接，再接入运行时工具和子运行创建，随后接入消息唤醒、SSE 和前端开关，最后删除旧 Crew 代码与表。

## 任务

### 1. 持久化策略与基础契约

- 新增 `AgentSubagentPolicy`、`AgentSubagentLink`、`AgentSubagentMessage` 模型和 repository CAS 方法。
- 注册新模型与 schema 迁移；开发环境允许清理旧 Crew 表。
- 新增 `/agent/subagent-policy` GET/PUT，校验用户和 canvas 归属。
- 定向测试：默认关闭、跨轮保持、关闭后生效、CAS 冲突、跨账号隔离。

### 2. 父运行动态编排

- 将运行时上下文从固定 Crew envelope 替换为动态 `SubagentRuntime`。
- 增加 `spawn_subagent`、`wait_subagents`、`message_subagent`、`subagent_status`。
- 创建独立 conversation/Pi session，记录 link 和幂等键；服务端执行深度、数量、并发和预算上限。
- 定向测试：工具能力、独立会话、幂等、限制和失败回报。

### 3. 子代理回报与恢复

- 增加 `send_parent_message`、`finish_subagent`，写入有序消息并唤醒父运行。
- 接入租约恢复、取消、重启后的状态重放和 SSE 事件。
- 定向测试：重复消息、断线重连、父子取消、恢复后不重复执行。

### 4. 前端画布入口

- 用单一“允许使用子代理”开关替换 Crew 配置/成员编辑界面。
- 发送请求携带冻结策略；显示父运行自动创建的角色、任务、进度和消息。
- 删除手工组、角色、成员配置组件。
- 运行现有前端定向组件/transport 测试，不重复全量 web suite。

### 5. 破坏式清理与收口

- 删除旧 Crew handler、repository、model、事件解析器和前端 API/组件。
- 清理 capability、路由、文案和 schema 中的旧名称。
- 运行新增 Go/Bun 定向测试、编译和静态引用检查；不重跑已通过的大型验收。

## Review focus

- 授权是持久画布策略，不是单轮 token。
- 角色是任务标签，不能绕过权限。
- 所有父子关系走显式 link，不把 `ParentID` 单独当作业务层级。
- 子代理不能直接写画布；父级聚合和审批仍是唯一提交边界。
