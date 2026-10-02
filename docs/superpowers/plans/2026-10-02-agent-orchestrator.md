# Agent Orchestrator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现画布公平领取、画布驻留配额及可热更新的统一调度设置，并将管理员“Agent 记忆”升级为“Agent（beta）”配置与管理页面。

**Architecture:** Go 持久化准入轮转与配置，事务更新租约和发件箱。Node 根据配置管理短执行槽和驻留准入，独立 AgentSession 生命周期保持不变。

**Tech Stack:** Go/GORM/PostgreSQL、SQLite 测试、Node/TypeScript、Pi SDK 0.87.1、React、Docker Compose。

**Spec:** [设计](../specs/2026-10-02-agent-orchestrator-design.md)

## Global Constraints

- 单进程默认 4 个调度槽、64 个驻留会话；建议画布驻留上限 16，明确限制单画布容量。
- 租约 45 秒、15 秒续租；保留 epoch fencing 和持久化任务幂等。
- 不修改 SDK 0.87.1；prompt() 不占调度槽。
- 仅隔离 3004；不推送远程，不切换生产端口；验收不调用收费模型。
- 文中新增文件和接口为拟议实现；先核对执行时分支及迁移版本。
- 管理员导航和页面标题准确使用“Agent（beta）”；用户个人“Agent 记忆”入口保持原名称和权限。
- 用户追加：保留已配置渠道可用性；界面 commit 后缀使用括号，版本核心保持可比较；改动写入 CHANGELOG.md。

## Review Focus

- A 的历史积压超过 20 条：B/C 可正常领取，失败事务不推进游标。（任务 2）
- 两个执行器同时读取容量：最终租约不超限、不重复领取。（任务 2）
- 活动数高于缩容后容量：继续续租并排空，心跳不能报协议错误。（任务 4）
- 全局配置事件没有 runId：所有执行器收到更新，丢通知可恢复。（任务 3/4）
- 旧配置缺字段、管理员覆盖写及回滚旧镜像：默认兼容、409 冲突、拒绝协议混跑。（任务 1/3/5/6）
- 旧记忆页面链接、权限及统计读取失败：链接仍可用，普通用户无管理员权限，失败不显示为零。（任务 3/5）

## Task 1: 调度设置及增量迁移

**Files:** 新增 backend/internal/platform/agent_scheduler_policy.go、backend/internal/model/agent_admission.go；修改 backend/internal/database/migrations.go、schema.go；新增对应测试。

**Interfaces:** AgentSchedulerSetting {DispatchConcurrency, MaxResidentSessions, MaxResidentPerCanvas int; Revision int64}；ValidateAgentSchedulerSetting(setting) error；准入计数器与画布状态模型由任务 2 使用。

- [ ] 写失败测试：默认 4/64/16；执行槽 0、17 被拒绝；resident<dispatch、canvas>resident 被拒绝；旧设置缺字段合并默认值。
- [ ] 运行相关测试，确认新类型/校验缺失导致失败。
- [ ] 实现设置结构、校验、新增准入表和索引；不删除既有数据，不在读取时重置管理员配置。
- [ ] 执行迁移历史保留测试与设置校验测试，验证重复迁移安全。
- [ ] 提交 feat: add agent scheduler policy and admission schema。

## Task 2: 公平领取与画布驻留配额

**Files:** 修改 backend/internal/repository/cloud_agent.go；新增 repository/agent_admission.go；修改 app/cloud_agent_pi_bridge.go、app/agent_scheduler.go；新增 database/agent_admission_test.go。

**Interfaces:** 保留 ClaimPiAgent(owner, until) 对调用者的兼容行为；内部调用 ClaimPiAgentFair(owner string, until time.Time, policy AgentSchedulerSetting) (*model.CloudAgentExecution, error)。画布键为用户与画布组合；无画布采用会话键。

- [ ] 写失败测试：A 100 条积压后 B/C 仍获准；画布内部 FIFO；持续就绪 A/B/C 轮转；失败事务不推进游标；同名跨用户画布隔离。
- [ ] 写 PostgreSQL 双执行器竞争测试：上限 16 时总有效租约不超过 16，同会话只一个 owner；过期租约恢复与旧 epoch 拒绝。
- [ ] 运行新测试确认现有全局最早 20 条算法失败。
- [ ] 实现设计中的事务轮转算法、配额计数、SQLite 有界重试；容量等待保存具体原因。核对锁顺序及事件回调。
- [ ] 运行相关 repository/app 测试及 PostgreSQL 集成测试，通过后提交 feat: fairly admit canvas agent sessions。

## Task 3: 配置管理 API 与持久化广播

**Files:** 修改 backend/internal/handler/auth.go、internal_agent.go、platform/agent_scheduler_policy.go、model/agent_scheduler.go、repository/agent_scheduler.go；新增配置 API 和事件事务测试。

**Interfaces:** 管理员 GET/PUT /api/admin/settings/agent-scheduler；PUT {expectedRevision, dispatchConcurrency, maxResidentSessions, maxResidentPerCanvas}。管理员 GET /api/admin/settings/agent-scheduler/status 返回执行器统计（instanceId、lastHeartbeatAt、online、resident、claimReservations、dispatchActive、readyQueued、draining、appliedConfigRevision）。内部 GET /internal-agent/config 返回 AgentSchedulerSetting；SSE kind=scheduler_config_changed 携带配置 revision。

