# Crew remaining implementation acceptance

Date: 2026-10-05. Worktree: `canary`, branch `codex/crew-admin-switch`.

## Source and verification

- Capability fix: `84e6eb8d`; canvas configuration: `78df66de`; run console: `bb7e58ce`.
- Configuration mutation lock/skin regressions: `0462903a`, RED then GREEN.
- Private skill selector regression: `bf553851`, RED then GREEN.
- Remote `canvas4ai/canary` at `7e94925e` fetched and merged; only CHANGELOG changed upstream.
- Linux focused command: `go test ./internal/app ./internal/handler ./internal/repository -run "Crew|AgentToolSchemaArtifact|CloudAgentSupportedToolNamesExcludeCrewOnlyTools" -count=1`. PASS for all three packages (61.764s, 1.198s, 0.373s).
- Agent `npm test`: 149 pass, zero failures after archiving three stale generated observability test files whose TypeScript sources are absent from this branch. Initial stale build run: 156/158; it does not represent current source.
- Final full web suite: 2175 pass, zero failures across 300 files (35.19s); typecheck passed. Private visibility normalization reproduced false instead of true, then passed after fixing `/skills/added` and its frontend projection.
- Linux `go test ./internal/skills -count=1`: PASS (71.494s). Service privacy mapping regression reproduced `skill private privacy = false`, then passed in the full skills suite.
- Cross-flow test exercises shared transport, stream parser and state reducer; it is not a DOM or live-model test.
- Windows SQLite CGO lifecycle tests crashed at sqlite3_open_v2; Linux results above provide the gate evidence.

## Deployment scope

Local only: new Compose project `canvas-crew-3000`, volume `canvas_crew_remaining_3000_data`, backend/web/Pi worker. Runtime token lives in ignored `.local/crew-remaining-3000/runtime.env` and is not printed. Existing `canary` containers, image tags and volume remain available for rollback. Project `canvas-skill-index-3003` and server `192.168.90.200` are outside this operation.

Build attempt one failed fetching `golang.org/x/text@v0.40.0` from goproxy.cn with TLS handshake timeout. goproxy.io returned 403; goproxy.cn returned 200 from host and container probe. Local HTTPS proxy endpoint is 127.0.0.1:7897. Retry uses the reachable domestic mirror.

## Script acceptance (no browser access)

User explicitly requested script acceptance and no browser operations. HTTP/live Pi scripts and frontend component/transport tests replace this run's DOM gate. Login itself, visual interaction and server deployment are not claimed.

- `deepseek-flash` ran through Canvas and its Pi worker. No direct provider API call by the acceptance runner. The copied local volume received an enabled zero-credit test price tier; original model configuration was saved in ignored `.local/crew-remaining-3000/model-price-before.json`. This is a fixture price, not commercial pricing.
- `scripts/crew-acceptance.py --extended`: **26/26 PASS**. Health/schema 55/55; feature disable rejection and preservation of other booleans; anonymous 401/non-admin 403; cross-account 404; configuration CAS 409; empty skills; run idempotency; SSE identity/cursors/reconnection; two completed members; pre-approval commit rejection; wrong hash rejection; approve/commit/replay; persisted nodes; reject; cancellation of two running members; terminal cancellation and unchanged canvas.
- `scripts/crew-acceptance-audit/main.go`: read-only SQLite projection confirms three independent live Pi sessions (14/14/10 entries), three different conversations and overlapping structured member task/result time windows. No transcript or provider credentials are read or emitted.
- Restart probe: both members were running when the isolated local Pi worker restarted; the same Crew recovered to `waiting_approval`. After backend restart, approval ID and state remained unchanged.
- Budget probe: both members with `maxSteps=1` failed; Coordinator exposed their failures in a degraded proposal requiring approval. The initial test incorrectly expected total Crew failure; source/spec inspection established the intended degraded-proposal behavior. Corrected retained-fixture check passed, rejected the proposal, and confirmed the canvas was unchanged.
- Independent review completed: 5 Important addressed with regressions, 0 Critical. Deferred minor: after deleting a middle member, new-member position may collide with an existing position.

### Reproduce locally

Authentication file contains isolated short-lived sessions and a test canvas ID; it is ignored and must not be committed. These are authenticated HTTP tests, not login-flow tests.

```powershell
python scripts/crew-acceptance.py --auth-file .local/crew-remaining-3000/acceptance-auth.json --output .local/crew-remaining-3000/script-acceptance-extended.json --extended
python scripts/crew-lifecycle-acceptance.py --auth-file .local/crew-remaining-3000/acceptance-auth.json --output .local/crew-remaining-3000/lifecycle-result.json --compose-env .local/crew-remaining-3000/runtime.env --compose-override .local/crew-remaining-3000/compose.override.yml
```

Lifecycle script restarts only Compose project `canvas-crew-3000`; no volume deletion. Audit helper runs inside the backend Go module by mounting its directory at `/src/backend/cmd/crew-acceptance-audit`, the copied volume read-only at `/data`, with `CREW_ACCEPTANCE_LOCAL=1` and `CREW_RUN_ID` set to the tested run.

## Regression limits and final deployment

- Remote `canvas4ai/canary` was refreshed after the tests and remains `7e94925e`, already merged.
- Six model-protocol fixture failures reproduced against a fresh archive of that exact remote baseline: `TestSaveAdminChannelModelRejectsActiveDuplicateKey`, `TestChannelModelLabelSaveAndCatalogPreserveChannelIdentity`, `TestSaveAdminChannelModelCascadesUpstreamKeyToMatchingTiers`, `TestRenamedUpstreamKeyReachesSystemChannelRequests`, `TestSaveAdminChannelModelPersistsAndPublishesIcon`, `TestChannelCostHTTPAuthorizationAndPublicProjection`. All reject an invalid model request protocol; they are inherited and do not become a passing full suite.
- Initial full app run exceeded the default 10-minute timeout. A new app/handler run uses `-timeout 30m`; its result remains a separate gate until recorded.
- Final VERSION bump, final Compose image/build metadata verification, and canary PR publication are recorded by the final delivery. Draft PR is appropriate while the inherited full-regression gate is unresolved.
