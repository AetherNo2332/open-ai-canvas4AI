import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { validateToolArguments, type ToolCall } from "@earendil-works/pi-ai";
import { assertToolSnapshotMatchesSchema } from "../src/tool-disclosure.js";

// Exercise Pi's actual validator against the Go-generated schema, rather than
// recreating a second contract in TypeScript.
const artifact = JSON.parse(await readFile(new URL("../../harness/TOOL_SCHEMA.json", import.meta.url), "utf8"));
const cases: [string, ToolCall["arguments"], boolean][] = [
  ["web_search", { query: "最新影视制作资讯" }, true],
  ["web_search", { query: "" }, false],
  ["web_search", { query: "a".repeat(501) }, false],
  ["web_search", { query: "news", apiKey: "forbidden-client-key" }, false],
  ["generate_media", { mode: "video", prompt: "cat", nodeId: "n", title: "cat", referenceNodeIds: [], size: "16:9", durationSeconds: 5 }, true],
  ["generate_media", { mode: "video", prompt: "cat", nodeId: "n", title: "cat", referenceNodeIds: [], size: "16:9", durationSeconds: 0 }, false],
  ["canvas_edit_storyboard", { snapshotHash: "h", nodeId: "n", action: "append", patch: { durationSeconds: 5, plotDescription: "cat" } }, true],
  ["canvas_edit_storyboard", { snapshotHash: "h", nodeId: "n", action: "append", patch: { durationSeconds: 5 } }, false],
  ["canvas_edit_storyboard", { snapshotHash: "h", nodeId: "n", action: "remove", rowId: "r", patch: {} }, false],
  ["canvas_edit_batch_table", { snapshotHash: "h", nodeId: "n", action: "set_concurrency", concurrency: 5 }, true],
  ["canvas_edit_batch_table", { snapshotHash: "h", nodeId: "n", action: "set_concurrency", concurrency: 5, patch: {} }, false],
  ["canvas_inspect_image", { nodeId: "n", sha256: "a".repeat(64), summary: { short: "cat", detailed: { subjects: ["cat"] } } }, true],
  ["canvas_inspect_image", { nodeId: "n", summary: { short: "cat", detailed: {} } }, false],
];
test("Pi validates the shared conditional contracts before execution", () => {
  for (const [name, args, valid] of cases) {
    const tool = artifact.tools.map((entry: any) => entry.function).find((entry: any) => entry.name === name);
    assert.ok(tool, name);
    const validate = () => validateToolArguments(tool, { type: "toolCall", id: "test", name, arguments: structuredClone(args) });
    if (valid) assert.doesNotThrow(validate, name);
    else assert.throws(validate, Error, name);
  }
});

test("an old frozen vision schema cannot silently bypass the SHA submission contract", () => {
  const tool = artifact.tools.map((entry: any) => entry.function).find((entry: any) => entry.name === "canvas_inspect_image");
  const parameters = structuredClone(tool.parameters);
  delete parameters.properties.sha256;
  delete parameters.oneOf;
  assert.throws(() => assertToolSnapshotMatchesSchema([
    { name: tool.name, category: "agent_tools_canvas_read", description: tool.description, allowed: true, parameters },
  ], artifact), /server snapshot schema differs/);
});
