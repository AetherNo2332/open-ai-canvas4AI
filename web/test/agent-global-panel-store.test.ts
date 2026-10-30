import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { AGENT_DOCK_DEFAULT_WIDTH, AGENT_DOCK_MIN_VIEWPORT, AGENT_PANEL_DOCK_KEY, LEGACY_AGENT_PANEL_WIDTH_KEY, agentDockWidthBounds, clampAgentDockWidth, migrateAgentPanelDock } from "@/lib/canvas/agent-panel-dock";
import {
    markAgentPanelUnavailable,
    readAgentPanelDockPreference,
    requestAgentPrefill,
    selectAgentOnCanvas,
    settleAgentPrefill,
    stabilizeCanvasContext,
    takeAgentPrefill,
    useGlobalAgentStore,
    type AgentCanvasContext,
} from "@/stores/use-global-agent-store";
import { DIRECTOR_OPEN_EVENT, dispatchDirectorOpen, subscribeDirectorOpen, type DirectorOpenRunEvent } from "@/lib/canvas/agent-director-launch";

const memory = new Map<string, string>();
const storage = {
    getItem: (key: string) => memory.get(key) ?? null,
    setItem: (key: string, value: string) => void memory.set(key, value),
    removeItem: (key: string) => void memory.delete(key),
    clear: () => memory.clear(),
};
const previousStorage = Object.getOwnPropertyDescriptor(globalThis, "localStorage");

const context = (patch: Partial<AgentCanvasContext> = {}): AgentCanvasContext => ({
    canvasId: "canvas-a",
    nodeCount: 2,
    selectedNodeIds: ["n1"],
    references: [{ id: "r1", nodeId: "n1", kind: "image", label: "图", title: "图", active: true, mentionToken: "@[node:n1]" }],
    ...patch,
});

beforeEach(() => {
    memory.clear();
    Object.defineProperty(globalThis, "localStorage", { configurable: true, writable: true, value: storage });
    useGlobalAgentStore.setState({ open: false, canvas: null, canvasRoute: false, canvasHeld: false, panelUnavailable: false, prefillRequest: null });
});
afterEach(() => {
    if (previousStorage) Object.defineProperty(globalThis, "localStorage", previousStorage);
    else delete (globalThis as { localStorage?: unknown }).localStorage;
});

describe("global agent store", () => {
    test("setCanvas keeps the previous reference when the published content is unchanged", () => {
        useGlobalAgentStore.getState().setCanvas(context());
        const first = useGlobalAgentStore.getState().canvas;
        useGlobalAgentStore.getState().setCanvas(context());
        expect(useGlobalAgentStore.getState().canvas).toBe(first);
        useGlobalAgentStore.getState().setCanvas(context({ nodeCount: 3 }));
        const next = useGlobalAgentStore.getState().canvas!;
        expect(next).not.toBe(first);
        // 未变化的数组沿用旧引用，订阅方按引用判断变化。
        expect(next.references).toBe(first!.references);
        expect(next.selectedNodeIds).toBe(first!.selectedNodeIds);
    });

    test("switching canvas replaces the context instead of merging references", () => {
        const a = context();
        const b = context({ canvasId: "canvas-b" });
        expect(stabilizeCanvasContext(a, b)).toBe(b);
        expect(stabilizeCanvasContext(a, null)).toBeNull();
    });

    test("reset on account switch clears open state, canvas and pending prefill but keeps the dock preference", async () => {
        useGlobalAgentStore.setState({ open: true, dock: { docked: true, width: 500 } });
        useGlobalAgentStore.getState().setCanvas(context());
        const pending = requestAgentPrefill({ canvasId: "canvas-a", userScope: "user-a", text: "hi", autoSubmit: true });
        useGlobalAgentStore.getState().reset();
        expect(useGlobalAgentStore.getState()).toMatchObject({ open: false, canvas: null, prefillRequest: null, dock: { docked: true, width: 500 } });
        expect(await pending).toBe(false);
    });

    test("leaving the canvas drops a prefill addressed to it", async () => {
        useGlobalAgentStore.getState().setCanvas(context());
        const pending = requestAgentPrefill({ canvasId: "canvas-a", userScope: "user-a", text: "hi" });
        useGlobalAgentStore.getState().setCanvas(null);
        expect(useGlobalAgentStore.getState().prefillRequest).toBeNull();
        expect(await pending).toBe(false);
    });
});

