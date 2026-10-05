import assert from "node:assert/strict";
import test from "node:test";
import { findCutPoint, shouldCompact } from "@earendil-works/pi-coding-agent";
import { canvasModel } from "../src/runner.js";
import type { CanvasBridge, PiSnapshot } from "../src/bridge.js";
import { compactionSettings, serializePreparation, runNativeCompaction } from "../src/native-compaction.js";

const snapshot = { request: { model: "model" }, modelLimits: {
  contextWindowTokens: 64_000, maxOutputTokens: 8_192, reservedOutputTokens: 8_192,
  compactAtTokens: 43_955, inputBudgetTokens: 51_712, overheadTokens: 4_096,
  configured: true, source: "channel-model",
} } as unknown as PiSnapshot;

test("Pi trigger matches Go and recent history fits small model windows", () => {
  const settings = compactionSettings(snapshot);
  assert.equal(shouldCompact(43_955, 64_000, settings), false);
  assert.equal(shouldCompact(43_956, 64_000, settings), true);
  assert.equal(shouldCompact(43_954, 64_000, settings), false);
  const small = compactionSettings({ ...snapshot, modelLimits: { ...snapshot.modelLimits,
    contextWindowTokens: 8_192, inputBudgetTokens: 4_096, compactAtTokens: 3_481 } });
  assert.ok(small.keepRecentTokens <= 1_024);
});

const splitPreparation = { firstKeptEntryId: "kept", tokensBefore: 30_000, isSplitTurn: true,
  messagesToSummarize: [{ role: "user", content: "old scene", timestamp: 1 }],
  turnPrefixMessages: [{ role: "user", content: "current scene", timestamp: 2 }],
  previousSummary: "Prior constraints", fileOps: { read: ["skill.md"], written: [], edited: [] },
  settings: { enabled: true, reserveTokens: 4_096, keepRecentTokens: 1_000 } };

test("split summaries replay frozen history and prefix calls and retain native file tracking", async () => {
  const ids: string[] = [];
  const bridge = {
    async nativeCompactionModel(_run: PiSnapshot, _op: string, request: any) {
      ids.push(request.callId);
      if (request.callId === "history") assert.match(request.prompt, /Prior constraints/);
      return { status: "succeeded", result: { text: request.callId === "history" ? "History summary" : "Prefix summary",
        stopReasonKind: "stop", usage: { input: 100, output: 10, totalTokens: 110 } } };
    },
    async finishNativeCompaction(_run: PiSnapshot, _op: string, input: any) {
      assert.equal(input.summary, "History summary\n\n---\n\n**Turn Context (split turn):**\n\nPrefix summary\n\n<read-files>\nskill.md\n</read-files>");
      assert.equal(input.usage.input, 200);
      return { operationId: "op", status: "succeeded", summary: input.summary };
    },
  } as unknown as CanvasBridge;
  const operation = { operationId: "op", status: "prepared", nativePreparation: splitPreparation, summaryMaxTokens: 2_000 };
  await runNativeCompaction(bridge, snapshot, operation, canvasModel(snapshot));
  await runNativeCompaction(bridge, snapshot, operation, canvasModel(snapshot));
  assert.deepEqual(ids, ["history", "turnPrefix", "history", "turnPrefix"]);
});

test("second summary failure requests Go fallback and transport failure is preserved", async () => {
  let fallback = 0;
  let transportFailure = false;
  const transportError = new Error("lease lost during summary transport");
  const bridge = {
    async nativeCompactionModel(_run: PiSnapshot, _op: string, request: any) {
      if (transportFailure) throw transportError;
      return request.callId === "history" ? { status: "succeeded", result: { text: "History", stopReasonKind: "stop" } } : { status: "failed" };
    },
    async finishNativeCompaction(_run: PiSnapshot, _op: string, input: any) {
      assert.equal(input.fallback, true);
      fallback++;
      return { operationId: "op", status: "succeeded", summary: "Go fallback" };
    },
  } as unknown as CanvasBridge;
  const operation = { operationId: "op", status: "prepared", nativePreparation: splitPreparation, summaryMaxTokens: 2_000 };
  await runNativeCompaction(bridge, snapshot, operation, canvasModel(snapshot));
  assert.equal(fallback, 1);
  transportFailure = true;
  await assert.rejects(runNativeCompaction(bridge, snapshot, operation, canvasModel(snapshot)), error => error === transportError);
  assert.equal(fallback, 1);
});

test("SDK splits a long tool turn and sends its native prompt through an independent bridge", async () => {
  const messages: any[] = [{ type: "message", id: "user", parentId: null, timestamp: new Date().toISOString(),
    message: { role: "user", content: "design a film", timestamp: 1 } }];
  for (let i = 0; i < 6; i++) {
    messages.push({ type: "message", id: `assistant-${i}`, parentId: messages.at(-1).id,
      timestamp: new Date().toISOString(), message: { role: "assistant", content: [
        { type: "text", text: "analysis".repeat(300) },
        { type: "toolCall", id: `call-${i}`, name: "canvas_read", arguments: {} },
      ], timestamp: i + 2, usage: { input: 0, output: 0, totalTokens: 0 }, stopReason: "toolUse" } });
    messages.push({ type: "message", id: `tool-${i}`, parentId: messages.at(-1).id,
      timestamp: new Date().toISOString(), message: { role: "toolResult", toolCallId: `call-${i}`,
        toolName: "canvas_read", content: [{ type: "text", text: "scene".repeat(500) }], isError: false, timestamp: i + 3 } });
  }
  const cut = findCutPoint(messages, 0, messages.length, 1_000);
  const preparation = { firstKeptEntryId: messages[cut.firstKeptEntryIndex].id,
    messagesToSummarize: [], turnPrefixMessages: messages.slice(0, cut.firstKeptEntryIndex).map(e => e.message),
    isSplitTurn: cut.isSplitTurn, tokensBefore: 15_000, fileOps: { read: new Set<string>(), written: new Set<string>(), edited: new Set<string>() },
    settings: { enabled: true, reserveTokens: 4_096, keepRecentTokens: 1_000 } };
  assert.equal(preparation.isSplitTurn, true);
  assert.notEqual(preparation.firstKeptEntryId, "user");
  let calls = 0;
  let summary = "";
  const bridge = {
    async nativeCompactionModel(_run: PiSnapshot, _op: string, request: any) {
      calls++;
      assert.equal(request.callId, "turnPrefix");
      assert.match(request.systemPrompt, /summarization assistant/);
      assert.match(request.prompt, /earlier context from an ongoing conversation/);
      assert.ok(request.maxTokens > 0 && request.maxTokens <= 8_192);
      return { status: "succeeded", result: { text: "Plan: keep scene continuity", stopReasonKind: "stop",
        usage: { input: 120, output: 20, totalTokens: 140 } } };
    },
    async finishNativeCompaction(_run: PiSnapshot, _op: string, input: any) {
      assert.equal(input.fallback, undefined, "native success must not fall back");
      summary = input.summary;
      return { operationId: "op", status: "succeeded", summary, firstKeptEntryId: preparation.firstKeptEntryId };
    },
  } as unknown as CanvasBridge;
  const operation = { operationId: "op", status: "prepared", nativePreparation: serializePreparation(preparation),
    summaryMaxTokens: 4_096 };
  await runNativeCompaction(bridge, snapshot, operation, canvasModel(snapshot));
  assert.equal(calls, 1);
  assert.match(summary, /Turn Context \(split turn\)/);
  assert.match(summary, /Plan: keep scene continuity/);
});
