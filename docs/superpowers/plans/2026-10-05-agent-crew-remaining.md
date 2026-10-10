# Agent Crew 剩余开发实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 完成 Crew 从画布配置、运行切换、成员事件、审批提交到本地 Compose 3000 验收的端到端闭环。

**Architecture:** 复用现有 Workspace/Crew/Agent Run/Pi session/CanvasMutation/SSE 基础设施。先修正后端 Crew 能力口径和生命周期，再把现有前端 API/组件接入画布设置和发送流程，最后用跨层测试与独立 Compose 验收收口。

**Tech Stack:** Go、GORM、Gin、React、TypeScript、Ant Design、Bun test、Docker Compose。

**Spec:** `docs/superpowers/specs/2026-10-05-agent-crew-remaining-design.md`

## Global Constraints

- Crew 配置按 `canvasId` 隔离；文案必须明确“各画布的 Crew 配置独立管理”。
- 普通 Agent 不得获得 Crew 专属工具；Coordinator 仅获得 `delegate_task`、`crew_wait`、`crew_propose`，Member 仅获得 `task_result`。
- 成员使用独立 conversation/Pi session，只通过结构化任务和结果通信。
- 未审批提案不得修改画布；提交必须带 expected snapshot hash 和幂等键。
- 全局 Crew 开关关闭时，Crew 配置和运行入口不可用；普通 Agent 保持可用。
- 不引入自由成员聊天、多人直接写画布或新的模型协议。
- 生产服务器、远端容器和数据库只读；本地 Compose 使用独立项目、端口和持久化目录。

## Review Focus

- 普通 Agent 开启全局 Crew 后的工具列表不能泄露 Crew 工具；由 Task 1 的能力矩阵测试覆盖。
- revision 冲突不能静默覆盖编辑中的成员配置；由 Task 2 的冲突测试覆盖。
- SSE 重连和重复序号不能重复改变运行态；由 Task 3 的流状态测试覆盖。
- 拒绝审批、snapshot 冲突和重复 commit 不能写画布；由 Task 3 的集成测试覆盖。
- 账号/画布切换不能复用其他 Crew 的配置或运行；由 Task 2/3 的隔离测试覆盖。

### Task 1: 统一 Crew 能力矩阵与运行时状态

**Files:**
- Modify: `backend/internal/app/cloud_agent_tools.go`
- Modify: `backend/internal/app/cloud_agent_runtime.go`
- Modify: `backend/internal/app/agent_crew_dispatch.go`
- Test: `backend/internal/app/agent_crew_run_test.go`
- Test: `backend/internal/app/cloud_agent_tool_schema_artifact_test.go`

**Interfaces:**
- Consumes: `CrewMemberRuntime`, `cloudAgentToolAllowed`, existing tool schema builder.
- Produces: one shared runtime capability predicate used by supported-name reporting and actual registration; stable Coordinator/Member/ordinary tool sets.

- [ ] **Step 1: Write failing capability matrix tests**

Add tests asserting ordinary requests contain none of `delegate_task`, `crew_wait`, `task_result`, `crew_propose`; Coordinator contains delegate/wait/propose but not task_result; Member contains task_result but not coordinator tools.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `cd backend && go test -ldflags=-linkmode=internal ./internal/app -run 'Test.*Crew.*Tool|TestAgentToolSchemaArtifactMatchesRuntime' -count=1`
Expected: FAIL because `CloudAgentSupportedToolNames` currently appends Crew names without Crew runtime context.

- [ ] **Step 3: Implement shared capability classification**

Add a private classifier keyed by `CrewMemberRuntime.Role` and use it in supported-name reporting, schema generation, and registration. Preserve non-Crew tools and existing public schema artifact names.

- [ ] **Step 4: Add lifecycle regression tests**

Cover duplicate identical delegate/task-result idempotency, changed-parameter conflict, member failure, terminal-run rejection, and recovery from a waiting member.

- [ ] **Step 5: Run focused tests and schema verification**

