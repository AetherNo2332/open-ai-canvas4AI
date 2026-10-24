import assert from "node:assert/strict";
import test from "node:test";
import { runCanvasAgent } from "../src/runner.js";
import type { CanvasBridge, PiSnapshot } from "../src/bridge.js";

for (const admitted of [false, true]) test(`retired calls recover only a persisted batch: ${admitted}`, async () => {
  const call = { id: "historical-call", type: "function" as const, function: { name: "previs_preview", arguments: '{"sceneId":"scene"}' } };
  const snapshot: PiSnapshot = {
    runId: "historical-run", userId: "user", revision: 4, status: "running",
    request: { prompt: "Continue", model: "canvas-model" },
    modelLimits: { contextWindowTokens: 64000, maxOutputTokens: 8192, configured: true, source: "channel-model" },
    canonical: { systemPrompt: "Policy", tools: [], toolChoice: "auto", messages: [
      { role: "user", content: "Continue" }, { role: "assistant", content: "", tool_calls: [call] },
    ] },
    tools: [{ name: "previs_preview", description: "Retired", allowed: false, parameters: { type: "object" } }],
    lastTaskId: "historical-task", piMessages: [],
    ...(admitted ? { pendingToolBatch: { taskId: "historical-task", calls: [call] } } : {}),
  };
  const executions: string[] = [];
  const stop = new Error("recovery checkpoint reached");
  const bridge = {
    startToolBatch: async () => { throw new Error("must not admit a new retired batch"); },
    executeTool: async (_run: unknown, _task: unknown, callId: string) => { executions.push(callId); return { result: { committed: true } }; },
    checkpoint: async () => { throw stop; },
  } as unknown as CanvasBridge;
  await assert.rejects(runCanvasAgent(bridge, snapshot), error => error === stop);
  assert.deepEqual(executions, admitted ? [call.id] : []);
});
