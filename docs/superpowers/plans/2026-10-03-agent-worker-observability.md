# Agent + Worker 可运营观测闭环 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Agent + Worker 集群建立统一的 OpenTelemetry 观测契约、运行指标、分布式 Trace、质量/成本评估、Grafana 运营面板和 Web 管理员运行总览。

**Architecture:** 后端和 Pi Agent 只产生统一的结构化观测数据，通过 OTLP 发送到 Collector。Collector 将指标发送到 Prometheus、Trace 发送到 Tempo、GenAI 观测和评估数据发送到 Langfuse；Grafana 与 Web 管理员后台只读取聚合结果和受控的 Trace 链接。观测后端不可用时，任务执行继续，应用侧使用有界队列和快速降级。

**Tech Stack:** Go 1.25/Gin/GORM、Node.js 22/TypeScript/Pi、OpenTelemetry OTLP、Prometheus、Tempo、Grafana、Langfuse、Docker Compose、Bun tests、Go tests。

**Spec:** `docs/superpowers/specs/2026-10-03-agent-worker-observability-design.md`

## Global Constraints

- 不把提示词、工具参数、上传内容、API Key、Cookie 或签名 URL 写入指标标签。
- 不改变现有 Agent 运行状态机、任务权限和账务状态机的业务语义。
- 观测后端故障不得阻塞 Agent 任务；exporter 必须有超时、有界队列和降级路径。
- Web 管理员后台只通过后端管理员接口读取聚合数据，不暴露 Prometheus、Tempo、Langfuse 凭据或内部地址。
- 指标使用低基数标签；`task_id`、`run_id`、`trace_id` 只作为 Trace 属性或受控查询参数。
- 本地 Compose 默认启动路径保持不变，观测组件通过独立 profile/覆盖文件启用。
- 所有新增 Go/TypeScript 代码先写失败测试，再写最小实现；修改后同步文档、版本和待测记录。

## Review Focus

- 观测后端宕机或超时时，任务仍能完成且不会无限堆积 exporter 队列（Task 2/3/4 resilience tests）。
- 重试、取消、过期和上下文压缩不会丢失或错误复用 `trace_id`/`run_id`（Task 3/4 lifecycle tests）。
- 高基数 ID、用户正文和密钥不会进入指标标签、日志或 Langfuse 原文（Task 1/2 redaction tests）。
- Worker/模型调用成功但领域任务失败时，业务成功率仍记录为失败（Task 3/5 success semantics tests）。
- Web 管理后台在无数据、延迟、权限不足和暗色主题下不把缺失读数显示为 0 或健康（Task 6 UI/API tests）。

---

### Task 1: 建立跨 Go/TypeScript 的观测契约与脱敏规则

**Files:**
- Create: `backend/internal/observability/contracts.go`
- Create: `backend/internal/observability/contracts_test.go`
- Create: `agent/src/observability-contract.ts`
- Create: `agent/test/observability-contract.test.ts`
- Create: `docs/content/docs/backend/agent-observability.mdx`
- Modify: `docs/content/docs/backend/code-map.mdx`

**Interfaces:**
- Produces `ObservationIdentity{TaskID, RunID, TraceID, SessionID, AgentVersion, PromptVersion, Model}` and span/event names defined by the spec.
- Produces `SanitizeAttributes(input map[string]any) map[string]any` and a shared low-cardinality metric label allowlist.
- Produces explicit task status mapping for `queued`, `claimed`, `running`, `waiting_model`, `waiting_tool`, `compacting`, `completed`, `failed`, `cancelled`, `expired`.

- [ ] **Step 1: Write failing contract tests** for status mapping, trace identity propagation, allowed labels, redaction of prompt/tool arguments/secrets, and unknown status handling.
- [ ] **Step 2: Run tests to verify failure.**
  - Run: `cd backend && go test ./internal/observability -run 'Test(Observation|Status|Sanitize)'`
  - Run: `cd agent && npm test -- --test-name-pattern='observability contract'`
  - Expected: FAIL because the contract package and functions do not exist.
- [ ] **Step 3: Implement the minimal Go and TypeScript contracts** with the exact field names and enum values from the spec; keep IDs out of metric labels and preserve unknown values as `unknown` plus an error marker.
- [ ] **Step 4: Run the focused tests and documentation link check.**
- [ ] **Step 5: Commit** `docs(observability): define Agent Worker observation contract`.

### Task 2: Add backend OTLP provider, metrics, trace lifecycle and failure isolation

