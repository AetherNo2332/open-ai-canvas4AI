import { afterEach, expect, test } from "bun:test";
import localforage from "localforage";
import { apiClient, ApiError } from "@/services/api/request";
import { ensureOrdinaryCanvasCommandWorkspace } from "@/services/canvas-command-bootstrap";
import { associateCanvasProjectWithRemoteSync, createCanvasProjectWithRemoteSync, initializeRemoteUserDataSession, resetRemoteUserDataSync, saveRemoteUserDataNow } from "@/services/user-data-sync";
import { readCanvasSyncDrafts } from "@/services/canvas-sync-drafts";
import { disconnectCanvasCommandWorkspace, openCanvasCommandSession, useCanvasSessionStore } from "@/stores/canvas/use-canvas-session-store";
import { useCanvasStore, type CanvasProject } from "@/stores/canvas/use-canvas-store";
import { CanvasNodeType } from "@/types/canvas";
import { canvasContentHash } from "@/lib/canvas/canvas-content";

const previous = { window: globalThis.window, adapter: apiClient.defaults.adapter, get: localforage.getItem, set: localforage.setItem };
const storage = new Map<string, unknown>();
const scope = "ordinary-command-owner";
function setup() {
    Object.defineProperty(globalThis, "window", { configurable: true, value: { localStorage: { getItem: () => scope }, setTimeout, clearTimeout } });
    localforage.getItem = (async (key: string) => storage.get(key) ?? null) as typeof localforage.getItem;
    localforage.setItem = (async (key: string, value: unknown) => {
        storage.set(key, value);
        return value;
    }) as typeof localforage.setItem;
}
afterEach(async () => {
    await disconnectCanvasCommandWorkspace();
    resetRemoteUserDataSync();
    useCanvasStore.getState().replaceProjects([]);
    Object.defineProperty(globalThis, "window", { configurable: true, value: previous.window });
    apiClient.defaults.adapter = previous.adapter;
    localforage.getItem = previous.get;
    localforage.setItem = previous.set;
    storage.clear();
});