describe("prefill requests", () => {
    test("a request is taken once by id and settles with the panel outcome", async () => {
        const pending = requestAgentPrefill({ canvasId: "canvas-a", userScope: "user-a", text: "生成预演台", autoSubmit: true, requiresVision: true, focusNodeIds: ["n1"] });
        const request = useGlobalAgentStore.getState().prefillRequest!;
        expect(request).toMatchObject({ canvasId: "canvas-a", autoSubmit: true, requiresVision: true, focusNodeIds: ["n1"] });
        expect(takeAgentPrefill(request.id)).toEqual(request);
        expect(takeAgentPrefill(request.id)).toBeNull();
        settleAgentPrefill(request.id, "sent");
        expect(await pending).toBe("sent");
    });

    test("ids increase across requests and a second request is rejected while one is undelivered", async () => {
        const first = requestAgentPrefill({ canvasId: "canvas-a", userScope: "user-a", text: "one" });
        const firstId = useGlobalAgentStore.getState().prefillRequest!.id;
        expect(await requestAgentPrefill({ canvasId: "canvas-a", userScope: "user-a", text: "two" })).toBe(false);
        takeAgentPrefill(firstId);
        settleAgentPrefill(firstId, "prefilled");
        expect(await first).toBe("prefilled");
        void requestAgentPrefill({ canvasId: "canvas-a", userScope: "user-a", text: "three" });
        expect(useGlobalAgentStore.getState().prefillRequest!.id).toBeGreaterThan(firstId);
    });
});

describe("panel chunk failure", () => {
    test("settles the waiting prefill and refuses later ones immediately until the next account mount", async () => {
        const pending = requestAgentPrefill({ canvasId: "canvas-a", userScope: "user-a", text: "hi", autoSubmit: true });
        markAgentPanelUnavailable();
        expect(await pending).toBe(false);
        expect(useGlobalAgentStore.getState().prefillRequest).toBeNull();
        expect(await requestAgentPrefill({ canvasId: "canvas-a", userScope: "user-a", text: "again" })).toBe(false);
        expect(useGlobalAgentStore.getState().prefillRequest).toBeNull();
        useGlobalAgentStore.getState().reset();
        void requestAgentPrefill({ canvasId: "canvas-a", userScope: "user-b", text: "new account" });
        expect(useGlobalAgentStore.getState().prefillRequest?.text).toBe("new account");
    });
});

describe("dock shell during canvas load and switch", () => {
    test("keeps the canvas decision while the context is briefly empty on a canvas route", () => {
        const store = useGlobalAgentStore.getState();
        store.setCanvasRoute(true);
        // 首次进入画布、尚未发布上下文：还不算在画布里。
        expect(selectAgentOnCanvas(useGlobalAgentStore.getState())).toBe(false);
        store.setCanvas(context());
        expect(selectAgentOnCanvas(useGlobalAgentStore.getState())).toBe(true);
        // 切换画布：旧画布撤回上下文，新画布尚未加载完。
        store.setCanvas(null);
        expect(selectAgentOnCanvas(useGlobalAgentStore.getState())).toBe(true);
        store.setCanvas(context({ canvasId: "canvas-b" }));
        expect(selectAgentOnCanvas(useGlobalAgentStore.getState())).toBe(true);
        // 离开画布路由后立即不再停靠。
        store.setCanvas(null);
        store.setCanvasRoute(false);
        expect(selectAgentOnCanvas(useGlobalAgentStore.getState())).toBe(false);
        store.setCanvasRoute(true);
        expect(selectAgentOnCanvas(useGlobalAgentStore.getState())).toBe(false);
    });
});

