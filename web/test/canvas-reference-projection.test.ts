import { expect, test } from "bun:test";
import parity from "../../backend/internal/canvas/references/testdata/reference-projection-parity.json";
import { applyCanvasReferenceGraphEdit, canvasReferenceIntentForConnection, connectCanvasReferences, deleteCanvasReferenceLine, replaceCanvasReferenceInput, type CanvasReferenceGraph } from "../src/lib/canvas/canvas-reference-edits";
import { reconcileCanvasReferenceConnections } from "../src/lib/canvas/canvas-reference-projection";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type StoryboardRow } from "../src/types/canvas";

type ParityCase = { name: string; owners: string[]; document: CanvasReferenceGraph; lines: CanvasConnection[] };
const sequence = () => {
    let next = 0;
    return () => `edge-${++next}`;
};
const node = (id: string, type: CanvasNodeType, metadata: Record<string, unknown> = {}): CanvasNodeData => ({ id, type, title: id, position: { x: 0, y: 0 }, width: 300, height: 200, metadata });
const image = (id: string) => node(id, CanvasNodeType.Image, { content: `https://example.test/${id}.png` });
const board = (assetBindings: StoryboardRow["assetBindings"] = []) => node("board", CanvasNodeType.Script, { storyboard: { rows: [{ id: "shot", shotNumber: 1, durationSeconds: 5, plotDescription: "shot", characters: [], assetBindings }], referenceNodeIds: [], visibleColumns: [] } });
const removeLine = (graph: CanvasReferenceGraph, edge: CanvasConnection) => applyCanvasReferenceGraphEdit(graph, canvasReferenceIntentForConnection(graph.nodes, edge, true, graph.connections)!, [edge.id]);
const reconcileProjected = (graph: CanvasReferenceGraph, owners: string[]) => ({ nodes: graph.nodes, connections: reconcileCanvasReferenceConnections(graph.nodes, graph.connections, owners) });

for (const item of parity as unknown as ParityCase[]) {
    test(`editor projection matches the server reconciler: ${item.name}`, () => {
        const owners = new Set(item.owners);
        const lines = reconcileCanvasReferenceConnections(item.document.nodes, item.document.connections, item.owners, sequence()).filter((edge) => edge.managedBy === "reference-fields-v1" && owners.has(edge.referenceOwnerNodeId || ""));
        expect(lines).toEqual(item.lines);
    });
}

test("connecting a reference draws its line in the same update", () => {
    const next = connectCanvasReferences({ nodes: [image("asset"), node("target", CanvasNodeType.Image)], connections: [] }, [{ id: "drag", fromNodeId: "asset", toNodeId: "target" }]);
    expect(next.nodes[1].metadata?.generationSpec?.referenceBindings.map((binding) => binding.nodeId)).toEqual(["asset"]);
    expect(next.connections).toEqual([{ id: expect.any(String), fromNodeId: "asset", toNodeId: "target", managedBy: "reference-fields-v1", referenceOwnerNodeId: "target", referenceBindingId: "node:asset" }]);
});

test("deleting a free line into a generation node removes the line and its input", () => {
    // Generation results, mentions and older canvases create free lines the server never manages.
    const graph = { nodes: [image("asset"), node("target", CanvasNodeType.Image)], connections: [{ id: "free", fromNodeId: "asset", toNodeId: "target" }] };
    const next = removeLine(graph, graph.connections[0]);
    expect(next.connections).toEqual([]);
    expect(next.nodes[1].metadata?.generationSpec?.referenceBindings).toEqual([]);
});

test("deleting a managed line erases it before the save round trip", () => {
    const connected = connectCanvasReferences({ nodes: [image("asset"), node("target", CanvasNodeType.Image)], connections: [] }, [{ id: "drag", fromNodeId: "asset", toNodeId: "target" }]);
    expect(removeLine(connected, connected.connections[0]).connections).toEqual([]);
});

test("free lines of an undeclared node become one managed line per kept reference", () => {
    const graph = {
        nodes: [image("a"), image("b"), node("target", CanvasNodeType.Image)],
        connections: [
            { id: "free-a", fromNodeId: "a", toNodeId: "target" },
            { id: "free-b", fromNodeId: "b", toNodeId: "target" },
        ],
    };
    const next = removeLine(graph, graph.connections[0]);
    expect(next.connections.map((edge) => [edge.fromNodeId, edge.managedBy])).toEqual([["b", "reference-fields-v1"]]);
    expect(next.nodes[2].metadata?.generationSpec?.referenceBindings.map((binding) => binding.nodeId)).toEqual(["b"]);
});

test("replacing a reference swaps its line without waiting for a save", () => {
    const graph = { nodes: [image("old"), image("new"), node("target", CanvasNodeType.Image)], connections: [{ id: "free-old", fromNodeId: "old", toNodeId: "target" }] };
    const next = applyCanvasReferenceGraphEdit(graph, replaceCanvasReferenceInput(graph.nodes, "target", "old", "new", graph.connections)!);
    expect(next.connections.map((edge) => edge.fromNodeId)).toEqual(["new"]);
});

test("a projected graph is stable and keeps its line ids", () => {
    const connected = connectCanvasReferences({ nodes: [image("asset"), node("target", CanvasNodeType.Image)], connections: [] }, [{ id: "drag", fromNodeId: "asset", toNodeId: "target" }]);
    expect(reconcileCanvasReferenceConnections(connected.nodes, connected.connections, ["target"])).toBe(connected.connections);
});

