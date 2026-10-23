export type RealtimePoint = { x: number; y: number };
export type RealtimeDrag = RealtimePoint & { nodeId: string };
export type RealtimeCoordination = "shared" | "local" | "degraded" | "recovering";
export type RealtimePresence = { cursor: RealtimePoint | null; drag: RealtimeDrag[] };
export type RealtimePeer = RealtimePresence & { sessionId: string; deviceNumber: number; coordination: RealtimeCoordination; deviceLabel: string; expiresAt: string };
export type CanvasRealtimeState = { status: "connecting" | "connected" | "offline" | "closed"; coordination: RealtimeCoordination; sessionId: string; deviceNumber: number; peers: RealtimePeer[] };
type Socket = { readyState: number; onopen: (() => void) | null; onmessage: ((event: { data: unknown }) => void) | null; onclose: ((event: { code: number }) => void) | null; onerror: (() => void) | null; send(data: string): void; close(): void };
type Dependencies = {
    scope: string; canvasId: string; apiBaseURL: string; pageURL: string;
    reconcile(): Promise<void>; deviceLabel: string;
    socket?: (url: string) => Socket;
    now?: () => number;
    schedule?: (fn: () => void, ms: number) => () => void;
};
const modes = new Set(["shared", "local", "degraded", "recovering"]);
const coord = (v: unknown): v is RealtimeCoordination => typeof v === "string" && modes.has(v);
const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v);
const point = (v: unknown): v is RealtimePoint => record(v) && typeof v.x === "number" && typeof v.y === "number" && Number.isFinite(v.x) && Number.isFinite(v.y) && Math.abs(v.x) <= 10_000_000 && Math.abs(v.y) <= 10_000_000;
const byteLength = (s: string) => new TextEncoder().encode(s).length;
function drag(v: unknown): v is RealtimeDrag[] {
    if (!Array.isArray(v) || v.length > 32) return false;
    const ids = new Set<string>();
    return v.every(item => {
        if (!point(item) || !record(item) || typeof item.nodeId !== "string" || !item.nodeId || byteLength(item.nodeId) > 128 || ids.has(item.nodeId)) return false;
        ids.add(item.nodeId); return true;
    });
}
function peer(v: unknown, now: number): v is RealtimePeer {
    return record(v) && typeof v.sessionId === "string" && v.sessionId.length > 0 && v.sessionId.length <= 128 && coord(v.coordination)
        && Number.isInteger(v.deviceNumber) && typeof v.deviceNumber === "number" && v.deviceNumber >= (v.coordination === "shared" || v.coordination === "local" ? 1 : 0) && v.deviceNumber <= 64
        && typeof v.deviceLabel === "string" && [...v.deviceLabel].length <= 64 && (v.cursor === null || point(v.cursor)) && drag(v.drag)
        && typeof v.expiresAt === "string" && Number.isFinite(Date.parse(v.expiresAt)) && Date.parse(v.expiresAt) > now;
}
export function canvasRealtimeURL(base: string, canvasId: string, pageURL: string) {
    const url = new URL(base, pageURL);
    if (!["https:", "http:"].includes(url.protocol) || url.username || url.password || url.search || url.hash || !canvasId) throw new Error("canvas_realtime_url_invalid");
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    url.pathname = `${url.pathname.replace(/\/$/, "")}/canvas-projects/${encodeURIComponent(canvasId)}/realtime`;
    return url.toString();
}

