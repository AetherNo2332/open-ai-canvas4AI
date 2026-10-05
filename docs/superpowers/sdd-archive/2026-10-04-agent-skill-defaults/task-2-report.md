# Task 2 Report: 默认技能与会话技能 repository（含 revision CAS）

**Commit:** `be107a4f` `feat(agent): 技能默认配置 - 默认技能与会话技能仓储及 revision CAS`
**Branch:** feat/agent-skill-defaults

## What Was Implemented

### 1. `backend/internal/repository/agent_skill_defaults.go` (new)

All six repository methods from the brief, receiver `*Repository`:

- `AgentSkillDefaultRows() ([]model.AgentSkillDefault, error)` — all rows, `Order("position asc, skill_id asc")`.
- `EnabledAgentSkillDefaults() ([]model.AgentSkillDefault, error)` — `enabled = true` only, same ordering.
- `ReplaceAgentSkillDefaults(expectedRevision int64, rows []model.AgentSkillDefault, updatedBy string) (int64, error)` — `r.db.Transaction`: computes current revision via `COALESCE(MAX(revision), 0)` scoped to the defaults table (empty table = 0); on `current != expectedRevision` returns `kernel.AgentSkillDefaultsConflict(current)` leaving the table untouched; otherwise deletes `scope = global` rows and inserts the new set with `Revision = current + 1` and `UpdatedBy` stamped on each row; returns new revision (including the empty-rows clear case).
- `AgentConversationSkills(conversationID string) ([]model.AgentConversationSkill, error)` — rows for one conversation, `Order("position asc")`.
- `SaveAgentConversationSkills(rows []model.AgentConversationSkill) error` — `clause.OnConflict{Columns: conversation_id + skill_id, DoUpdates: skill_version_id/content_hash/source/position}` upsert following the `SetUserSkillAdded` pattern (skills.go:178); no-op on empty input.

### 2. Controller-ruled model registrations

- `backend/internal/database/schema.go` `Models()`: added `&model.AgentSkillDefault{}, &model.AgentConversationSkill{}` immediately after the AgentProfile/AgentLesson/AgentMemorySetting cluster (one per line, matching style).
- `backend/cmd/migrate-sqlite-postgres/main.go` `migrations()`: added `migrateTable[model.AgentSkillDefault]("agent_skill_defaults")` and `migrateTable[model.AgentConversationSkill]("agent_conversation_skills")` at the same position.

### 3. `backend/internal/repository/agent_skill_defaults_test.go` (new)

sqlite in-memory setup per `skills_test.go:15` (`kernel.NewID()` DSN, Silent logger, AutoMigrate of the two models). Six tests:

| Test | Covers |
| --- | --- |
| `TestReplaceAgentSkillDefaultsCASChain` | CAS 0→1→2; replace clears old rows; Revision/UpdatedBy stamped |
| `TestReplaceAgentSkillDefaultsStaleRevisionConflict` | expected=1 vs current=2 → `errors.As` `*kernel.AppError`, status 409, reason `agent_skill_defaults_revision_conflict`, `Details["currentRevision"] == int64(2)`; rows unchanged after conflict |
| `TestReplaceAgentSkillDefaultsEmptyTableExpectedZero` | empty table expected=0 saves, revision=1, empty set clears table |
| `TestAgentSkillDefaultRowsOrderedByPositionAndSkillID` | stable (position, skill_id) ordering; enabled filter returns 3 of 4 |
| `TestSaveAgentConversationSkillsUpsertWithoutDuplicates` | upsert overwrites version/hash/source/position, no duplicate rows (3 rows total), ordered by position |
| `TestAgentConversationSkillsIsolatedByConversationID` | other conversationID returns empty; own conversationID returns its 2 rows |

## TDD Evidence

**RED:** `go test ./internal/repository/ -run AgentSkill` → build failed with `repo.ReplaceAgentSkillDefaults undefined`, `repo.AgentSkillDefaultRows undefined`, etc. (compile failure as expected).

**GREEN (focused):**
```
go test ./internal/repository/ -run AgentSkill -v
--- PASS: TestReplaceAgentSkillDefaultsCASChain (0.00s)
--- PASS: TestReplaceAgentSkillDefaultsStaleRevisionConflict (0.00s)
--- PASS: TestReplaceAgentSkillDefaultsEmptyTableExpectedZero (0.00s)
--- PASS: TestAgentSkillDefaultRowsOrderedByPositionAndSkillID (0.00s)
--- PASS: TestSaveAgentConversationSkillsUpsertWithoutDuplicates (0.00s)
--- PASS: TestAgentConversationSkillsIsolatedByConversationID (0.00s)
```

**Full package run:**
```
go test ./internal/repository/ ./internal/database/ ./cmd/migrate-sqlite-postgres/
ok  infinite-canvas/backend/internal/repository      1.505s
ok  infinite-canvas/backend/internal/database        9.660s
ok  infinite-canvas/backend/cmd/migrate-sqlite-postgres  0.283s
```
This includes the pre-existing `TestMigrationListCoversSchemaModels` (migrate tool) and `TestMigrateSchemaCreatesAgentSkillConfigTables` (database), both still green with the new registrations.

## Self-Review Findings

- All six method names/signatures match the brief verbatim.
- `ReplaceAgentSkillDefaults` conflict path is read-only before any delete: the MAX query and CAS check happen before the delete statement inside the transaction, so a conflict leaves existing rows untouched (verified by test).
- The `Details["currentRevision"]` value is `int64` (kernel constructor passes the int64 through), asserted with a type assertion in the test.
- gofmt: both new files are clean. `schema.go` and `migrate main.go` are flagged by `gofmt -l` because the whole files use CRLF line terminators — verified via `git stash` that this pre-exists my change (stashed version is also flagged); my additions match the surrounding file style and `git diff` shows only the 2 inserted lines per file.
- Test list unchanged in behavior: the stale-conflict test originally asserted "2 rows remain" which was wrong for its own fixture (two sequential successful replaces of 1 row each leave 1 row); fixed the assertion to verify the actual surviving row content (skill-2, revision 2). This was a test-fixture expectation bug, not an implementation bug.

## Concerns

None blocking. One note for later tasks: `ReplaceAgentSkillDefaults` reads MAX(revision) across the table rather than per-scope; today only `scope = global` rows exist (per the brief's semantics), so this is equivalent, and the brief specifies exactly this computation.
