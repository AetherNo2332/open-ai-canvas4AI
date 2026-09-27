import assert from "node:assert/strict";
import test from "node:test";
import { CanvasBridge, type PiCanonical, type PiSnapshot } from "../src/bridge.js";

test("modelStep preserves Go task usage in CanvasModelResult", async () => {
  const previousFetch = globalThis.fetch;
  globalThis.fetch = (async () => new Response(JSON.stringify({
    code: 0,
    data: {
      taskId: "task-usage",
      status: "succeeded",
      result: { text: "answer", usage: { input: 8, output: 3, totalTokens: 11 } },
    },
  }), { status: 200, headers: { "Content-Type": "application/json" } })) as typeof fetch;
  try {
    const run = { runId: "run-usage" } as PiSnapshot;
    const canonical: PiCanonical = { systemPrompt: "test", messages: [], tools: [], toolChoice: "auto" };
    const { result } = await new CanvasBridge("http://backend:8080", "token", "worker").modelStep(run, canonical);
    assert.deepEqual(result.usage, { input: 8, output: 3, totalTokens: 11 });
  } finally {
    globalThis.fetch = previousFetch;
  }
});

test("modelStep preserves missing usage as missing", async () => {
  const previousFetch = globalThis.fetch;
  globalThis.fetch = (async () => new Response(JSON.stringify({
    code: 0,
    data: { taskId: "task-no-usage", status: "succeeded", result: { text: "answer" } },
  }), { status: 200, headers: { "Content-Type": "application/json" } })) as typeof fetch;
  try {
    const run = { runId: "run-no-usage" } as PiSnapshot;
    const canonical: PiCanonical = { systemPrompt: "test", messages: [], tools: [], toolChoice: "auto" };
    const { result } = await new CanvasBridge("http://backend:8080", "token", "worker").modelStep(run, canonical);
    assert.equal(result.usage, undefined);
  } finally {
    globalThis.fetch = previousFetch;
  }
});
