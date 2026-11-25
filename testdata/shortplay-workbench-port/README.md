# Shortplay workbench port baseline (W01)

Replayable evidence for the independent shortplay workbench port. The pinned
donor is `yyvan1/open-ai-canvas@13731aae6c4cefadc2a1719e3e7e59b22e249f94`;
the target baseline is `submap@0a601d2f8`.

## Evidence matrix

| Area | File | Evidence | What it proves |
| --- | --- | --- | --- |
| HTTP contract, pages, jobs, agent tools, tables, migrations | `donor/inventory.json` | source-observed (collector run) | The donor registers 13 shortplay routes, 3 workbench routes, 4 job kinds, 4 agent tools, 6 shortplay tables and migrations 88/91/92/110/111 |
| Two episodes, one shared character, one derived look | `sample/two-episodes.json` | source-observed rules, replayed through the target repository | Episode 1 uses the base character only; episode 2 reuses it plus a derived `战袍` look that falls back to the parent's name/voice/description and carries its own appearance and image |

`donor/inventory.json` is generated, never hand-edited:

```powershell
python scripts/shortplay-port/capture_workbench_donor.py `
  --donor-root .local/donor-13731aae6 `
  --out testdata/shortplay-workbench-port/donor
```

The collector refuses a dirty donor tree or a HEAD other than the pinned
commit, and exits non-zero if the sample breaks a donor rule quoted below.

## What W01 observed

### HTTP (13 routes, `/api` prefix)

| Action | Method and path | Body limit | Service |
| --- | --- | --- | --- |
| List works | `GET /shortplays` | 0 | `ListShortplays` |
| Create work (whole script in one request) | `POST /shortplays` | 32 MiB | `CreateShortplay` |
| Read detail (`:id` is the projects row id) | `GET /shortplays/:id` | 0 | `ShortplayDetail` |
| Read assets (`:id` accepts shortplayId or projectId) | `GET /shortplays/:id/assets` | 0 | `ShortplayAssets` |
| Create or update asset | `POST /shortplays/:id/assets` | 1 MiB | `UpsertShortplayAsset` |
| Save settings (whole object, not a field patch) | `PATCH /shortplays/:id/settings` | 64 KiB | `UpdateShortplaySettings` |
| Queue reference images | `POST /shortplays/:id/assets/images` | 1 MiB | `QueueShortplayAssetImages` |
| Queue voice samples | `POST /shortplays/:id/assets/audio` | 1 MiB | `QueueShortplayAssetAudio` |
| Adopt a free-canvas card into an episode | `POST /shortplays/:id/adopt` | 1 MiB | `AdoptShortplayAsset` |
| Ensure the episode canvas (idempotent) | `POST /shortplays/:id/episodes/:episodeId/canvas` | 0 | `EnsureShortplayEpisodeCanvas` |
| Queue asset extraction | `POST /shortplays/:id/extract` | 0 | `QueueShortplayExtract` |
| Write a canvas-generated image back | `POST /shortplays/by-canvas/:canvasId/assets/:assetId/image` | 64 KiB | `ApplyShortplayAssetImage` |
| Resolve canvas to episode (`{linked:false}` when free) | `GET /shortplays/by-canvas/:canvasId` | 0 | `ShortplayByCanvas` |

Deleting a work reuses `DELETE /api/projects/:id`; the donor cascades the
shortplay tables when `project.Type == "shortplay"`. That is why W02 must keep
the four-table cascade when the project type is registered.

### Pages and project type

- Routes: `/shortplays`, `/shortplays/new`, `/shortplays/:projectId`, with a
  sidebar entry. Page modules: `list`, `create`, `detail`, `detail-model`,
  `detail-parts`, `cast-view`, `inbox-view`, `settings-view`, `settings-model`.
- Project type is the literal `shortplay`; there is no shared enum. The detail
  page takes the projects row id, while domain calls take `shortplayId`.

### Jobs

Four kinds, five statuses, four priority tiers:

