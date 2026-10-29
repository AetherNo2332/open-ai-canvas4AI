import { afterEach, beforeEach, expect, test } from "bun:test";
import localforage from "localforage";
import { renderToStaticMarkup } from "react-dom/server";
import { apiClient } from "../src/services/api/request";
import { setActiveUserScope } from "../src/lib/user-scope";
import { applyAgentCanvasChange, resetRemoteUserDataSync, syncRemoteUserData } from "../src/services/user-data-sync";
import { createCanvasRealtimeClient, type CanvasRealtimeSnapshot } from "../src/services/canvas-realtime-client";
import { createAgentCanvasSync } from "../src/services/agent-canvas-sync";
import { CanvasAgentPresenceMarkers } from "../src/components/canvas/canvas-agent-presence";
import { flushCanvasStorePersistence, useCanvasStore, type CanvasProject } from "../src/stores/canvas/use-canvas-store";
import { flushAssetStorePersistence, useAssetStore } from "../src/stores/use-asset-store";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";
import type { AgentEvent } from "../src/services/api/agent";

const originalWindow = globalThis.window;
const originalAdapter = apiClient.defaults.adapter;
const originalGet = localforage.getItem;
const originalSet = localforage.setItem;
const originalRemove = localforage.removeItem;
const disk = new Map<string, unknown>();
let remote: CanvasProject;
let reads = 0;
let scope: string;
let sequence = 0;

const node = (id: string, title = id): CanvasNodeData => ({ id, type: CanvasNodeType.Text, title, position: { x: 0, y: 0 }, width: 100, height: 80 });
const live = () => useCanvasStore.getState().openProject("canvas")!;
const titles = (project: CanvasProject) => Object.fromEntries(project.nodes.map((item) => [item.id, item.title]));
function agentWrite(id: string, title: string) {
    remote = { ...remote, revision: (remote.revision || 0) + 1, nodes: remote.nodes.map((item) => (item.id === id ? { ...item, title } : item)) };
}

beforeEach(async () => {
    resetRemoteUserDataSync();
    disk.clear();
    reads = 0;
    scope = `agent-realtime-${++sequence}`;
    Object.defineProperty(globalThis, "window", {
        configurable: true,
        value: {
            setTimeout: () => 1,
            clearTimeout: () => {},
            localStorage: { getItem: (key: string) => disk.get(key) ?? null, setItem: (key: string, value: string) => disk.set(key, value) },
            addEventListener: () => {},
            removeEventListener: () => {},
        },
    });
    localforage.getItem = (async (key: string) => disk.get(key) ?? null) as typeof localforage.getItem;
    localforage.setItem = (async (key: string, value: unknown) => {
        disk.set(key, value);
        return value;
    }) as typeof localforage.setItem;
    localforage.removeItem = async (key: string) => {
        disk.delete(key);
    };
    setActiveUserScope(scope);
    remote = {
        id: "canvas",
        revision: 1,
        title: "Canvas",
        createdAt: "2026-10-29",
        updatedAt: "2026-10-29",
        nodes: [node("a", "A"), node("b", "B")],
        connections: [],
        chatSessions: [],
        activeChatId: null,
        backgroundMode: "dots",
        showImageInfo: false,
        viewport: { x: 0, y: 0, k: 1 },
        previsScenes: [],
    };
    useCanvasStore.setState({ projects: [] });
    useAssetStore.setState({ assets: [] });
    apiClient.defaults.adapter = async (config) => {
        const url = String(config.url);
        let data: unknown;
        if (url === "/user-data/snapshot") data = { projects: [structuredClone(remote)], assets: [] };
        else if (url === "/canvas-projects/canvas" && config.method === "get") {
            reads++;
            data = { project: structuredClone(remote) };
        } else throw new Error(`Unexpected request: ${config.method} ${url}`);
        return { config, status: 200, statusText: "", headers: {}, data: { code: 0, data, msg: "ok" } };
    };
    await syncRemoteUserData(scope);
});

afterEach(async () => {
    await flushCanvasStorePersistence();
    await flushAssetStorePersistence();
    resetRemoteUserDataSync();
    apiClient.defaults.adapter = originalAdapter;
    localforage.getItem = originalGet;
    localforage.setItem = originalSet;
    localforage.removeItem = originalRemove;
    if (originalWindow === undefined) delete (globalThis as { window?: unknown }).window;
    else Object.defineProperty(globalThis, "window", { configurable: true, value: originalWindow });
});

test("Agent 改节点 A、本地未保存地改节点 B：增量合并后两者都在", async () => {
    useCanvasStore.setState({ projects: [{ ...live(), nodes: live().nodes.map((item) => (item.id === "b" ? { ...item, title: "本地 B" } : item)) }] });
    agentWrite("a", "Agent A");
    const merged = await applyAgentCanvasChange(live(), { revision: 2 });
    expect(titles(merged)).toEqual({ a: "Agent A", b: "本地 B" });
    expect(titles(live())).toEqual({ a: "Agent A", b: "本地 B" });
    expect(live().revision).toBe(2);
});