test("an imported revision-zero canvas is archived and created once with CAS before command open", async () => {
    setup();
    const id = useCanvasStore.getState().importProject({ title: "Imported", nodes: [], connections: [] });
    let cloud: CanvasProject | undefined;
    const puts: CanvasProject[] = [];
    apiClient.defaults.adapter = async (config) => {
        let data: unknown;
        if (config.url === "/desktop/capabilities") data = { protocolVersion: 1, nodes: [], structuredFields: {} };
        else if (config.url?.endsWith("/snapshot")) {
            if (!cloud) throw new ApiError("missing", { status: 404 });
            data = { protocolVersion: 1, canvasId: id, documentEpoch: "e", revision: 1, eventSeq: 0, document: cloud, entityVersions: {} };
        } else if (config.method === "put" && config.url === `/canvas-projects/${id}`) {
            const { project } = JSON.parse(config.data);
            puts.push(project);
            cloud = { ...project, revision: 1 };
            data = { project: cloud };
        } else if (config.url?.endsWith("/events")) data = [];
        else throw new Error(`Unexpected ${config.method} ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await initializeRemoteUserDataSession(scope);
    await ensureOrdinaryCanvasCommandWorkspace(scope);
    await saveRemoteUserDataNow(id);
    expect(puts).toHaveLength(1);
    expect(puts[0].revision).toBe(0);
    expect(cloud?.title).toBe("Imported");
    expect((await readCanvasSyncDrafts(id, scope)).length).toBeGreaterThan(0);
});

test("project association changes only a fresh remote document and preserves a failed CAS", async () => {
    setup();
    const id = useCanvasStore.getState().createProject("Local title");
    useCanvasStore.setState((state) => ({ projects: state.projects.map((project) => ({ ...project, revision: 1 })) }));
    const cloud = { ...structuredClone(useCanvasStore.getState().openProject(id))!, title: "New cloud title", revision: 7 };
    const puts: CanvasProject[] = [];
    let reject = false;
    apiClient.defaults.adapter = async (config) => {
        let data: unknown;
        if (config.url === "/desktop/capabilities") data = { protocolVersion: 1, nodes: [], structuredFields: {} };
        else if (config.url?.endsWith("/snapshot")) data = { protocolVersion: 1, canvasId: id, documentEpoch: "e", revision: cloud.revision, eventSeq: 0, document: cloud, entityVersions: {} };
        else if (config.url?.endsWith("/events")) data = [];
        else if (config.method === "get") data = { project: cloud };
        else if (config.method === "put") {
            const { project } = JSON.parse(config.data);
            puts.push(project);
            if (reject) throw new ApiError("revision conflict", { status: 409 });
            Object.assign(cloud, project, { revision: cloud.revision + 1 });
            data = { project: cloud };
        } else throw new Error(`Unexpected ${config.method} ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await initializeRemoteUserDataSession(scope);
    await ensureOrdinaryCanvasCommandWorkspace(scope);
    await associateCanvasProjectWithRemoteSync(id, "domain");
    expect(puts[0]).toMatchObject({ title: "New cloud title", revision: 7, projectId: "domain" });
    reject = true;
    await expect(associateCanvasProjectWithRemoteSync(id, "another")).rejects.toMatchObject({ status: 409 });
    expect(puts).toHaveLength(2);
    expect(cloud.projectId).toBe("domain");
});

test("ordinary command activation waits for a remote baseline and archives local content before connecting", async () => {
    setup();
    const id = useCanvasStore.getState().createProject("Cached canvas");
    useCanvasStore.getState().updateProject(id, { nodes: [{ id: "text", type: CanvasNodeType.Text, title: "Text", position: { x: 0, y: 0 }, width: 200, height: 100, metadata: { content: "local draft" } }] });
    useCanvasStore.setState((state) => ({ projects: state.projects.map((project) => ({ ...project, revision: 1 })) }));
    const remote = structuredClone(useCanvasStore.getState().openProject(id)) as CanvasProject;
    remote.nodes[0].metadata.content = "cloud body";
    const baselineHash = await canvasContentHash(remote);
    useCanvasStore.setState((state) => ({ projects: state.projects.map((project) => ({ ...project, remoteContentHash: baselineHash })) }));
    const calls: string[] = [];
    let revision = 1;
    let seq = 0;
    apiClient.defaults.adapter = async (config) => {
        calls.push(`${config.method} ${config.url}`);
        let data: unknown;
        if (config.url === "/desktop/capabilities") {
            expect((await readCanvasSyncDrafts(id, scope))[0]?.project.nodes[0].metadata.content).toBe("local draft");
            data = { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" } } }], structuredFields: {} };
        } else if (config.url?.endsWith("/snapshot")) data = { protocolVersion: 1, canvasId: id, documentEpoch: "epoch", revision, eventSeq: seq, document: remote, entityVersions: { "node:text": 1 }, entityGenerations: { "node:text": 1 } };
        else if (config.url?.endsWith("/events")) data = [];
        else if (config.url?.endsWith("/commands")) {
            const command = JSON.parse(config.data);
            remote.nodes[0].metadata.content = command.operations[0].patch.content;
            data = {
                protocolVersion: 1,
                canvasId: id,
                documentEpoch: "epoch",
                clientId: command.clientId,
                operationId: command.operationId,
                revision: ++revision,
                eventSeq: ++seq,
                changes: { entities: [{ key: "node:text", value: remote.nodes[0] }] },
                entityVersions: { "node:text": revision },
                entityGenerations: { "node:text": 1 },
            };
        } else throw new Error(`Unexpected request: ${config.method} ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await expect(ensureOrdinaryCanvasCommandWorkspace(scope)).rejects.toThrow("基线");
    expect(calls).toEqual([]);
    await initializeRemoteUserDataSession(scope);
    await ensureOrdinaryCanvasCommandWorkspace(scope);
    const session = await openCanvasCommandSession(id);
    await session.flush();
    await saveRemoteUserDataNow([]);

    expect(useCanvasSessionStore.getState().scope).toBe(scope);
    expect(remote.nodes[0].metadata.content).toBe("local draft");
    expect(calls.some((call) => call.endsWith("/commands"))).toBe(true);
    expect(calls.some((call) => call.startsWith("put /canvas-projects/"))).toBe(false);
});

test("reopening restores an unknown original wire instead of converting legacy cache a second time", async () => {
    setup();
    const id = useCanvasStore.getState().createProject("Cached");
    useCanvasStore.getState().updateProject(id, { nodes: [{ id: "n", type: CanvasNodeType.Text, title: "Text", position: { x: 0, y: 0 }, width: 200, height: 100, metadata: { content: "draft" } }] });
    useCanvasStore.setState((state) => ({ projects: state.projects.map((project) => ({ ...project, revision: 1 })) }));
    const cloud = structuredClone(useCanvasStore.getState().openProject(id))!;
    cloud.nodes[0].metadata.content = "cloud";
    const baselineHash = await canvasContentHash(cloud);
    useCanvasStore.setState((state) => ({ projects: state.projects.map((project) => ({ ...project, remoteContentHash: baselineHash })) }));
    const wires: string[] = [];
    apiClient.defaults.adapter = async (config) => {
        let data: unknown;
        if (config.url === "/desktop/capabilities") data = { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" } } }], structuredFields: {} };
        else if (config.url?.endsWith("/snapshot")) data = { protocolVersion: 1, canvasId: id, documentEpoch: "e", revision: 1, eventSeq: 0, document: cloud, entityVersions: { "node:n": 1 }, entityGenerations: { "node:n": 1 } };
        else if (config.url?.includes("/receipts/")) throw new ApiError("not committed", { status: 404 });
        else if (config.url?.endsWith("/events")) data = [];
        else if (config.url?.endsWith("/commands")) {
            wires.push(config.data);
            throw new ApiError("offline", { status: 503 });
        } else throw new Error(`Unexpected ${config.method} ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await initializeRemoteUserDataSession(scope);
    await ensureOrdinaryCanvasCommandWorkspace(scope);
    const first = await openCanvasCommandSession(id);
    expect(await first.flush()).toBe(false);
    await disconnectCanvasCommandWorkspace();
    await ensureOrdinaryCanvasCommandWorkspace(scope);
    const second = await openCanvasCommandSession(id);
    await second.flush();
    expect(second.state.pending).toHaveLength(1);
    expect(second.state.projected?.nodes).toMatchObject([{ metadata: { content: "draft" } }]);
    expect(wires.length).toBeGreaterThanOrEqual(2);
    expect(wires.every((wire) => wire === wires[0])).toBe(true);
});

test.each(["old-epoch", "missing-epoch"])("same revision with %s cannot turn an unverified old cache into a new incarnation command", async (kind) => {
    setup();
    const id = useCanvasStore.getState().createProject("Cache");
    useCanvasStore.getState().updateProject(id, { nodes: [{ id: "n", type: CanvasNodeType.Text, title: "Text", position: { x: 0, y: 0 }, width: 200, height: 100, metadata: { content: "old acknowledged" } }] });
    const base = { ...structuredClone(useCanvasStore.getState().openProject(id))!, revision: 1 };
    const hash = await canvasContentHash(base);
    const cache = { ...base, ...(kind === "old-epoch" ? { documentEpoch: "old" } : {}), remoteContentHash: hash, nodes: base.nodes.map((node) => ({ ...node, metadata: { content: "old unsaved draft" } })) };
    useCanvasStore.setState({ projects: [cache] });
    const cloud = { ...base, nodes: base.nodes.map((node) => ({ ...node, metadata: { content: "new incarnation" } })) };
    let commands = 0;
    apiClient.defaults.adapter = async (config) => {
        let data: unknown;
        if (config.url === "/desktop/capabilities") data = { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" } } }], structuredFields: {} };
        else if (config.url?.endsWith("/snapshot")) data = { protocolVersion: 1, canvasId: id, documentEpoch: "new", revision: 1, eventSeq: 0, document: cloud, entityVersions: { "node:n": 1 }, entityGenerations: { "node:n": 1 } };
        else if (config.url?.endsWith("/events")) data = [];
        else {
            commands++;
            throw new Error("old cache must not write");
        }
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await initializeRemoteUserDataSession(scope);
    await ensureOrdinaryCanvasCommandWorkspace(scope);
    const session = await openCanvasCommandSession(id);
    await session.flush();
    expect(commands).toBe(0);
    expect(session.state.projected?.nodes).toMatchObject([{ metadata: { content: "new incarnation" } }]);
    expect((await readCanvasSyncDrafts(id, scope))[0].project.nodes[0].metadata.content).toBe("old unsaved draft");
});

test.each(["connecting", "failed"] as const)("explicit project save cannot report success while bootstrap is %s; assets-only sync and later recovery remain available", async (phase) => {
    setup();
    await initializeRemoteUserDataSession(scope);
    useCanvasSessionStore.setState({ bootstrapState: phase, scope: null });
    const created = await createCanvasProjectWithRemoteSync("Preserved local");
    expect(created.syncError).toBeInstanceOf(Error);
    expect(useCanvasStore.getState().openProject(created.id)?.title).toBe("Preserved local");
    await expect(saveRemoteUserDataNow(created.id)).rejects.toThrow("尚未就绪");
    await saveRemoteUserDataNow([]);
    let cloud: CanvasProject | undefined;
    let writes = 0;
    apiClient.defaults.adapter = async (config) => {
        let data: unknown;
        if (config.url === "/desktop/capabilities") data = { protocolVersion: 1, nodes: [], structuredFields: {} };
        else if (config.url?.endsWith("/snapshot")) {
            if (!cloud) throw new ApiError("missing", { status: 404 });
            data = { protocolVersion: 1, canvasId: created.id, documentEpoch: "e", revision: 1, eventSeq: 0, document: cloud, entityVersions: {} };
        } else if (config.method === "put") {
            writes++;
            cloud = { ...JSON.parse(config.data).project, revision: 1 };
            data = { project: cloud };
        } else if (config.url?.endsWith("/events")) data = [];
        else throw new Error(`Unexpected ${config.method} ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    await ensureOrdinaryCanvasCommandWorkspace(scope);
    await saveRemoteUserDataNow(created.id);
    expect(cloud?.title).toBe("Preserved local");
    expect(writes).toBe(1);
});
