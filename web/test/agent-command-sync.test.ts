import { expect, test } from "bun:test";
import localforage from "localforage";
import { apiClient } from "../src/services/api/request";
import { initializeRemoteUserDataSession, resetRemoteUserDataSync, saveRemoteUserDataNow, applyAgentCanvasPatches, refreshCanvasAfterAgent } from "../src/services/user-data-sync";
import { connectCanvasCommandWorkspace, disconnectCanvasCommandWorkspace, openCanvasCommandSession, registerCanvasCommandEditor } from "../src/stores/canvas/use-canvas-session-store";
import { useCanvasStore, type CanvasProject } from "../src/stores/canvas/use-canvas-store";
import { bindCanvasCommandEditor } from "../src/services/canvas-command-editor-binding";
import { readCanvasSyncDrafts } from "../src/services/canvas-sync-drafts";

test("Agent boundaries save the current editor and reconcile command events without legacy conflicts", async () => {
    const previous = { window: globalThis.window, adapter: apiClient.defaults.adapter, get: localforage.getItem, set: localforage.setItem };
    const storage = new Map<string, unknown>();
    const scope = "agent-command-owner",
        id = "agent-command-canvas";
    Object.defineProperty(globalThis, "window", { configurable: true, value: { localStorage: { getItem: () => scope }, setTimeout, clearTimeout } });
    localforage.getItem = (async (key: string) => storage.get(key) ?? null) as typeof localforage.getItem;
    localforage.setItem = (async (key: string, value: unknown) => {
        storage.set(key, structuredClone(value));
        return value;
    }) as typeof localforage.setItem;
    const remote = {
        id,
        title: "Canvas",
        revision: 1,
        createdAt: "2026-10-24",
        updatedAt: "2026-10-24",
        nodes: [{ id: "text", type: "text", title: "Script", position: { x: 0, y: 0 }, width: 200, height: 100, metadata: { content: "old" } }],
        connections: [],
        chatSessions: [],
        activeChatId: null,
    } as CanvasProject;
    const capabilities = { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" } } }], structuredFields: {} };
    let revision = 1,
        commandCalls = 0;
    const events: any[] = [];
    let unregister = () => {};
    apiClient.defaults.adapter = async (config) => {
        let data: unknown;
        if (config.url === "/desktop/capabilities") data = capabilities;
        else if (config.url?.endsWith("/snapshot"))
            data = { protocolVersion: 1, canvasId: id, documentEpoch: "epoch", revision, eventSeq: revision - 1, document: structuredClone(remote), entityVersions: { "node:text": revision }, entityGenerations: { "node:text": 1 } };
        else if (config.url?.endsWith("/events")) data = events.filter((event) => event.eventSeq > Number(config.params?.after || 0));
        else if (config.url?.endsWith("/commands")) {
            commandCalls++;
            const request = JSON.parse(config.data);
            remote.nodes[0].metadata.content = request.operations[0].patch.content;
            data = {
                protocolVersion: 1,
                canvasId: id,
                documentEpoch: "epoch",
                clientId: request.clientId,
                operationId: request.operationId,
                revision: ++revision,
                eventSeq: revision - 1,
                changes: { entities: [{ key: "node:text", value: structuredClone(remote.nodes[0]) }] },
                entityVersions: { "node:text": revision },
                entityGenerations: { "node:text": 1 },
            };
        } else throw new Error(`Unexpected legacy request ${config.method} ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    try {
        useCanvasStore.setState({ projects: [structuredClone(remote)] });
        await initializeRemoteUserDataSession(scope);
        await connectCanvasCommandWorkspace(scope, "agent-command-client");
        const session = await openCanvasCommandSession(id);
        const draftsBefore = await readCanvasSyncDrafts(id, scope);
        let live = structuredClone(session.state.projected);
        const binding = bindCanvasCommandEditor(session, capabilities, {
            refs: { nodes: { current: live.nodes }, connections: { current: live.connections } },
            read: () => live,
            present: (document) => {
                live = document;
                useCanvasStore.setState({ projects: [{ ...remote, ...document } as CanvasProject] });
            },
        });
        unregister = registerCanvasCommandEditor(id, { commit: () => binding.commit(), dispose: () => binding.dispose() });
        (live.nodes as typeof remote.nodes)[0].metadata.content = "new script";
        await saveRemoteUserDataNow(id);
        expect(commandCalls).toBe(1);
        expect(remote.nodes[0].metadata.content).toBe("new script");
        const node = { ...remote.nodes[0], id: "agent-node" };
        events.push({
            protocolVersion: 1,
            canvasId: id,
            documentEpoch: "epoch",
            clientId: "agent",
            operationId: "agent-write",
            revision: ++revision,
            eventSeq: revision - 1,
            changes: { entities: [{ key: "node:agent-node", value: node }] },
            entityVersions: { "node:agent-node": 1 },
        });
        await applyAgentCanvasPatches(id, [{ canvasId: id, baseRevision: 2, revision: 3, nodes: [{ before: null, after: node }], connections: [] }]);
        await refreshCanvasAfterAgent(id);
        expect(session.state.projected.nodes).toHaveLength(2);
        expect(session.state.saveState).toBe("saved");
        expect(await readCanvasSyncDrafts(id, scope)).toEqual(draftsBefore);
    } finally {
        unregister();
        await disconnectCanvasCommandWorkspace();
        resetRemoteUserDataSync();
        useCanvasStore.setState({ projects: [] });
        apiClient.defaults.adapter = previous.adapter;
        localforage.getItem = previous.get;
        localforage.setItem = previous.set;
        Object.defineProperty(globalThis, "window", { configurable: true, value: previous.window });
    }
});