test("断线期间两次 Agent 写入：重连后按 revision 一次补齐，已覆盖的旧 revision 不再读取", async () => {
    agentWrite("a", "Agent A1");
    agentWrite("b", "Agent B2");
    const merged = await applyAgentCanvasChange(live(), { revision: 3 });
    expect(titles(merged)).toEqual({ a: "Agent A1", b: "Agent B2" });
    expect(merged.revision).toBe(3);
    const before = reads;
    // A late/out-of-order announcement for an included revision is a no-op.
    await applyAgentCanvasChange(live(), { revision: 2 });
    await applyAgentCanvasChange(live(), { revision: 3 });
    expect(reads).toBe(before);
    await expect(applyAgentCanvasChange(live(), { revision: 0 })).rejects.toThrow();
});

class Socket {
    readyState = 1;
    bufferedAmount = 0;
    onmessage?: (event: { data: string }) => void;
    onclose?: (event: { code: number }) => void;
    send() {}
    close() {
        this.onclose?.({ code: 1006 });
    }
    receive(value: unknown) {
        this.onmessage?.({ data: JSON.stringify(value) });
    }
}

test("realtime 客户端：canvas_changed 携带 revision 与 actor，重连后清空并重新校验", () => {
    const sockets: Socket[] = [];
    const timers: (() => void)[] = [];
    const client = createCanvasRealtimeClient({
        url: "ws://x",
        socket: () => {
            const socket = new Socket();
            sockets.push(socket);
            return socket as unknown as WebSocket;
        },
        schedule: (callback) => {
            timers.push(callback);
            return timers.length as unknown as ReturnType<typeof setTimeout>;
        },
        clear: () => {},
        random: () => 0.5,
    });
    const events: string[] = [];
    let state: CanvasRealtimeSnapshot = { connected: false, selfId: null, peers: [] };
    client.subscribe((snapshot, event) => {
        state = snapshot;
        events.push(event);
    });
    const welcome = () => sockets.at(-1)!.receive({ type: "welcome", selfId: "self", peers: [] });
    welcome();
    sockets[0]!.receive({ type: "canvas_changed", revision: 3, actor: "agent" });
    sockets[0]!.receive({ type: "canvas_changed", revision: 2, actor: "browser" });
    expect(state.change).toEqual({ revision: 3, actor: "agent" });
    sockets[0]!.receive({ type: "agent_presence", agent: { runId: "run", nodeIds: ["a"], phase: "editing" } });
    expect(state.agents?.map((agent) => agent.phase)).toEqual(["editing"]);
    expect(client.snapshot().connected).toBe(true);
    sockets[0]!.close();
    expect(client.snapshot().connected).toBe(false);
    expect(state.agents).toEqual([]);
    // First timer is the watchdog; the reconnect is the latest scheduled one.
    timers.pop()!();
    welcome();
    expect(state.change).toBeNull();
    expect(events.filter((event) => event === "invalidate")).toHaveLength(2);
    sockets[1]!.receive({ type: "agent_presence", agent: { runId: "run", nodeIds: ["a"], phase: "editing" } });
    sockets[1]!.receive({ type: "agent_presence", agent: { runId: "run", nodeIds: [], phase: "idle" } });
    expect(state.agents).toEqual([]);
    client.close();
});

const sleep = (ms = 15) => new Promise((resolve) => setTimeout(resolve, ms));
const event = (type: string, payload: Record<string, unknown> = {}): AgentEvent => ({ eventId: type, runId: "run", seq: 1, type, payload, createdAt: "2026-10-29" });

test("实时在线时 Agent 写入事件不再触发整份刷新；离线时退回整份刷新", async () => {
    let connected = true;
    let refreshes = 0;
    const sync = createAgentCanvasSync({
        canvasId: "canvas",
        applyPatches: async () => {},
        refresh: async () => {
            refreshes++;
        },
        realtimeConnected: () => connected,
        onError: () => {},
        batchMs: 0,
        refreshIntervalMs: 0,
    });
    sync.receive(event("canvas_updated", { canvasId: "canvas" }));
    sync.receive(event("tool_completed", { toolName: "director", arguments: JSON.stringify({ action: "apply_patch" }) }));
    sync.receive(event("canvas_updated", { canvasId: "canvas", canvasPatchSlimmed: true }));
    await sleep();
    expect(refreshes).toBe(0);
    connected = false;
    sync.receive(event("canvas_updated", { canvasId: "canvas" }));
    await sleep();
    expect(refreshes).toBe(1);
    sync.dispose();
});

test("presence：editing 节点渲染 data-agent-editing=true，reading 为 false，idle/未知节点不渲染", () => {
    const nodes = [node("a"), node("b"), node("c")];
    const html = renderToStaticMarkup(
        <CanvasAgentPresenceMarkers
            agents={[
                { runId: "r1", nodeIds: ["a", "missing"], phase: "editing", updatedAt: 0 },
                { runId: "r2", nodeIds: ["a", "b"], phase: "reading", updatedAt: 0 },
            ]}
            nodes={nodes}
        />,
    );
    expect(html).toContain('data-agent-node-id="a" data-agent-editing="true"');
    expect(html).toContain('data-agent-node-id="b" data-agent-editing="false"');
    expect(html).not.toContain('data-agent-node-id="c"');
    expect(html).not.toContain("missing");
    expect(renderToStaticMarkup(<CanvasAgentPresenceMarkers agents={[]} nodes={nodes} />)).toBe("");
});