Run the command from Step 2 plus `go test -ldflags=-linkmode=internal ./internal/app -run 'TestCrew' -count=1`.
Expected: PASS; schema artifact matches runtime.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/app/cloud_agent_tools.go backend/internal/app/cloud_agent_runtime.go backend/internal/app/agent_crew_dispatch.go backend/internal/app/agent_crew_run_test.go backend/internal/app/cloud_agent_tool_schema_artifact_test.go
git commit -m "fix(agent): align crew tool capabilities with runtime"
```

### Task 2: 接入画布 Crew 配置

**Files:**
- Modify: `web/src/components/canvas/canvas-cloud-agent-settings.tsx`
- Modify: `web/src/components/canvas/canvas-agent-crew-settings.tsx`
- Modify: `web/src/services/api/agent-crew.ts`
- Modify: `web/src/components/canvas/canvas-cloud-agent-panel.tsx`
- Test: `web/test/canvas-agent-crew-settings.test.tsx`

**Interfaces:**
- Consumes: `listCrews`, `createCrew`, existing Crew mutation routes, `agentCrewEnabled`, `canvasId`.
- Produces: settings section that returns selected enabled Crew and refreshes after CAS conflict; panel can query current canvas Crew.

- [ ] **Step 1: Write failing UI integration tests**

Cover Crew entry text, empty state, loading `listCrews(canvasId)`, create/update/delete callbacks, member editor fields, and revision-conflict reload preserving unsaved form values.

- [ ] **Step 2: Run test to verify failure**

Run: `cd web && bun test web/test/canvas-agent-crew-settings.test.tsx`
Expected: FAIL because the settings page does not render a Crew section or load the API.

- [ ] **Step 3: Implement settings integration**

Add a Crew section to the existing settings sections. Load by `canvasId`, render the existing `CrewSettings`/`CrewMemberEditor`, wire CRUD and member/skill mutations with the latest revision, and expose a structured error for `agent_crew_revision_conflict`. Hide and reset the section when `agentCrewEnabled` is false.

- [ ] **Step 4: Add accessible states and API helpers**

Ensure labels for role, model, permission, budget, focus nodes and skills; add typed helpers for member/skill updates if missing; keep the “只影响新 Run” explanation visible.

- [ ] **Step 5: Run focused tests and typecheck**

Run: `cd web && bun test web/test/canvas-agent-crew-settings.test.tsx && bun run typecheck`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/canvas/canvas-cloud-agent-settings.tsx web/src/components/canvas/canvas-agent-crew-settings.tsx web/src/services/api/agent-crew.ts web/src/components/canvas/canvas-cloud-agent-panel.tsx web/test/canvas-agent-crew-settings.test.tsx
git commit -m "feat(web): add canvas crew configuration"
```

### Task 3: 接入 Crew Run、SSE、审批和画布提交

**Files:**
- Modify: `web/src/components/canvas/canvas-cloud-agent-panel.tsx`
- Modify: `web/src/components/canvas/canvas-agent-crew-run-card.tsx`
- Modify: `web/src/services/api/agent-crew.ts`
- Modify: `web/src/lib/canvas/crew-run-state.ts`
- Test: `web/test/canvas-agent-crew-run.test.tsx`

**Interfaces:**
- Consumes: Task 2 selected Crew, `createCrewRun`, `createCrewStreamParser`, approval/commit/cancel API helpers.
- Produces: Crew mode state in the panel, one active Crew Run card, idempotent event reducer and approval/commit actions.

- [ ] **Step 1: Write failing run integration tests**

Cover mode switch visibility, Crew submission calling `createCrewRun`, SSE snapshot/event application, sequence deduplication and reconnect, approval approve/reject, commit with snapshot hash/idempotency key, cancel, and ordinary Agent regression.

- [ ] **Step 2: Run test to verify failure**

Run: `cd web && bun test web/test/canvas-agent-crew-run.test.tsx`
Expected: FAIL because panel submission always calls `createAgentRun` and `CrewRunCard` has no stream lifecycle.

