import { expect, test } from "bun:test";
import { createCanvasRealtimeClient, canvasRealtimeURL } from "../src/services/canvas-realtime-client";

class Socket {
    readyState = 0;
    onopen: (() => void) | null = null;
    onmessage: ((event: { data: unknown }) => void) | null = null;
    onclose: ((event: { code: number }) => void) | null = null;
    onerror: (() => void) | null = null;
    sent: any[] = [];
    open() { this.readyState = 1; this.onopen?.(); }
    frame(frame: object) { this.onmessage?.({ data: JSON.stringify({ protocolVersion: 1, ...frame }) }); }
    send(frame: string) { this.sent.push(JSON.parse(frame)); }
    close() { this.readyState = 3; }
    drop(code = 1006) { this.close(); this.onclose?.({ code }); }
}
function harness(reconcile: () => Promise<void> = async () => {}) {
    let now = 1_000;
    const timers = new Map<number, { at: number; fn: () => void }>();
    let next = 0;
    const sockets: Socket[] = [];
    const client = createCanvasRealtimeClient({ scope: "account-a", canvasId: "canvas-a", apiBaseURL: "/api", pageURL: "https://canvas.test/project", reconcile, deviceLabel: "Browser",
        socket: () => { const socket = new Socket(); sockets.push(socket); return socket; },
        now: () => now,
        schedule: (fn, ms) => { const id = ++next; timers.set(id, { at: now + ms, fn }); return () => { timers.delete(id); }; },
    });
    async function tick(ms: number) {
        const end = now + ms;
        for (;;) {
            const due = [...timers].sort((a, b) => a[1].at - b[1].at).find(([, t]) => t.at <= end);
            if (!due) break;
            now = due[1].at; timers.delete(due[0]); due[1].fn(); await flush();
        }
        now = end; await flush();
    }
    return { client, sockets, tick, timers, now: () => now };
}
async function flush() { for (let i = 0; i < 8; i++) await Promise.resolve(); }
const ready = { type: "ready", sessionId: "self", deviceNumber: 1, coordination: "shared", heartbeatSeconds: 15, leaseSeconds: 60 };
const peer = (sessionId = "remote", expiresAt = new Date(61_000).toISOString()) => ({ sessionId, deviceNumber: 2, coordination: "shared", deviceLabel: "Chrome", cursor: { x: 100, y: 200 }, drag: [{ nodeId: "node", x: 5, y: 6 }], expiresAt });