**Files:**
- Create: `backend/internal/observability/provider.go`
- Create: `backend/internal/observability/metrics.go`
- Create: `backend/internal/observability/traces.go`
- Create: `backend/internal/observability/provider_test.go`
- Modify: `backend/go.mod`
- Modify: `backend/internal/app/service.go` (or the existing service constructor)
- Modify: `backend/cmd/server/main.go`
- Modify: `.env.example`

**Interfaces:**
- Produces an application-owned `observability.Provider` with `Start(ctx)`, `Shutdown(ctx)`, `Meter()`, `Tracer()` and `RecordTaskOutcome(...)`.
- Reads OTLP endpoint, sampling, queue size, timeout and redaction settings from explicit environment variables.
- Exposes metrics for worker up/busy, queue depth/oldest age, throughput, errors, retries, success, latency, tool calls, steps, LLM calls/tokens and costs.

- [ ] **Step 1: Write failing tests** for disabled-by-default configuration, OTLP timeout/degraded mode, low-cardinality labels, histogram boundaries and cost omission when price is unknown.
- [ ] **Step 2: Run `cd backend && go test ./internal/observability -run 'TestProvider|TestMetric|TestOTLP'` and verify failure.**
- [ ] **Step 3: Add the OpenTelemetry Go dependencies and implement the provider** with batch/span processors, bounded queues, fast shutdown and no request-path error propagation.
- [ ] **Step 4: Wire provider lifecycle into server startup/shutdown** without changing the default Compose behavior.
- [ ] **Step 5: Run focused tests and `go vet` for the package.**
- [ ] **Step 6: Commit** `feat(observability): add backend OTLP provider and metrics`.

### Task 3: Instrument backend queue, Worker and Cloud Agent lifecycle

**Files:**
- Create: `backend/internal/observability/task_observation.go`
- Create: `backend/internal/observability/task_observation_test.go`
- Modify: `backend/internal/app/task_worker.go`
- Modify: `backend/internal/app/worker_runtime.go`
- Modify: `backend/internal/app/cloud_agent_runtime.go`
- Modify: `backend/internal/app/cloud_agent_completion.go`
- Modify: `backend/internal/app/cloud_agent_context_compaction.go`
- Modify: `backend/internal/app/cloud_agent_pi_bridge.go`
- Modify: related existing worker/cloud-agent tests

**Interfaces:**
- Consumes Task 1 identity/status and Task 2 provider.
- Produces root `task.lifecycle`, `queue.wait`, `worker.claim`, `agent.execute`, `context.compaction`, and `task.finish` spans.
- Records queue wait, claim/lease, execution, retry, cancellation, expiry, compaction, final business outcome and failure reason.

- [ ] **Step 1: Add failing lifecycle tests** covering normal success, tool/model failure, retry, lease expiry, cancellation, compaction and worker shutdown; assert span names, parentage, final status and counters.
- [ ] **Step 2: Run the relevant Go tests and verify failure.**
  - Run: `cd backend && go test ./internal/app -run 'TestCloudAgent|TestTaskWorker|TestWorkerRuntime|Test.*Observ'`
- [ ] **Step 3: Instrument queue claim and worker execution** with one propagated identity; keep task business status as the source of truth.
- [ ] **Step 4: Instrument LLM/tool/compaction boundaries** and record token/cost data only after the existing trusted usage/price parsers succeed.
- [ ] **Step 5: Verify failure isolation** by injecting an exporter error and asserting task completion is unchanged.
- [ ] **Step 6: Run focused tests and commit** `feat(observability): trace Agent Worker task lifecycle`.

### Task 4: Instrument Pi Agent and Worker pool

**Files:**
- Create: `agent/src/observability.ts`
- Create: `agent/test/observability.test.ts`
- Modify: `agent/src/runner.ts`
- Modify: `agent/src/bridge.ts`
- Modify: `agent/src/worker-pool.ts`
- Modify: `agent/src/pi-stream.ts`
- Modify: `agent/src/native-compaction.ts`
- Modify: `agent/src/event-scheduler.ts`

**Interfaces:**
- Consumes the propagated identity from the internal Agent protocol.
- Produces `agent.execute`, `llm.call`, `tool.call`, `context.compaction` child spans and step/tool/retry events.
- Never sends prompt or tool argument content as metric labels; raw GenAI content is opt-in and redacted before Langfuse export.

- [ ] **Step 1: Write failing Node tests** for parent trace propagation, one span per LLM/tool call, compaction span, worker busy transitions, retry linkage and exporter failure.
- [ ] **Step 2: Run `cd agent && npm test -- --test-name-pattern='observability|runner|worker|compaction'` and verify failure.**
- [ ] **Step 3: Implement the Agent-side provider/bridge adapters** using the same contract names as Go; preserve existing Pi protocol and error semantics.
- [ ] **Step 4: Add token usage, model version, tool category, step count and outcome attributes** from existing trusted runtime values.
- [ ] **Step 5: Run Agent build and focused tests.**
- [ ] **Step 6: Commit** `feat(observability): instrument Pi Agent execution`.

