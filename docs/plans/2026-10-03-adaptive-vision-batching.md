# 自适应多图识图实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Agent 识图链路支持按图片数量和视觉成本动态分批、自适应处理供应商容量错误，并在不可恢复错误时进入终态。

**Architecture:** 保留 `visionSupported` 能力开关，增加管理员安全封顶和按模型/渠道保存的动态预算状态。运行时先复用 SHA 视觉缓存，再将未缓存图片组成可恢复的批次；容量错误二分拆分，其他错误直接失败。批次计划、结果和探测状态写入 checkpoint，确保租约恢复不重复发送已完成图片。

**Tech Stack:** Go 1.25、Gin/GORM、现有 Cloud Agent checkpoint/repository、provider protocol、React/TypeScript 管理后台、Go test、Bun/Vite。

**Spec:** `docs/design/adaptive-vision-batching-design.md`

## Global Constraints

- `visionSupported` 是模型是否支持 Agent 识图的必需声明。
- `visionMaxBatchImages` 和 `visionMaxBatchCost` 为可选安全封顶，`0` 表示未知。
- 原图只接受账号资源引用，并以服务端 SHA-256 绑定视觉摘要。
- 只对容量类错误自适应拆分；鉴权、余额、模型不存在、参数、安全和内部错误直接失败。
- 已完成、失败、取消和完成的运行不能再次被租约领取。
- 不改变图片生成、视频参考图和普通图片模型的供应商限制。

## Review Focus

- 同一轮多个工具调用必须保持 `assistant(tool_calls) → tool×N → user(image batch)` 顺序；由 Task 3 的配对回归测试覆盖。
- 视觉缓存 SHA 变化只能使对应图片重新入队；由 Task 2 的缓存/变化测试覆盖。
- 批次在容量 400 后拆分且不重复成功图片；由 Task 4 的自适应重试测试覆盖。
- 单张容量错误必须终止运行且租约不可重新领取；由 Task 5 的终态测试覆盖。
- 管理员封顶和动态上限冲突时必须取更小值；由 Task 1 和 Task 4 的预算测试覆盖。

---

### Task 1: 扩展模型能力和视觉预算合同

**Files:**
- Modify: `backend/internal/app/model_capability.go`
- Modify: `web/src/lib/model-capabilities.ts`
- Modify: `web/src/pages/admin/logical-models/model-routing-capabilities.tsx`
- Test: `backend/internal/app/model_capability_test.go`

**Interfaces:**
- Produces `TextCapabilityConfig.VisionMaxBatchImages`、`VisionMaxBatchCost` 及前端对应可选字段。
- 保持旧配置中 `VisionSupported == nil` 的兼容推断。

- [ ] **Step 1: Write failing tests** for JSON round-trip, zero-as-unknown, negative/out-of-range validation, and capability editor serialization without a text image-count input.
- [ ] **Step 2: Run** `go test ./internal/app -run 'Test.*Capability' -count=1`; expect failures for missing fields/validation.
- [ ] **Step 3: Implement** the fields, defaults, validation, merge logic, and admin serialization. Do not reuse `references.maxImages` as the Agent batch limit.
- [ ] **Step 4: Run** the focused capability tests and `cd web && bun run build`.
- [ ] **Step 5: Commit** `feat(agent): add adaptive vision budget capability contract`.

### Task 2: Add visual cost calculation, cache-aware batch planning, and checkpoint state

**Files:**
- Modify: `backend/internal/app/cloud_agent_runtime.go`
- Modify: `backend/internal/app/cloud_agent_vision.go`
- Modify: `backend/internal/app/cloud_agent_vision_delivery_test.go`
- Create or modify: `backend/internal/app/cloud_agent_vision_batch_test.go`

**Interfaces:**
- Produces pure helpers for `visionImageCost`, `visionBatchBudget`, and deterministic batch planning.
- Extends runtime checkpoint state with batch ID, queued items, completed items, attempt count, budget snapshot, and error class.

- [ ] **Step 1: Write failing tests** for cost tiers, deterministic ordering, deduplication by `(nodeID, SHA)`, cache-only summaries, and splitting by both image count and cost.
- [ ] **Step 2: Run** `go test ./internal/app -run 'TestCloudAgentVisionBatch|TestCloudAgentVisionCache' -count=1`; expect failures for missing planner/state.
- [ ] **Step 3: Implement** cost calculation and planner. Start with probe image limit `4`, enforce positive minimum batch size `1`, and make all planner output checkpoint-serializable.
- [ ] **Step 4: Run** the focused batch and delivery tests; verify cached entries produce no `image_url` part.
- [ ] **Step 5: Commit** `feat(agent): plan adaptive vision batches with checkpoint state`.

