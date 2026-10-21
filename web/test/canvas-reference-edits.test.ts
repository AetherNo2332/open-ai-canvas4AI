import { expect, test } from "bun:test";
import { applyCanvasReferenceEdit, buildCanvasReferenceEdit, canvasReferenceIntentForConnection, reorderCanvasReferenceInputs, replaceCanvasReferenceInput, storyboardInheritedBindings, assertStoryboardGenerationReady } from "../src/lib/canvas/canvas-reference-edits";
import { storyboardRowReferenceNodeIds } from "../src/lib/canvas/canvas-storyboard-materializer";
import { storyboardDeclaredNodeIds } from "../src/lib/canvas/canvas-reference-inputs";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
import { projectCanvasOperations } from "../src/lib/canvas/canvas-event-projection";
import { CanvasNodeType, type CanvasNodeData, type StoryboardRow } from "../src/types/canvas";
const node = (id: string, type: CanvasNodeType, metadata: Record<string, unknown> = {}): CanvasNodeData => ({ id, type, title: id, position: { x: 0, y: 0 }, width: 300, height: 200, metadata });
const row = (): StoryboardRow => ({ id: "row", shotNumber: 1, durationSeconds: 5, assetBindings: [], characters: [], plotDescription: "shot" }) as StoryboardRow;
const board = () => node("board", CanvasNodeType.Script, { storyboard: { rows: [row()], referenceNodeIds: ["global"], visibleColumns: [] } });
const document = () => ({ id: "canvas", referenceFieldsVersion: 1, nodes: [board(), node("asset", CanvasNodeType.Image), node("global", CanvasNodeType.Image)], connections: [] });