### Task 5: Add Langfuse adapter, quality evaluation and Golden Dataset

**Files:**
- Create: `backend/internal/observability/langfuse.go`
- Create: `backend/internal/observability/langfuse_test.go`
- Create: `backend/internal/evaluation/golden_dataset.go`
- Create: `backend/internal/evaluation/golden_dataset_test.go`
- Create: `backend/internal/evaluation/deterministic_checks.go`
- Create: `backend/internal/evaluation/deterministic_checks_test.go`
- Create: `docs/evaluation/golden-dataset-v1.jsonl`
- Create: `docs/content/docs/backend/agent-evaluation.mdx`
- Modify: `.env.example`

**Interfaces:**
- Produces versioned evaluation records containing `eval_version`, `dataset_version`, `judge_model`, deterministic checks, human feedback and score evidence.
- Sends only configured, redacted GenAI observations to Langfuse; unavailable price/quality values remain absent rather than zero.
- Separates business task success from quality score and Judge output.

- [ ] **Step 1: Write failing tests** for deterministic success/failure, Golden Case versioning, Judge result merge, user feedback aggregation and Langfuse-disabled behavior.
- [ ] **Step 2: Run `cd backend && go test ./internal/evaluation ./internal/observability -run 'Test(Golden|Deterministic|Langfuse|Quality)'` and verify failure.**
- [ ] **Step 3: Implement the Golden Dataset loader and deterministic checks** with explicit allowed/forbidden tools and assertions.
- [ ] **Step 4: Implement the Langfuse adapter** with bounded asynchronous delivery, redaction and version metadata.
- [ ] **Step 5: Add a repeatable offline evaluation command** that emits comparable success, P95, steps, token, cost/success and Judge distributions without writing user data back into the dataset.
- [ ] **Step 6: Run focused tests and commit** `feat(evaluation): add Agent quality and Golden Dataset pipeline`.

### Task 6: Add backend admin observability API

**Files:**
- Create: `backend/internal/app/admin_observability.go`
- Create: `backend/internal/app/admin_observability_test.go`
- Create: `backend/internal/handler/admin_observability.go`
- Create: `backend/internal/handler/admin_observability_test.go`
- Modify: `backend/internal/handler/routes.go`
- Modify: `docs/content/docs/backend/http-api.mdx`

**Interfaces:**
- Adds `GET /api/admin/observability/overview` for aggregated overview data and `GET /api/admin/observability/tasks/:id` for an authorized, redacted task detail/Trace link.
- Returns `{ code, data, msg, reason }` and explicit freshness/source state: `fresh`, `delayed`, `unavailable`, `empty`.
- Enforces administrator authorization in the service layer; never proxies vendor credentials to the browser.

- [ ] **Step 1: Write failing handler/service tests** for admin authorization, aggregation fields, freshness states, missing cost, redaction and Trace-link generation.
- [ ] **Step 2: Run focused Go tests and verify failure.**
- [ ] **Step 3: Implement service aggregation** from the metrics/trace query adapters, preserving the same metric definitions used by Grafana.
- [ ] **Step 4: Implement handlers and route registration** with pagination/limits for recent anomalies.
- [ ] **Step 5: Run API tests and update HTTP API documentation.**
- [ ] **Step 6: Commit** `feat(admin): expose Agent observability overview API`.

### Task 7: Build Web 管理员后台运行总览

**Files:**
- Create: `web/src/services/api/admin-observability.ts`
- Create: `web/src/pages/admin/observability/observability-page.tsx`
- Create: `web/src/pages/admin/observability/observability-page.css`
- Create: `web/src/pages/admin/observability/observability-page.test.tsx`
- Modify: `web/src/router.tsx`
- Modify: `web/src/pages/admin/components/admin-shell.tsx`

**Interfaces:**
- Consumes Task 6 API and exposes the four overview areas: Agent Overview, Worker Cluster, Agent Quality and Cost.
- Displays Worker up/busy, queue depth/oldest age, task success/error/retry, P50/P95/P99, tool success, steps, calls, tokens, cost, quality, alerts and redacted recent anomalies.
- Uses existing admin permissions, appearance tokens, dark mode, responsive layout, empty/delayed/unavailable states and Trace links.

