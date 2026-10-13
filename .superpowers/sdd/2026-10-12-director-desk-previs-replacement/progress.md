# SDD ledger — plan: docs/superpowers/plans/2026-10-12-director-desk-previs-replacement.md

## Preflight interface review

- Task 1 runtime -> Task 3 workbench: `createDirectorRuntime`, `disposeDirectorRuntime`, browser-safe runtime host callbacks.
- Task 2 adapter -> Task 3/4: stable `PrevisScene` <-> DirectorDesk document conversion and preview input.
- Task 3 lifecycle -> Task 4/7: workbench open/close/onChange/onApply and save-coordinator integration.
- Task 4 bridges -> Task 7: authorized assets, render jobs, cancellation, retry and idempotent writeback.
- Task 5 tools -> Task 6/7: director workflow tool contracts, approval/CAS/error semantics.
- Task 6 image flow -> Task 7: real image inspection and ordered scene creation without partial scenes.

## Progress

- Plan approved by user; execution mode: Native.
- Task 1: not started.
- Task 1: `web/src/integrations/director-desk/index.ts` exposes a browser-safe Three runtime with injected renderer lifecycle and no Electron/MCP/filesystem boundary; license metadata recorded.
- Task 1 verification: `cd web && bun test test/director-desk-runtime-import.test.ts` (1 pass), `cd web && bun run typecheck` (pass).
- Task 1 ruling: keep cannon-es/mediabunny out of this first runtime slice because the selected browser-safe adapter does not import physics/media modules; add them only when Task 4 render/asset bridges require them.
- Task 2: `previs-runtime-types.ts` and `previs-adapter.ts` provide a constrained DirectorDesk Project projection, stable IDs, object/camera/path/cut mappings, lossless canvas extension payload, and preview input.
- Task 2 verification: `cd web && bun test test/director-desk-previs-adapter.test.ts test/director-desk-previs-roundtrip.test.ts` (2 pass); `cd web && bun run typecheck` (pass).
- Task 3: `DirectorDeskPrevisWorkbench` is now the sole lazy-loaded canvas entry, owns the browser runtime hook, and preserves the existing save/Agent workbench lifecycle through the wrapper.
- Task 3 verification: `cd web && bun test test/director-desk-previs-workbench.test.tsx test/director-desk-previs-lifecycle.test.ts` (2 pass); `cd web && bun run typecheck` (pass).
- Task 3 ruling: keep the proven CanvasPrevisWorkbench UI shell behind the DirectorDesk entry during migration; this preserves current save/approval/Agent behavior while the browser runtime is made authoritative in later bridge tasks.
- Task 4: browser-safe asset and render bridges now resolve authorized canvas resources, reject unsafe local/token URLs, deduplicate preview requests, and support cancellation through the existing render submission boundary.
- Task 4 verification: `cd web && bun test test/director-desk-asset-loader.test.ts test/director-desk-render-bridge.test.ts` (2 pass); `cd web && bun run typecheck` (pass).
- Task 4 ruling: the existing backend previs renderer already owns lease, retry, cancellation, CAS, and idempotent writeback semantics, so this task adds only the web boundary and avoids duplicating production state machines.
- Task 5: six high-level Director Agent tools are advertised in canvas scope, presented distinctly in the web Agent UI, and compiled into the existing previs scene/patch/preview contracts with write approval preserved.
- Task 5 verification: `cd backend && go test ./internal/app -run CloudAgentDirector -count=1` (pass); `cd web && bun test test/agent-director-tool-presentation.test.ts test/agent-tool-presentation.test.ts` (19 pass); `cd web && bun run typecheck` (pass).
- Task 5 ruling: the director workflow is a controlled vocabulary over the existing Agent/previs contracts rather than a second MCP or persistence surface.
- Task 6: image-node context menu now starts an image-to-director Agent run, selects an image-capable system model, requires real `canvas_inspect_image`, and directs the ordered block/design/continuity flow without local scene mutation.
- Task 6 verification: `cd backend && go test ./internal/app -run 'CloudAgentDirector|CloudAgentDirectorImage' -count=1` (pass); `cd web && bun test test/image-to-director-agent.test.ts test/image-to-previs-flow.test.ts` (3 pass); `cd web && bun run typecheck` (pass).
- Task 6 ruling: existing vision capability and image inspection receipts remain authoritative; the one-click entry adds no parallel vision endpoint and refuses unsupported models before scene creation.
- Task 7: feature documentation, pending-test boundaries, README capability summary, and an end-to-end web contract test are in place; the implementation remains browser-safe and does not touch production services.
- Task 7 verification: focused web/backend tests, `bun run typecheck`, and `git diff --check` pass; real browser/model/production deployment checks remain documented as pending acceptance.
- Review follow-up: strengthened adapter cut/path round-trip, sanitized DirectorDesk object extensions, constrained asset URLs to resource endpoints, handled stale vision capability and aborted renders, cleaned partial renderer creation, and materialized a visible browser runtime preview.
- Review disposition: full DirectorDesk editor replacement, server-side image receipt transaction, production render bridge wiring, and real browser/model acceptance remain pending and are called out in the documentation rather than being claimed as complete.
- Production-readiness execution: added server-controlled `directorDeskPrevisMode` (`off`/`canary`/`on`) with fail-closed normalization, admin switching UI, and focused backend/frontend coverage.
- Production-readiness execution: added a draft sync bridge from the existing previs editor into the DirectorDesk runtime so canary sessions update the runtime on every scene draft change; the legacy workbench remains the editing host until the P1 editor event bridge is complete.
- Production-readiness verification: `cd backend && go test ./internal/app -run 'FeatureAvailability' -count=1`; `cd web && bun test test/director-desk-feature.test.ts test/director-desk-previs-lifecycle.test.ts test/director-desk-previs-workbench.test.tsx test/admin-ui-regressions.test.ts`; `cd web && bun run typecheck` all pass.
- Production-readiness execution: image-to-director requests now carry a server-validated `workflow=image_to_director`; the runtime persists the workflow phase and rejects scene writes that skip the successful image inspection or ordered block/design/continuity phases.
- Production-readiness execution: successful high-level director receipts now include durable run `requestId`, tool `callId`, and the latest scene snapshot token as `revision`.
- Production-readiness verification: `cd backend && go test ./internal/app -run 'CloudAgentDirector' -count=1`; `cd web && bun test test/image-to-previs-flow.test.ts test/image-to-director-agent.test.ts test/director-desk-feature.test.ts`; `cd web && bun run typecheck` all pass.
- Production-readiness execution: image workflow failure handling now attempts a guarded rollback of the exact block mutation when the current canvas still matches its after-snapshot; concurrent edits prevent silent overwrite, and continuity-passed workflows are never rolled back.
- Production-readiness verification: `cd backend && go test ./internal/app -run 'CloudAgentDirector' -count=1` passes after the guarded rollback contract changes.
- Production-readiness execution: DirectorDesk canary/on workbench preview export now submits through the existing canvas command `previs-tasks` session when available; legacy mode keeps browser recording as a fallback.
- Production-readiness verification: `cd web && bun test test/director-desk-previs-workbench.test.tsx test/director-desk-feature.test.ts test/director-desk-render-bridge.test.ts`; `cd web && bun run typecheck` pass.
- Production-readiness verification: focused web suite 13 pass / 53 expectations; backend app and handler focused suites pass; renderer 5 pass / 1 Chromium skip; full web suite 2298 pass / 9 unrelated baseline failures (video preview resource, admin lazy routes, shared request client, Live2D loader, password recovery, workspace routes, wallet entry).
- Production-readiness execution: bundled the upstream DirectorDesk `src/engine.ts` browser dependency graph at version 0.4.11, added an import contract test, and wired runtime transform commits through `directorDocumentToPrevisScene` into the existing canvas save callback.
- Production-readiness ruling: keep the proven legacy workbench as the product shell until the upstream native UI panels are migrated; the real Engine is now the canary rendering/interaction core, but `on` remains non-production until native UI and browser acceptance are complete.
- Production-readiness fix: added a red/green contract for selected-entity transforms so upstream TransformControls cannot accidentally mutate the first scene entity.
- Production-readiness follow-up: canary mode now uses a stable 10% user cohort keyed by FNV-1a bucket; missing user identity fails closed to the legacy workbench. Verification: `cd web && bun test test/director-desk-feature.test.ts` (1 pass / 8 expectations).
- Ruling: keep the cohort percentage fixed in this slice instead of adding another admin setting; this removes global canary exposure without expanding the persisted feature schema. Cost if wrong: operators cannot tune the percentage until a later schema/UI change.
- P1 follow-up: DirectorDesk runtime transform commits now register with CanvasPrevisWorkbench's `writeAndPublish` through a commit bridge, so runtime edits use the existing save coordinator/CAS path instead of calling the outer project callback directly. Verification: `cd web && bun test test/director-desk-commit-bridge.test.ts test/director-desk-previs-workbench.test.tsx test/director-desk-previs-lifecycle.test.ts` (3 pass / 5 expectations); TypeScript compiler passes.
