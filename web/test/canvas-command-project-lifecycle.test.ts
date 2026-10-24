import { expect, test } from "bun:test";
import { attachCanvasCommandProject } from "../src/services/canvas-command-project-lifecycle";
import { CanvasCommandSession, type CommandTransport } from "../src/services/canvas-command-session";
import { customCanvasAppearanceFromTheme, defaultCanvasAppearance, normalizeCanvasAppearance } from "../src/lib/canvas/canvas-appearance";
import { canvasDocumentChanges, projectCanvasOperations, type CanvasDocument } from "../src/lib/canvas/canvas-event-projection";
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

test.each(["preset", "custom"] as const)("normalized host %s appearance stays idle across reconnects and saves one real color setting change", async (mode) => {
    // Go JSON map responses sort keys; presentation rebuilds custom values and
    // preset theme memory in the appearance normalizer's field order.
    const hostJSON = (value: unknown): any =>
        Array.isArray(value)
            ? value.map(hostJSON)
            : value && typeof value === "object"
              ? Object.fromEntries(
                    Object.keys(value)
                        .sort()
                        .map((key) => [key, hostJSON((value as Record<string, unknown>)[key])]),
                )
              : value;
    const appearance = mode === "custom" ? customCanvasAppearanceFromTheme("dark") : defaultCanvasAppearance("dark");
    const values = (value: any) => (mode === "custom" ? value.custom : value.preset.values);
    let document: CanvasDocument = hostJSON({ id: "canvas", nodes: [], connections: [], appearance });
    let revision = 1;
    let eventSeq = 0;
    let live: any = {};
    const sent: any[] = [];
    const session = new CanvasCommandSession(
        {
            snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision, eventSeq, document: structuredClone(document), entityVersions: {} }),
            events: async () => [],
            execute: async (command) => {
                sent.push(command);
                const next = hostJSON(projectCanvasOperations(document, command.operations));
                const changes = canvasDocumentChanges(document, next);
                document = next;
                return { protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", clientId: "client", operationId: command.operationId, actorId: "owner", revision: ++revision, eventSeq: ++eventSeq, changes, entityVersions: {} };
            },
            receipt: async () => {
                throw new Error("unexpected receipt lookup");
            },
        } as CommandTransport,
        "client",
    );
    await session.open("canvas");
    const editor = await attachCanvasCommandProject({
        canvasId: "canvas",
        session,
        capabilities: { protocolVersion: 1, nodes: [], structuredFields: {} },
        refs: { nodes: { current: [] }, connections: { current: [] } },
        read: () => live,
        present: (projected) => {
            live = { ...projected, appearance: normalizeCanvasAppearance(projected.appearance, "dark") };
        },
        view: { read: async () => null, write: async () => {} },
        readViewport: () => ({ x: 0, y: 0, k: 1 }),
        presentViewport: () => {},
    });
    try {
        for (let cycle = 0; cycle < 5; cycle++) {
            await session.reconnect();
            await editor.commit();
            expect(await session.flush()).toBe(true);
            expect(sent).toHaveLength(0);
        }
        values(live.appearance).gridOpacity = 47;
        await editor.commit();
        expect(sent).toHaveLength(1);
        expect(sent[0].operations).toEqual([{ type: "edit_canvas_settings", id: "canvas", patch: { appearance: mode === "custom" ? { custom: { gridOpacity: 47 } } : { preset: { values: { gridOpacity: 47 } } } } }]);
        expect(values(document.appearance).gridOpacity).toBe(47);
        for (let cycle = 0; cycle < 5; cycle++) {
            await session.reconnect();
            await editor.commit();
            expect(await session.flush()).toBe(true);
        }
        expect(sent).toHaveLength(1);
        expect(revision).toBe(2);
    } finally {
        editor.dispose();
        session.close();
    }
});
