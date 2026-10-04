# SDD ledger — plan: docs/superpowers/plans/2026-10-04-agent-skill-defaults-capacity.md

Branch: feat/agent-skill-defaults (base: edb348e6, from main @ cd13b79b canary)
Session model for implementers/reviewers: new-provider/glm-5.3-flash (session model; ListModels has exactly this one available, so per-task model selection is a no-op)

## Pre-flight scan (conflict table)

| Pair / Task | Shared file or interface | Finding | Ruling |
| --- | --- | --- | --- |
| Task 5 / Task 8 | backend/internal/handler/api_test.go, backend/internal/handler/agent.go | Both append route-registration assertions and add routes | Sequential edits; Task 8 runs after Task 5, no conflict |
| Task 4 / Task 8 | backend/internal/app/admin_agent_skill_defaults.go | Task 8 appends `AgentSkillDefaultsForUser` to the file Task 4 creates | Sequential, fine |
| Task 9 / Task 10 | web/src/pages/admin/settings/agent-settings-page.tsx, agent-settings-page.css, web/test/agent-settings-admin.test.ts | Task 9 inserts skill-defaults section; Task 10 deletes runtime section. Both edit nav line and description | Execute strictly in order 9 → 10; Task 10's description text "调度配置、默认技能与记忆管理" assumes Task 9 landed. Ruling: keep order. |
| Task 6 / Task 3+2 | skills.ResolveSkillSelection / repo methods | Interfaces match plan text (verified by reading plan) | Clean |
| Task 12 / Task 11 | canvas-cloud-agent-panel.tsx | Task 12 inserts subagent row near :1040; Task 11 edits modal + panel skill props | Different regions of a 2117-line file; sequential execution avoids clashes. Ruling: keep order. |
| Task 10 self | agent-settings-admin.test.ts:22 | Plan says change assertion to negative; test currently asserts positive | Task 10 Step 1 covers it |
| Task 12 self | plan says create web/src/lib/canvas/agent-subagent-layout.test.ts inside lib dir, but repo convention puts tests in web/test/ | Plan mixes both (Files list has layout test in lib/canvas/ and component test in web/test/) | Ruling: keep plan text as-is; both locations compile under bun test. Actually — repo runs `bun test` from web/, which only picks up web/ tree; test file inside web/src/lib/canvas is still under web/ so it runs. Keep. |

No contradictions with Global Constraints found.

## Progress

