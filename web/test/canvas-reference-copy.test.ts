import { expect, test } from "bun:test";
import { copyCanvasNodeGraph } from "../src/lib/canvas/canvas-node-copy";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";
const node = (id: string, type: CanvasNodeType, metadata: Record<string, unknown> = {}, parentId?: string): CanvasNodeData => ({ id, type, title: id, parentId, position: { x: 0, y: 0 }, width: 300, height: 200, metadata });
function graph() {
    const binding = { id: "asset-ref", nodeId: "asset", mediaType: "image", role: "reference", order: 0, resolution: "latest" };
    return [
        node("frame", CanvasNodeType.Frame),
        node("asset", CanvasNodeType.Image, {}, "frame"),
        node(
            "board",
            CanvasNodeType.Script,
            { storyboard: { rows: [{ id: "row", assetBindings: [{ nodeId: "asset", role: "style", priority: 60 }], imageNodeId: "output", taskId: "old-task", taskStatus: "running", status: "loading" }], visibleColumns: [], referenceNodeIds: ["asset"] } },
            "frame",
        ),
        node("output", CanvasNodeType.Image, { taskId: "old-task", generationSpec: { version: 1, mode: "image", prompt: "frame", options: {}, referenceBindings: [binding], textInputMode: "prompt-only" } }, "frame"),
        node("compare", CanvasNodeType.Compare, { inputBindings: [binding] }, "frame"),
    ];
}
test("copying only a board preserves external materials and clears output/task ownership", () => {
    const result = copyCanvasNodeGraph(graph(), [], "board", new Map([["board", "copy"]]), () => "edge");
    const row = result.nodes[0].metadata!.storyboard!.rows[0] as any;
    expect(row.assetBindings[0].nodeId).toBe("asset");
    expect(row.imageNodeId).toBeUndefined();
    expect(row.taskId).toBeUndefined();
    expect(row.taskStatus).toBeUndefined();
    expect(row.id).not.toBe("row");
    expect(row.status).toBe("idle");
});
test("copying a container remaps every canonical reference and derives new managed edges", () => {
    const nodes = graph(),
        ids = new Map(nodes.map((node) => [node.id, `copy-${node.id}`]));
    const result = copyCanvasNodeGraph(nodes, [{ id: "owned", fromNodeId: "asset", toNodeId: "output", managedBy: "reference-fields-v1", referenceOwnerNodeId: "output", referenceBindingId: "asset-ref" }], "frame", ids, () => "copy-edge");
    expect(result.nodes.find((node) => node.id === "copy-output")?.metadata?.generationSpec?.referenceBindings[0].nodeId).toBe("copy-asset");
    expect(result.nodes.find((node) => node.id === "copy-compare")?.metadata?.inputBindings?.[0].nodeId).toBe("copy-asset");
    const row = result.nodes.find((node) => node.id === "copy-board")!.metadata!.storyboard!.rows[0] as any;
    expect(row.imageNodeId).toBe("copy-output");
    expect(row.taskId).toBeUndefined();
    expect(row.status).toBe("idle");
    expect(result.connections).toEqual([]);
});
test("copying a container remaps embedded batch storyboard references inside the scope and keeps external ones", () => {
    const sourceRows = [
        { id: "r1", imageNodeId: "asset", assetBindings: [{ nodeId: "asset", role: "style", priority: 60 }] },
        { id: "r2", imageNodeId: "outside", assetBindings: [{ nodeId: "outside", role: "style", priority: 60 }] },
    ];
    const nodes = [
        node("frame", CanvasNodeType.Frame),
        node("asset", CanvasNodeType.Image, {}, "frame"),
        node("outside", CanvasNodeType.Image),
        node("batch", CanvasNodeType.BatchTable, { batchTable: { operation: "creative", concurrency: 1, rows: [], storyboardRows: sourceRows, storyboardSourceNodeIds: ["asset", "outside"] } }, "frame"),
    ];
    const ids = new Map(nodes.map((item) => [item.id, `copy-${item.id}`]));
    const copied = copyCanvasNodeGraph(nodes, [], "frame", ids, () => "edge").nodes.find((item) => item.id === "copy-batch")!;
    const table = copied.metadata!.batchTable!;
    expect(table.storyboardSourceNodeIds).toEqual(["copy-asset", "outside"]);
    expect(table.storyboardRows!.map((row) => row.imageNodeId)).toEqual(["copy-asset", "outside"]);
    expect(table.storyboardRows!.map((row) => row.assetBindings[0].nodeId)).toEqual(["copy-asset", "outside"]);
    // 内嵌内容是展示引用：复制不改变行身份，也不清除行上的业务字段。
    expect(table.storyboardRows!.map((row) => row.id)).toEqual(["r1", "r2"]);
});
