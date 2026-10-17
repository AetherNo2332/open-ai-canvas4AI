# 画布资产批量下载与节点命名 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add multi-select media downloads and make every canvas media download use the node title as its sanitized filename.

**Architecture:** Keep downloads browser-direct through `downloadBrowserMedia`; add pure filename/queue helpers around the existing `CanvasNodeData` shape; connect a sequential download queue to the current selection toolbar and the existing single-node editor callback. Batch state stays local to the canvas page/selection toolbar and never enters persisted canvas data.

**Tech Stack:** React 19, TypeScript, Bun tests, existing canvas tool registry, existing browser download service.

**Spec:** `docs/superpowers/specs/2026-10-16-canvas-asset-batch-download-design.md`

## Global Constraints

- 批量下载方式：直接触发多个单文件下载。
- 文件名：清洗后的节点标题加媒体扩展名，例如 `片头镜头.mp4`。
- 不新增 ZIP 或服务端打包接口。
- 不把视频、音频或大图片全部读入浏览器内存。
- 草稿、版本、调试记录等非媒体下载继续使用各自命名规则。

## Review Focus

- Illegal Windows filename characters and reserved device names must remain downloadable.
- Same titles with different extensions must not collide, while same complete names receive ` (2)` suffixes.
- Mixed selections must skip text/config/folder/empty media nodes and continue after one failed resource.
- Browser-direct remote downloads must not regress into Blob fetching.
- Unmounting or rerendering during a batch must not leave duplicate queues or block later items.

---

### Task 1: Extract node-based media filename and selection helpers

**Files:**
- Modify: `web/src/lib/canvas/canvas-media-download.ts`
- Create: `web/src/lib/canvas/canvas-media-batch-download.ts`
- Test: `web/test/canvas-media-download.test.ts`

**Interfaces:**
- Produces `buildCanvasMediaDownloadFileName(node: CanvasNodeData, usedNames?: Set<string>): string`.
- Produces `isCanvasDownloadableMediaNode(node: CanvasNodeData): boolean`.
- Produces `resolveCanvasMediaDownloadItems(nodes: CanvasNodeData[]): Array<{ node: CanvasNodeData; fileName: string }>`.

- [ ] **Step 1: Write failing tests** for title-only names (`片头镜头.mp4`), illegal characters, empty titles, reserved names, same-name suffixes, MIME/content extension fallback, and mixed-node filtering.
- [ ] **Step 2: Run** `bun test test/canvas-media-download.test.ts`; confirm failures against the current canvas-title/date naming and missing batch helpers.
- [ ] **Step 3: Implement** the helpers using the existing sanitization and extension inference rules; preserve the existing exported extension function and keep uniqueness scoped to one batch.
- [ ] **Step 4: Run** the focused test file and confirm all naming/filter assertions pass.
- [ ] **Step 5: Commit** with `feat: add node-based canvas media download naming`.

### Task 2: Add a sequential browser download queue

**Files:**
- Create: `web/src/lib/canvas/canvas-media-download-queue.ts`
- Test: `web/test/canvas-media-download-queue.test.ts`

**Interfaces:**
- `type CanvasMediaDownloadQueueItem = { node: CanvasNodeData; fileName: string }`.
- `type CanvasMediaDownloadQueueResult = { item: CanvasMediaDownloadQueueItem; status: "success" | "failed"; error?: unknown }`.
- `downloadCanvasMediaQueue(items, downloadOne, options?): Promise<CanvasMediaDownloadQueueResult[]>`, where `downloadOne(item)` calls the existing browser download service and `options.delayMs` defaults to a short browser-safe delay.

- [ ] **Step 1: Write failing tests** for sequential order, delay invocation, continue-on-error, result accounting, and empty queues.
- [ ] **Step 2: Run** `bun test test/canvas-media-download-queue.test.ts`; confirm the queue module is absent/failing.
- [ ] **Step 3: Implement** the queue with one active promise at a time, no parallel resource reads, and per-item error capture; do not add cancellation that could imply a false revoke of already-triggered browser downloads.
- [ ] **Step 4: Run** the focused queue tests and confirm failures continue to later items.
- [ ] **Step 5: Commit** with `feat: add sequential canvas media download queue`.

### Task 3: Wire single-node and multi-node canvas actions

**Files:**
- Modify: `web/src/pages/canvas/use-canvas-node-editor.ts`
- Modify: `web/src/pages/canvas/project.tsx`
- Modify: `web/src/pages/canvas/canvas-project-selection-toolbar.tsx`
- Modify: `web/src/lib/canvas/tool-registry/tool-definition.ts`
- Modify: `web/src/lib/canvas/tool-registry/definitions/selection-toolbar-tools.tsx`
- Test: `web/test/canvas-asset-batch-download.test.ts`

**Interfaces:**
- Extend the editor hook result with `downloadSelectedMedia: (nodes: CanvasNodeData[]) => Promise<CanvasMediaDownloadQueueResult[]>`.
- Extend selection toolbar props with `downloadableCount`, `downloading`, and `onDownloadSelected`.
- Add a selection tool entry labeled `下载选中媒体`, visible only when the selection has at least two nodes and `downloadableCount > 0`.

- [ ] **Step 1: Write failing integration/source tests** asserting the single-node callback uses the new node-only filename, selection filtering reaches the queue, the tool is disabled for zero downloadable nodes, and a failed item does not stop later items.
- [ ] **Step 2: Run** `bun test test/canvas-asset-batch-download.test.ts`; confirm the entry point and wiring are missing.
- [ ] **Step 3: Implement** the hook callback around `downloadBrowserMedia`, pass selected nodes in stable canvas order, maintain local `idle/running/completed` state, and expose progress/result feedback through the existing message/toast mechanism.
- [ ] **Step 4: Add the selection toolbar item** without duplicating single-node download logic; keep existing selection actions unchanged.
- [ ] **Step 5: Run** the focused integration test and confirm mixed selections, progress, and completion feedback behavior.
- [ ] **Step 6: Commit** with `feat: support batch downloads for selected canvas media`.

### Task 4: Regression verification and release checks

**Files:**
- Modify: only files required by failing checks; no unrelated refactors.
- Test: existing canvas media, toolbar, and browser download tests.

- [ ] **Step 1: Run** `bun test test/canvas-media-download.test.ts test/canvas-media-download-queue.test.ts test/canvas-asset-batch-download.test.ts test/canvas-image-source.test.ts`.
- [ ] **Step 2: Run** `bun run typecheck` from `web`.
- [ ] **Step 3: Run** `bun run build` from `web`; accept only the repository’s existing chunk-size warnings.
- [ ] **Step 4: Run** `git diff --check` and inspect the final diff for accidental Blob fetching, ZIP use, or changes to draft/version/debug naming.
- [ ] **Step 5: Commit** any verification-only repair separately, then prepare main/canary release commits.

