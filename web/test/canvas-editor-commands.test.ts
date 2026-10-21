import { expect, test } from "bun:test";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
const capabilities = {
    protocolVersion: 1,
    nodes: [
        { type: "text", patchFields: { title: { path: "title" }, x: { path: "position.x" }, y: { path: "position.y" }, content: { path: "metadata.content" }, locked: { path: "metadata.locked" } } },
        { type: "script", patchFields: { title: { path: "title" } } },
    ],
    structuredFields: { storyboard: { dialogue: {}, durationSeconds: {} }, batch: { prompt: {}, enabled: {} } },
};
const before = () => ({
    id: "canvas",
    nodes: [
        { id: "a", type: "text", title: "A", position: { x: 0, y: 0 }, metadata: { content: "old", extension: "keep" } },
        { id: "board", type: "script", title: "board", metadata: { storyboard: { rows: [{ id: "row", dialogue: "old", durationSeconds: 5, extension: "preserve" }] } } },
    ],
    connections: [],
});
test("actual editor node and row changes map to canonical field and native row operations", () => {
    const source = before(),
        next = structuredClone(source);
    next.nodes[0].metadata.content = "draft";
    next.nodes[0].position.x = 20;
    next.nodes[1].metadata.storyboard.rows[0].dialogue = "new";
    const operations = canvasEditorCommands(source, next, capabilities as any);
    expect(operations).toEqual([
        { type: "update_node", id: "a", patch: { x: 20, content: "draft" } },
        { type: "edit_storyboard", id: "board", action: "update", rowId: "row", patch: { dialogue: "new" } },
    ]);
    expect(source.nodes[0].metadata.content).toBe("old");
    expect(JSON.stringify(operations)).not.toContain("extension");
});
test("graph add delete parent and reorder use semantic operations rather than entity replacement", () => {
    const source = before(),
        next = structuredClone(source) as any;
    next.nodes = next.nodes.filter((n: any) => n.id !== "board");
    next.nodes.push({ id: "new", type: "text", title: "New", position: { x: 10, y: 30 }, metadata: { content: "hello" } });
    next.nodes[0].parentId = "new";
    next.nodes.reverse();
    next.connections.push({ id: "edge", fromNodeId: "a", toNodeId: "new" });
    const ops = canvasEditorCommands(source, next, capabilities as any);
    expect(ops).toContainEqual({ type: "delete_node", id: "board" });
    expect(ops).toContainEqual({ type: "add_node", id: "new", nodeType: "text", x: 10, y: 30, title: "New", content: "hello" });
    expect(ops).toContainEqual({ type: "set_parent", id: "a", parentId: "new" });
    expect(ops).toContainEqual({ type: "connect_nodes", id: "edge", fromNodeId: "a", toNodeId: "new" });
    expect(ops.at(-1)).toEqual({ type: "reorder_nodes", id: "canvas", nodeIds: ["new", "a"] });
});
test("unmapped editing fields are surfaced with their paths instead of silently lost", () => {
    const source = before(),
        next = structuredClone(source) as any;
    next.nodes[0].metadata.extension = "changed";
    expect(() => canvasEditorCommands(source, next, capabilities as any)).toThrow("canvas_editor_unmapped:node:a:metadata.extension");
});
test("shared appearance uses a small settings patch and viewport stays personal", () => {
    const source = { ...before(), appearance: { mode: "light", extension: "keep" }, backgroundMode: "blank", viewport: { x: 0, y: 0, k: 1 } },
        next = structuredClone(source);
    next.appearance.mode = "dark";
    next.viewport.x = 20;
    expect(canvasEditorCommands(source, next, capabilities as any)).toEqual([{ type: "edit_canvas_settings", id: "canvas", patch: { appearance: { mode: "dark" } } }]);
    const edges = {
        ...before(),
        connections: [
            { id: "a", fromNodeId: "a", toNodeId: "board" },
            { id: "b", fromNodeId: "board", toNodeId: "a" },
        ],
    };
    const order = { ...edges, connections: [...edges.connections].reverse() };
    expect(canvasEditorCommands(edges, order, capabilities as any)).toEqual([{ type: "reorder_connections", id: "canvas", nodeIds: ["b", "a"] }]);
});
test("new node creative options use the registry instead of dropping whole metadata branches", () => {
    const source = before(),
        next = structuredClone(source) as any;
    next.nodes.push({ id: "new", type: "text", title: "New", position: { x: 0, y: 0 }, metadata: { content: "new", locked: true, status: "idle" } });
    expect(canvasEditorCommands(source, next, capabilities as any)).toContainEqual({ type: "update_node", id: "new", patch: { locked: true } });
});
test("native anchors and structured output bindings travel through connection commands", () => {
    const source = before(),
        next = structuredClone(source) as any;
    next.nodes[1].metadata.storyboard.rows[0].imageNodeId = "a";
    next.connections.push({ id: "output", fromNodeId: "board", toNodeId: "a", fromHandleId: "row:row", fromAnchorRatio: 0.3, toAnchorRatio: 0.7, relation: "storyboard-output", storyboardRowId: "row" });
    const ops = canvasEditorCommands(source, next, capabilities as any);
    expect(ops).toEqual([{ type: "connect_nodes", ...next.connections[0] }]);
    const detached = structuredClone(next);
    detached.connections = [];
    delete detached.nodes[1].metadata.storyboard.rows[0].imageNodeId;
    expect(canvasEditorCommands(next, detached, capabilities as any)).toEqual([{ type: "delete_connection", id: "output" }]);
    const forged = structuredClone(source) as any;
    forged.nodes[1].metadata.storyboard.rows[0].imageNodeId = "a";
    expect(() => canvasEditorCommands(source, forged, capabilities as any)).toThrow("imageNodeId");
});
test("timeline edits map to native track and clip operations while retaining extensions", () => {
    const caps = { ...capabilities, timelineFields: { track: { kind: "kind", label: "string", order: "number" }, clip: { kind: "kind", nodeId: "string", trackId: "string", startMs: "number", durationMs: "positive", title: "string" } } };
    const source = {
        ...before(),
        timeline: {
            version: 2,
            tracks: [{ id: "track", kind: "video", label: "视频", order: 0, extension: "keep" }],
            clips: [{ id: "clip", kind: "video", nodeId: "a", trackId: "track", startMs: 0, durationMs: 1000, extension: "keep" }],
            durationMs: 1000,
        },
    };
    const next = structuredClone(source);
    next.timeline.clips[0].startMs = 1200;
    next.timeline.durationMs = 2200;
    expect(canvasEditorCommands(source, next, caps as any)).toEqual([{ type: "edit_timeline", id: "canvas", timelineOperations: [{ type: "upsert_clip", id: "clip", patch: { startMs: 1200 } }] }]);
    const missing = { ...next, timeline: undefined };
    expect(canvasEditorCommands(source, missing, caps as any)).toEqual([{ type: "edit_timeline", id: "canvas", timelineOperations: [{ type: "clear" }] }]);
    next.timeline.clips[0].extension = "changed";
    expect(() => canvasEditorCommands(source, next, caps as any)).toThrow("extension");
});
test("drawing saves publish native records and explicit layer order instead of metadata replacement", () => {
    const caps = { ...capabilities, nodes: [...capabilities.nodes, { type: "drawing", patchFields: { drawingEngine: { path: "metadata.drawingEngine" } } }] };
    const source = { id: "canvas", nodes: [{ id: "drawing", type: "drawing", metadata: { drawingId: "document", drawingEngine: "excalidraw" } }], connections: [] } as any;
    const next = structuredClone(source),
        shape = { id: "shape", type: "rectangle", x: 0, y: 0, width: 40, height: 30 };
    next.nodes[0].metadata.drawingDocument = { version: 2, engine: "excalidraw", snapshot: { elements: [shape], files: {}, appState: {} }, revision: 3, updatedAt: "local", shapeCount: 1, pageCount: 1 };
    next.nodes[0].metadata.drawingRevision = 3;
    next.nodes[0].metadata.drawingShapeCount = 1;
    const ops = canvasEditorCommands(source, next, caps as any);
    expect(ops).toEqual([
        { type: "edit_drawing", id: "drawing", engine: "excalidraw", drawingId: "document", drawingSnapshot: { elements: [], files: {}, appState: {} }, drawingOperations: [{ type: "upsert", id: "shape", record: shape }], drawingElementIds: ["shape"] },
    ]);
    const changed = structuredClone(next);
    changed.nodes[0].metadata.drawingDocument.snapshot.elements[0].x = 20;
    expect(canvasEditorCommands(next, changed, caps as any)[0].drawingOperations).toEqual([{ type: "upsert", id: "shape", record: { ...shape, x: 20 } }]);
});
test("editor-drawn managed edges on a legacy canvas leave the field operation to the host gate", () => {
    const source = before(),
        next = structuredClone(source) as any;
    // A legacy canvas has no referenceFieldsVersion; the host answers the field edit with reference_migration_required.
    next.connections.push({ id: "drawn", fromNodeId: "a", toNodeId: "board", toHandleId: "storyboard:context", managedBy: "reference-fields-v1", referenceOwnerNodeId: "board", referenceBindingId: "node:a:source-text" });
    expect(canvasEditorCommands(source, next, capabilities as any)).toEqual([]);
    const removed = structuredClone(next);
    removed.connections = [];
    expect(canvasEditorCommands(next, removed, capabilities as any)).toEqual([]);
});
