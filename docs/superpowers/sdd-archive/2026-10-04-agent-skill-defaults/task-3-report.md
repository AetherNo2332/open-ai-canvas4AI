# Task 3 Report: 技能选择解析与容量准入（skills 域纯函数）

## Status: DONE

## What Was Implemented

Two new files in `backend/internal/skills/`, pure functions only — no DB, no service wiring, no new dependencies:

### `backend/internal/skills/skill_selection.go`

- Constants `SkillSourceGlobal = "global"`, `SkillSourceUser = "user"`.
- Types `SkillSelectionItem{SkillID, VersionID, ContentHash, Source string}`, `SkillCapacityFacts{SkillID string; FileCount int; TotalBytes, ContextBytes int64}`, `SkillCapacityBudgets{MaxFiles int; MaxTotalBytes, MaxContextBytes int64}` — all matching the brief verbatim.
- Run-level budget constants with const-block style matching `skill_packages.go:36-42`:
  - `SkillRunMaxFiles = 1024`
  - `SkillRunMaxTotalBytes = 16 << 20`
  - `SkillRunMaxContextBytes = 512 << 10`, with a Chinese doc comment noting the v1 approximation: fixed run-level context budget instead of per-model input-budget check (reasoning: `piNativeSkillReadMaxRunes=12000` rune ≈ 48KB/技能 × 约 10 技能取整), per-model budget deferred to P2 Workspace plan.
- `ResolveSkillSelection(layers ...[]SkillSelectionItem) ([]SkillSelectionItem, error)`:
  - Layers passed low→high priority (global first arg, user last).
  - Single-pass dedupe: map of SkillID → resolved position; a repeated SkillID **overwrites in place** at its lowest-layer position, so output order = lowest layer's original order first, higher layers' new entries in appearance order.
  - Empty or whitespace-only SkillID (in any layer) → `kernel.BadAuthRequest` (400). Whitespace is trimmed before storage.
  - Empty/nil layers → empty slice, no error.
- `AdmitSkillCapacity(facts []SkillCapacityFacts, budgets SkillCapacityBudgets) error`:
  - Sums FileCount/TotalBytes/ContextBytes across facts.
  - Checks files → total_bytes → context_bytes; strictly-greater-than rejects (exactly-at-limit passes).
  - Over-limit returns `kernel.AgentSkillBudgetExceeded(map[string]any{"budget": ..., "limit": ..., "actual": ...})`; `limit`/`actual` are `int` for files and `int64` for byte budgets.

## TDD Evidence

**RED** — wrote `skill_selection_test.go` first, ran:

```
go test ./internal/skills/ -run "SkillSelection|SkillCapacity|ResolveSkill|AdmitSkill"
internal\skills\skill_selection_test.go:15:14: undefined: SkillSelectionItem
internal\skills\skill_selection_test.go:23:19: undefined: ResolveSkillSelection
... FAIL infinite-canvas/backend/internal/skills [build failed]
```

**GREEN** — implemented `skill_selection.go`, same command:

```
--- PASS: TestResolveSkillSelectionDedupesWithHighestPriorityLayer
--- PASS: TestResolveSkillSelectionStableOrderForSingleLayer
--- PASS: TestResolveSkillSelectionRejectsEmptySkillID
--- PASS: TestResolveSkillSelectionAcceptsEmptyLayers
--- PASS: TestAdmitSkillCapacityPassesAtExactLimits
--- PASS: TestAdmitSkillCapacityRejectsOverBudget (files / total_bytes / context_bytes subtests)
--- PASS: TestAdmitSkillCapacityAcceptsEmptyFacts
PASS ok infinite-canvas/backend/internal/skills 0.194s
```

## Test Results

| Command | Result |
| --- | --- |
| `go test ./internal/skills/ -run "SkillSelection\|SkillCapacity\|ResolveSkill\|AdmitSkill" -v` | PASS (8 tests, 3 subtests) |
| `go test ./internal/skills/` (full package regression) | `ok ... 1.671s` |
| `go vet ./internal/skills/` | clean |
| `gofmt -l` on both new files | clean (existing package files are pre-existing gofmt drift — untouched, not introduced by this task) |
| `go build ./...` | clean |

## Test Coverage Per Brief Checklist

- [x] 三层去重且 user 版本覆盖 global 版本（`TestResolveSkillSelectionDedupesWithHighestPriorityLayer`：skill-a 被 workspace 层覆盖、skill-b 被 user 层覆盖）
- [x] 输出顺序稳定：global 在前、用户新增在后（同上；另加单层原始顺序不排序的 `TestResolveSkillSelectionStableOrderForSingleLayer`）
- [x] 空 ID 报错（含空白字符串 `"   "`）
- [x] 三项预算各自恰好通过 / 超 1 拒绝（exactly-at-limit pass 表 + over-by-one 拒绝表）
- [x] details 的 budget/limit/actual 值正确（含类型：files 为 int，字节为 int64）
- [x] 空集合通过（nil layers 与 nil facts）
- [x] 常量值正确（`TestSkillRunBudgetConstants`）

## Files Changed

- `backend/internal/skills/skill_selection.go` (new, 91 lines)
- `backend/internal/skills/skill_selection_test.go` (new, 174 lines)

## Commit

- `b4cf3b55` `feat(skills): 技能默认配置 - 多层技能去重解析与容量准入纯函数`

## Self-Review Findings

- Exact names verified against brief: `SkillSelectionItem` fields, `SkillSourceGlobal/SkillSourceUser`, `SkillCapacityFacts`, `SkillCapacityBudgets`, `SkillRunMaxFiles/SkillRunMaxTotalBytes/SkillRunMaxContextBytes`, `ResolveSkillSelection`, `AdmitSkillCapacity`. Param order is low→high priority (global first).
- No identifier collisions: pre-existing `skillSourceUser = 1` (int, in skills.go) is unexported and distinct from the new exported string constants `SkillSourceUser = "user"` — different identifiers (case differs), coexist safely.
- Dedupe semantics: overwrite-in-place keeps the *lowest-layer position* of a duplicated skill while carrying the *highest-layer content*. Verified by test (skill-a stays at index 0 with workspace-layer version).
- Error path uses `kernel.BadAuthRequest` (400) for empty ID, matching the brief's "空 ID 报错" with the repo's existing 400 constructor; budget errors use `kernel.AgentSkillBudgetExceeded` per brief.
- The approximation note is in Chinese on `SkillRunMaxContextBytes` as required.

## Concerns

- None blocking. One note: `ResolveSkillSelection` trims SkillID whitespace before dedupe/validation; the brief did not specify trimming, so `" a "` and `"a"` unify. This is a defensive normalization consistent with repo style (`strings.TrimSpace` used throughout skills package); no callers exist yet in this task.