- [ ] **Step 1: Write failing component tests** for complete data, empty data, delayed/unavailable state, forbidden response, dark theme token usage and Trace navigation.
- [ ] **Step 2: Run `cd web && bun test src/pages/admin/observability/observability-page.test.tsx` and verify failure.**
- [ ] **Step 3: Implement the API client and page** with no hard-coded colors, no vendor URLs, and no raw user content.
- [ ] **Step 4: Register the admin route/navigation entry** and verify permission gating.
- [ ] **Step 5: Run the focused test and `bun run build`.**
- [ ] **Step 6: Commit** `feat(web): add admin Agent observability overview`.

### Task 8: Add local/production observability Compose stack, dashboards and alerts

**Files:**
- Create: `docker-compose.observability.yml`
- Create: `docker/observability/otel-collector-config.yml`
- Create: `docker/observability/prometheus.yml`
- Create: `docker/observability/tempo.yml`
- Create: `docker/observability/grafana/dashboards/agent-overview.json`
- Create: `docker/observability/grafana/dashboards/worker-cluster.json`
- Create: `docker/observability/grafana/dashboards/agent-quality.json`
- Create: `docker/observability/grafana/dashboards/cost.json`
- Create: `docker/observability/grafana/rules/agent-alerts.yml`
- Modify: `.env.example`
- Modify: `README.md` and deployment documentation

**Interfaces:**
- Provides optional Compose services for Collector, Prometheus, Tempo, Grafana and Langfuse with named volumes and explicit credentials.
- Exposes only local/admin-bound ports by default; no vendor secret enters the web image or browser config.
- Uses the exact metric names and dimensions from Tasks 1–2 and the same freshness/Trace links as Task 6.

- [ ] **Step 1: Write config validation tests** for service names, profile isolation, required env vars, dashboard metric names, alert recovery clauses and no secret literals.
- [ ] **Step 2: Run `docker compose -f docker-compose.yml -f docker-compose.observability.yml config` and the config tests to verify failure before files exist.**
- [ ] **Step 3: Add the Compose override and collector pipelines** with bounded queues, memory limits, retry and sampling configuration.
- [ ] **Step 4: Add four dashboards and alert rules** for queue growth, oldest task, saturation, errors/retries, success, P95, token/cost spikes and Worker outage.
- [ ] **Step 5: Run local Compose smoke tests**: start the observability profile, send a synthetic task, verify Prometheus samples, Tempo trace and Langfuse-disabled degradation; stop the stack and verify the default stack remains unchanged.
- [ ] **Step 6: Commit** `build(observability): add local telemetry stack and dashboards`.

### Task 9: End-to-end validation, documentation and release bookkeeping

**Files:**
- Create: `backend/internal/observability/observability_integration_test.go`
- Create: `agent/test/observability-e2e.test.ts`
- Modify: `docs/content/docs/progress/pending-test.mdx`
- Modify: `docs/content/docs/progress/todo.mdx`
- Modify: `docs/content/docs/backend/code-map.mdx`
- Modify: `CHANGELOG.md`
- Modify: `VERSION`

**Interfaces:**
- Produces a repeatable local validation path for enqueue → claim → LLM → tool → compaction → finish, plus Admin Overview readback.
- Records known limits honestly: real external Langfuse/Judge quality requires configured credentials; no production deployment or paid model call is part of local validation.

- [ ] **Step 1: Write the end-to-end test** with a stub LLM/tool and assertions for one shared Trace, task success semantics, metrics, admin overview and missing-cost behavior.
- [ ] **Step 2: Run the test against local Compose and verify any failures before broad testing.**
- [ ] **Step 3: Fix only integration defects found by the test; do not weaken contract assertions.**
- [ ] **Step 4: Run the required checks:**
  - `cd backend && go test ./...`
  - `cd agent && npm test`
  - `cd web && bun run build && bun test src/pages/admin/observability/observability-page.test.tsx`
  - `docker compose -f docker-compose.yml -f docker-compose.observability.yml config`
  - `git diff --check`
- [ ] **Step 5: Update pending-test, code map, changelog and parenthesized internal version using the pre-bump commit short SHA.**
- [ ] **Step 6: Commit** `test(observability): validate Agent Worker operations loop`.

## Execution Order and Integration Notes

Tasks 1–4 are prerequisites for reliable data. Task 5 can proceed after the contract and trusted usage fields exist. Task 6 depends on the provider and aggregation shape but can use adapters/stubs during development. Task 7 depends on Task 6. Task 8 can be developed in parallel with Tasks 5–7 after metric names are frozen, but its smoke test must use the real application output. Task 9 runs last.

Do not add a separate `agent` business success definition in the frontend or Grafana. The backend task outcome and deterministic checks remain authoritative; all other systems consume that result.
