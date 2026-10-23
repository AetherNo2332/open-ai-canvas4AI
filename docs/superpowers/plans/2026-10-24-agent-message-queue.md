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

- [ ] Write and run repository tests for FIFO/ownership/revision, idempotent enqueue after cancellation, and atomic admission rollback (expected RED: missing queue types/methods).
- [ ] Implement migration 65 for message/control/UI setting tables; serialize on existing account lock, retain request receipts, enforce item/user limits and storage growth.
- [ ] Add leased dispatch preparation and atomic admission commit; normal holding admission obeys the same queue guard; journal save tracks questions and abnormal terminal pauses.
- [ ] Run `go test ./internal/repository -run TestAgentMessageQueue -count=1` (expected PASS); commit focused repository change.

### Task 2: Queue application, lifecycle, settings and HTTP

**Files:** Create `backend/internal/app/agent_message_queue.go`, `agent_message_dispatch.go`, `agent_message_queue_test.go`, `backend/internal/handler/agent_message_queue.go`, `agent_message_queue_test.go`, `backend/internal/service/aliases_agent_message_queue.go`; Modify service worker start, cancel/interjection/admission and conversation directory, runtime policy and admin consumers, OpenAPI source.

**Interfaces:** `SendAgentConversationMessage(userID,conversationID string, input AgentConversationMessageRequest) (*AgentMessageReceipt,error)`; `AgentConversationMessageRequest={Mode,ExpectedRunID,ReplyToQuestionID,Request CloudAgentRequest}`. `AgentMessageQueue`, `UpdateAgentQueuedMessage`, `CancelAgentQueuedMessage`, `UpdateAgentMessageQueue`, `AgentUISettings`, `UpdateAgentUISettings`, `DispatchAgentMessages` expose ownership-checked boundaries. API routes exactly follow spec.

- [ ] Write application/HTTP RED tests: enqueue no reservation, FIFO single dispatch, ask_user blocker, explicit answer, stop/pause/resume, promotion/claim race, same-key replay and stale/foreign scope, settings CAS.
- [ ] Implement template-free domain coordination; prepare existing admissions outside transaction then commit only if claim and latest parent still valid; recover via bounded worker scan.
- [ ] Wire old endpoints and cancel into same boundary; add queue directory metadata, strict bounded JSON, rate limiting, service aliases/OpenAPI; add runtime task limits `agentMaxQueuedMessagesPerConversation=20`, `agentMaxQueuedMessagesPerUser=100`.
- [ ] Run targeted Go application/handler/policy tests and repository tests (expected PASS); commit service/API change.

### Task 3: Agent panel controls and request recovery

**Files:** Create `web/src/components/canvas/canvas-agent-message-queue.tsx`; Modify `services/api/agent.ts`, `services/cloud-agent-conversations.ts`, panel/chat UI/CSS, canvas page external dispatch, admin runtime-policy types/controls; Tests in `web/test/fixtures/cloud-agent-panel-sync.harness.tsx` and focused queue component/API tests.

**Interfaces:** `AgentSendMode='interject'|'queue'`; API message request carries frozen request/mode/expectedRunId/questionId. Panel handle returns explicit `sent|queued` receipt to node caller, preserving original draft. UI receives queue state and handlers without dispatch policy.

- [ ] Add RED tests for account default selection, button/Enter/external node consistency, queue edit/promotion/remove, question answer bypass, unknown POST recovery with frozen mode, old-account response isolation.
- [ ] Implement API wrappers and scoped pending record; fetch queue and settings from server, refresh authoritative snapshot and latest run; stop using front-end running status to choose interjection/new-turn route for existing conversations.
- [ ] Render compact mode menu and bounded queue list, preserve draft and skin, show precise sent/queued results, no explanatory product/implementation text.
- [ ] Run focused Bun tests and `node node_modules/typescript/bin/tsc --noEmit` (expected PASS); commit front-end change.

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