### Task 3: Replace single-image staging with multi-batch message delivery

**Files:**
- Modify: `backend/internal/app/cloud_agent_tools.go`
- Modify: `backend/internal/app/cloud_agent_runtime.go`
- Modify: `backend/internal/app/cloud_agent_vision.go`
- Modify: `backend/internal/app/cloud_agent_vision_pairing_test.go`
- Modify: `backend/internal/app/agent-tool-descriptions.md`

**Interfaces:**
- `canvas_inspect_image` accepts multiple calls in one model turn and returns per-image receipts.
- Delivery consumes the planner from Task 2 and emits one user image message per batch while preserving tool-message adjacency.

- [ ] **Step 1: Write failing tests** for four images in one turn, two planned batches, cached-plus-new mixed input, and checkpoint round-trip.
- [ ] **Step 2: Run** `go test ./internal/app -run 'TestCloudAgentVisionBatchKeepsToolResults|TestCloudAgentVisionDelivery' -count=1`; expect old one-image guard failures.
- [ ] **Step 3: Implement** queue admission, batch flush, stable batch IDs, and updated tool schema/description. Remove the single pending-image rejection while retaining loop guards per node/SHA.
- [ ] **Step 4: Run** pairing and delivery tests and inspect canonical message ordering.
- [ ] **Step 5: Commit** `feat(agent): deliver adaptive multi-image vision batches`.

### Task 4: Implement provider capacity classification and adaptive split retry

**Files:**
- Modify: `backend/internal/app/provider.go`
- Modify: `backend/internal/app/provider_text.go`
- Modify: `backend/internal/app/cloud_agent_vision.go`
- Create or modify: `backend/internal/app/cloud_agent_vision_retry_test.go`

**Interfaces:**
- Produces `classifyVisionBatchError(error) VisionBatchErrorClass` and a retry coordinator that returns updated batch state or a terminal error.
- Capacity errors are split recursively; non-capacity errors are returned unchanged.

- [ ] **Step 1: Write failing tests** for too-many-images, context overflow, payload-too-large, timeout/OOM, auth, billing, safety, and unknown errors.
- [ ] **Step 2: Run** `go test ./internal/app -run 'TestVisionBatchErrorClassification|TestVisionBatchRetry' -count=1`; expect failures for missing classification/coordinator.
- [ ] **Step 3: Implement** conservative provider error matching using status/code/body fields, binary splitting for `N > 1`, dynamic-limit updates only after success, and global retry ceilings.
- [ ] **Step 4: Run** retry tests plus provider protocol tests; ensure no retry occurs for non-capacity errors.
- [ ] **Step 5: Commit** `feat(agent): split vision batches on capacity errors`.

### Task 5: Make terminal failures durable and non-reclaimable

**Files:**
- Modify: `backend/internal/app/cloud_agent_runtime_tools.go`
- Modify: `backend/internal/app/cloud_agent_runtime.go`
- Modify: `backend/internal/repository/*cloud_agent*` as needed for status predicates
- Create or modify: `backend/internal/app/cloud_agent_failure_test.go`

**Interfaces:**
- Produces a single terminal transition that writes `run_failed`, error class, batch ID, and provider detail without exposing secrets.
- Lease acquisition rejects terminal statuses.

- [ ] **Step 1: Write failing tests** for single-image capacity failure, repeated lease acquisition, checkpoint restart, and idempotent failure event emission.
- [ ] **Step 2: Run** `go test ./internal/app -run 'TestCloudAgent.*Fail|TestCloudAgentLease.*Terminal' -count=1`; expect failures around reclaim behavior.
- [ ] **Step 3: Implement** atomic status transition and repository lease predicate; preserve user-visible failure text and machine-readable reason.
- [ ] **Step 4: Run** failure, lease, and focused vision suites.
- [ ] **Step 5: Commit** `fix(agent): make vision capacity failures terminal`.

### Task 6: Full verification, documentation, and versioning

**Files:**
- Modify: `VERSION`
- Modify: `docs/content/docs/progress/pending-test.mdx` if full-suite limitations remain
- Test: repository Go and web build commands

- [ ] **Step 1: Run** `gofmt` and `git diff --check`.
- [ ] **Step 2: Run** `go test ./... -timeout 20m` and `cd web && bun run build`.
- [ ] **Step 3: If full tests are blocked or time out, record the exact package and reason in `pending-test.mdx`; do not claim full pass.
- [ ] **Step 4: Update** `VERSION` using the pre-bump canary commit short SHA required by `AGENTS.md`.
- [ ] **Step 5: Review** the complete diff for scope, secrets, checkpoint compatibility, and untracked artifacts.
- [ ] **Step 6: Commit** `chore(agent): verify adaptive vision batching`.

