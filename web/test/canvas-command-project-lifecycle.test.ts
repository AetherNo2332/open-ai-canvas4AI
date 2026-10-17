import { expect, test } from "bun:test";
import { attachCanvasCommandProject } from "../src/services/canvas-command-project-lifecycle";
import { CanvasCommandSession, type CommandTransport } from "../src/services/canvas-command-session";
test("editor lifecycle commits semantic changes, stores personal view and never invokes whole-document save", async () => {
    const sent: any[] = [];
    let live: any = {};
    const views: any[] = [];
    let legacy = 0;
    const node = { id: "text", type: "text", position: { x: 0, y: 0 }, metadata: { content: "saved" } };
    const session = new CanvasCommandSession(
        {
            snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0, document: { id: "canvas", nodes: [node], connections: [] }, entityVersions: { "node:text": 1 } }),
            events: async () => [],
            execute: async (command) => {
                sent.push(command);
                return {
                    protocolVersion: 1,
                    canvasId: "canvas",
                    documentEpoch: "epoch",
                    clientId: "client",
                    actorId: "owner",
                    operationId: command.operationId,
                    revision: 2,
                    eventSeq: 1,
                    changes: { entities: [{ key: "node:text", value: { ...node, metadata: { content: "edited" } } }] },
                    entityVersions: { "node:text": 2 },
                };
            },
            receipt: async () => {
                legacy++;
                throw new Error("unexpected receipt lookup");
            },
        } as CommandTransport,
        "client",
    );
    await session.open("canvas");
    const refs = { nodes: { current: [] as any[] }, connections: { current: [] as any[] } };
    let viewport = { x: 0, y: 0, k: 1 };
    const editor = await attachCanvasCommandProject({
        canvasId: "canvas",
        session,
        capabilities: { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" } } }], structuredFields: {} },
        refs,
        read: () => live,
        present: (document) => {
            expect(refs.nodes.current).toEqual(document.nodes);
            live = document;
        },
        view: {
            read: async () => ({ x: 100, y: 50, k: 0.8 }),
            write: async (id, value) => {
                views.push({ id, value });
            },
        },
        readViewport: () => viewport,
        presentViewport: (value) => {
            viewport = value;
        },
    });
    expect(viewport).toEqual({ x: 100, y: 50, k: 0.8 });
    live.nodes[0].metadata.content = "edited";
    viewport.x = 120;
    expect(await editor.flush()).toBe(true);
    expect(sent).toHaveLength(1);
    expect(sent[0].operations).toEqual([{ type: "update_node", id: "text", patch: { content: "edited" } }]);
    expect(views.at(-1)).toEqual({ id: "canvas", value: { x: 120, y: 50, k: 0.8 } });
    expect(legacy).toBe(0);
    editor.dispose();
    session.close();
});