/** A wake channel only: reconciliation stays in the existing command session. */
export function createCanvasRealtimeClient(deps: Dependencies) {
    const url = canvasRealtimeURL(deps.apiBaseURL, deps.canvasId, deps.pageURL);
    const now = deps.now || Date.now;
    const schedule = deps.schedule || ((fn, ms) => { const id = setTimeout(fn, ms); return () => clearTimeout(id); });
    const listeners = new Set<() => void>();
    let state: CanvasRealtimeState = { status: "offline", coordination: "recovering", sessionId: "", deviceNumber: 0, peers: [] };
    let socket: Socket | WebSocket | null = null, stopped = false, started = false, attempt = 0, lastFrame = 0;
    let retryCancel: (() => void) | undefined, tickCancel: (() => void) | undefined, presenceCancel: (() => void) | undefined;
    let lastPresence = -Infinity, lastHeartbeat = 0;
    let local: RealtimePresence = { cursor: null, drag: [] };
    let reconciling = false, dirty = false;
    const label = [...deps.deviceLabel].slice(0, 64).join("");
    const publish = (patch: Partial<CanvasRealtimeState>) => { state = { ...state, ...patch }; for (const listener of listeners) listener(); };
    function reconcile() {
        if (stopped) return;
        dirty = true;
        if (reconciling) return;
        reconciling = true;
        void Promise.resolve().then(async () => {
            while (dirty && !stopped) {
                dirty = false;
                try { await deps.reconcile(); } catch { /* Existing polling keeps recovery alive. */ }
            }
        }).finally(() => { reconciling = false; });
    }
    function send(frame: object) {
        if (socket?.readyState !== 1 || !state.sessionId || stopped) return false;
        try { socket.send(JSON.stringify({ protocolVersion: 1, ...frame })); return true; } catch { disconnect(); return false; }
    }
    function sendPresence() {
        presenceCancel?.(); presenceCancel = undefined;
        if (send({ type: "presence", ...local, deviceLabel: label })) lastPresence = now();
    }
    function detach() {
        if (!socket) return;
        const old = socket; socket = null;
        old.onopen = old.onmessage = old.onclose = old.onerror = null;
        old.close();
    }
    function retry() {
        if (stopped || retryCancel) return;
        const delay = Math.min(30_000, 1000 * 2 ** Math.min(attempt++, 5));
        retryCancel = schedule(() => { retryCancel = undefined; connect(); }, delay);
    }
    function disconnect(code = 1006) {
        detach(); presenceCancel?.(); presenceCancel = undefined;
        publish({ status: "offline", peers: [], sessionId: "", deviceNumber: 0, coordination: "recovering" });
        if (code !== 1008) retry();
    }
    function connect() {
        if (stopped || socket) return;
        publish({ status: "connecting" }); lastFrame = now();
        let current: Socket | WebSocket;
        try { current = (deps.socket || (url => new WebSocket(url)))(url); } catch { disconnect(); return; }
        socket = current;
        const active = () => !stopped && socket === current;
        current.onopen = () => { if (active()) reconcile(); };
        current.onclose = (event: { code: number }) => { if (active()) disconnect(event.code); };
        current.onerror = () => { if (active()) disconnect(); };
        current.onmessage = (event: { data: unknown }) => {
            if (!active() || typeof event.data !== "string" || byteLength(event.data) > 512 * 1024) return;
            let frame: unknown;
            try { frame = JSON.parse(event.data); } catch { return; }
            if (!record(frame) || frame.protocolVersion !== 1) return;
            lastFrame = now();
            if (frame.type === "ready") {
                if (typeof frame.sessionId !== "string" || !frame.sessionId || frame.sessionId.length > 128 || !coord(frame.coordination) || !Number.isInteger(frame.deviceNumber) || typeof frame.deviceNumber !== "number" || frame.deviceNumber < (["shared", "local"].includes(frame.coordination) ? 1 : 0) || frame.deviceNumber > 64) return;
                attempt = 0; lastHeartbeat = now();
                publish({ status: "connected", sessionId: frame.sessionId, coordination: frame.coordination, deviceNumber: ["degraded", "recovering"].includes(frame.coordination) ? 0 : frame.deviceNumber, peers: [] });
                sendPresence();
            } else if (frame.type === "sync" || frame.type === "invalidate") reconcile();
            else if (frame.type === "coordination" && coord(frame.coordination)) {
                publish({ coordination: frame.coordination, peers: [], deviceNumber: 0 });
            } else if (frame.type === "peers" && state.sessionId && ["shared", "local"].includes(state.coordination) && Array.isArray(frame.peers) && frame.peers.length <= 64) {
                const peers = new Map<string, RealtimePeer>();
                let deviceNumber = state.deviceNumber;
                for (const item of frame.peers) {
                    if (!peer(item, now()) || item.coordination !== state.coordination) continue;
                    if (item.sessionId === state.sessionId) deviceNumber = item.deviceNumber;
                    else peers.set(item.sessionId, { ...item, expiresAt: new Date(Math.min(Date.parse(item.expiresAt), now() + 60_000)).toISOString() });
                }
                publish({ peers: [...peers.values()], deviceNumber });
            }
        };
    }
    function tick() {
        if (stopped) return;
        const peers = state.peers.filter(p => Date.parse(p.expiresAt) > now());
        if (peers.length !== state.peers.length) publish({ peers });
        if (socket && now() - lastFrame >= (state.sessionId ? 60_000 : 20_000)) disconnect();
        if (state.sessionId && now() - lastHeartbeat >= 15_000) { lastHeartbeat = now(); send({ type: "heartbeat" }); }
        tickCancel = schedule(tick, 1000);
    }
    return {
        state: () => state,
        subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
        start() { if (started || stopped) return; started = true; reconcile(); connect(); tickCancel = schedule(tick, 1000); },
        wake() { if (stopped) return; reconcile(); if (!socket) { retryCancel?.(); retryCancel = undefined; connect(); } },
        presence(value: RealtimePresence) {
            if (stopped || (value.cursor !== null && !point(value.cursor)) || !drag(value.drag)) return;
            local = { cursor: value.cursor ? { ...value.cursor } : null, drag: value.drag.map(p => ({ ...p })) };
            if (!presenceCancel) presenceCancel = schedule(sendPresence, Math.max(0, 80 - (now() - lastPresence)));
        },
        close() {
            if (stopped) return;
            stopped = true; dirty = false; retryCancel?.(); tickCancel?.(); presenceCancel?.(); detach();
            publish({ status: "closed", peers: [], sessionId: "", deviceNumber: 0 }); listeners.clear();
        },
    };
}
export type CanvasRealtimeClient = ReturnType<typeof createCanvasRealtimeClient>;
