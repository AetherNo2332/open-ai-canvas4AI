# Task 5 Report: 管理员 HTTP 接口与路由注册

**Status:** DONE
**Commit:** 6c9c3832 `feat(agent): 技能默认配置 - 管理员默认技能查询与保存接口`
**Branch:** feat/agent-skill-defaults

## What Was Implemented

### New: `backend/internal/handler/admin_agent_skill_defaults.go`
- `RegisterAdminAgentSkillDefaultsRoutes(r *gin.RouterGroup, svc *service.Service)` — exact signature per brief, matching `admin_analytics.go:13` pattern.
- `GET /admin/agent/skill-defaults`: `currentUser(c, svc)` → `svc.AdminAgentSkillDefaults(user)` → `ok(c, view)`. GET passes only the actor, no business logic.
- `PUT /admin/agent/skill-defaults` (via `saveAgentSkillDefaults` helper, mirroring `saveModelPricing`): `currentUser` → `c.ShouldBindJSON` into anonymous `struct { Revision int64; Items []service.AgentSkillDefaultItem }` → `svc.ReplaceAgentSkillDefaults(user, req.Revision, req.Items)` → `ok(c, gin.H{"revision": revision})`.
- Error paths: bind failure → `fail(c, http.StatusBadRequest, err)`; service errors → `failService(c, err)`, which routes `*service.AppError` with `Status` 409/400 through `writeAppError` preserving status/code/reason/details (verified by reading `response.go:30-48,50-78`). No business logic in handler.

### New: `backend/internal/handler/admin_agent_skill_defaults_test.go`
- Two route-registration tests in the `admin_observability_test.go` minimal pattern (`gin.TestMode`, `RegisterAdminAgentSkillDefaultsRoutes(router.Group("/api"), &service.Service{})`, iterate `router.Routes()`): asserts `GET /api/admin/agent/skill-defaults` and `PUT /api/admin/agent/skill-defaults` exist.

### Modified: `backend/internal/handler/api.go`
- Added `RegisterAdminAgentSkillDefaultsRoutes(api, svc)` next to sibling admin registrations (after `RegisterAdminObservabilityRoutes`, before `RegisterAdminStorageRoutes`).

### Modified: `backend/internal/handler/api_test.go`
- `wanted` map extended with `"GET /api/admin/agent/skill-defaults": false` and `"PUT /api/admin/agent/skill-defaults": false`; map realigned to gofmt tabwriter alignment (this file was already gofmt-flagged pre-change due to CRLF on disk; LF-normalized content is now clean).

### Modified: `backend/internal/service/aliases_types.go` (necessary addition)
- Added `AgentSkillDefaultItem = app.AgentSkillDefaultItem` type alias. Task 4 did not alias this type; per repo convention (`service` re-exports `app` types; handler never imports `internal/app` — verified `rg "internal/app" internal/handler/` returns nothing), the handler consumes it as `service.AgentSkillDefaultItem`. Without the alias the handler would have to import `internal/app` directly, violating the layering rule.

## TDD Evidence

RED (before handler existed):

```
$ go test ./internal/handler/ -run "AdminAgentSkill|RegisterCanvasAPI"
# infinite-canvas/backend/internal/handler [infinite-canvas/backend/internal/handler.test]
internal\handler\admin_agent_skill_defaults_test.go:12:2: undefined: RegisterAdminAgentSkillDefaultsRoutes
internal\handler\admin_agent_skill_defaults_test.go:24:2: undefined: RegisterAdminAgentSkillDefaultsRoutes
FAIL	infinite-canvas/backend/internal/handler [build failed]
```

(An intermediate RED also caught a stray `RegisterAgentSkillDefaultsRoutes` typo I had briefly added to api.go before correcting it to the admin registration; final implementation uses only `RegisterAdminAgentSkillDefaultsRoutes`.)

GREEN (focused):

```
=== RUN   TestAdminAgentSkillDefaultsRoutesAreRegistered
--- PASS: TestAdminAgentSkillDefaultsRoutesAreRegistered (0.00s)
=== RUN   TestAdminAgentSkillDefaultsPutRouteIsRegistered
--- PASS: TestAdminAgentSkillDefaultsPutRouteIsRegistered (0.00s)
=== RUN   TestRegisterCanvasAPIExposesOpenAPIAndProjects
--- PASS: TestRegisterCanvasAPIExposesOpenAPIAndProjects (0.02s)
PASS
ok  	infinite-canvas/backend/internal/handler	0.231s
```

Full package:

```
$ go test ./internal/handler/
ok  	infinite-canvas/backend/internal/handler	17.831s
```

Additional verification:
- `go build ./...` — clean.
- `go vet ./internal/handler/ ./internal/service/` — clean.
- `go test ./internal/service/` — `[no test files]` (aliases file is type-only).
- gofmt: both new files and LF-normalized versions of all touched files produce zero diff (Windows checkout is CRLF; `gofmt -l` flags nearly the entire handler package for CRLF alone, pre-existing — confirmed by stashing my changes and re-running).

## Files Changed

- Created: `backend/internal/handler/admin_agent_skill_defaults.go`
- Created: `backend/internal/handler/admin_agent_skill_defaults_test.go`
- Modified: `backend/internal/handler/api.go` (+1 line)
- Modified: `backend/internal/handler/api_test.go` (+2 routes, realignment)
- Modified: `backend/internal/service/aliases_types.go` (+1 alias line)

## Self-Review Checklist

- [x] Register function name/signature exactly `RegisterAdminAgentSkillDefaultsRoutes(r *gin.RouterGroup, svc *service.Service)`
- [x] Routes registered in api.go next to sibling admin registrations
- [x] api_test.go wanted map updated with both routes
- [x] PUT decodes `{ revision, items }` via `c.ShouldBindJSON` and passes both to service; GET passes only actor
- [x] gofmt clean (LF-normalized; new files ASCII)
- [x] Handler is parse + service call + respond only; `failService` preserves 409/400 via `writeAppError`
- [x] No docs sync needed (no API contract change beyond plan-defined new endpoints; plan owns docs per its own task sequence)

## Concerns

- `service.AgentSkillDefaultItem` alias added in `aliases_types.go` — technically outside the brief's file list, but required by the repo's `service`-facade rule; one-line, follows existing convention (e.g. `ModelPricingRequest`). Flagging for coordinator awareness.
- Windows working copy is CRLF-wide; `gofmt -l` on this machine flags most of the package regardless of my changes. Verified via LF-normalized diffs and stash test that my changes introduce no real formatting drift.
