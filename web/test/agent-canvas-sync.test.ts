import { expect, mock, test } from "bun:test";
import { createAgentCanvasSync } from "@/services/agent-canvas-sync";
import type { AgentEvent } from "@/services/api/agent";

const sleep = (ms = 15) => new Promise((resolve) => setTimeout(resolve, ms));
const event = (type: string, payload: Record<string, unknown> = {}): AgentEvent => ({ eventId: type, runId: "run", seq: 1, type, payload, createdAt: "2026-09-13" });
const delta = event("canvas_updated", { canvasId: "canvas", canvasPatch: { canvasId: "canvas", nodes: [], connections: [], updatedAt: "2026-09-13" } });

test("50 concurrent node completions are one local batch with zero full-canvas GETs", async () => {
    const applyPatches = mock(async () => {}), refresh = mock(async () => {}), onError = mock(() => {});
    const sync = createAgentCanvasSync({ canvasId: "canvas", applyPatches, refresh, onError, batchMs: 0 });
    for (let i = 0; i < 50; i++) { sync.receive(delta); sync.receive(event("tool_failed", { toolName: "generate_media" })); sync.receive(event("run_failed")); }
    await sleep(); sync.dispose();
    expect(applyPatches).toHaveBeenCalledTimes(1);
    expect((applyPatches.mock.calls[0] as unknown as [unknown[]])[0]).toHaveLength(50);
    expect(refresh).not.toHaveBeenCalled();
    expect(onError).not.toHaveBeenCalled();
});

test("legacy success/failure bursts coalesce into one snapshot", async () => {
    const refresh = mock(async () => {});
    const sync = createAgentCanvasSync({ canvasId: "canvas", applyPatches: async () => {}, refresh, onError: () => {}, batchMs: 0 });
    for (let i = 0; i < 50; i++) { sync.receive(event("tool_failed")); sync.receive(event("run_failed")); sync.receive(event("tool_completed", { toolName: "generate_media" })); }
    await sleep(); sync.dispose();
    expect(refresh).toHaveBeenCalledTimes(1);
});

test("in-flight refreshes never overlap; later invalidations produce one trailing refresh", async () => {
    let release!: () => void;
    let active = 0, peak = 0;
    const refresh = mock(async () => { peak = Math.max(peak, ++active); await new Promise<void>((resolve) => { release = resolve; }); active--; });
    const sync = createAgentCanvasSync({ canvasId: "canvas", applyPatches: async () => {}, refresh, onError: () => {}, batchMs: 0, refreshIntervalMs: 0 });
    sync.reconcile(); await sleep();
    for (let i = 0; i < 50; i++) sync.reconcile();
    expect(refresh).toHaveBeenCalledTimes(1);
    release(); await sleep();
    expect(refresh).toHaveBeenCalledTimes(2);
    release(); await sleep(); sync.dispose();
    expect(peak).toBe(1);
});

test("conflicting/out-of-order patches reconcile once and surface unresolved local conflicts", async () => {
    const error = new Error("本地存在未同步编辑");
    const refresh = mock(async () => { throw error; }), onError = mock(() => {});
    const sync = createAgentCanvasSync({ canvasId: "canvas", applyPatches: async () => { throw new Error("delta gap"); }, refresh, onError, batchMs: 0 });
    sync.receive(delta); await sleep(); sync.dispose();
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(onError).toHaveBeenCalledWith(error);
});

test("cleanup and other-canvas events cannot update the new canvas", async () => {
    const applyPatches = mock(async () => {}), refresh = mock(async () => {});
    const sync = createAgentCanvasSync({ canvasId: "canvas", applyPatches, refresh, onError: () => {}, batchMs: 0 });
    sync.receive(event("canvas_updated", { ...delta.payload, canvasId: "other" }));
    sync.receive(delta); sync.dispose();
    await sleep();
    expect(applyPatches).not.toHaveBeenCalled(); expect(refresh).not.toHaveBeenCalled();
});


// 长 deadline 轮询替代固定 sleep:高负载(全量套件/SMB 文件系统)下固定
// 120ms 等待会在 timer 抖动时误报;合同语义是「至少两次且任意两次间隔 ≥ 间隔」。
const waitFor = async (condition: () => boolean, deadlineMs: number, stepMs = 5) => {
    const start = Date.now();
    while (Date.now() - start < deadlineMs) {
        if (condition()) return true;
        await new Promise((resolve) => setTimeout(resolve, stepMs));
    }
    return condition();
};

test("fallback snapshots obey the configured minimum refresh interval", async () => {
    const times: number[] = [];
    const sync = createAgentCanvasSync({ canvasId: "canvas", applyPatches: async () => {}, refresh: async () => { times.push(Date.now()); }, onError: () => {}, batchMs: 0, refreshIntervalMs: 100 });
    try {
        sync.reconcile();
        expect(await waitFor(() => times.length >= 1, 5000)).toBe(true);
        for (let index = 0; index < 50; index++) sync.reconcile();
        expect(await waitFor(() => times.length >= 2, 5000)).toBe(true);
    } finally {
        sync.dispose();
    }
    // 断言真实刷新间隔；测试线程晚唤醒不代表刷新提前发生。
    expect(times).toHaveLength(2);
    for (let index = 1; index < times.length; index++) {
        expect(times[index] - times[index - 1]).toBeGreaterThanOrEqual(100);
    }
});

test("a full invalidation and undo still refresh after delta support was detected", async () => {
    const refresh = mock(async () => {});
    const sync = createAgentCanvasSync({ canvasId: "canvas", applyPatches: async () => {}, refresh, onError: () => {}, batchMs: 0, refreshIntervalMs: 0 });
    sync.receive(delta); await sleep();
    sync.receive(event("canvas_updated", { canvasId: "canvas", requiresRefresh: true })); await sleep();
    expect(refresh).toHaveBeenCalledTimes(1);
    sync.receive(event("canvas_undone", { canvasId: "canvas" })); await sleep();
    expect(refresh).toHaveBeenCalledTimes(2);
    sync.dispose();
});

test("director writes and director_open refresh the canvas; director reads do not", async () => {
    const refresh = mock(async () => {});
    const sync = createAgentCanvasSync({ canvasId: "canvas", applyPatches: async () => {}, refresh, onError: () => {}, batchMs: 0, refreshIntervalMs: 0 });
    sync.receive(event("tool_completed", { toolName: "director", arguments: JSON.stringify({ action: "read" }) }));
    await sleep();
    expect(refresh).not.toHaveBeenCalled();
    sync.receive(event("tool_completed", { toolName: "director", arguments: JSON.stringify({ action: "create_scene" }) }));
    await sleep();
    expect(refresh).toHaveBeenCalledTimes(1);
    sync.receive(event("director_open", { canvasId: "canvas", nodeId: "n", sceneId: "s", action: "apply_patch" }));
    await sleep();
    sync.dispose();
    expect(refresh).toHaveBeenCalledTimes(2);
});
