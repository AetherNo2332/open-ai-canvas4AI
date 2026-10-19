import { expect, test } from "bun:test";
import { buildCanvasReferenceEdit, canvasReferenceIntentForConnection, storyboardInheritedBindings, assertStoryboardGenerationReady } from "../src/lib/canvas/canvas-reference-edits";
import { storyboardRowReferenceNodeIds } from "../src/lib/canvas/canvas-storyboard-materializer";
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
    expect(storyboardInheritedBindings(doc.nodes[0], shot, doc.nodes).map(binding => binding.nodeId)).toEqual(["global"]);
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
