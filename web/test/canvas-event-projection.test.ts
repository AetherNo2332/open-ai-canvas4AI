import { expect, test } from "bun:test";
import { applyCanvasDraftChanges, applyCanvasEventChanges, canvasDocumentChanges, projectCanvasOperations } from "../src/lib/canvas/canvas-event-projection";

test("object key ordering is not a draft change while array ordering and explicit null are", () => {
    const before = { nodes: [{ id: "a", metadata: { content: "saved", extension: { a: 1, b: 2 } } }], connections: [], appearance: { mode: "preset", values: { a: 1, b: 2 } } };
    const reordered = { appearance: { values: { b: 2, a: 1 }, mode: "preset" }, connections: [], nodes: [{ metadata: { extension: { b: 2, a: 1 }, content: "saved" }, id: "a" }] };
    expect(canvasDocumentChanges(before, reordered)).toEqual({ entities: [], fields: {} });
    const source = { nodes: [{ id: "a", metadata: { values: ["first", "second"] } }, { id: "b" }], connections: [], nullable: undefined };
    const draft = structuredClone(source) as any;
    draft.nodes.reverse();
    draft.nodes[1].metadata.values.reverse();
    draft.nullable = null;
    const changes = canvasDocumentChanges(source, draft);
    expect(changes.nodeOrder).toEqual(["b", "a"]);
    expect(changes.entities).toEqual([{ key: "node:a", value: draft.nodes[1] }]);
    expect(changes.fields).toEqual({ nullable: { value: null } });
});

test("a reordered unchanged object draft does not replace a newer host scalar", () => {
    const base = { nodes: [], connections: [], extension: { a: 1, b: 2 } };
    const changes = { entities: [], fields: { extension: { value: { b: 2, a: 1 } } } };
    expect(applyCanvasDraftChanges({ ...base, extension: "new host value" }, changes, {}, { extension: base.extension }).extension).toBe("new host value");
});

test("event projection preserves extensions, explicit deletion, null fields and entity order", () => {
    const before = {
        nodes: [
            { id: "a", type: "text", metadata: { extension: { keep: true }, content: "before" } },
            { id: "b", type: "text" },
        ],
        connections: [{ id: "e", fromNodeId: "a", toNodeId: "b" }],
        appearance: { unknown: 1 },
        timeline: { old: true },
    };
    const next = applyCanvasEventChanges(before, {
        entities: [
            { key: "node:a", value: { ...before.nodes[0], metadata: { ...before.nodes[0].metadata, content: "after" } } },
            { key: "connection:e", delete: true },
        ],
        fields: { timeline: { delete: true }, nullable: { value: null } },
        nodeOrder: ["b", "a"],
    });
    expect((next.nodes as any[]).map((n) => n.id)).toEqual(["b", "a"]);
    expect((next.nodes as any[])[1].metadata.extension).toEqual({ keep: true });
    expect(next.connections).toEqual([]);
    expect(next.appearance).toEqual({ unknown: 1 });
    expect("timeline" in next).toBe(false);
    expect(next.nullable).toBeNull();
    expect(before.nodes[0].metadata?.content).toBe("before");
    expect(before.connections.length).toBe(1);
});
test("local operation preview updates geometry and metadata without changing input", () => {
    const before = { id: "canvas", nodes: [{ id: "a", type: "text", position: { x: 1, y: 2 }, metadata: { extension: "keep", content: "before" } }], connections: [] };
    const next = projectCanvasOperations(before, [{ type: "update_node", id: "a", patch: { content: "draft", x: 20, title: "new" } }]);
    const n = (next.nodes as any[])[0];
    expect(n.position.x).toBe(20);
    expect(n.metadata.content).toBe("draft");
    expect(n.metadata.extension).toBe("keep");
    expect(n.title).toBe("new");
    expect(before.nodes[0].position.x).toBe(1);
});
test("shared settings and connection ordering have an immutable optimistic preview", () => {
    const doc = { nodes: [], connections: [{ id: "a" }, { id: "b" }], appearance: { mode: "light", custom: { baseTheme: "light", extension: "keep" } } };
    const next = projectCanvasOperations(doc, [
        { type: "edit_canvas_settings", id: "canvas", patch: { appearance: { custom: { baseTheme: "dark" } } } },
        { type: "reorder_connections", id: "canvas", nodeIds: ["b", "a"] },
    ]);
    expect(next.appearance).toEqual({ mode: "light", custom: { baseTheme: "dark", extension: "keep" } });
    expect((next.connections as any[]).map((e) => e.id)).toEqual(["b", "a"]);
    expect(doc.appearance.custom.baseTheme).toBe("light");
});
