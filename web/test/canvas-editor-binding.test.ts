import { expect, test } from "bun:test";
import { CanvasCommandSession, type CommandTransport } from "../src/services/canvas-command-session";
import { bindCanvasCommandEditor } from "../src/services/canvas-command-editor-binding";
import type { CanvasDocument } from "../src/lib/canvas/canvas-event-projection";
const initial = { id: "canvas", nodes: [{ id: "a", type: "text", metadata: { content: "before", extension: "keep" } }], connections: [] };
const cap = { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" } } }], structuredFields: {} };

test("local progress survives unrelated events but host terminal state wins", async () => {
    const document = { ...structuredClone(initial), nodes: [{ ...initial.nodes[0], metadata: { content: "before", taskId: "task", taskStatus: "running", status: "loading", taskProgress: 0 } }] };
    const s = new CanvasCommandSession(
        {
            snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0, entityVersions: { "node:a": 1 }, document }),
            events: async () => [],
            execute: async () => {
                throw new Error("unexpected command");
            },
            receipt: async () => {
                throw new Error("missing");
            },
        } as CommandTransport,
        "client",
    );
    await s.open("canvas");
    let live: any = structuredClone(document);
    const binding = bindCanvasCommandEditor(s, cap, {
        refs: { nodes: { current: live.nodes }, connections: { current: live.connections } },
        read: () => live,
        present: (doc) => {
            live = doc;
        },
    });
    live.nodes[0].metadata.taskProgress = 45;
    await s.receive({
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 2,
        eventSeq: 1,
        clientId: "other",
        actorId: "other",
        operationId: "title",
        changes: { entities: [], fields: { title: { value: "Other edit" } } },
        entityVersions: { "project:title": 2 },
    });
    expect(live.nodes[0].metadata.taskProgress).toBe(45);
    await binding.commit();
    expect(s.state.pending).toEqual([]);
    await s.receive({
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 3,
        eventSeq: 2,
        clientId: "worker",
        actorId: "owner",
        operationId: "finished",
        changes: { entities: [{ key: "node:a", value: { ...document.nodes[0], metadata: { content: "result", taskId: "task", taskStatus: "succeeded", status: "success", taskProgress: 100 } } }] },
        entityVersions: { "node:a": 3 },
    });
    expect(live.nodes[0].metadata.taskProgress).toBe(100);
    expect(live.nodes[0].metadata.content).toBe("result");
    binding.dispose();
    s.close();
});
test("editor binding updates refs before rendering and preserves text not yet submitted across task events", async () => {
    const s = new CanvasCommandSession(
        {
            snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0, entityVersions: { "node:a": 1 }, document: structuredClone(initial) }),
            events: async () => [],
            execute: async () => {
                throw new Error("offline");
            },
            receipt: async () => {
                throw new Error("offline");
            },
        } as CommandTransport,
        "client",
    );
    await s.open("canvas");
    let live: CanvasDocument = structuredClone(initial);
    const refs = { nodes: { current: initial.nodes as any[] }, connections: { current: [] as any[] } };
    let presentations = 0;
    const binding = bindCanvasCommandEditor(s, cap, {
        refs,
        read: () => live,
        present: (doc) => {
            expect(refs.nodes.current).toBe(doc.nodes as any[]);
            expect(refs.connections.current).toBe(doc.connections as any[]);
            live = doc;
            presentations++;
        },
    });
    (live.nodes as any[])[0].metadata.content = "unsent draft";
    await s.receive({
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 2,
        eventSeq: 1,
        clientId: "worker",
        actorId: "owner",
        operationId: "result",
        entityVersions: { "node:a": 2 },
        changes: { entities: [{ key: "node:a", value: { id: "a", type: "text", metadata: { content: "generated", taskId: "task", taskStatus: "succeeded", extension: "keep" } } }] },
    });
    expect((live.nodes as any[])[0].metadata.content).toBe("unsent draft");
    expect((live.nodes as any[])[0].metadata.taskId).toBe("task");
    const saved = binding.commit().catch((e) => e);
    await new Promise((r) => setTimeout(r, 0));
    expect(s.state.pending[0].operations).toEqual([{ type: "update_node", id: "a", patch: { content: "unsent draft" } }]);
    expect(s.state.pending[0].expectedEntityVersions["node:a"]).toBe(1);
    expect(presentations).toBeGreaterThan(1);
    binding.dispose();
    s.close();
    await saved;
});
