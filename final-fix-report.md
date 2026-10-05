# Pi Native Skills Review Fix Report

## Scope

The review wave covered native Skill loading, frozen snapshots, Pi recovery, tool admission, read authorization, failed-read semantics, and text/binary validation. The implementation is split into three commits: runtime Agent/Go protocol, Web projection, and documentation/version metadata.

## Findings

1. Mixed native/canvas tool batches: fixed by admitting the canvas subset while native reads are paired separately. Covered by runner and Go admission tests.
2. Interrupted native reads: fixed by rebuilding the durable Pi branch and pairing pending reads after restart. Covered by `runner.test.ts` recovery cases and Go Pi tests.
3. Worker-directory changes invalidating in-flight tasks: fixed by normalizing generated worker paths before identity comparison. Covered by restart fingerprint tests.
4. Frozen snapshot failures after claim: fixed with deterministic terminal cleanup and billing-safe failure handling. Covered by native snapshot and lifecycle tests.
5. Entry reads bypassing Go authorization: fixed by routing every model-visible read through the bridge and rechecking enablement. Covered by revocation tests.
6. Failed reads being counted as success or being uncheckpointable: fixed with explicit failed-read receipts and retry-preserving transport errors. Covered by native read and runner tests.
7. Frontmatter EOF panic: fixed with exact delimiter handling for EOF and CRLF. Covered by package validation tests.
8. Binary content accepted as text: fixed with UTF-8, NUL, and control-byte validation on frozen content. Covered by native package tests.

## Verification

- Agent: `npm test` -> 107 passed, 0 failed.
- Go: `go test ./internal/app ./internal/handler ./internal/skills` -> all packages passed.
- Web: `bun run build` -> passed; existing large-chunk warnings remain.
- Web focused: `bun test test/agent-api-reliability.test.ts test/agent-run-state.test.ts` -> 25 passed, 0 failed.
- `git diff --check` -> passed before each commit.

## Remaining concerns

- Compose rebuild, browser Skill read, SSE refresh/recovery, and real provider acceptance still need to run against the committed tree.
- The full Go repository has a Windows-specific `internal/hostupdate` permission-mode test outside this change; focused packages are green.
- No production deployment or push was performed.
