# Crew remaining implementation acceptance

Date: 2026-10-05. Worktree: `canary`, branch `codex/crew-admin-switch`.

## Source and verification

- Capability fix: `84e6eb8d`; canvas configuration: `78df66de`; run console: `bb7e58ce`.
- Configuration mutation lock/skin regressions: `0462903a`, RED then GREEN.
- Private skill selector regression: `bf553851`, RED then GREEN.
- Remote `canvas4ai/canary` at `7e94925e` fetched and merged; only CHANGELOG changed upstream.
- Linux focused command: `go test ./internal/app ./internal/handler ./internal/repository -run "Crew|AgentToolSchemaArtifact|CloudAgentSupportedToolNamesExcludeCrewOnlyTools" -count=1`. PASS for all three packages (61.764s, 1.198s, 0.373s).
- Agent `npm test`: 149 pass, zero failures after archiving three stale generated observability test files whose TypeScript sources are absent from this branch. Initial stale build run: 156/158; it does not represent current source.
- Previous full web suite: 2165 pass, zero failures across 298 files. Focused cross-flow/settings/run/events: 10 pass before private selector addition; settings after addition: 6 pass. Typecheck passed.
- Cross-flow test exercises shared transport, stream parser and state reducer; it is not a DOM or live-model test.
- Windows SQLite CGO lifecycle tests crashed at sqlite3_open_v2; Linux results above provide the gate evidence.

## Deployment scope

Local only: new Compose project `canvas-crew-3000`, volume `canvas_crew_remaining_3000_data`, backend/web/Pi worker. Runtime token lives in ignored `.local/crew-remaining-3000/runtime.env` and is not printed. Existing `canary` containers, image tags and volume remain available for rollback. Project `canvas-skill-index-3003` and server `192.168.90.200` are outside this operation.

Build attempt one failed fetching `golang.org/x/text@v0.40.0` from goproxy.cn with TLS handshake timeout. goproxy.io returned 403; goproxy.cn returned 200 from host and container probe. Local HTTPS proxy endpoint is 127.0.0.1:7897. Retry uses the reachable domestic mirror.

## Pending gates

- [ ] Full Linux app/handler regression.
- [ ] Compose build and switch; running image tags, health build metadata and schema.
- [ ] Administrator switch and canvas configuration CRUD/CAS in production frontend.
- [ ] Coordinator and two members, independent sessions, parallel execution, failure and lease recovery with real model.
- [ ] SSE reconnect, approve/reject, snapshot conflict and idempotent commit in browser.
- [ ] Fresh whole-branch review, important fixes and final suite.
- [ ] Version bump and PR to remote canary.

Local administrator `/admin/channels` visibly contains zero channels. User has been asked to configure an available text model through the local UI; do not send keys through chat. Real model acceptance remains open. Do not claim Crew fully complete based on health or protocol tests.