test("character field references appear in the same asset list consumed by generation", () => {
    const doc = document();
    const shot = { ...row(), characters: [{ characterName: "主角", characterImageNodeId: "asset" }] } as StoryboardRow;
    expect(storyboardInheritedBindings(doc.nodes[0], shot, doc.nodes)).toEqual([
        { nodeId: "asset", role: "character", priority: 100, source: "出镜角色" },
        { nodeId: "global", role: "style", priority: 60, source: "表级共享" },
    ]);
    shot.assetBindings = [{ nodeId: "asset", role: "character", priority: 80 }];
    expect(storyboardInheritedBindings(doc.nodes[0], shot, doc.nodes).map((binding) => binding.nodeId)).toEqual(["global"]);
});
test("an explicitly bound character version suppresses ambiguous asset matching", () => {
    const v1 = node("v1", CanvasNodeType.Text, { workflowKind: "character", characterAssetId: "hero" });
    const v2 = node("v2", CanvasNodeType.Text, { workflowKind: "character", characterAssetId: "hero" });
    const script = board();
    const shot = { ...row(), characters: [{ characterName: "主角", characterAssetId: "hero" }] } as StoryboardRow;
    expect(() => storyboardDeclaredNodeIds(script, shot, [script, v1, v2], false)).toThrow("人物素材匹配多个节点");
    shot.assetBindings = [{ nodeId: "v2", role: "character", priority: 100 }];
    expect(storyboardDeclaredNodeIds(script, shot, [script, v1, v2], false)).toEqual(["v2"]);
});
test("semantic output binding projects a pending draft without fabricating a line", () => {
    const result = projectCanvasOperations(document(), [{ type: "bind_storyboard_output", id: "board", rowId: "row", outputMode: "image", sourceNodeId: "asset" }]);
    expect((result.nodes as CanvasNodeData[])[0].metadata?.storyboard?.rows[0].imageNodeId).toBe("asset");
    expect(result.connections).toEqual([]);
});
test("clearing row bindings ignores stale edges and preserves inherited scope", () => {
    const doc = document(),
        script = doc.nodes[0];
    const refs = storyboardRowReferenceNodeIds(script, row(), doc.nodes, [{ id: "stale", fromNodeId: "asset", toNodeId: "board", toHandleId: "row:row" }], false);
    expect(refs).toEqual(["global"]);
    const ops = buildCanvasReferenceEdit(doc, { kind: "set_row_assets", nodeId: "board", rowId: "row", assetBindings: [] });
    expect(ops).toEqual([{ type: "edit_storyboard", id: "board", rowId: "row", action: "update", patch: { assetBindings: [] } }]);
});
test("dragging a reference emits field operations without connection commands", () => {
    const doc = document();
    const intent = canvasReferenceIntentForConnection(doc.nodes, { fromNodeId: "asset", toNodeId: "board", toHandleId: "row:row" })!;
    const ops = buildCanvasReferenceEdit(doc, intent);
    expect(ops[0].patch?.assetBindings).toEqual([{ nodeId: "asset", role: "style", priority: 60 }]);
    expect(ops.some((op) => ["connect_nodes", "delete_connection", "update_connection"].includes(op.type))).toBe(false);
});
test("inherited references identify their owner and cannot be disconnected in a row", () => {
    const doc = document();
    expect(storyboardInheritedBindings(doc.nodes[0], row(), doc.nodes)).toEqual([{ nodeId: "global", role: "style", priority: 60, source: "表级共享" }]);
    expect(() => canvasReferenceIntentForConnection(doc.nodes, { fromNodeId: "global", toNodeId: "board", toHandleId: "row:row" }, true)).toThrow("共享引用");
});
test("missing keyframe prevents submission while direct mode permits it", () => {
    const script = board();
    script.metadata!.storyboardVideoInputMode = "keyframe";
    let submitCount = 0;
    expect(() => {
        assertStoryboardGenerationReady(script, row(), [], "video");
        submitCount++;
    }).toThrow("首帧");
    expect(submitCount).toBe(0);
    script.metadata!.storyboardVideoInputMode = "direct";
    expect(() => assertStoryboardGenerationReady(script, row(), [], "video")).not.toThrow();
});
test("generic node fields preserve the supplied input order", () => {
    const doc = document();
    doc.nodes.push(node("target", CanvasNodeType.Compare));
    const bindings = ["global", "asset"].map((id, order) => ({ id: `node:${id}`, nodeId: id, mediaType: "image" as const, role: "reference" as const, order, resolution: "latest" as const }));
    expect(buildCanvasReferenceEdit(doc, { kind: "set_node_inputs", nodeId: "target", bindings })).toEqual([{ type: "update_node", id: "target", patch: { inputBindings: bindings } }]);
});
test("replacing a declared reference keeps its slot and rewrites old prompt titles", () => {
    const target = node("target", CanvasNodeType.Image, {
        composerContent: "参考 @Old portrait, also use @图片2.",
        generationSpec: { version: 1, mode: "image", prompt: "参考 @Old portrait, also use @图片2.", options: {}, textInputMode: "prompt-only", referenceBindings: [
            { id: "node:old:reference", nodeId: "old", mediaType: "image", role: "reference", order: 0, resolution: "latest" },
            { id: "node:second:reference", nodeId: "second", mediaType: "image", role: "reference", order: 1, resolution: "latest" },
        ] },
    });
    const old = node("old", CanvasNodeType.Image, { content: "old" });
    old.title = "Old portrait";
    const second = node("second", CanvasNodeType.Image, { content: "second" });
    const replacement = node("replacement", CanvasNodeType.Image, { content: "replacement" });
    const nodes = [old, second, replacement, target];
    const intent = replaceCanvasReferenceInput(nodes, target.id, old.id, replacement.id)!;
    const updated = applyCanvasReferenceEdit(nodes, intent, []).find((item) => item.id === target.id)!;
    expect(updated.metadata?.generationSpec?.referenceBindings.map((binding) => [binding.nodeId, binding.order])).toEqual([["replacement", 0], ["second", 1]]);
    expect(updated.metadata?.composerContent).toBe("参考 @图片1, also use @图片2.");
});

test("replacing a storyboard output reference preserves its row slot and prompt", () => {
    const script = board();
    script.metadata!.storyboard!.rows[0] = { ...row(), imageNodeId: "output", assetBindings: [{ nodeId: "old", role: "character", priority: 100 }, { nodeId: "second", role: "style", priority: 60 }] };
    const output = node("output", CanvasNodeType.Image, { composerContent: "参考 @Old portrait, also use @图片2." });
    const old = node("old", CanvasNodeType.Image, { content: "old" });
    old.title = "Old portrait";
    const nodes = [script, output, old, node("second", CanvasNodeType.Image, { content: "second" }), node("replacement", CanvasNodeType.Image, { content: "replacement" })];
    const intent = replaceCanvasReferenceInput(nodes, script.id, old.id, "replacement", [])!;
    expect(intent?.kind).toBe("set_row_assets");
    const updated = applyCanvasReferenceEdit(nodes, intent, []);
    expect(updated[0].metadata?.storyboard?.rows[0].assetBindings).toEqual([{ nodeId: "replacement", role: "character", priority: 100 }, { nodeId: "second", role: "style", priority: 60 }]);
    expect(updated[1].metadata?.composerContent).toBe("参考 @图片1, also use @图片2.");
});

