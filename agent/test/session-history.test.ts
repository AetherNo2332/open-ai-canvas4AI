import assert from "node:assert/strict";
import test from "node:test";
import type { AgentMessage } from "@earendil-works/pi-agent-core";
import type { AssistantMessage } from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import type { PiSnapshot } from "../src/bridge.js";
import { createImageContextExtension, createTerminalHistoryExtension } from "../src/session-history.js";

test("saved SHA-bound vision summaries replace images even across compaction", async () => {
  const sha = "a".repeat(64);
  const image = { role: "user", timestamp: 1, content: "", canvasContent: [
    { type: "text", text: `画面：${JSON.stringify({ nodeId: "cat", sha256: sha })}` },
    { type: "image_url", image_url: { url: "resource:cat" } }] } as unknown as AgentMessage;
  const saved = { role: "toolResult", toolName: "canvas_inspect_image", toolCallId: "save", timestamp: 2,
    content: [{ type: "text", text: JSON.stringify({ nodeId: "cat", sha256: sha, summarySaved: true, imageAttached: false,
      visionCache: { short: "white cat", detailed: {} } }) }] } as unknown as AgentMessage;
  let handler: any;
  const extension = createImageContextExtension();
  if (typeof extension === "function") throw new Error("expected image extension");
  await extension.factory({ on: (_: string, listener: unknown) => { handler = listener; } } as unknown as ExtensionAPI);
  const context = { sessionManager: { getBranch: () => [{ type: "message", message: image }, { type: "message", message: saved }] } };
  const before = JSON.stringify(image);
  const result = handler({ messages: [image, saved] }, context);
  assert.ok(result);
  assert.equal(JSON.stringify(result.messages).includes('"type":"image_url"'), false);
  assert.ok(JSON.stringify(result.messages).includes("white cat"));
  const compacted = handler({ messages: [{ role: "user", content: "continue", timestamp: 3 }] }, context);
  assert.equal(JSON.stringify(compacted?.messages || []).includes('"type":"image_url"'), false);
  assert.ok(JSON.stringify(compacted?.messages || []).includes("white cat"), "compaction must retain the SHA-bound observation");
  const cacheOnly = handler({ messages: [{ role: "user", content: "continue", timestamp: 3 }] },
    { sessionManager: { getBranch: () => [{ type: "message", message: saved }] } });
  assert.ok(JSON.stringify(cacheOnly?.messages || []).includes("white cat"), "a cached-only read must survive compaction without an original image");
  assert.equal(JSON.stringify(image), before, "durable original must remain intact");
});

test("image projection sends at most the newest unsummarized image", async () => {
  const images = ["one", "two"].map((id, i) => ({ role: "user", timestamp: i, content: "", canvasContent: [
    { type: "text", text: JSON.stringify({ nodeId: id, sha256: id === "one" ? "a".repeat(64) : "b".repeat(64) }) },
    { type: "image_url", image_url: { url: `resource:${id}` } },
  ] })) as unknown as AgentMessage[];
  let handler: any;
  const extension = createImageContextExtension();
  if (typeof extension === "function") throw new Error("expected image extension");
  await extension.factory({ on: (_: string, listener: unknown) => { handler = listener; } } as unknown as ExtensionAPI);
  const result = handler({ messages: images }, { sessionManager: { getBranch: () => images.map(message => ({ type: "message", message })) } });
  const parts = result.messages.flatMap((m: any) => m.canvasContent || []);
  assert.equal(parts.filter((p: any) => p.type === "image_url").length, 1);
  assert.equal(parts.find((p: any) => p.type === "image_url").image_url.url, "resource:two");
});

test("old SHA and failed summary receipts cannot hide an updated image", async () => {
  const image = { role: "user", timestamp: 3, content: "", canvasContent: [
    { type: "text", text: JSON.stringify({ bytes: 12, nodeId: "cat", sha256: "b".repeat(64) }) },
    { type: "image_url", image_url: { url: "resource:new" } },
  ] } as unknown as AgentMessage;
  const saved = (sha: string, isError: boolean) => ({ role: "toolResult", toolName: "canvas_inspect_image", toolCallId: "save", timestamp: 2, isError,
    content: [{ type: "text", text: JSON.stringify({ nodeId: "cat", sha256: sha, imageAttached: false,
      visionCache: { short: "old cat", detailed: {} } }) }] }) as unknown as AgentMessage;
  let handler: any;
  const extension = createImageContextExtension();
  if (typeof extension === "function") throw new Error("expected image extension");
  await extension.factory({ on: (_: string, listener: unknown) => { handler = listener; } } as unknown as ExtensionAPI);
  for (const receipt of [saved("a".repeat(64), false), saved("b".repeat(64), true)]) {
    const source = [receipt, image];
    const result = handler({ messages: [image] }, { sessionManager: { getBranch: () => source.map(message => ({ type: "message", message })) } });
    const messages = result?.messages || [image];
    assert.equal(messages.flatMap((m: any) => m.canvasContent || []).filter((p: any) => p.type === "image_url").length, 1);
    assert.ok(JSON.stringify(messages).includes("resource:new"));
  }
});

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
