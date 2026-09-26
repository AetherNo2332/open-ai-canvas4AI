import assert from "node:assert/strict";
import test from "node:test";
import type { Model } from "@earendil-works/pi-ai";
import { createCanvasStreamFn } from "../src/pi-stream.js";

const model: Model<any> = {
  id: "canvas-test", name: "Canvas test", api: "openai-completions", provider: "canvas", baseUrl: "http://backend",
  reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  contextWindow: 8192, maxTokens: 2048,
};

test("bridges a billed model result into Pi tool events", async () => {
  const fn = createCanvasStreamFn(async () => ({
    text: "Working", reasoning: "Inspecting", stopReasonKind: "tool_use",
    toolCalls: [{ id: "call-1", function: { name: "canvas_get_state", arguments: '{"offset":0}' } }],
    usage: { input: 10, output: 5, totalTokens: 15 },
  }));
  const stream = await fn(model, { messages: [] } as never);
  const types: string[] = [];
  for await (const event of stream) types.push(event.type);
  const result = await stream.result();
  assert.deepEqual(types, ["start", "thinking_start", "thinking_delta", "thinking_end", "text_start", "text_delta", "text_end", "toolcall_start", "toolcall_delta", "toolcall_end", "done"]);
  assert.equal(result.stopReason, "toolUse");
  assert.deepEqual(result.content[2], { type: "toolCall", id: "call-1", name: "canvas_get_state", arguments: { offset: 0 } });
  assert.equal(result.usage.totalTokens, 15);
});

test("truncated model responses do not execute tool calls", async () => {
  const fn = createCanvasStreamFn(async () => ({ text: "partial", stopReasonKind: "length", toolCalls: [{ id: "partial", function: { name: "canvas_apply_ops", arguments: "{}" } }] }));
  const stream = await fn(model, { messages: [] } as never);
  for await (const _ of stream) { /* drain */ }
  assert.equal((await stream.result()).stopReason, "length");
  assert.equal((await stream.result()).content.some((block) => block.type === "toolCall"), false);
});
