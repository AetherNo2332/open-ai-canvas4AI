import { expect, test } from "bun:test";

import { applyCanvasReferenceEdit, buildCanvasReferenceEdit, canvasReferenceIntentForConnection } from "../src/lib/canvas/canvas-reference-edits";
import { canvasDeclaredInputNodeIds } from "../src/lib/canvas/canvas-reference-inputs";
import { buildCanvasNodeMentionReferenceMap, getGenerationResourceNodes } from "../src/lib/canvas/canvas-resource-references";
import { projectCanvasOperations, type CanvasDocument } from "../src/lib/canvas/canvas-event-projection";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData } from "../src/types/canvas";

const capabilities = {
    protocolVersion: 1,
    nodes: [
        { type: "script", patchFields: { title: { path: "title" } } },
        { type: "image", patchFields: {} },
        { type: "compare", patchFields: { inputBindings: { path: "metadata.inputBindings" } } },
    ],
    structuredFields: { storyboard: { dialogue: {} }, batch: { prompt: {} } },
};

const node = (id: string, type: CanvasNodeType, metadata: Record<string, unknown> = {}): CanvasNodeData => ({ id, type, title: id, position: { x: 0, y: 0 }, width: 300, height: 200, metadata });
const row = () => ({
    id: "row-1",
    shotNumber: 1,
    durationSeconds: 6,
    plotDescription: "夜雨里的对峙",
    dialogue: "",
    characters: [],
    narrativeIntent: "",
    viewerPOV: "",
    performanceBlocking: "",
    shotSize: "",
    emotion: "",
    lightingAndAtmosphere: "",
    audioEffects: "",
    camera: "",
    motion: "",
    timeBeats: "",
    imageGenerationPrompt: "雨夜街道，霓虹反光",
    videoMotionPrompt: "",
    mustHave: [],
    optionalDetails: [],
    continuityOut: "",
    negativePrompt: "",
    assetBindings: [],
    status: "idle",
});
const document = (): CanvasDocument => ({
    id: "canvas-1",
    referenceFieldsVersion: 1,
    nodes: [node("asset", CanvasNodeType.Image, { content: "asset.png" }), node("board", CanvasNodeType.Script, { storyboard: { rows: [row()], visibleColumns: [], referenceNodeIds: [] } }), node("output", CanvasNodeType.Image, {})],
    connections: [],
});
const nodesOf = (doc: CanvasDocument) => doc.nodes as CanvasNodeData[];

test("dragged image references appear in the prompt before server connections arrive", () => {
    let nodes = [node("first", CanvasNodeType.Image, { content: "first.png" }), node("second", CanvasNodeType.Image, { content: "second.png" }), node("target", CanvasNodeType.Image)];
    for (const fromNodeId of ["second", "first"]) {
        const intent = canvasReferenceIntentForConnection(nodes, { fromNodeId, toNodeId: "target" })!;
        nodes = applyCanvasReferenceEdit(nodes, intent);
    }
    const references = buildCanvasNodeMentionReferenceMap(nodes, []).get("target")!;
    expect(references.map(({ nodeId, label }) => [nodeId, label])).toEqual([["second", "图片1"], ["first", "图片2"]]);
    expect(references.map(({ nodeId }) => nodeId)).toEqual(getGenerationResourceNodes("target", nodes, []).map(({ id }) => id));
    const cleared = applyCanvasReferenceEdit(nodes, { kind: "set_node_inputs", nodeId: "target", bindings: [] });
    expect(buildCanvasNodeMentionReferenceMap(cleared, [{ id: "stale", fromNodeId: "first", toNodeId: "target" }]).get("target")).toEqual([]);
});

test("declared row bindings feed the storyboard output without creating any connection", () => {
    const doc = document();
    const withAssets = applyCanvasReferenceEdit(nodesOf(doc), { kind: "set_row_assets", nodeId: "board", rowId: "row-1", assetBindings: [{ nodeId: "asset", role: "character", priority: 80 }] });
    const [binding] = buildCanvasReferenceEdit({ nodes: withAssets }, { kind: "bind_output", nodeId: "board", rowId: "row-1", outputMode: "image", outputNodeId: "output" });
    expect(binding).toEqual({ type: "bind_storyboard_output", id: "board", rowId: "row-1", outputMode: "image", sourceNodeId: "output" });
    const bound = projectCanvasOperations({ ...doc, nodes: withAssets }, [binding]);
    const board = (bound.nodes as CanvasNodeData[]).find((item) => item.id === "board")!;
    expect(board.metadata!.storyboard!.rows[0]).toMatchObject({ id: "row-1", imageNodeId: "output", status: "idle" });
    // 生成输入直接来自行字段与归属，不依赖任何连线。
    expect(canvasDeclaredInputNodeIds("output", bound.nodes as CanvasNodeData[])).toEqual(["asset"]);
    expect(getGenerationResourceNodes("output", bound.nodes as CanvasNodeData[], []).map((item) => item.id)).toEqual(["asset"]);
    expect(buildCanvasNodeMentionReferenceMap(nodesOf(bound), []).get("output")?.map((item) => item.nodeId)).toEqual(["asset"]);
    expect(bound.connections).toEqual([]);
    // 引用编辑不写任务身份。
    expect(board.metadata!.storyboard!.rows[0]).not.toHaveProperty("taskId");
});

