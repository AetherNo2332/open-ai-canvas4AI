# Canvas Agent operation parity implementation plan

**Goal:** Give the canvas Agent the same user-editable node, content and connection operations as manual editing, then verify the real worker/API in an isolated local Compose project.

**Architecture:** Extend the existing server-owned canvas capability registry and mutation planner. Both preview and committed writes use the same typed operation contract, ownership checks, optimistic revision checks, history and SSE patches. Preserve generation/task/resource service ownership; never allow model-written task status or credentials. Frontend consumes server-confirmed deltas and reconciles deletion, copy and content updates.

**Tech Stack:** Go/GORM, TypeScript/React/Zustand, Pi worker, Docker Compose, Python scripted provider/API acceptance.

**Spec:** Approved conversation roadmap P0-P4, including all built-in node CRUD, precise content edits, structured rows, layout/grouping, coherent history and concurrency. User explicitly instructed implementation followed by local Compose script testing.

## Constraints
- Work only in codex/canvas-agent-parity; preserve concurrent previs changes in the original checkout.
- Use isolated Compose names, images, port and volume; preserve existing 3030 services.
- Keep read_only/request_approval/auto, user ownership, generation budget and resource references enforced.
- Do not expose arbitrary metadata writes; publish editable fields/actions from the registry.
- Backend confirmation is required before claiming durable edits. Replay must not duplicate operations.
- No SSH or production mutation is part of this task.

## Review focus
- Deleting nodes cleans graph, storyboard, batch, parent and resource references without deleting shared resources or resurrecting task outputs.
- Copies remap internal relationships and clear submitted task identities.
- Local and remote concurrent edits preserve nonconflicting changes and reject conflicts.
- Long content is read completely or edited with checked exact-match fragments; truncated reads never imply complete content.
- Tool schema artifact and live worker snapshots agree; approval and read-only behavior cover new operations.

## Task 1: Registry and editable content coverage
- [x] Add failing registry tests for every builtin, geometry/style/editable configuration and task-field rejection.
- [x] Implement typed fields, default metadata, read projections and discoverable operation support in backend/internal/canvas/capability and cloud_agent_nodes.go.
- [x] Run capability tests and report exact unsupported business actions if any.

## Task 2: Unified node/edge operation planner
- [x] Add failing planner tests for delete_node, delete_connection, update_connection, duplicate_node, set_parent, replace_text and reorder_nodes/rows.
- [x] Extend agentCanvasOp, tool schema and decode validation with these typed commands.
- [x] Implement graph/reference cleanup, copy remapping, geometry/lock semantics and precise text editing using the existing transactional save/history/event path.
- [x] Add complete-content paging/search and history tool entry points where needed.
- [x] Verify atomic rollback, approvals, ownership, snapshots, replay and terminal task behavior.

## Task 3: Frontend feedback and convergence
- [x] Add failing delta/action tests for deletion, copy, reorder and concurrent edits.
- [x] Implement operation labels, structural reconciliation and order-aware patching.
- [x] Ensure user-visible deleted editors/selection are cleared and shared resource cleanup does not defeat undo.
- [x] Verify frontend typecheck/build and targeted tests.

## Task 4: Compose acceptance
- [x] Add an isolated Compose overlay and deterministic HTTP provider acceptance script.
- [x] Build actual backend/web/agent with current TOOL_SCHEMA.json.
- [x] Exercise real Agent worker end-to-end: create/edit/copy/delete/edges/grouping/long text, approvals, read-only, conflicts, restart and undo.
- [x] Verify persisted canvas after reload and record machine-readable acceptance results with image/build metadata.

## Task 5: Integration and review
- [x] Regenerate harness schema/descriptions and update feature/code-map/pending-test documentation.
- [x] Run bounded backend/frontend/worker regression checks and staged Compose acceptance.
- [x] Review full diff, fix material findings, commit focused changes and report scope/results.

## Progress
- Baseline: remote main b277b753; isolated worktree created. Original checkout has unrelated concurrent changes.

- Registry/operations/frontend implementation and review are complete. Native drawing format, multi-step undo/redo, branch rollback, cache authority, prompt mirror and role-variant boundaries have regression coverage.
- Frontend final: 138 tests / 17 files, 655 assertions, typecheck and Vite production build passed. Go pure contract/native/schema/dispatch checks passed; real SQLite history and ownership tests passed in Docker.
- Harness descriptions reduced below 20 KiB with LF/CRLF guards; policies synchronized from backend sources. Final Linux CGO/SQLite regression passed, including repair-scope catalog consistency and runtime phase storage/lease classification. All 92 selected Worker tests passed, including bridge-failure propagation and recoverable rejected receipts. Final images include the frozen source.
- Compose main acceptance passed 59 checks, then hit its original terminal-wait bound; the affected run recovered from three phase storage errors and completed naturally. The user instructed against redundant repeat tests: stop full replays, preserve the failed report, verify that existing run read-only, and test only the remaining UI undo, restart/persistence and SSE gates. Report the evidence as staged acceptance, not as one uninterrupted full-script success.
- The 11 continuation checks passed at 03:07:57 Beijing, with one new UI-undo run, identical runtime images/build, schema 58/58, persisted canvas after restart, ordered/resumed SSE and unchanged 3030 containers. All required verification is complete; evidence and limitations are recorded in the test report.
