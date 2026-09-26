import assert from "node:assert/strict";
import test from "node:test";
import type { CanvasBridge, PiCanonical, PiSnapshot, PiToolCall } from "../src/bridge.js";
import { runCanvasAgent } from "../src/runner.js";

const schema = { type: "object", properties: {}, required: [], additionalProperties: false };
const initial: PiSnapshot = {
  runId: "run-1", userId: "user-1", revision: 1, status: "running",
  request: { prompt: "Inspect", model: "test" },
  canonical: { systemPrompt: "Canvas assistant", messages: [{ role: "user", content: "Inspect" }],
    tools: [], toolChoice: "auto" },
  tools: [
    { name: "agent_tools_canvas_read", description: "Open read", parameters: schema, allowed: true },
    { name: "canvas_get_state", description: "Read state", category: "agent_tools_canvas_read", parameters: schema, allowed: true },
  ],
};

test("leased Pi run persists messages and executes a disclosed child in order", async () => {
  const batches: string[][] = [];
  const checkpoints: string[] = [];
  const visible: string[][] = [];
  let step = 0;
  let status = "running";
  const bridge = {
    async checkpoint(_run: PiSnapshot, sequence: number, message: Record<string, unknown>, taskId?: string) {
      assert.equal(sequence, checkpoints.length + 1);
      checkpoints.push(`${message.role}:${taskId || ""}`);
    },
    async modelStep(_run: PiSnapshot, canonical: PiCanonical) {
      visible.push(canonical.tools.map((tool) => String((tool.function as Record<string, unknown>).name)));
      step++;
      if (step === 1) return { taskId: "task-1", result: { toolCalls: [{ id: "call-1", function: { name: "agent_tools_canvas_read", arguments: "{}" } }] } };
      if (step === 2) return { taskId: "task-2", result: { toolCalls: [{ id: "call-2", function: { name: "canvas_get_state", arguments: "{}" } }] } };
      return { taskId: "task-3", result: { text: "Finished" } };
    },
    async startToolBatch(_run: PiSnapshot, calls: PiToolCall[]) { batches.push(calls.map((call) => call.function.name)); },
    async executeTool(_run: PiSnapshot, callId: string) { return { callId, pending: false, result: { ok: true } }; },
    async noToolTurn() { status = "completed"; return { status }; },
    async snapshot(run: PiSnapshot) { return { ...run, status }; },
    async renew() {},
  } as unknown as CanvasBridge;
  await runCanvasAgent(bridge, structuredClone(initial));
  assert.deepEqual(batches, [["agent_tools_canvas_read"], ["canvas_get_state"]]);
  assert.ok(visible[0]?.includes("agent_tools_canvas_read"));
  assert.equal(visible[0]?.includes("canvas_get_state"), false);
  assert.ok(visible[1]?.includes("canvas_get_state"));
  assert.ok(checkpoints.includes("assistant:task-1"));
  assert.ok(checkpoints.includes("assistant:task-3"));
});