test("clearing declared references stops reading the old material even when a legacy edge remains", () => {
    const doc = document();
    const legacy: CanvasConnection[] = [{ id: "legacy", fromNodeId: "asset", toNodeId: "output" }];
    const bound = applyCanvasReferenceEdit(applyCanvasReferenceEdit(nodesOf(doc), { kind: "set_row_assets", nodeId: "board", rowId: "row-1", assetBindings: [{ nodeId: "asset", role: "character", priority: 80 }] }), {
        kind: "bind_output",
        nodeId: "board",
        rowId: "row-1",
        outputMode: "image",
        outputNodeId: "output",
    });
    expect(getGenerationResourceNodes("output", bound, legacy).map((item) => item.id)).toEqual(["asset"]);
    const cleared = applyCanvasReferenceEdit(bound, { kind: "set_row_assets", nodeId: "board", rowId: "row-1", assetBindings: [] });
    // 显式空数组表示"已清空"，不能回读遗留连线里的旧素材。
    expect(canvasDeclaredInputNodeIds("output", cleared)).toEqual([]);
    expect(getGenerationResourceNodes("output", cleared, legacy)).toEqual([]);
    // 对照：未规范化（没有字段）的节点仍按遗留连线读取，说明上面不是因为读不到边才为空。
    const explicitEmpty = node("compare", CanvasNodeType.Compare, { inputBindings: [] });
    const legacyCompare = node("legacy-compare", CanvasNodeType.Compare, {});
    const legacyEdge: CanvasConnection[] = [{ id: "legacy-compare", fromNodeId: "asset", toNodeId: "legacy-compare" }];
    expect(getGenerationResourceNodes("compare", [node("asset", CanvasNodeType.Image, { content: "asset.png" }), explicitEmpty], legacyEdge)).toEqual([]);
    expect(getGenerationResourceNodes("legacy-compare", [node("asset", CanvasNodeType.Image, { content: "asset.png" }), legacyCompare], legacyEdge).map((item) => item.id)).toEqual(["asset"]);
});

test("editor commits reference fields as semantic operations and ignores server-derived managed edges", () => {
    const before = document();
    before.nodes = [...nodesOf(before), node("compare", CanvasNodeType.Compare, { inputBindings: [] })];
    const after = structuredClone(before) as CanvasDocument;
    (after.nodes as CanvasNodeData[]).find((item) => item.id === "board")!.metadata!.storyboard!.referenceNodeIds = ["asset"];
    (after.nodes as CanvasNodeData[]).find((item) => item.id === "compare")!.metadata!.inputBindings = [{ id: "node:asset", nodeId: "asset", mediaType: "image", role: "reference", order: 0, resolution: "latest" }];
    // 服务端按字段派生出的托管边出现在投影里，编辑器不能把它当成用户新建的连线。
    after.connections = [{ id: "managed", fromNodeId: "asset", toNodeId: "board", managedBy: "reference-fields-v1", referenceOwnerNodeId: "board", referenceBindingId: "binding-1" }];
    const operations = canvasEditorCommands(before, after, capabilities as never);
    expect(operations).toContainEqual({ type: "edit_storyboard", id: "board", action: "set_references", patch: { referenceNodeIds: ["asset"] } });
    expect(operations).toContainEqual({ type: "update_node", id: "compare", patch: { inputBindings: [{ id: "node:asset", nodeId: "asset", mediaType: "image", role: "reference", order: 0, resolution: "latest" }] } });
    expect(operations.some((operation) => ["connect_nodes", "delete_connection", "update_connection"].includes(operation.type))).toBe(false);
    expect(JSON.stringify(operations)).not.toContain("managedBy");
});
