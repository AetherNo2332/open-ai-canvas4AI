import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { validateToolArguments, type ToolCall } from "@earendil-works/pi-ai";

// Exercise Pi's actual validator against the Go-generated schema, rather than
// recreating a second contract in TypeScript.
const artifact = JSON.parse(await readFile(new URL("../../harness/TOOL_SCHEMA.json", import.meta.url), "utf8"));

const cases: [string, ToolCall["arguments"], boolean][] = [
  ["storyboard", {action:"split",nodeId:"b",channelId:"c",channelModelKey:"m"}, true],
  ["storyboard", {action:"split",nodeId:"b"}, false],
  ["storyboard", {action:"split",nodeId:"b",logicalModelId:"m",rowIds:["r"]}, false],
  ["storyboard", {action:"resplit",nodeId:"b",rowIds:["r"],request:"延长停顿",selectionId:"s"}, true],
  ["storyboard", {action:"resplit",nodeId:"b",rowIds:[],selectionId:"s"}, false],
  ["storyboard", {action:"lock",nodeId:"b",rowId:"r",snapshotHash:"h"}, true],
  ["storyboard", {action:"unlock",nodeId:"b",rowIds:["r"],snapshotHash:"h"}, true],
  ["storyboard", {action:"update",nodeId:"b",rowId:"r",snapshotHash:"h",patch:{locked:false}}, false],
  ["storyboard", {action:"update",nodeId:"b",rowId:"r",snapshotHash:"h",patch:{mediaReview:"pending"}}, false],
  ["storyboard", {action:"read",nodeId:"b",rows:21,view:"full"}, false],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "add_assets", rowIds: ["r"], assets: [{ref:"hero",role:"character"}] }, true],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "remove_assets", rowId: "r", assets: [] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "upsert_assets", rowId: "r", assets: [{ref:"hero",role:"character"}], patch:{dialogue:"mixed"} }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "add_references", references: ["hero"] }, true],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "remove_references", referenceNodeIds: [] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "append_rows", operationId: "task/chunk-1", rows:[{durationSeconds:5,plotDescription:"new"}] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "update", rowId:"r", operationId:"short", patch:{dialogue:"new"} }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "update", rowId:"r", storyboardRevision:"rev", patch:{dialogue:"{{ref:node:hero:reference}}"} }, true],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "update", rowId:"r", storyboardRevision:"rev", patch:{assets:[]} }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "update", storyboardRevision:"rev", updates:[{rowId:"r",patch:{assets:[]}}] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "b", action: "append_rows", storyboardRevision:"rev", rows:[{durationSeconds:5,plotDescription:"new"}] }, false],
  ["storyboard", { action:"read", nodeId:"b", operationIds:["task/chunk-1","task/chunk-2"] }, true],
  ["storyboard", { action:"read", nodeId:"b", operationIds:[] }, false],
  ["storyboard", { action:"read", nodeId:"b", operationIds:["task/chunk-1","task/chunk-1"] }, false],
  // 连线操作已从 Agent 合同删除：旧调用必须被 Pi 的验证器直接拒绝。
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "connect_nodes", id: "e", fromNodeId: "a", toNodeId: "b", toHandleId: "row:r1" }] }, false],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "update_connection", id: "e", fromNodeId: "a", toNodeId: "b" }] }, false],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "delete_connection", id: "e" }] }, false],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "update_node", id: "n", patch: { title: "t" }, fromNodeId: "a" }] }, false],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "add_node", id: "n", nodeType: "text" }] }, true],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "add_node", id: "n", nodeType: "text", fromNodeId: "a" }] }, false],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "delete_node", id: "n" }] }, true],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "duplicate_node", id: "new", sourceNodeId: "n" }] }, true],
  // 节点版本历史：读一次返回全部版本，切换与取出都用 media_versions 返回的版本 id。
  ["canvas_get_state", { view: "media_versions", nodeIds: ["hero"] }, true],
  ["canvas_get_state", { view: "media_versions", nodeIds: ["hero"], beforeVersion: 20 }, false],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "use_media_version", id: "hero", versionId: "v1" }] }, true],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "use_media_version", id: "hero" }] }, false],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "extract_media_version", id: "hero", versionId: "v1", nodeId: "hero-v1" }] }, true],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "extract_media_version", id: "hero", versionId: "v1" }] }, false],
  ["canvas_apply_ops", { snapshotHash: "h", ops: [{ type: "delete_media_version", id: "hero", versionId: "v1" }] }, false],
  ["canvas_read_content", { nodeId: "n", field: "content", offset: 0, limit: 16000 }, true],
  ["canvas_read_content", { nodeId: "n", field: "content", offset: -1 }, false],
  ["canvas_bind_asset", { snapshotHash: "h", nodeId: "n", assetId: "a" }, true],
  ["canvas_undo", { snapshotHash: "h" }, true],
  ["canvas_redo", { snapshotHash: "h", taskId: "forbidden" }, false],
  ["web_search", { query: "最新影视制作资讯" }, true],
  ["web_search", { query: "" }, false],
  ["web_search", { query: "a".repeat(501) }, false],
  ["web_search", { query: "news", apiKey: "forbidden-client-key" }, false],
  ["generate_media", { mode: "video", prompt: "cat", nodeId: "n", title: "cat", referenceNodeIds: [], size: "16:9", durationSeconds: 5 }, true],
  ["generate_media", { mode: "video", prompt: "cat", nodeId: "n", title: "cat", referenceNodeIds: [], size: "16:9", durationSeconds: 0 }, false],
  ["generate_media", { mode: "video", nodeId: "n", title: "cat", storyboardNodeId: "board", storyboardRowId: "row", size: "16:9" }, true],
  ["generate_media", { mode: "video", nodeId: "n", title: "cat", storyboardNodeId: "board", storyboardRowId: "row", size: "16:9", prompt: "duplicated" }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "append", patch: { durationSeconds: 5, plotDescription: "cat" } }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "append", patch: { durationSeconds: 5 } }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "update", updates: [{ rowId: "a", patch: { imageGenerationPrompt: "frame A", videoMotionPrompt: "motion A" } }, { rowId: "b", patch: { imageGenerationPrompt: "frame B", shotSize: "全景" } }] }, true],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "update", updates: [] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "update", updates: [{ rowId: "a", patch: {} }] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "update", rowId: "a", updates: [{ rowId: "a", patch: { dialogue: "line" } }] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "update", updates: [{ rowId: "a", patch: { status: "success" } }] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "append_rows", rows: [{ durationSeconds: 3, plotDescription: "A" }, { durationSeconds: 4, videoMotionPrompt: "B" }] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "append_rows", rows: [{ durationSeconds: 3, plotDescription: "A", id: "forged" }] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "append_rows", rows: [], patch: { dialogue: "line" } }, false],
  ["storyboard", { action:"read", nodeId: "b", fields: ["imageGenerationPrompt", "videoMotionPrompt", "durationSeconds"], missingAnyFields: ["imageGenerationPrompt", "videoMotionPrompt"] }, true],
  ["storyboard", { action:"read", nodeId: "b", view: "summary", fields: ["motion"] }, true],
  ["storyboard", { action:"read", nodeId: "b", fields: [] }, false],
  ["storyboard", { action:"read", nodeId: "b", fields: ["motion", "motion"] }, false],
  ["storyboard", { action:"read", nodeId: "b", fields: ["status"] }, false],
  ["storyboard", { action:"read", nodeId: "b", missingAnyFields: ["imageNodeId"] }, false],
  ["storyboard", { action:"read", nodeId: "b", missingAnyFields: [] }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "remove", rowId: "r", patch: {} }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "set_references", referenceNodeIds: ["asset"] }, true],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "set_references", referenceNodeIds: [] }, true],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "set_references" }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "set_references", referenceNodeIds: ["asset"], rowId: "r" }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "bind_output", rowId: "r", outputMode: "video", outputNodeId: "v" }, true],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "bind_output", rowId: "r", outputMode: "video" }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "bind_output", rowId: "r", outputMode: "video", outputNodeId: "v", patch: { durationSeconds: 5 } }, false],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "unbind_output", rowId: "r", outputMode: "image" }, true],
  ["storyboard", { snapshotHash: "h", nodeId: "n", action: "unbind_output", rowId: "r", outputMode: "image", outputNodeId: "v" }, false],
  ["canvas_edit_batch_table", { snapshotHash: "h", nodeId: "n", action: "set_concurrency", concurrency: 5 }, true],
  ["canvas_edit_batch_table", { snapshotHash: "h", nodeId: "n", action: "set_concurrency", concurrency: 5, patch: {} }, false],
  ["canvas_inspect_image", { nodeId: "n", sha256: "a".repeat(64), summary: { short: "cat", detailed: { subjects: ["cat"] } } }, true],
  ["canvas_inspect_image", { nodeId: "n", summary: { short: "cat", detailed: {} } }, false],
  // director：一个工具按 action 分支约束必填字段。
  ["director", { action: "read" }, true],
  ["director", { action: "read", sceneId: "scene" }, true],
  ["director", { action: "create_scene", template: "dialogue" }, true],
  ["director", { action: "create_scene" }, false],
  ["director", { action: "apply_patch", sceneId: "scene", expectedSceneHash: "h", patch: { operations: [{ type: "scene_update", id: "scene", title: "t" }] } }, true],
  ["director", { action: "apply_patch", sceneId: "scene", patch: { operations: [{ type: "scene_update", id: "scene", title: "t" }] } }, false],
  ["director", { action: "apply_patch", expectedSceneHash: "h", patch: { operations: [{ type: "scene_update", id: "scene" }] } }, false],
  ["director", { action: "preview", sceneId: "scene" }, true],
  ["director", { action: "render" }, false],
  ["director", { sceneId: "scene" }, false],
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

test("director character binding fields explain their source and joint requirement", () => {
  const director = artifact.tools.map((entry: any) => entry.function).find((entry: any) => entry.name === "director");
  assert.ok(director, "director");
  assert.match(director.description, /character\.assetId/);
  assert.match(director.description, /character\.versionId/);
  const operation = director.parameters.properties.patch.properties.operations.items;
  for (const [field, source] of <[string, string][]>[["characterAssetId", "character.assetId"], ["characterVersionId", "character.versionId"], ["referenceNodeId", "角色卡节点 id"]]) {
    const description: string = operation.properties[field].description ?? "";
    assert.ok(description.includes(source), `${field} must name its source`);
    assert.ok(description.includes("三者必须同时提供"), `${field} must state the joint requirement`);
  }
});
