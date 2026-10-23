import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { realtimeCursorWorldPoint, realtimeDragPositions } from "../src/lib/canvas/canvas-realtime-presence";
import { CanvasRealtimePresence, CanvasRealtimeStatus } from "../src/components/canvas/canvas-realtime-presence";
import type { RealtimePeer, CanvasRealtimeState } from "../src/services/canvas-realtime-client";

test("cursor projects from container coordinates through pan and zoom; drag uses world delta without mutation", () => {
    expect(realtimeCursorWorldPoint({ x: 510, y: 320 }, { left: 10, top: 20 }, { x: 100, y: -100, k: 2 })).toEqual({ x: 200, y: 200 });
    expect(realtimeCursorWorldPoint({ x: 0, y: 0 }, { left: 0, top: 0 }, { x: 0, y: 0, k: 0 })).toBeNull();
    const nodes = [{ id: "a", position: { x: 10, y: 20 } }, { id: "b", position: { x: 20, y: 30 } }];
    const copy = structuredClone(nodes);
    expect(realtimeDragPositions(nodes, { nodeIds: new Set(["a"]), x: 5, y: -2 })).toEqual([{ nodeId: "a", x: 15, y: 18 }]);
    expect(realtimeDragPositions(nodes, null)).toEqual([]);
    expect(nodes).toEqual(copy);
    expect(realtimeDragPositions(Array.from({ length: 40 }, (_, i) => ({ id: `${i}`, position: { x: i, y: 0 } })), { nodeIds: new Set(Array.from({ length: 40 }, (_, i) => `${i}`)), x: 1, y: 0 })).toHaveLength(32);
});
test("pure presence renderer projects peers and drag markers without rendering document nodes", () => {
    const peers: RealtimePeer[] = [{ sessionId: "remote", deviceNumber: 2, coordination: "shared", deviceLabel: "Chrome", cursor: { x: 100, y: 200 }, drag: [{ nodeId: "n", x: 4, y: 8 }], expiresAt: "2099-01-01T00:00:00Z" }];
    const markup = renderToStaticMarkup(<CanvasRealtimePresence peers={peers} viewport={{ x: 10, y: 20, k: 2 }} />);
    expect(markup).toContain("translate3d(210px, 420px, 0)");
    expect(markup).toContain("translate3d(18px, 36px, 0)");
    expect(markup).toContain("pointer-events-none");
    expect(markup).toContain("设备 2"); expect(markup).not.toContain("data-node-id");
});
test("online status distinguishes instance coordination from degraded state and saved state", () => {
    const base: CanvasRealtimeState = { status: "connected", coordination: "shared", deviceNumber: 1, sessionId: "self", peers: [] };
    const shared = renderToStaticMarkup(<CanvasRealtimeStatus state={base} />);
    expect(shared).toContain("1 台在线"); expect(shared).not.toContain("已保存");
    expect(renderToStaticMarkup(<CanvasRealtimeStatus state={{ ...base, coordination: "local" }} />)).toContain("本实例");
    const degraded = renderToStaticMarkup(<CanvasRealtimeStatus state={{ ...base, coordination: "degraded", deviceNumber: 0 }} />);
    expect(degraded).toContain("协同降级"); expect(degraded).not.toContain("台在线");
});
