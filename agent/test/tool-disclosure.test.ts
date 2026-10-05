import assert from "node:assert/strict";
import test from "node:test";
import { assertToolSnapshotMatchesSchema, type CanvasToolSpec } from "../src/tool-disclosure.js";
import { createCanvasToolsExtension, SessionToolDisclosure, type SessionToolDefinitionLike } from "../src/session-tools.js";

const objectSchema = { type: "object", properties: {}, required: [], additionalProperties: false };
const specs: CanvasToolSpec[] = [
  { name: "agent_tools_canvas_read", description: "Legacy category", parameters: objectSchema, allowed: true },
  { name: "canvas_get_state", category: "agent_tools_canvas_read", description: "Get state", parameters: objectSchema, allowed: true },
  { name: "canvas_apply_ops", category: "agent_tools_canvas_edit", description: "Apply ops", parameters: objectSchema, allowed: false },
  { name: "plan_update", category: "agent_tools_control", description: "Update plan", parameters: objectSchema, allowed: true },
];

function toolOf(tools: SessionToolDefinitionLike[], name: string): SessionToolDefinitionLike {
  const tool = tools.find((item) => item.name === name);
  assert.ok(tool, `missing tool ${name}`);
  return tool;
}

test("all eligible concrete tools are registered from the first step; categories are not tools", async () => {
  const called: string[] = [];
  const disclosure = new SessionToolDisclosure(specs, async (name) => {
    called.push(name);
    return { result: { ok: true } };
  });
  assert.deepEqual(disclosure.activeNames(), ["canvas_get_state", "plan_update"]);
  assert.equal(disclosure.isVisible("agent_tools_canvas_read"), false);
  assert.equal(disclosure.isVisible("canvas_apply_ops"), false);
  await toolOf(disclosure.tools(), "canvas_get_state").execute("call-1", {}, undefined);
  assert.deepEqual(called, ["canvas_get_state"]);
});

test("previous-step concrete call names are written to model-facing tool schema and reset per run", () => {
  const disclosure = new SessionToolDisclosure(specs, async () => ({ result: {} }), "Previous tools: {names}");
  const original = [{ type: "function", function: { name: "canvas_get_state", description: "Get state", parameters: objectSchema } }];
  assert.equal(disclosure.decorateCanonicalTools(original)[0]?.function &&
    (disclosure.decorateCanonicalTools(original)[0]!.function as Record<string, unknown>).description, "Get state");
  disclosure.recordStepCalls(["canvas_get_state", "agent_tools_canvas_read", "plan_update"]);
  const decorated = disclosure.decorateCanonicalTools(original);
  assert.equal((decorated[0]!.function as Record<string, unknown>).description,
    "Get state Previous tools: canvas_get_state、plan_update");
  const newRun = new SessionToolDisclosure(specs, async () => ({ result: {} }), "Previous tools: {names}");
  assert.equal((newRun.decorateCanonicalTools(original)[0]!.function as Record<string, unknown>).description, "Get state");
});

test("Pi tool_call hook blocks ineligible names and calls outside the admitted batch", async () => {
  const registry = new SessionToolDisclosure(specs, async () => ({ result: {} }));
  const registered: SessionToolDefinitionLike[] = [];
  const handlers = new Map<string, (event: any) => unknown>();
  const extension = createCanvasToolsExtension(registry, (id) => id === "admitted" ? undefined : "batch rejected");
  assert.equal(typeof extension, "object");
  if (typeof extension === "function") throw new Error("expected inline extension object");
  extension.factory({
    registerTool(tool: unknown) { registered.push(tool as SessionToolDefinitionLike); },
    on(event: string, handler: (event: any) => unknown) { handlers.set(event, handler); return () => {}; },
  } as never);
  assert.deepEqual(registered.map(({ name }) => name), ["canvas_get_state", "plan_update"]);
  const hook = handlers.get("tool_call");
  assert.ok(hook);
  assert.deepEqual(hook({ toolName: "canvas_get_state", toolCallId: "admitted", input: {} }), undefined);
  assert.deepEqual(hook({ toolName: "canvas_get_state", toolCallId: "unadmitted", input: {} }),
    { block: true, reason: "batch rejected" });
  assert.deepEqual(hook({ toolName: "agent_tools_canvas_read", toolCallId: "admitted", input: {} }),
    { block: true, terminate: true, reason: "Tool agent_tools_canvas_read is not eligible for this run" });
});

test("server snapshot schema artifact drift fails closed", () => {
  const schema = { type: "object", properties: {}, required: [], additionalProperties: false };
  const artifact = { schemaVersion: "cloud-agent-tools/v3", tools: [{ type: "function", function: { name: "canvas_get_state", parameters: schema } }] };
  assert.throws(
    () => assertToolSnapshotMatchesSchema([{ name: "canvas_apply_ops", description: "x", parameters: schema, allowed: true }], artifact),
    /missing from cloud-agent-tools\/v3: canvas_apply_ops/,
  );
  assertToolSnapshotMatchesSchema([{ name: "canvas_get_state", description: "x", parameters: schema, allowed: true }], artifact);
  assert.throws(() => assertToolSnapshotMatchesSchema([], { schemaVersion: "", tools: [] }), /artifact is empty/);
});

test("same-name parameter schema drift fails closed regardless of key order", () => {
  const server = { type: "object", properties: {}, required: [], additionalProperties: false };
  const artifact = { schemaVersion: "cloud-agent-tools/v3", tools: [{ type: "function", function: { name: "canvas_get_state", parameters: { type: "object", properties: { offset: { type: "integer" } } } } }] };
  assert.throws(
    () => assertToolSnapshotMatchesSchema([{ name: "canvas_get_state", description: "x", parameters: server, allowed: true }], artifact),
    /schema differs from cloud-agent-tools\/v3: canvas_get_state/,
  );
  assertToolSnapshotMatchesSchema(
    [{ name: "canvas_get_state", description: "x", parameters: { properties: { offset: { type: "integer" } }, type: "object" }, allowed: true }],
    artifact,
  );
});

test("older canvas_inspect_image snapshots remain compatible with the optional summary field", () => {
  const oldParameters = {
    type: "object",
    properties: {
      nodeId: { type: "string" },
      refresh: { type: "boolean" },
    },
    required: ["nodeId"],
    additionalProperties: false,
  };
  const currentParameters = {
    ...oldParameters,
    properties: {
      ...oldParameters.properties,
      summary: { type: "object" },
    },
  };
  assertToolSnapshotMatchesSchema(
    [{ name: "canvas_inspect_image", description: "x", parameters: oldParameters, allowed: true }],
    { schemaVersion: "cloud-agent-tools/v3", tools: [{ type: "function", function: { name: "canvas_inspect_image", parameters: currentParameters } }] },
  );
});