test("cookie-only websocket URL uses the captured API origin and rejects URL credentials", () => {
    expect(canvasRealtimeURL("/api", "a/b", "https://canvas.test/canvas")).toBe("wss://canvas.test/api/canvas-projects/a%2Fb/realtime");
    expect(canvasRealtimeURL("https://api.test/root/api/", "c", "https://canvas.test")).toBe("wss://api.test/root/api/canvas-projects/c/realtime");
    for (const base of ["https://token@api.test/api", "/api?token=secret", "/api#proof", "file:///api"]) expect(() => canvasRealtimeURL(base, "c", "https://canvas.test")).toThrow();
});
test("initial/open/sync/invalidate reconcile is coalesced and never acknowledges a document", async () => {
    let calls = 0;
    let release!: () => void;
    const h = harness(() => { calls++; return new Promise<void>((resolve) => { release = resolve; }); });
    h.client.start(); await flush(); expect(calls).toBe(1);
    const socket = h.sockets[0]; socket.open(); socket.frame(ready); socket.frame({ type: "sync" });
    for (let i = 0; i < 20; i++) socket.frame({ type: "invalidate", revision: i });
    await flush(); expect(calls).toBe(1);
    release(); await flush(); expect(calls).toBe(2);
    h.client.close(); release(); await flush(); expect(calls).toBe(2);
});
test("presence is full state, throttled, versioned and never writes document nodes", async () => {
    const h = harness(); h.client.start(); h.sockets[0].open(); h.sockets[0].frame(ready);
    h.client.presence({ cursor: { x: 1, y: 2 }, drag: [{ nodeId: "n", x: 3, y: 4 }] });
    h.client.presence({ cursor: { x: 5, y: 6 }, drag: [] });
    await h.tick(80);
    const last = h.sockets[0].sent.at(-1);
    expect(last).toEqual({ type: "presence", protocolVersion: 1, cursor: { x: 5, y: 6 }, drag: [], deviceLabel: "Browser" });
    h.client.presence({ cursor: null, drag: [] }); await h.tick(80);
    expect(h.sockets[0].sent.at(-1).cursor).toBeNull();
    await h.tick(15_000);
    expect(h.sockets[0].sent).toContainEqual({ type: "heartbeat", protocolVersion: 1 });
    h.client.close();
});
test("self-filter, validation, expiry and coordination recovery prevent ghost peers", async () => {
    const h = harness(); h.client.start(); const socket = h.sockets[0]; socket.open(); socket.frame(ready);
    socket.frame({ type: "peers", peers: [peer("self"), peer(), peer("bad", "invalid")] });
    expect(h.client.state().peers.map(p => p.sessionId)).toEqual(["remote"]);
    socket.frame({ type: "coordination", coordination: "degraded" });
    expect(h.client.state().peers).toEqual([]); expect(h.client.state().deviceNumber).toBe(0);
    socket.frame({ type: "peers", peers: [peer()] }); expect(h.client.state().peers).toEqual([]);
    socket.frame({ type: "coordination", coordination: "recovering" });
    socket.frame({ type: "coordination", coordination: "shared" });
    socket.frame({ type: "peers", peers: [peer("self"), peer("remote", new Date(3_000).toISOString()), { ...peer("nan"), cursor: { x: null, y: 1 } }] });
    expect(h.client.state().deviceNumber).toBe(2); expect(h.client.state().peers).toHaveLength(1);
    await h.tick(2_000); expect(h.client.state().peers).toEqual([]); h.client.close();
});
test("reconnect backs off, replays presence and reconciles; policy close waits for explicit wake", async () => {
    let calls = 0;
    const h = harness(async () => { calls++; }); h.client.start(); await flush();
    h.sockets[0].open(); h.sockets[0].frame(ready); h.sockets[0].drop();
    expect(h.client.state().peers).toEqual([]); await h.tick(999); expect(h.sockets).toHaveLength(1);
    await h.tick(1); expect(h.sockets).toHaveLength(2); h.sockets[1].open(); await flush(); expect(calls).toBeGreaterThanOrEqual(2);
    h.sockets[1].frame(ready); h.sockets[1].drop(1008); await h.tick(60_000); expect(h.sockets).toHaveLength(2);
    h.client.wake(); expect(h.sockets).toHaveLength(3);
    const late = h.sockets[2].onmessage;
    h.client.close(); late?.({ data: JSON.stringify({ protocolVersion: 1, ...ready }) });
    expect(h.client.state().status).toBe("closed"); expect(h.timers.size).toBe(0);
});
test("bounded server peer snapshot supports the full 64 by 32 presence room", () => {
    const h = harness(); h.client.start(); h.sockets[0].open(); h.sockets[0].frame(ready);
    const peers = Array.from({ length: 64 }, (_, i) => ({ ...peer(i === 0 ? "self" : `peer-${i}`), deviceNumber: i + 1,
        drag: Array.from({ length: 32 }, (_, j) => ({ nodeId: `${j}`.padEnd(128, "n"), x: 1, y: 2 })),
    }));
    expect(JSON.stringify(peers).length).toBeGreaterThan(256_000);
    h.sockets[0].frame({ type: "peers", peers });
    expect(h.client.state().peers).toHaveLength(63);
    h.sockets[0].frame({ type: "peers", peers: [], padding: "x".repeat(512 * 1024) });
    expect(h.client.state().peers).toHaveLength(63);
    h.client.close();
});
