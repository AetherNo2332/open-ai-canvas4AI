import { expect, test } from "bun:test";
import { applyCanvasEventChanges, projectCanvasOperations } from "../src/lib/canvas/canvas-event-projection";

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