describe("dock preference", () => {
    test("migrates the fork width once as the initial dock width, starts floating and retires the legacy key", () => {
        memory.set(LEGACY_AGENT_PANEL_WIDTH_KEY, "560");
        expect(readAgentPanelDockPreference()).toEqual({ docked: false, width: 560 });
        expect(memory.has(LEGACY_AGENT_PANEL_WIDTH_KEY)).toBe(false);
        expect(JSON.parse(memory.get(AGENT_PANEL_DOCK_KEY)!)).toEqual({ docked: false, width: 560 });
    });

    test("an existing upstream preference wins over the legacy width", () => {
        memory.set(AGENT_PANEL_DOCK_KEY, JSON.stringify({ docked: false, width: 400 }));
        memory.set(LEGACY_AGENT_PANEL_WIDTH_KEY, "700");
        expect(readAgentPanelDockPreference()).toEqual({ docked: false, width: 400 });
        expect(memory.get(LEGACY_AGENT_PANEL_WIDTH_KEY)).toBe("700");
    });

    test("without any stored value the panel floats at the upstream default width", () => {
        expect(migrateAgentPanelDock(null, null)).toEqual({ preference: { docked: false, width: AGENT_DOCK_DEFAULT_WIDTH }, migrated: true });
        expect(migrateAgentPanelDock(null, "garbage").preference.width).toBe(AGENT_DOCK_DEFAULT_WIDTH);
    });

    test("width is clamped to 360-720 and never more than half the viewport", () => {
        expect(AGENT_DOCK_MIN_VIEWPORT).toBe(960);
        expect(clampAgentDockWidth(1000, 1920)).toBe(720);
        expect(clampAgentDockWidth(100, 1920)).toBe(360);
        expect(clampAgentDockWidth(700, 1000)).toBe(500);
        expect(clampAgentDockWidth(Number.NaN, 1920)).toBe(AGENT_DOCK_DEFAULT_WIDTH);
        expect(agentDockWidthBounds(1200)).toEqual({ min: 360, max: 600 });
    });
});

describe("director_open routing", () => {
    const fresh = () => new Date().toISOString();
    const event = (patch: Record<string, unknown> = {}) => ({ type: "director_open", runId: "run-1", seq: 5, createdAt: fresh(), payload: { canvasId: "canvas-a", nodeId: "node-1", sceneId: "scene-1", action: "create_scene" }, ...patch });

    test("only the subscriber for the event's canvas receives it", () => {
        const target = new EventTarget();
        const received: Record<string, DirectorOpenRunEvent[]> = { a: [], b: [] };
        const offA = subscribeDirectorOpen("canvas-a", (item) => received.a.push(item), target);
        const offB = subscribeDirectorOpen("canvas-b", (item) => received.b.push(item), target);
        expect(dispatchDirectorOpen(event(), target)).toMatchObject({ canvasId: "canvas-a", seq: 5 });
        expect(received.a).toHaveLength(1);
        expect(received.b).toHaveLength(0);
        offA();
        offB();
        dispatchDirectorOpen(event(), target);
        expect(received.a).toHaveLength(1);
    });

    test("snapshot (seq 0) and stale replays are not dispatched", () => {
        const target = new EventTarget();
        let count = 0;
        target.addEventListener(DIRECTOR_OPEN_EVENT, () => count++);
        expect(dispatchDirectorOpen(event({ seq: 0 }), target)).toBeNull();
        expect(dispatchDirectorOpen(event({ createdAt: new Date(Date.now() - 10 * 60_000).toISOString() }), target)).toBeNull();
        expect(dispatchDirectorOpen(event({ createdAt: undefined }), target)).toBeNull();
        expect(count).toBe(0);
    });
});
