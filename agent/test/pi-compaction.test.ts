import assert from "node:assert/strict";
import test from "node:test";
import type { CanvasBridge, PiSnapshot } from "../src/bridge.js";
import { createCanvasContextCompactionExtension } from "../src/runner.js";
import { FatalWorkerError } from "../src/tool-disclosure.js";

const run = {
  runId: "run-compaction",
  userId: "user-compaction",
  revision: 1,
  status: "running",
  request: { prompt: "compact this context" },
  modelLimits: { contextWindowTokens: 64_000, maxOutputTokens: 8_192, configured: true, source: "channel-model" },
  canonical: { systemPrompt: "policy", messages: [], tools: [], toolChoice: "auto" },
  tools: [],
} as PiSnapshot;

function extensionHandlers(extension: ReturnType<typeof createCanvasContextCompactionExtension>) {
  const handlers = new Map<string, (event: any) => Promise<any> | any>();
  if (typeof extension === "function") throw new Error("expected named inline extension");
  extension.factory({ on: (event: string, handler: (event: any) => Promise<any> | any) => {
    handlers.set(event, handler);
    return () => handlers.delete(event);
  } } as any);
  return handlers;
}

test("Go checkpoint supplies Pi compaction and the committed entry advances the durable session", async () => {
  const calls: string[] = [];
  let revision = 7;
  let failure: Error | undefined;
  let committed: Record<string, unknown> | undefined;
  const bridge = {
    async compactContext(_run: PiSnapshot, request: Record<string, unknown>) {
      calls.push(`start:${String(request.activeLeafId)}`);
      return { operationId: "op-1", status: "succeeded", summary: "<agent-context-checkpoint/>",
        firstKeptEntryId: "kept-1", tokensBefore: 12000, sourceDigest: "source-hash", checkpointDigest: "checkpoint-hash",
        details: { protocolVersion: "canvas-pi-compaction/v1", operationId: "op-1" } };
    },
    async commitContextCompaction(_run: PiSnapshot, operationId: string, sessionRevision: number, entry: Record<string, unknown>) {
      calls.push(`commit:${operationId}:${sessionRevision}`);
      committed = entry;
      return { committed: true, sessionRevision: 8 };
    },
  } as unknown as CanvasBridge;
  const extension = createCanvasContextCompactionExtension({
    bridge, snapshot: () => run, sessionRevision: () => revision, activeLeafId: () => "leaf-7",
    pendingCompaction: () => undefined,
    drain: async () => { calls.push("drain"); }, onCommitted: (entry, nextRevision) => {
      committed = entry;
      revision = nextRevision;
    }, onFailure: (error) => { failure = error; },
  });
  const handlers = extensionHandlers(extension);
  const before = await handlers.get("session_before_compact")!({
    preparation: { tokensBefore: 12_000 }, reason: "threshold", willRetry: false,
    signal: new AbortController().signal,
  });
  assert.deepEqual(calls.slice(0, 2), ["drain", "start:leaf-7"]);
  assert.equal(before.compaction.summary, "<agent-context-checkpoint/>");
  assert.equal(before.compaction.firstKeptEntryId, "kept-1");
  assert.equal(before.compaction.tokensBefore, 12_000);

  const entry = { type: "compaction", id: "compact-8", parentId: "leaf-7", details: before.compaction.details };
  await handlers.get("session_compact")!({ compactionEntry: entry, fromExtension: true, reason: "threshold", willRetry: false });
  assert.deepEqual(calls.slice(2), ["drain", "commit:op-1:7"]);
  assert.equal(revision, 8);
  assert.equal(committed, entry);
  assert.equal(failure, undefined);
});

test("failed Go compaction cancels Pi's built-in summarizer", async () => {
  let failure: Error | undefined;
  const bridge = {
    async compactContext() { throw new FatalWorkerError("Go compaction service unavailable"); },
  } as unknown as CanvasBridge;
  const extension = createCanvasContextCompactionExtension({
    bridge, snapshot: () => run, sessionRevision: () => 3, activeLeafId: () => "leaf-3",
    pendingCompaction: () => undefined,
    drain: async () => {}, onCommitted: () => {}, onFailure: (error) => { failure = error; },
  });
  const handlers = extensionHandlers(extension);
  const result = await handlers.get("session_before_compact")!({
    preparation: { tokensBefore: 1_000 }, reason: "overflow", willRetry: true,
    signal: new AbortController().signal,
  });
  assert.deepEqual(result, { cancel: true });
  assert.ok(failure instanceof FatalWorkerError);
  assert.match(failure.message, /Go compaction service unavailable/);
});

test("a restarted Pi session fetches and commits the pending Go operation without starting another task", async () => {
  const calls: string[] = [];
  let revision = 12;
  let pending: PiSnapshot["pendingContextCompaction"] = {
    operationId: "op-restart", sessionRevision: 12, activeLeafId: "leaf-12",
    reason: "overflow", willRetry: true, tokensBefore: 21_000,
  };
  const bridge = {
    async compactContext() { calls.push("unexpected-start"); throw new Error("must resume, not create another task"); },
    async resumeContextCompaction(_run: PiSnapshot, operationId: string) {
      calls.push(`resume:${operationId}`);
      return { operationId, status: "succeeded", summary: "<agent-context-checkpoint/>",
        firstKeptEntryId: "kept-12", tokensBefore: 21_000, details: { operationId } };
    },
    async commitContextCompaction(_run: PiSnapshot, operationId: string, sessionRevision: number, entry: Record<string, unknown>) {
      calls.push(`commit:${operationId}:${sessionRevision}`);
      assert.equal(entry.parentId, "leaf-12");
      return { committed: true, sessionRevision: 13 };
    },
  } as unknown as CanvasBridge;
  const extension = createCanvasContextCompactionExtension({
    bridge, snapshot: () => run, sessionRevision: () => revision, activeLeafId: () => "leaf-12",
    pendingCompaction: () => pending,
    drain: async () => { calls.push("drain"); },
    onCommitted: (_entry, nextRevision) => { revision = nextRevision; pending = undefined; },
    onFailure: (error) => { throw error; },
  });
  const handlers = extensionHandlers(extension);
  const before = await handlers.get("session_before_compact")!({
    preparation: { tokensBefore: 21_000 }, reason: "manual", willRetry: false,
    signal: new AbortController().signal,
  });
  assert.equal(before.compaction.details.operationId, "op-restart");
  const entry = { type: "compaction", id: "compact-13", parentId: "leaf-12", summary: before.compaction.summary,
    firstKeptEntryId: before.compaction.firstKeptEntryId, tokensBefore: before.compaction.tokensBefore,
    details: before.compaction.details };
  await handlers.get("session_compact")!({ compactionEntry: entry, fromExtension: true, reason: "manual", willRetry: false });
  assert.deepEqual(calls, ["drain", "resume:op-restart", "drain", "commit:op-restart:12"]);
  assert.equal(revision, 13);
  assert.equal(pending, undefined);
});