Task 2: complete (commits 8d3a6a7d..be107a4f, review clean, 1 Important parked with ruling)
- Ruling: reviewer's Important — CAS max(revision) read+write not atomic under PostgreSQL READ COMMITTED (concurrent replaces can both succeed; last-writer-wins with same revision number). Plan-mandated shape; degradation is benign (no corruption, both admins see success). Carry hardening (advisory lock or serializable tx) into Task 4 dispatch — service layer wraps this repo call and is the right place to add `sql.TxOptions`/advisory lock. If wrong: rare double-admin concurrent save race, recoverable by re-editing.
- Minor (deferred): ReplaceAgentSkillDefaults mutates caller's rows slice in place; add doc comment or copy.
- Minor (deferred): AgentConversationSkills ordering lacks skill_id tiebreak while upsert test relies on rowid order; add tiebreak or relax assertion.
- Minor (deferred): report said "six methods", brief defines five (cosmetic).
- Ruling: implementer deviation accepted — AgentConversationSkill uses composite PK (conversation_id, skill_id) instead of single-column; single-column PK would make SQLite's implicit unique index reject the 2nd skill row per conversation (test caught it). Follows CloudAgentPiEntry precedent. — costs nothing if wrong (index semantics identical to plan).
- Ruling: Models()/migrate-tool registration gap is a plan-scope gap → assigned to Task 2 (its writes are the first to these tables; adding `&model.AgentSkillDefault{}, &model.AgentConversationSkill{}` to database/schema.go Models() + backend/cmd/migrate-sqlite-postgres/main.go migrations() belongs in Task 2's dispatch). Must not drift to Task 13. If wrong: silent sqlite→postgres data loss once rows exist.
- Minor (deferred): model/agent_skills_config.go Source field is unconstrained string; consider typed constants when a second source (workspace) lands.
- Minor (deferred): low-selectivity secondary indexes (Enabled, Position, Source) beyond brief; revisit when query patterns exist.
Task 1 (restored): complete (commits edb348e6..8d3a6a7d, review clean)
Task 3: complete (commits be107a4f..b4cf3b55, review clean)
- Minor (deferred): ResolveSkillSelection doc comment doesn't document whitespace trimming normalization; add a line when touching this file next.
- Minor (deferred): empty-ID test only asserts err != nil, not the error class (BadAuthRequest/400); add errors.As check for parity with budget tests.
Task 4: complete (commits b4cf3b55..a7b7d859 [cd922e46 + a7b7d859 fix], review clean)
- Ruling: implementer's second commit (view revision = max over rows instead of last-row) accepted — matches repo CAS baseline COALESCE(MAX,0); empty table → 0 stays a valid expected-revision.
- Minor (deferred): duplicate SkillID in one save → opaque DB unique-constraint error instead of AgentSkillDefaultsInvalid; add dedup check when touching.
- Minor (deferred): clear-all save returns current+1 but next GET reports 0 (repo quirk); client tracking PUT-response revision gets one spurious conflict. Whole-branch review should triage.
- Minor (deferred): Enabled coerced via != 0 (2/-1 become enabled); Position unvalidated.
- Minor (deferred): skill.Status != 1 branch dead code (repo.Skill filters status=1); disabled-skill test actually hits NotFound path — behavior contract still satisfied.
- Minor (deferred): boolToInt single-use helper.
Task 5: complete (commits a7b7d859..6c9c3832, review clean)
- Ruling: aliases_types.go one-line alias (AgentSkillDefaultItem = app.AgentSkillDefaultItem) accepted — handlers never import internal/app (verified by reviewer grep), facade rule requires it.
- Minor (deferred): two route tests near-identical; table-driven would be tighter (brief mandated sibling style).
Ruling: Switching execution mode subagent-driven → native per user instruction ("你后面换native吧") mid Task 6. Tasks 1-5 remain SDD-reviewed commits (edb348e6..6c9c3832). Task 6 onward implemented inline per superpowers:executing-plans; task-scoped self-review gates still applied, final whole-branch review unchanged. If wrong: Tasks 6+ lose per-task independent review until the final review catches it.
Task 6: complete pending review (commit 6c9c3832..064101fc, native mode, focused tests green: ResolveRunSkills* 9 scenarios + CloudAgentConversationIDFor + existing TestCloudAgentSkillsLoadOnDemand/TestNativeSkill/TestAdminAgentSkill regressions; full internal/app suite NOT yet run)
- Note: test seeding gotcha for future tasks — skill fixtures must carry real PackageKey ZIP on dataDir + real SHA256 per file (validateSkillPackageSnapshot enforces); see cloud_agent_skill_selection_test.go seedSelectionSkillFor.
- Review pending: Task 6 self-review done; task reviewer + final whole-branch review still owed.
Task 7: complete (native, go test -ldflags=-linkmode=internal ./internal/skills/ -run Preset PASS; default linker blocked by cc1 DLL error 0xc0000135).
Task 9: complete (native, bun test ./test/admin-skill-defaults-section.test.ts PASS, bun run lint PASS, bun run typecheck PASS; dependencies installed from frozen lockfile).
Task 8: complete (native, go test with temporary external linker directory ./internal/app/ ./internal/handler/ -run AgentSkillDefaults|RegisterCanvasAPI PASS).
Ruling: While Task 8 database tests were blocked by DLL search failures, Task 9 implementation proceeded and committed first; interfaces stayed unchanged, both verified independently. Cost if wrong: commit ordering differs from plan, no runtime dependency mismatch.
Task 10: complete (native, bun test ./test/agent-settings-admin.test.ts 4 PASS; bun run typecheck PASS; bun run lint PASS).
Task 11: complete (native, bun test ./test/agent-skill-selector-ui.test.ts 2 PASS, bun run lint PASS, bun run typecheck PASS).
Task 12: Ruling: Spec and AGENTS require transform/opacity-only motion, overriding plan width transitions and persistent pulse. Labels fade/translate at measured natural width; running uses a stable primary ring. Reduced-motion labels appear below the row with no avatar movement. Cost if wrong: differs from plan animation detail, preserves requested reveal/spacing and accessibility.
Task 12: Ruling: Move layout test from src/lib to web/test; src is typechecked without bun:test types, so original plan location breaks build. Cost if wrong: test file location differs from plan, production API unchanged.
Task 12: complete (native, bun test ./test/agent-subagent-layout.test.ts ./test/canvas-agent-subagent-list.test.tsx 5 PASS; bun run typecheck PASS; bun run lint PASS).
Task 13: complete (OpenAPI 3 operations + HTTP, schema, feature, code map and pending acceptance docs; staged diff --check clean).

## Final review and concentrated fix pass

- Independent reviewer: branch_review, read-only, whole branch cd13b79b..ae6d6f16; 0 Critical, 8 Important. Task 6 review is covered here.
- Findings accepted: uninstalled default Pi authorization; rejected Run baseline writes; missing frozen global metadata; user version drift; clear revision/ABA; cross-instance CAS; lost budget details; incorrect/incomplete default UI.
- RED evidence: review-red.log reproduces six backend defects; web-review-red.log reproduces real budget details and absent effective-default helper. The original native-name assertion exposed a regression during GREEN; the user-library display contract was restored before final verification.
- Fixes: trusted persisted source chooses frozen global/public or installed/user reads with ZIP/version/hash validation; global names/descriptions read frozen SKILL.md; user versions stay pinned; baseline commits in the holding task/credit/Run transaction; system_settings revision row survives clear and serializes CAS; repeatable-read snapshot keeps rows/revision coherent; UI uses frozen Run source for existing conversations and complete summary references for new ones; budget details survive title and chat text.
- Extra integration coverage: 20 uninstalled defaults through actual CreateCloudAgentRun/ClaimPiAgent/PiSkillFile; failed continuation retains baseline; independent file-backed SQLite connections race at revision 0; default reference card needs no fabricated Skill or market page.
- Final: Ruling: supersede the earlier benign-CAS and empty-list revision rulings — the existing platform supports multiple service instances, so repository write-lock serialization and durable revision are required. Cost if wrong: lock contention/driver differences; PostgreSQL live integration is still unverified.
- Final: Ruling: repeated user SkillIDs retain their previously frozen version — the browser sends all selected IDs each turn, so ID-only repetition cannot mean a version upgrade. Global→user upgrade and new IDs remain allowed. Cost if wrong: upgrading an already-user skill requires a new conversation until an explicit version-update contract exists.
- Final: Ruling: user display metadata preserves the current installed-version contract; older pinned versions derive metadata from their frozen entry — version rows do not store a separate display snapshot. Cost if wrong: old user display names can reflect entry metadata rather than the original library title; version/hash stay frozen.
- Final: Ruling: one scoped re-review follows the concentrated fix commit as the user-supplied handoff explicitly requires — overrides executing-plans' default no-re-review rule. Cost if wrong: one additional bounded review.
- Final: Ruling: leave unrelated homepage/appearance failures in place — e89abfeb isolated five-file baseline is 58/58; merged canary 20689871 isolated baseline is 47/58 with exactly the same 11 failures as the current full frontend suite. Cost if wrong: branch-wide green-suite/merge gate remains unmet until that separate work is repaired.
- Final: Ruling: P2 Workspace, P3–P5 Crew/SSE remain outside this plan — avatar source stays empty, as designed. Cost if wrong: no live crew display until later phases. Real hover/keyboard/screen-reader/browser appearance acceptance was not performed.
- Final: minor resolved while touching required fixes: caller rows copied, skill_id sort tie-break, duplicate IDs return typed invalid error, default refresh request sequencing.
- Final: minor (deferred): enabled 2/-1 coercion and unnormalized position; dead disabled-skill branch; unconstrained Source string; low-selectivity secondary indexes; trim documentation and empty-ID error-class test; single-use bool helper; near-duplicate route tests; old cosmetic method-count note.
- Final: minor (deferred): avatar expansion may change row height under ordinary Windows scrollbars/reduced motion. Current data source is empty; verify/fix alongside P3 integration.
- Final verification: frontend focused 16/16 PASS; lint PASS; actual rejected-continuation budget case PASS after assigning a fresh idempotency key in the test. Full suites/build running; exact outcomes will be carried into the delivery report before commit.
- Task 14: verification complete at 392426c2 — backend full go test (external linker workaround) PASS, internal/app 568.122s; frontend full 2154 PASS / 11 FAIL, all 11 reproduced at merged canary 20689871; final focused 16/16, lint and build PASS. Committed Go LF blobs: 35, all gofmt-clean. Whole-branch merge gate remains unmet because the inherited frontend failures remain.
- Final fixes committed: 392426c2. All eight Important fixes covered by final backend suite and focused frontend tests. Scoped re-review requested from the same branch_review agent; awaiting result. Independent PostgreSQL live integration and real browser UI checks remain unverified.
- Scoped re-review completed by branch_review: ae6d6f16..392426c2, all 8 Important addressed, no new Critical/Important. Repair scope accepted, branch merge gate remains red for the inherited 11 frontend failures. Final backend exit 0 is confirmed separately by the root executor.
- Final: Ruling: archive only this plan's ignored SDD directory instead of destroying evidence/toolchain — removes its active SDD path as requested while preserving reproducibility and rollback. Cost if wrong: ignored archive occupies local disk until the user chooses to discard it.