- [ ] 写失败测试：未授权拒绝；有效保存 revision+1；并发旧 revision PUT 返回 409；事务回滚后无设置变更、审计或广播事件。
- [ ] 实现持久化设置、审计、发件箱同事务提交；内部鉴权；启动默认值优先级。
- [ ] 实现管理员只读状态接口和容量统计存储：普通用户拒绝，心跳超过 45 秒标为离线，不返回凭据或会话正文；与任务 4 的容量上报字段保持一致。
- [ ] 验证无 runId 的事件能够被 SSE 订阅端重放；两个执行器均收到；5 秒恢复读取可补漏。
- [ ] 运行 handler/platform/repository 相关测试，提交 feat: publish versioned agent scheduler config。

## Task 4: Node 热更新与独立驻留准入

**Files:** 修改 agent/src/event-scheduler.ts、server.ts、bridge.ts、test/event-scheduler.test.ts；新增 agent/src/runtime-config.ts、test/runtime-config.test.ts；修改 Go 容量报告校验及对应测试。

**Interfaces:** EventScheduler.setConcurrency(value: number): void；RuntimeConfigController.apply(setting: AgentSchedulerSetting): boolean，仅接收更高 revision。驻留控制器消费同一配置快照，容量上报输出 resident/claimReservations/dispatchActive/readyQueued/draining/appliedConfigRevision。

- [ ] 写失败测试：4→8 能启动新增步骤；8→2 等在途排空后才准入；64→16 不取消既有会话、不越限增加驻留；已预留领取返回时重新检查配置并安全释放不准入租约。
- [ ] 写失败测试：重复/旧配置不回退；断线/丢通知恢复；首次配置不可读不领取；运行中断线保持最后有效配置。
- [ ] 解耦启动时固定 claimer 数量与执行槽配置；实现配置广播路由、5 秒恢复读取，保持独立 prompt 和 15 秒续租。
- [ ] 修改容量协议以允许缩容期间 resident 高于新上限，并区分领取预留数；同步升级内部协议，拒绝旧执行器混跑。
- [ ] 运行 npm test；验证 Go 容量、协议、看门狗测试，提交 feat: hot reload agent scheduling limits。

## Task 5: Agent（beta）统一配置与管理界面

**Files:** 新增 web/src/pages/admin/settings/agent-settings-page.tsx、web/src/services/api/admin-agent-settings.ts；修改 web/src/pages/admin/components/admin-shell.tsx、admin-route-pages.tsx、web/src/router.tsx、web/src/pages/admin/settings/runtime-policy-settings-page.tsx；复用 web/src/pages/admin/components/agent-lessons-panel.tsx 和 services/api/admin-agent-lessons.ts。

**Interfaces:** 界面使用任务 3 设置及状态 API；新路由 /admin/settings/agent。旧 /admin/agent-lessons 保留查询参数并重定向到记忆管理区域。调度配置、运行状态、记忆管理三个区域共用“Agent（beta）”页面，记忆 API 与用户批准边界保持原样。

- [ ] 将管理员“Agent 记忆”导航改为“Agent（beta）”并移入“平台配置”，页面标题一致；注册新路由及旧路由兼容跳转，不修改用户个人记忆导航。
- [ ] 接入调度配置表单：分别标注调度槽、单执行器驻留、跨执行器画布配额；验证非法值、409 冲突、保存失败保留草稿与保存成功后尚待执行器应用。
- [ ] 接入只读运行状态：在线/离线、心跳、实际驻留、领取预留、槽占用、排队、缩容排空与配置版本；读取失败显示错误，恢复后可刷新，不显示虚假零值。
- [ ] 将现有 AgentLessonsPanel 置于记忆管理区域，保留筛选、删除确认、用户批准权限；“资源与策略”增加 Agent 页面跳转，保留 Go worker/渠道表单，不复制 Agent 参数编辑入口。
- [ ] 使用现有前端测试工具或浏览器验收验证新旧路由、导航文案、管理员/普通用户权限、配置保存与记忆筛选/删除回归；运行前端构建，提交 feat: unify agent beta admin settings。

## Task 6: 部署与联合验收

**Files:** 修改 docker-compose.event-canary.yml；扩展 scripts/verify-event-canary.py、docs/reports/event-canary-acceptance.md 和部署说明。

追加版本发布文件：VERSION、CHANGELOG.md、web/vite.config.ts、web/src/lib/app-version.ts、scripts/build-event-canary.sh。界面显示 `v1.5.7.2 (构建commit短号)`，后端版本比较保留核心版本；发行说明覆盖公平领取、热更新、Agent（beta）、迁移与协议变化。

**Interfaces:** 消费任务 1..5 的设置、统计、事件及页面；部署匹配内部协议的 Go/Node/Web。

- [ ] Compose 改为可覆盖初始值，说明数据库配置优先、worker 不等于 Agent 槽，记录“Agent（beta）”配置入口。
- [ ] 运行相关 Go 测试、PostgreSQL 集成、Agent 测试和前端构建；记录具体命令与结果。
- [ ] 备份 3004 数据与镜像，构建部署匹配协议的 Go/Node；保持生产端口不变。
- [ ] 使用可控模型服务验收：16 请求真实重叠；A 积压+B/C 准入；16 个 A 驻留时 B 仍推进；热缩容；双执行器竞争；配置 SSE 丢失恢复；审批与重启回归。
- [ ] 在 3004 登录管理员，验收“Agent（beta）”三个区域及旧记忆链接；保存设置后验证执行器应用版本与实际额度一致，确认既有记忆管理可用。
- [ ] 记录领取顺序、配置 revision、实际/预留驻留数、槽占用、等待原因、恢复时间和资源使用；验证回滚路径，提交交付记录。

## 执行顺序与停止条件

按 1→2→3→4→5→6 推进。主智能体实现和集成；沿用用户已授权的 Luna 独立测试审计方式，重点审计双执行器竞争、缩容、事件丢失和管理员权限。每个任务测试通过再提交；公平性、配额或 fencing 失败时停止发布并修正。计划本身不表示代码已实现或测试已通过。
