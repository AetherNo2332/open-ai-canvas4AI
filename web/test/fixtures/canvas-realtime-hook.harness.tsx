import { expect, test } from "bun:test";
import { Window } from "happy-dom";
const dom = new Window({ url: "https://canvas.test/" });
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, HTMLElement: dom.HTMLElement, Element: dom.Element, Node: dom.Node, Event: dom.Event, CustomEvent: dom.CustomEvent, IS_REACT_ACT_ENVIRONMENT: true })) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
const { act, useRef, StrictMode } = await import("react");
const { createRoot } = await import("react-dom/client");
const { useCanvasRealtime } = await import("../../src/components/canvas/use-canvas-realtime");
const { CanvasRealtimePresenceLayer, CanvasRealtimeStatus } = await import("../../src/components/canvas/canvas-realtime-presence");
const { CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, CANVAS_NODE_DRAG_PREVIEW_EVENT } = await import("../../src/lib/canvas/canvas-live-viewport");
const sockets: any[] = [];
class FakeSocket {
    readyState = 0; onopen: any; onclose: any; onmessage: any; onerror: any; sent: any[] = [];
    constructor(readonly url: string) { sockets.push(this); }
    send(value: string) { this.sent.push(JSON.parse(value)); }
    close() { this.readyState = 3; }
    frame(frame: any) { this.onmessage?.({ data: JSON.stringify({ protocolVersion: 1, ...frame }) }); }
    open() { this.readyState = 1; this.onopen?.(); this.frame({ type: "ready", sessionId: "self", deviceNumber: 1, coordination: "shared" }); }
}
Object.defineProperty(globalThis, "WebSocket", { configurable: true, value: FakeSocket });
test("mount, real viewport/drag events, scope replacement and unmount", async () => {
    let activeScope = "a";
    const reads: string[] = [];
    const nodes = [{ id: "node", position: { x: 10, y: 20 } }];
    const reconcileA = async () => { if (activeScope === "a") reads.push("a"); };
    const reconcileB = async () => { if (activeScope === "b") reads.push("b"); };
    function Harness({ scope }: { scope: string }) {
        const containerRef = useRef<HTMLDivElement>(null);
        const nodesRef = useRef(nodes);
        const viewport = { x: 100, y: -100, k: 2 };
        const state = useCanvasRealtime({ scope, canvasId: "canvas", apiBaseURL: scope === "a" ? "https://a.test/api" : "https://b.test/api", enabled: true, containerRef, viewport, nodesRef, reconcile: scope === "a" ? reconcileA : reconcileB });
        return <><div data-container ref={containerRef} /><CanvasRealtimeStatus state={state} /><CanvasRealtimePresenceLayer containerRef={containerRef} viewport={viewport} peers={state.peers} /></>;
    }
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
    await act(async () => { root.render(<Harness scope="a" />); });
    expect(reads).toEqual(["a"]); expect(sockets).toHaveLength(1); expect(sockets[0].url).toContain("wss://a.test/api/");
    const container = host.querySelector("[data-container]")!;
    Object.defineProperty(container, "getBoundingClientRect", { value: () => ({ left: 10, top: 20 }) });
    await act(async () => {
        sockets[0].open();
        container.dispatchEvent(new dom.PointerEvent("pointermove", { clientX: 510, clientY: 320, bubbles: true }));
        container.dispatchEvent(new dom.CustomEvent(CANVAS_NODE_DRAG_PREVIEW_EVENT, { detail: { x: 5, y: -2, nodeIds: new Set(["node"]) } }));
        await new Promise(r => setTimeout(r, 100));
    });
    expect(sockets[0].sent.at(-1)).toMatchObject({ cursor: { x: 200, y: 200 }, drag: [{ nodeId: "node", x: 15, y: 18 }], protocolVersion: 1 });
    await act(async () => {
        sockets[0].frame({ type: "peers", peers: [{ sessionId: "remote", deviceNumber: 2, coordination: "shared", deviceLabel: "Browser", cursor: { x: 2, y: 3 }, drag: [], expiresAt: new Date(Date.now() + 60_000).toISOString() }] });
        container.dispatchEvent(new dom.CustomEvent(CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, { detail: { x: 10, y: 20, k: 3 } }));
    });
    expect(host.textContent).toContain("2 台在线"); expect(host.innerHTML).toContain("translate3d(16px, 29px, 0)");
    const late = sockets[0].onmessage;
    activeScope = "b";
    await act(async () => { root.render(<Harness scope="b" />); });
    expect(sockets[0].readyState).toBe(3); expect(sockets).toHaveLength(2); expect(host.textContent).not.toContain("2 台在线");
    const count = reads.length;
    await act(async () => { late({ data: JSON.stringify({ type: "invalidate", protocolVersion: 1 }) }); });
    expect(reads).toHaveLength(count);
    await act(async () => { dom.dispatchEvent(new dom.Event("online")); });
    expect(reads.at(-1)).toBe("b");
    expect(nodes).toEqual([{ id: "node", position: { x: 10, y: 20 } }]);
    await act(async () => { root.unmount(); }); expect(sockets[1].readyState).toBe(3);
    const after = reads.length; dom.dispatchEvent(new dom.Event("focus")); await Promise.resolve(); expect(reads.length).toBe(after);
    dom.happyDOM.abort();
});
test("React StrictMode remount owns a fresh live controller", async () => {
    const start = sockets.length;
    const reconcile = async () => {};
    function Harness() {
        const containerRef = useRef<HTMLDivElement>(null);
        const nodesRef = useRef([]);
        const state = useCanvasRealtime({ scope: "strict", canvasId: "canvas", apiBaseURL: "/api", enabled: true, containerRef, viewport: { x: 0, y: 0, k: 1 }, nodesRef, reconcile });
        return <div ref={containerRef}><CanvasRealtimeStatus state={state} /></div>;
    }
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
    await act(async () => { root.render(<StrictMode><Harness /></StrictMode>); });
    expect(sockets.length - start).toBe(2);
    expect(sockets[start].readyState).toBe(3);
    await act(async () => { sockets.at(-1).open(); });
    expect(host.textContent).toContain("1 台在线");
    await act(async () => { root.unmount(); });
});
