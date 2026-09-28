import assert from "node:assert/strict";
import test from "node:test";
import type { AgentMessage } from "@earendil-works/pi-agent-core";
import type { AssistantMessage } from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { PiSnapshot } from "../src/bridge.js";
import { createImageContextExtension, createTerminalHistoryExtension } from "../src/session-history.js";

test("context repair never substitutes for current-run receipt recovery, even with a reused call ID", async () => {
  const old = { role: "assistant", timestamp: 1, content: [
    { type: "toolCall", id: "reused", name: "canvas_apply_ops", arguments: {} },
  ] } as AssistantMessage;
  const current = { ...old, timestamp: 2 };
  const source = [old, { role: "user", content: "new turn", timestamp: 2 }, current,
    { role: "user", content: "another message", timestamp: 3 }] as AgentMessage[];
  const before = JSON.stringify(source);
  const snapshot = { runId: "current-run", piSessionEntries: [
    { runId: "old-run", entry: { type: "message", message: old } },
    { runId: "current-run", entry: { type: "message", message: current } },
  ] } as unknown as PiSnapshot;
  let context: ((event: { messages: AgentMessage[] }) => { messages: AgentMessage[] } | undefined) | undefined;
  const extension = createTerminalHistoryExtension(snapshot);
  if (typeof extension === "function") throw new Error("expected a named context extension");
  await extension.factory({
    on: (_name: string, handler: typeof context) => { context = handler; },
  } as unknown as ExtensionAPI);
  assert.ok(context);
  const result = context({ messages: source });
  assert.ok(result);
  assert.equal(result.messages.filter(message => message.role === "toolResult").length, 1);
  assert.equal(result.messages[1]?.role, "toolResult", "only the terminal historical batch receives a projection repair");
  assert.equal(result.messages.at(-2), current, "an in-flight assistant message remains unchanged");
  assert.equal(JSON.stringify(source), before, "the persistent transcript must not be edited");
});

test("image restoration uses only active ancestors and preserves exact references without duplicates", async () => {
  const original = { type: "image_url", image_url: { url: "resource:original", detail: "high" } };
  const image = { role: "user", timestamp: 1, content: "",
    canvasContent: [{ type: "text", text: "obsolete user instructions" }, original] } as unknown as AgentMessage;
  const source = [{ role: "user", content: "current request", timestamp: 2 }] as AgentMessage[];
  const before = JSON.stringify([image, source]);
  let handler: any;
  const extension = createImageContextExtension();
  if (typeof extension === "function") throw new Error("expected a named image context extension");
  await extension.factory({ on: (_name: string, listener: unknown) => { handler = listener; } } as unknown as ExtensionAPI);
  const context = { sessionManager: {
    getBranch: () => [{ type: "message", message: image }],
    getEntries: () => { throw new Error("must not scan other users or abandoned sibling branches"); },
  } };
  const restored = handler({ messages: source }, context);
  assert.deepEqual(restored.messages[0].canvasContent, [original]);
  assert.equal(restored.messages[1], source[0]);
  assert.equal(handler({ messages: restored.messages }, context), undefined, "image already in context must not be duplicated");
  assert.equal(JSON.stringify([image, source]), before, "restoration must leave persistent entries untouched");
});
