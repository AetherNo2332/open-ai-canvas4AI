# Isolated Canvas Agent acceptance

This fixture replaces only the OpenAI-compatible model protocol. The real Pi
worker, Go backend and SQLite persistence execute the acceptance. The built
frontend route is checked; editor behavior is covered separately by frontend tests.
The script uses authenticated public APIs and observes tool receipts, approvals,
canvas reloads and SSE. It does not write or inspect SQLite directly.

Use the project `canvas-agent-parity`, loopback port 3040, its four dedicated
image tags and its dedicated data volume. Existing 3030 services must remain
unchanged. Check port availability before starting the project.

Create the ignored `.local/agent-parity.env` with a fresh
`PARITY_INTERNAL_TOKEN`, `PARITY_BUILD_COMMIT` ending in
`-canvas-agent-parity`, and a UTC `PARITY_BUILD_TIME`. The token is shared only
between this project's backend and worker. Do not reuse production credentials.

```powershell
docker compose --env-file .local/agent-parity.env -p canvas-agent-parity -f docker-compose.agent-parity.yml config --images
docker compose --env-file .local/agent-parity.env -p canvas-agent-parity -f docker-compose.agent-parity.yml build
docker compose --env-file .local/agent-parity.env -p canvas-agent-parity -f docker-compose.agent-parity.yml up -d --no-build
python scripts/verify-agent-canvas-parity.py --restart
```

Before `up`, rebuild or restart, review the four service names, dedicated images,
3040 binding and volume. `--restart` restarts only this project's worker during
an approval and its backend/worker after mutations, then restarts its web gateway to refresh the
statically resolved upstream address. It preserves the volume and
checks the existing 3030 container identities and start times.

The script writes redacted evidence to `.local/agent-parity-acceptance.json`.
Disposable local login credentials are stored separately in ignored
`.local/agent-parity-account.json`. Never attach either credential file or the
environment file to a report.

The `backend-test` build target runs CGO integration tests. Its temporary test
databases use tmpfs to avoid Docker overlay filesystem initialization overhead;
the runtime acceptance always uses the real persistent named volume.
`backend-history-test` provides a focused multi-step undo/redo test target.

Run the bounded regression stages against the same source and worker image:

```powershell
docker build --progress=plain --target backend-test -f tools/agent-parity-backend.Dockerfile .
docker build --progress=plain -f tools/agent-parity-worker-test.Dockerfile --output type=cacheonly .
```

Use the same `GO_BUILDER_IMAGE`/`GOPROXY` build arguments as the runtime build
when using the optional offline cache. The worker test stage reuses the built
`canvas-agent-parity-agent:working` runtime dependencies, compiles current
source and runs eight selected test files serially with a bounded file timeout.
This target assumes the worker image has already been built.

On a dependency download failure, inspect the original error, check domestic
module/npm mirrors and then a usable local proxy. `GO_BUILDER_IMAGE` can use a
dedicated image containing the host Go download archive cache, and `GOPROXY=off`
verifies that all module dependencies are available offline. Avoid mounting an
empty module-cache volume over an inherited populated cache.

The model stub has no published host port. The script controls scenarios through
the dedicated stub container. Gates permit authenticated manual edits while a
model step is waiting, making snapshot conflicts deterministic. Forced calls to
omitted read-only tools exercise the real worker rejection path.

The API script waits for backend readiness, respects the 500-event page limit,
and attempts run creation at most three times only for HTTP 500 using the same
idempotency key. It records every retry in `transientApiRetries`; all other API
failures stop the script. Reports include expected rejected tool calls and must
not be described as proving that no runtime errors occurred.

Run completion waits stop after 120 seconds without a new public step or event
sequence, with a fixed 300-second total limit. Heartbeats and `updatedAt` changes
do not reset that watchdog. Reports record each run's `terminalWait` timing and
observed progress. Health checks and protocol gates retain their 120-second
limit. This permits observed lease recovery without retrying failed tool calls
or suppressing API errors; internal phase/storage errors must still be reported.

To finish unexecuted tail checks after a recorded timeout, preserve the original
report and use a separate continuation output:

```powershell
python scripts/verify-agent-canvas-parity.py --restart --resume-from-report .local/agent-parity-acceptance-final-third-failure.json --output .local/agent-parity-continuation.json
```

This mode checks the existing multi-step history run through public APIs without
executing it again, then runs the UI undo API, persisted reload, SSE ordering and
resume checks. It reads the existing test channel and compares image IDs, build
metadata, schema and 3030 baseline with the source report. Its `sourceEvidence`
keeps the earlier failure explicit; a passing continuation is staged evidence,
not a continuously passing full run.