test("storyboard row references are drawn and erased with the row field", () => {
    const connected = connectCanvasReferences({ nodes: [image("scene"), board()], connections: [] }, [{ id: "drag", fromNodeId: "scene", toNodeId: "board", toHandleId: "row:shot" }]);
    expect(connected.nodes[1].metadata?.storyboard?.rows[0].assetBindings?.map((binding) => binding.nodeId)).toEqual(["scene"]);
    expect(connected.connections).toEqual([{ id: expect.any(String), fromNodeId: "scene", toNodeId: "board", toHandleId: "row:shot", storyboardRowId: "shot", managedBy: "reference-fields-v1", referenceOwnerNodeId: "board", referenceBindingId: "node:scene:reference" }]);
    expect(removeLine(connected, connected.connections[0]).connections).toEqual([]);
});

test("removing a row asset also clears its older free line", () => {
    const graph = { nodes: [image("scene"), board([{ nodeId: "scene", role: "style", priority: 60 }])], connections: [{ id: "free", fromNodeId: "scene", toNodeId: "board", toHandleId: "row:shot" }] };
    const next = applyCanvasReferenceGraphEdit(graph, { kind: "set_row_assets", nodeId: "board", rowId: "shot", assetBindings: [] });
    expect(next.connections).toEqual([]);
});

test("deleting a line also removes its exact twins", () => {
    const graph = {
        nodes: [image("asset"), node("target", CanvasNodeType.Image)],
        connections: [
            { id: "free", fromNodeId: "asset", toNodeId: "target" },
            { id: "copy", fromNodeId: "asset", toNodeId: "target", fromHandleId: "output" },
        ],
    };
    expect(deleteCanvasReferenceLine(graph, graph.connections[0])?.connections).toEqual([]);
});

const shotOutputGraph = () => {
    const spec = { version: 1, mode: "image", prompt: "", options: {}, textInputMode: "append-sources", referenceBindings: [{ id: "node:asset", nodeId: "asset", mediaType: "image", role: "reference", order: 0, resolution: "latest" }] };
    const script = board([{ nodeId: "asset", role: "style", priority: 60 }]);
    script.metadata!.storyboard!.rows[0].imageNodeId = "frame";
    return reconcileProjected({ nodes: [image("asset"), node("frame", CanvasNodeType.Image, { generationSpec: spec }), script], connections: [] }, ["board", "frame"]);
};

for (const managed of [true, false]) {
    test(`deleting a ${managed ? "managed" : "free"} shot output line also clears the copy in the output spec`, () => {
        const graph = shotOutputGraph();
        const line = managed ? graph.connections.find((edge) => edge.referenceOwnerNodeId === "frame")! : { id: "free", fromNodeId: "asset", toNodeId: "frame" };
        const next = deleteCanvasReferenceLine(managed ? graph : { ...graph, connections: [...graph.connections, line] }, line)!;
        expect(next.nodes[1].metadata?.generationSpec?.referenceBindings).toEqual([]);
        expect(next.nodes[2].metadata?.storyboard?.rows[0].assetBindings).toEqual([]);
        expect(next.connections.map((edge) => edge.referenceBindingId)).toEqual(["output:image"]);
    });
}

const sharedGraph = () => {
    const script = board();
    script.metadata!.storyboard!.referenceNodeIds = ["asset"];
    return reconcileProjected({ nodes: [image("asset"), script], connections: [] }, ["board"]);
};

test("shared references are still edited through their own field", () => {
    const graph = sharedGraph();
    expect(() => deleteCanvasReferenceLine(graph, graph.connections.find((edge) => edge.toHandleId === "row:shot")!)).toThrow("共享引用");
});

test("a free line the owning field does not draw stays deletable", () => {
    // Older canvases may carry a plain row line for an image that only the shared list declares.
    const graph = { nodes: [image("asset"), image("loose"), sharedGraph().nodes[1]], connections: [{ id: "free", fromNodeId: "loose", toNodeId: "board", toHandleId: "row:shot" }] };
    expect(deleteCanvasReferenceLine(graph, graph.connections[0])).toBeUndefined();
});

test("lines that are not references are left to the plain delete path", () => {
    const graph = { nodes: [node("note", CanvasNodeType.Text), node("frame", CanvasNodeType.Frame)], connections: [{ id: "free", fromNodeId: "note", toNodeId: "frame" }] };
    expect(deleteCanvasReferenceLine(graph, graph.connections[0])).toBeUndefined();
});

test("other owners and batch tables keep their own projection", () => {
    const connections: CanvasConnection[] = [
        { id: "other", fromNodeId: "asset", toNodeId: "other", managedBy: "reference-fields-v1", referenceOwnerNodeId: "other", referenceBindingId: "node:asset" },
        { id: "cell", fromNodeId: "asset", toNodeId: "table", toHandleId: "batch-reference:reference-1", managedBy: "reference-fields-v1", referenceOwnerNodeId: "table", referenceBindingId: "node:asset:reference" },
    ];
    const nodes = [image("asset"), node("other", CanvasNodeType.Image), node("table", CanvasNodeType.BatchTable, { batchTable: { rows: [] } })];
    expect(reconcileCanvasReferenceConnections(nodes, connections, ["table"])).toBe(connections);
    expect(reconcileCanvasReferenceConnections(nodes, connections, ["missing"])).toBe(connections);
});

test("missing and self references never become lines", () => {
    const target = node("target", CanvasNodeType.Image, {
        generationSpec: {
            version: 1,
            mode: "image",
            prompt: "",
            options: {},
            textInputMode: "append-sources",
            referenceBindings: [
                { id: "node:gone", nodeId: "gone", mediaType: "image", role: "reference", order: 0, resolution: "latest" },
                { id: "node:target", nodeId: "target", mediaType: "image", role: "reference", order: 1, resolution: "latest" },
            ],
        },
    });
    expect(reconcileCanvasReferenceConnections([target], [], ["target"])).toEqual([]);
});
