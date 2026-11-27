# Shortplay donor baselines

## Workbench inventory (W01)

`capture_workbench_donor.py` reads the pinned donor tree and writes the
workbench inventory plus validates the two-episode sample:

```powershell
python scripts/shortplay-port/capture_workbench_donor.py `
  --donor-root .local/donor-13731aae6 `
  --out testdata/shortplay-workbench-port/donor
```

It never runs donor code: every inventory row is `source-observed`. It refuses a
dirty donor tree or a HEAD other than the pinned commit, and exits non-zero when
the sample breaks a donor rule (project-scoped asset ids, derived-look fallback,
job dedupe key forms). The evidence matrix lives in
`testdata/shortplay-workbench-port/README.md`.

## Canvas submap flow (S01)

`capture_donor_baseline.py` replays the built-in storyboard submap flow against
the donor at commit `13731aae6c4cefadc2a1719e3e7e59b22e249f94`:

```powershell
python scripts/shortplay-port/capture_donor_baseline.py `
  --donor-root "$env:TEMP\shortplay-donor-13731aae" `
  --out testdata/shortplay-canvas-port/donor
```

The script injects `donor_capture_test.go.txt` into the donor's
`backend/internal/app` package, runs only `TestS01CaptureDonorBehavior`, and
removes the file afterwards. The donor worktree must be clean and at the pinned
commit.

Generated evidence:

- `initial.json`, `created.json`, `edited.json`, `duplicated.json` and
  `reopened.json` are runtime snapshots from an isolated SQLite database.
- `operations.json` contains each command, before/after snapshots, field order,
  projected edges and the raw command receipt summary.
- `provenance.json` records the donor identity and capture method.

The collector normalizes only dynamic timestamp-valued fields. It does not
normalize IDs, reference order, scope IDs or document field order. The target
legacy and ZIP samples are separate fixtures and are marked in their own
provenance files.
