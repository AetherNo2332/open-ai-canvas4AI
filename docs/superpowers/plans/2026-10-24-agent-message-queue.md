# Agent Message Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为当前 Agent 会话增加服务端持久队列和账号默认插队/排队选择。

**Architecture:** 独立消息收条及队列控制表负责待发意图，复用 CloudAgentAdmission 原子创建运行与预留额度。派发准备在事务外进行，提交事务重验消息租约、会话最新轮和问题/暂停状态。React 只展示服务端状态并触发明确操作。

**Tech Stack:** Go/GORM SQLite/PostgreSQL，现有 worker 生命周期及 runtime policy，React 19/TypeScript/Bun。

**Spec:** `docs/superpowers/specs/2026-10-24-agent-message-queue-design.md`（用户已确认）。

## Global Constraints

- 初始默认 interject；账号保存 interject|queue，跨画布与设备使用；在途提交模式不可变。
- 默认每会话 20 条、每账号 100 条，接入后台热更新限额；入队不预留费用，实际派发复用准入。
- 队列在服务端；等待问题、审批、失败、停止及清理状态均受控。未回答 ask_user 即使 completed 也不能推进。
- 保留草稿、原请求幂等身份、权限与画布归属。队列项提升失败不丢失，停止竞态不漏运行。
- 不动 Agent 记忆/压缩、待办和工具披露；不新增依赖，不运行全量测试或构建，不操作生产，不推送远端。
- UI 只保留“插队/排队”“待发 N”“编辑/插队/移除”“已暂停/继续”和必要短状态；沿用皮肤和圆角。

## Review Focus

1. ask_user completed：必须关联 questionId 后才允许回答续轮，队列里的确认文字不能代答。
2. 用户停止与派发提交竞态：共享账号事务边界，已派发的新轮也属于停止目标。
3. 原请求响应丢失且消息被编辑/移除：重试仍返回同一收条，不能复活或重复计费。
4. 切换账号/会话及多设备同时提交：回执作用域隔离、预期最新轮与 revision 冲突不静默重定向。
5. 旧续聊/插话入口与新队列竞争：共用锁顺序、问题和派发状态约束，不能绕过队列边界。

---

### Task 1: Durable message records and atomic repository boundary

**Files:** Create `backend/internal/model/agent_message_queue.go`, `backend/internal/repository/agent_message_queue.go`, `backend/internal/repository/agent_message_queue_test.go`; Modify `repository/repository.go`, `repository/finance.go`, `repository/cloud_agent.go`, `database/migrations.go`.

**Interfaces:** `WithAgentConversation(userID, conversationID string, fn func(*Repository,*model.AgentConversationQueue,*model.CloudAgentExecution) error) error`; queue CRUD and claim/commit methods return durable revisions and exact receipts; `CloudAgentAdmission` remains execution/task/order/skills.

- [x] Write and run repository tests for FIFO/ownership/revision, idempotent enqueue after cancellation, and atomic admission rollback (expected RED: missing queue types/methods).
- [x] Implement migration 65 for message/control/UI setting tables; serialize on existing account lock, retain request receipts, enforce item/user limits and storage growth.
- [x] Add leased dispatch preparation and atomic admission commit; normal holding admission obeys the same queue guard; journal save tracks questions and abnormal terminal pauses.
- [x] Run `go test ./internal/repository -run TestAgentMessageQueue -count=1` (expected PASS); commit focused repository change.

### Task 2: Queue application, lifecycle, settings and HTTP

**Files:** Create `backend/internal/app/agent_message_queue.go`, `agent_message_dispatch.go`, `agent_message_queue_test.go`, `backend/internal/handler/agent_message_queue.go`, `agent_message_queue_test.go`, `backend/internal/service/aliases_agent_message_queue.go`; Modify service worker start, cancel/interjection/admission and conversation directory, runtime policy and admin consumers, OpenAPI source.

**Interfaces:** `SendAgentConversationMessage(userID,conversationID string, input AgentConversationMessageRequest) (*AgentMessageReceipt,error)`; `AgentConversationMessageRequest={Mode,ExpectedRunID,ReplyToQuestionID,Request CloudAgentRequest}`. `AgentMessageQueue`, `UpdateAgentQueuedMessage`, `CancelAgentQueuedMessage`, `UpdateAgentMessageQueue`, `AgentUISettings`, `UpdateAgentUISettings`, `DispatchAgentMessages` expose ownership-checked boundaries. API routes exactly follow spec.

- [x] Write application/HTTP RED tests: enqueue no reservation, FIFO single dispatch, ask_user blocker, explicit answer, stop/pause/resume, promotion/claim race, same-key replay and stale/foreign scope, settings CAS.
- [x] Implement template-free domain coordination; prepare existing admissions outside transaction then commit only if claim and latest parent still valid; recover via bounded worker scan.
- [x] Wire old endpoints and cancel into same boundary; add queue directory metadata, strict bounded JSON, rate limiting, service aliases/OpenAPI; add runtime task limits `agentMaxQueuedMessagesPerConversation=20`, `agentMaxQueuedMessagesPerUser=100`.
- [x] Run targeted Go application/handler/policy tests and repository tests (expected PASS); commit service/API change.

### Task 3: Agent panel controls and request recovery

**Files:** Create `web/src/components/canvas/canvas-agent-message-queue.tsx`; Modify `services/api/agent.ts`, `services/cloud-agent-conversations.ts`, panel/chat UI/CSS, canvas page external dispatch, admin runtime-policy types/controls; Tests in `web/test/fixtures/cloud-agent-panel-sync.harness.tsx` and focused queue component/API tests.

