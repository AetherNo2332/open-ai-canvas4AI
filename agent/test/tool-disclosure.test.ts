import assert from "node:assert/strict";
import test from "node:test";
import { Agent } from "@earendil-works/pi-agent-core";
import type { Model } from "@earendil-works/pi-ai";
import { createCanvasStreamFn } from "../src/pi-stream.js";
import { ToolDisclosure, type CanvasToolSpec } from "../src/tool-disclosure.js";

const model: Model<any> = {
  id: "canvas-test", name: "Canvas test", api: "openai-completions", provider: "canvas", baseUrl: "http://backend",
  reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
  contextWindow: 8192, maxTokens: 2048,
};
const objectSchema = { type: "object", properties: {}, required: [], additionalProperties: false };
const specs: CanvasToolSpec[] = [
  { name: "agent_tools_canvas_read", description: "Read canvas", parameters: objectSchema, allowed: true },
  { name: "canvas_get_state", category: "agent_tools_canvas_read", description: "Get state", parameters: objectSchema, allowed: true },
  { name: "agent_tools_canvas_edit", description: "Edit canvas", parameters: objectSchema, allowed: true },
  { name: "canvas_apply_ops", category: "agent_tools_canvas_edit", description: "Apply ops", parameters: objectSchema, allowed: false },
];

test("Pi sees parents first and only eligible children after opening a category", async () => {
  const seen: string[][] = [];
  const replies = [
    { toolCalls: [{ id: "c1", function: { name: "agent_tools_canvas_read", arguments: "{}" } }] },
    { toolCalls: [{ id: "c2", function: { name: "canvas_get_state", arguments: "{}" } }] },
    { text: "Done" },
  ];
  const streamFn = createCanvasStreamFn(async ({ messages }) => {
    const system = messages.filter((message: any) => message.role === "system");
    const declarations = system.flatMap((message: any) => message.toolsAdded || []).map((tool: any) => tool.name);
    seen.push(declarations);
    return replies.shift() || { text: "Done" };
  });
  const called: string[] = [];
  const disclosure = new ToolDisclosure(specs, async (name) => { called.push(name); return { result: { ok: true } }; });
  const agent = new Agent({ initialState: { model, systemPrompt: "Canvas assistant" }, streamFn, toolExecution: "sequential" });
  disclosure.attach(agent);
  await agent.prompt("Inspect canvas");
  assert.deepEqual(called, ["agent_tools_canvas_read", "canvas_get_state"], JSON.stringify(agent.state.messages));
  assert.equal(disclosure.opened.has("agent_tools_canvas_read"), true);
  assert.equal(disclosure.isVisible("canvas_get_state"), true);
  assert.equal(disclosure.isVisible("canvas_apply_ops"), false);
  assert.equal(seen.length, 3);
  assert.ok(seen[0]?.includes("agent_tools_canvas_read"));
  assert.equal(seen[0]?.includes("canvas_get_state"), false);
  assert.ok(seen[1]?.includes("canvas_get_state"));
});