test("replacing a board-level storyboard reference keeps its index and rewrites row output prompts", () => {
    const script = board();
    script.metadata!.storyboard!.referenceNodeIds = ["old", "second"];
    script.metadata!.storyboard!.rows[0] = { ...row(), imageNodeId: "output" };
    const output = node("output", CanvasNodeType.Image, { composerContent: "参考 @Old portrait, also use @图片2." });
    const old = node("old", CanvasNodeType.Image, { content: "old" });
    old.title = "Old portrait";
    const nodes = [script, output, old, node("second", CanvasNodeType.Image, { content: "second" }), node("replacement", CanvasNodeType.Image, { content: "replacement" })];
    const intent = replaceCanvasReferenceInput(nodes, script.id, old.id, "replacement", [])!;
    expect(intent).toEqual({ kind: "set_board_references", nodeId: "board", referenceNodeIds: ["replacement", "second"], replaceNodeId: "old" });
    const updated = applyCanvasReferenceEdit(nodes, intent, []);
    expect(updated[0].metadata?.storyboard?.referenceNodeIds).toEqual(["replacement", "second"]);
    expect(updated[1].metadata?.composerContent).toBe("参考 @图片1, also use @图片2.");
});

test("editor turns output ownership into semantic binding without a synthetic edge", () => {
    const before = document(),
        after = structuredClone(before);
    after.nodes[0].metadata!.storyboard!.rows[0].imageNodeId = "asset";
    const caps = {
        protocolVersion: 1,
        nodes: [
            { type: "script", patchFields: {} },
            { type: "image", patchFields: {} },
        ],
        structuredFields: { storyboard: { assetBindings: {} } },
    };
    expect(canvasEditorCommands(before, after, caps)).toEqual([{ type: "bind_storyboard_output", id: "board", rowId: "row", outputMode: "image", sourceNodeId: "asset" }]);
});



test("legacy input lines are migrated before adding a declared reference", () => {
    const target = node("target", CanvasNodeType.Image, { composerContent: "merge @图片1 and @图片2" });
    const sources = ["a", "b", "c"].map((id) => node(id, CanvasNodeType.Image, { content: id }));
    const connections = sources.slice(0, 2).map((source) => ({ id: "edge-" + source.id, fromNodeId: source.id, toNodeId: target.id }));
    const intent = canvasReferenceIntentForConnection([...sources, target], { fromNodeId: "c", toNodeId: target.id }, false, connections)!;
    expect(intent.kind).toBe("set_node_inputs");
    if (intent.kind === "set_node_inputs") expect(intent.bindings.map((binding) => binding.nodeId)).toEqual(["a", "b", "c"]);
});

test("declared reference removal and reorder keep prompt identity aligned", () => {
    const target = node("target", CanvasNodeType.Image, {
        composerContent: "style @图片1 subject @图片2",
        generationSpec: { version: 1, mode: "image", prompt: "style @图片1 subject @图片2", options: {}, textInputMode: "append-sources", referenceBindings: [
            { id: "node:a", nodeId: "a", mediaType: "image", role: "reference", order: 0, resolution: "latest" },
            { id: "node:b", nodeId: "b", mediaType: "image", role: "reference", order: 1, resolution: "latest" },
        ] },
    });
    const nodes = [node("a", CanvasNodeType.Image, { content: "a" }), node("b", CanvasNodeType.Image, { content: "b" }), target];
    const connections = [{ id: "a-target", fromNodeId: "a", toNodeId: "target" }, { id: "b-target", fromNodeId: "b", toNodeId: "target" }];
    const remove = canvasReferenceIntentForConnection(nodes, connections[0], true, connections)!;
    const removed = applyCanvasReferenceEdit(nodes, remove, connections).find((item) => item.id === "target")!;
    expect(removed.metadata?.composerContent).toBe("style subject @图片1");
    const reorder = reorderCanvasReferenceInputs(nodes, "target", ["b", "a"], connections)!;
    const reordered = applyCanvasReferenceEdit(nodes, reorder, connections).find((item) => item.id === "target")!;
    expect(reordered.metadata?.composerContent).toBe("style @图片2 subject @图片1");
});
