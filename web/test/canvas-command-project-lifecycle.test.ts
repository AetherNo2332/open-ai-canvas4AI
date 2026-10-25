import { expect, test } from "bun:test";
import { CANVAS_DRAFT_UNSUPPORTED, attachCanvasCommandProject, canvasCommandSyncProgress } from "../src/services/canvas-command-project-lifecycle";
import { CanvasCommandSession, type CommandTransport } from "../src/services/canvas-command-session";
import { useSyncProgressStore } from "../src/stores/use-sync-progress-store";
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

test("a draft the host cannot express stays unsaved without any request and recovers once undone", async () => {
    const node = { id: "text", type: "text", position: { x: 0, y: 0 }, metadata: { content: "saved", extension: "keep" } };
    const sent: any[] = [];
    const session = new CanvasCommandSession(
        {
            snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0, document: { id: "canvas", nodes: [node], connections: [] }, entityVersions: { "node:text": 1 } }),
            events: async () => [],
            execute: async (command) => {
                sent.push(command);
                throw new Error("unexpected command");
            },
            receipt: async () => {
                throw new Error("unexpected receipt lookup");
            },
        } as CommandTransport,
        "client",
    );
    await session.open("canvas");
    let live: any = {};
    const reports: unknown[] = [];
    const editor = await attachCanvasCommandProject({
        canvasId: "canvas",
        session,
        capabilities: { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" } } }], structuredFields: {} },
        refs: { nodes: { current: [] }, connections: { current: [] } },
        read: () => live,
        present: (document) => {
            live = document;
        },
        onDraftConversion: (error) => reports.push(error),
        view: { read: async () => null, write: async () => {} },
        readViewport: () => ({ x: 0, y: 0, k: 1 }),
        presentViewport: () => {},
    });
    try {
        live = { ...live, nodes: [{ ...node, metadata: { ...node.metadata, extension: "changed" } }] };
        await expect(editor.commit()).rejects.toThrow("canvas_editor_unmapped:node:text:metadata.extension");
        expect(await editor.flush()).toBe(false);
        // Nothing was queued, so the session alone still reads as saved.
        expect(sent).toHaveLength(0);
        expect(session.state.pending).toHaveLength(0);
        expect(session.state.saveState).toBe("saved");
        expect(reports.at(-1)).toBeInstanceOf(Error);
        const progress = canvasCommandSyncProgress(session.state, { detail: (reports.at(-1) as Error).message });
        expect(progress).toMatchObject({ phase: "error", reason: CANVAS_DRAFT_UNSUPPORTED });
        expect(progress.message).toContain("canvas_editor_unmapped:node:text:metadata.extension");
        expect(canvasCommandSyncProgress(session.state, { detail: "x", backup: "saved" }).message).toContain("本地草稿");
        useSyncProgressStore.getState().setProjectProgress("canvas", progress);
        // The leave-page guard covers every phase other than done.
        expect(useSyncProgressStore.getState().isAnySyncing()).toBe(true);

        live = { ...live, nodes: [node] };
        await editor.commit();
        expect(reports.at(-1)).toBeUndefined();
        expect(sent).toHaveLength(0);
        useSyncProgressStore.getState().setProjectProgress("canvas", canvasCommandSyncProgress(session.state));
        expect(useSyncProgressStore.getState().syncingProjects.canvas).toMatchObject({ phase: "done", reason: undefined });
        expect(useSyncProgressStore.getState().isAnySyncing()).toBe(false);
    } finally {
        useSyncProgressStore.getState().setProjectProgress("canvas", null);
        editor.dispose();
        session.close();
    }
});

test("conflicts still take precedence over a local draft failure in the save status", () => {
    const state = { saveState: "conflict", rejections: [], pending: [] } as any;
    expect(canvasCommandSyncProgress(state, { detail: "canvas_editor_unmapped:node:a:metadata.x" })).toMatchObject({ phase: "conflict" });
    expect(canvasCommandSyncProgress({ ...state, saveState: "saved" })).toMatchObject({ phase: "done" });
    expect(canvasCommandSyncProgress({ ...state, saveState: "error", rejections: [{ reason: "node_locked", message: "其他窗口正在编辑" }] })).toMatchObject({ phase: "error", reason: "node_locked", message: "其他窗口正在编辑" });
});
