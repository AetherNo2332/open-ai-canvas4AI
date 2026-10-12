// Invoked only by the opt-in Go integration test. No upstream credential enters Node.
import { CanvasBridge, type PiSnapshot } from "../src/bridge.js";
import { canvasModel } from "../src/runner.js";
import { runNativeCompaction, serializePreparation } from "../src/native-compaction.js";
import assert from "node:assert/strict";

let raw = "";
for await (const chunk of process.stdin) raw += chunk;
const input = JSON.parse(raw) as { base: string; snapshot: PiSnapshot; split: boolean };
const snapshot = input.snapshot;
const { prepareCompaction } = await import(new URL("../../node_modules/@earendil-works/pi-coding-agent/dist/core/compaction/compaction.js", import.meta.url).href);
const entries = snapshot.piSessionEntries!.map(item => item.entry);
let preparation: any;
for (const keepRecentTokens of [1, 32, 64, 128, 256, 512, 1024, 2048]) {
  const candidate = prepareCompaction(entries, { enabled: true, reserveTokens: 1024, keepRecentTokens });
  if (candidate && candidate.isSplitTurn === input.split && candidate.messagesToSummarize.length > 0 &&
      (!input.split || candidate.turnPrefixMessages.length > 0)) { preparation = candidate; break; }
}
assert.ok(preparation, "SDK could not prepare requested scenario");
const bridge = new CanvasBridge(input.base, "local-test-only", "worker-a");
const operation = await bridge.compactContext(snapshot, { sessionRevision: snapshot.piSessionRevision!,
  activeLeafId: snapshot.piActiveLeafId!, reason: "manual", willRetry: false,
  tokensBefore: preparation.tokensBefore, preparation: serializePreparation(preparation) });
const ready = await runNativeCompaction(bridge, snapshot, operation, canvasModel(snapshot));
// A provider may stop a split-turn summary with `length` even when the
// request itself succeeded. Go deliberately converts that semantic truncation
// into its bounded fallback checkpoint; only transport/lease failures abort.
if (ready.fallback === true) {
  assert.equal(ready.mode, "fallback");
} else {
  assert.equal(ready.mode, "pi-native");
}
assert.ok(ready.usage && ready.usage.input > 0 && ready.usage.output > 0);
// Recreate the native algorithm on the durable preparation; Go must replay every receipt.
const replay = await runNativeCompaction(bridge, snapshot, operation, canvasModel(snapshot));
assert.equal(replay.summary, ready.summary);
assert.equal(replay.fallback, ready.fallback);
const committed = await bridge.commitContextCompaction(snapshot, operation.operationId, snapshot.piSessionRevision!, {
  type: "compaction", id: `live-${input.split ? "split" : "normal"}`, parentId: snapshot.piActiveLeafId,
  timestamp: new Date().toISOString(), summary: ready.summary, firstKeptEntryId: ready.firstKeptEntryId,
  tokensBefore: ready.tokensBefore, details: ready.details, usage: ready.usage,
});
assert.equal(committed.committed, true);
console.log(JSON.stringify({ split: preparation.isSplitTurn, historyMessages: preparation.messagesToSummarize.length,
  prefixMessages: preparation.turnPrefixMessages.length, mode: ready.mode, usage: ready.usage,
  sessionRevision: committed.sessionRevision }));