| Kind | Dedupe key | Priority | Dispatch file |
| --- | --- | --- | --- |
| `extract` | `extract|shortplayId` | 60 | `shortplay_extract.go` |
| `asset_image` | `asset_image|shortplayId|assetId` | 40 | `shortplay_asset_image.go` |
| `asset_audio` | `asset_audio|shortplayId|assetId` | 40 | `shortplay_asset_audio.go` |
| `asset_sync` | `asset_sync|canvasId` | 20 | `shortplay_asset_sync.go` |

The unique index only covers unfinished rows, so a finished key may be queued
again. The scheduler ticks every 3s, leases `shortplay:<id>` for 2 minutes,
processes 20 works per tick with 2 concurrent runs, and **plans nothing by
itself**: every job comes from an explicit entry point.

### Agent

Four tools, all `RequiresDeclaration` + `RequiresShortplay` (so a plain canvas
agent never sees them): `shortplay_list_assets` (read), `shortplay_asset`
(write), `shortplay_asset_image` (media, approval + billing), `shortplay_asset_audio`
(media, approval + billing). Asset ids are per-project integers starting at 1;
the id is the composite `(shortplayId, id)`.

### Tables and migrations

`shortplays`, `shortplay_episodes`, `shortplay_assets`, `shortplay_jobs`,
`shortplay_asset_images`, `shortplay_asset_audios`, next to the `projects`
container row. Donor migrations: 88 (`shortplay_tables`), 91
(`shortplay_asset_voice`), 92 (`shortplay_asset_int_id`, string id →
project-scoped int id), 110 (`shortplay_derived_asset`: image/audio history
tables, five asset columns, backfill `image_resource_id` into image v1, drop the
old column), 111 (`shortplay_asset_audio_status`).

The target is at schema 111 (`CurrentSchemaVersion`), so the port starts at 112.

### Derived-look rules used by the sample

- `DerivedFrom != 0` marks a derived row; it owns `variantName`, appearance and
  its own render rule, and falls back to the parent for kind, name, aliases,
  voice, description and render rule (`ResolveShortplayAsset`).
- The display name `{parent}-{variant}` is synthesised at read time, capped at
  20 runes, and the variant may not contain `-`.
- `ResolveShortplayLook` tries the display name, then `variantName`, then the
  base name/aliases (returns 0), then the base-look vocabulary (returns 0), and
  otherwise falls back to the base row silently.
- A derived image records `sourceImageId`; it is stale when the parent's current
  image moved on. A derived row with `currentAudioId == 0` inherits the parent
  audio and is never stale.
- The builtin render rule is applied by the image job, not by the read model:
  `参考图1是角色既有形象，保持五官、脸型与体态完全一致，只改服装、发型与配饰。`

## Runtime versus inference

- **Run now**: the collector above (source observation; it executed and its
  output is the checked-in `inventory.json`), the sample validation it performs,
  and the W02 replay of the sample through the target repository
  (`TestShortplayWorkbenchSampleReplay`, real SQLite rows).
- **Not run**: no donor code was executed in W01, no model channel was called,
  and no browser walkthrough was recorded. Those are marked `pending` inside
  `inventory.json`.
- **Not production data**: the sample is a schema-faithful reconstruction from
  donor types and rules, not an exported project. Its values were authored for
  the fixture; only the shapes and rules come from the donor.
- W02 replay covers SQLite in-process and PostgreSQL 17 in an isolated schema (empty database, old-drama upgrade, composite asset key, unfinished-job dedupe index, concurrent create). The HTTP layer is still open (W03).

## Replay commands

```powershell
# Rebuild the inventory and validate the sample
python scripts/shortplay-port/capture_workbench_donor.py `
  --donor-root .local/donor-13731aae6 `
  --out testdata/shortplay-workbench-port/donor
```

From W02 on, target-side tests read `sample/two-episodes.json` directly:

```powershell
cd backend
go test -count=1 ./internal/repository -run 'Shortplay'
go test -count=1 ./internal/database -run 'Shortplay'
```

`TestShortplayWorkbenchSampleReplay` writes the sample through
`CreateShortplayProject` / `CreateShortplayAsset` and reads it back from SQLite;
`TestMigrateShortplayWorkbenchOnEmptyDatabase`, `...UpgradesOldDramaCopy` and
`TestShortplayDedupeIndexOnlyCoversUnfinishedJobs` cover the schema side.