**Interfaces:** `AgentSendMode='interject'|'queue'`; API message request carries frozen request/mode/expectedRunId/questionId. Panel handle returns explicit `sent|queued` receipt to node caller, preserving original draft. UI receives queue state and handlers without dispatch policy.

- [x] Add RED tests for account default selection, button/Enter/external node consistency, queue edit/promotion/remove, question answer bypass, unknown POST recovery with frozen mode, old-account response isolation.
- [x] Implement API wrappers and scoped pending record; fetch queue and settings from server, refresh authoritative snapshot and latest run; stop using front-end running status to choose interjection/new-turn route for existing conversations.
- [x] Render compact mode menu and bounded queue list, preserve draft and skin, show precise sent/queued results, no explanatory product/implementation text.
- [x] Run focused Bun tests and `node node_modules/typescript/bin/tsc --noEmit` (expected PASS); commit front-end change.

### Task 4: Regression, documentation and final review

**Files:** Update topical features/API/database/code-map/pending-test docs, this plan and VERSION/CHANGELOG after preview; create focused regression tests where missing.

**Interfaces:** Full branch behavior conforms to approved spec; no backend queue rule depends on an open browser.

- [ ] Run minimum sufficient real implementation tests for new queue and affected admission/history/UI recovery; include SQLite and available isolated PostgreSQL concurrent proof, no production/container changes.
- [ ] Perform fresh independent whole-branch review on an explicitly non-Astra model; reproduce and fix material findings, record evidence and any deviations below.
- [ ] Sync documentation, preview changelog then update existing v1.6.0 internal version; verify diff and changelog checks, commit. Keep remote unchanged and report delivery/remaining environment checks accurately.

## Execution ledger

- Setup: branch `codex/agent-message-queue`, reused isolated linked worktree; base implementation `bc593e8d`, approved design commit `f785d977`.
- Ruling: 用户“是的”已批准完整设计与开始实施，延续当前会话自行实施的方式；不再为相同范围请求第二次批准。
- Ruling: 用户仅允许有限测试，覆盖本次风险的定向回归替代 skill 默认全量测试，避免扩大验证范围。
- Pre-flight: Task 1 提供持久记录/原子边界，Task 2 调用并提供消息收条/HTTP，Task 3 消费同一枚举和 DTO；Task 4 验证整条路径，接口名称在实施时据实际编译同步。
- Task 1: complete，提交 `7f6ee103`。观察缺类型/方法 RED 后实现，仓储顺序/归属/移除重放/租约/提问闸门及既有多连接准入定向通过；原子额度回滚用 Task 2 的真实应用夹具补证。
- Task 2: complete，主体提交 `1df90d53`。应用与 HTTP RED→GREEN，覆盖派发、无入队预留、显式回答、已准备运行被停止拒绝且不扣费、双实例争抢、移除收条、编辑提升、设置 CAS；HTTP 严格正文与 OpenAPI 路由检查通过。
- Task 3: in progress。冻结投递方式/请求恢复 7 项通过；默认排队/未知提交 RED→GREEN，旧面板 16 项通过；真实队列组件 2 项通过，类型检查通过一次，后续边界修改待最终复验。
- Ruling: 新表同时进入 `database.Models()`、migration 65 和 CurrentSchemaVersion；原子额度检查在真实应用夹具验证，避免夹具与正式表清单分叉。
- Ruling: 普通 Prepared 准入沿用现有 Workspace/技能读取；仅子代理要求调用方给冻结快照，不改 Agent 记忆/压缩算法。
- Ruling: 目录一次聚合计数，归属检查不解析运行正文，避免轮询搬运待发正文或历史损坏阻挡控制操作。
- Ruling: 外部节点提示词不自动回答问题；问题控件或会话输入才关联 questionId。发送回执只清空仍等于原正文的输入，未知重试保留后写草稿并提供“重试发送”；均观察 RED 后修复。
- Ruling: 同一问题最多一个待派发回答，第二设备不同回答明确拒绝，原请求重放仍命中原收条；新增测试 RED→GREEN。
- Ruling: 即时插话和唯一显式回答不占普通待发任务数量，避免队列满时无法更正/确认；仍校验存储和运行准入。后台限额测试走实际更新入口并断言生效，不能直接写 DB 绕过既有缓存失效。
- Ruling: 就绪候选在 SQL LIMIT 之前筛选，避免等待中的会话饿死就绪会话；限量 1 的用例 RED→GREEN。显式问题回答可越过普通待发项，并在崩溃后恢复。
- Environment: 没有 `CANVAS_TEST_POSTGRES_DSN`，未启动或改动数据库容器；SQLite 真实事务和双 Service 并发已验证，PostgreSQL 留作环境验收。
- Task 3: complete。实际面板 22 项、API/恢复 13 项、队列组件 2 项、分镜入口 4 项均通过；类型检查通过，正文期间新草稿、节点不代答、显式问题回答及冻结模式均有运行证据。
- Task 4: in progress。六个 Go 包定向验证中，队列/仓储/策略/HTTP/迁移/架构通过；发现旧预算测试沿用已移除的待办阻塞，在未修改的 main 上单独复现同一失败。
- Ruling: `TestAgentCallLimitsNoToolTurnUsesRaisedBudget` 改用待送用户插话保持继续，并断言热更新步数上限，RED→GREEN；仅同步测试夹具，不恢复待办催办或修改生产记忆算法。