- [ ] **Step 3: Implement Crew mode and run state**

Add mode/selectedCrew state guarded by the feature flag. Route new submissions to `createCrewRun`; retain ordinary continuation behavior. Mount `CrewRunCard`, subscribe to `/events`, apply snapshots/events through a reducer keyed by run ID and sequence, reconnect from the last sequence, and clear state on canvas/conversation switch.

- [ ] **Step 4: Implement approval, commit, cancellation and errors**

Wire buttons to the typed API helpers. Use the server snapshot hash and a fresh idempotency key for commit. Surface feature-disabled, conflict, failed-member and network errors without converting a Crew Run into a normal Agent Run.

- [ ] **Step 5: Run focused tests, typecheck and build**

Run: `cd web && bun test web/test/canvas-agent-crew-run.test.tsx web/test/agent-crew-events.test.ts && bun run typecheck && bun run build`
Expected: PASS and production build succeeds.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/canvas/canvas-cloud-agent-panel.tsx web/src/components/canvas/canvas-agent-crew-run-card.tsx web/src/services/api/agent-crew.ts web/src/lib/canvas/crew-run-state.ts web/test/canvas-agent-crew-run.test.tsx
git commit -m "feat(web): run crews from canvas agent"
```

### Task 4: 跨层回归和本地 Compose 3000 验收

**Files:**
- Test: `backend/internal/handler/agent_crew_test.go`
- Test: `backend/internal/handler/agent_crew_events_test.go`
- Test: `backend/internal/app/agent_crew_recovery_test.go`
- Test: `web/test/canvas-agent-crew-e2e.test.tsx`
- Create: `docs/superpowers/plans/2026-10-05-agent-crew-acceptance.md`

**Interfaces:**
- Consumes: Tasks 1–3 public contracts and existing Compose files.
- Produces: reproducible evidence for feature gate, isolation, lifecycle, browser path and localhost:3000 deployment.

- [ ] **Step 1: Add missing cross-account and feature-gate tests**

Assert disabled routes return feature-disabled, another user cannot read/update Crew or SSE, and approval/commit paths preserve idempotency and snapshot conflict semantics.

- [ ] **Step 2: Run backend regression**

Run: `cd backend && go test -ldflags=-linkmode=internal ./internal/app ./internal/handler -count=1`
Expected: PASS.

- [ ] **Step 3: Add frontend cross-flow test**

Exercise disabled→enabled feature state, canvas-scoped Crew selection, create run, event completion, approval and commit without ordinary Agent fallback.

- [ ] **Step 4: Run frontend regression**

Run: `cd web && bun test web/test/canvas-agent-crew-e2e.test.tsx web/test/canvas-agent-crew-settings.test.tsx web/test/canvas-agent-crew-run.test.tsx && bun run typecheck && bun run build`
Expected: PASS; any unrelated inherited canary failures are recorded separately.

- [ ] **Step 5: Build and run independent Compose on localhost:3000**

Use a separate Compose project/data directory. Verify `docker compose ... config --images`, running image tags, `/api/health` build metadata, schema readiness, and browser login. Then exercise Admin Agent(beta) Crew switch, canvas Crew settings, Crew Run, SSE, approval and commit.

- [ ] **Step 6: Record acceptance evidence**

Write exact commands, commit/build metadata, test counts, and any blocker to `docs/superpowers/plans/2026-10-05-agent-crew-acceptance.md`.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/handler/agent_crew_test.go backend/internal/handler/agent_crew_events_test.go backend/internal/app/agent_crew_recovery_test.go web/test/canvas-agent-crew-e2e.test.tsx docs/superpowers/plans/2026-10-05-agent-crew-acceptance.md
git commit -m "test(agent): verify crew end to end"
```

## Execution Order

Task 1 → Task 2 → Task 3 → Task 4. Tasks 2 and 3 share the panel/settings state and must remain ordered. Task 4 is the merge gate and may not claim completion if browser or Compose evidence is missing.
