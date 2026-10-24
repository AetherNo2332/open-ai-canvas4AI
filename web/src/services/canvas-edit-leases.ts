import { nanoid } from "nanoid";
import { getActiveUserScope } from "@/lib/user-scope";
import { createCanvasEditLeaseAPI, type CanvasEditLeaseAPI, type CanvasEditProof, type CanvasSyncActivity } from "./api/canvas-edit-leases";
import type { Command } from "@/lib/canvas/command-contract.generated";

export type CanvasEditPhase = "idle" | "acquiring" | "editing" | "saving" | "saved" | "denied" | "offline" | "expired";
export type CanvasNodeEditStatus = { phase: CanvasEditPhase; message: string; expiresAt?: string };
export type CanvasSyncActivityView = Omit<CanvasSyncActivity, "leases"> & { leases: Array<CanvasSyncActivity["leases"][number] & { isCurrentWindow: boolean }> };
export type CanvasEditState = { nodes: Record<string, CanvasNodeEditStatus>; activity?: CanvasSyncActivityView; activityError?: string };
const idle: CanvasNodeEditStatus = { phase: "idle", message: "点击编辑后获取占用" };
type Owned = { proof: CanvasEditProof; expiresAt: number; holders: number; awaitingAck: boolean; owned: boolean; version: number; request?: Promise<void>; save?: Promise<void>; cancelTimer?: () => void };

export function createCanvasEditLeaseController(deps: { scope: string; canvasId: string; api: CanvasEditLeaseAPI; current: () => boolean; now?: () => number; schedule?: (fn: () => void, ms: number) => () => void }) {
    const now = deps.now || Date.now;
    const schedule =
        deps.schedule ||
        ((fn, ms) => {
            const timer = setTimeout(fn, ms);
            return () => clearTimeout(timer);
        });
    const sessionId = nanoid(48),
        entries = new Map<string, Owned>(),
        listeners = new Set<() => void>();
    const abort = new AbortController();
    let state: CanvasEditState = { nodes: {} },
        closed = false,
        statusWatchers = 0,
        cancelStatus: (() => void) | undefined,
        statusFlight: Promise<void> | undefined;
    const check = () => {
        if (closed || !deps.current()) throw new DOMException("编辑账号已切换，草稿已保留", "AbortError");
    };
    const publish = (nodeId: string, value: CanvasNodeEditStatus) => {
        state = { ...state, nodes: { ...state.nodes, [nodeId]: value } };
        for (const listener of listeners) listener();
    };
    const update = (value: Partial<CanvasEditState>) => {
        state = { ...state, ...value };
        for (const listener of listeners) listener();
    };
    const failure = (nodeId: string, error: unknown) => {
        const reason = error && typeof error === "object" && "reason" in error ? error.reason : undefined;
        const phase = reason === "node_locked" ? "denied" : reason === "edit_proof_invalid" ? "expired" : "offline";
        publish(nodeId, { phase, message: phase === "denied" ? "其他窗口正在编辑，当前草稿已保留" : phase === "expired" ? "编辑占用已失效，请重新获取后保存" : "连接中断，草稿已保留；恢复连接后可重试" });
    };
    function record(nodeId: string) {
        let entry = entries.get(nodeId);
        if (!entry) {
            entry = { proof: { canvasId: deps.canvasId, nodeId, sessionId, token: nanoid(48) }, expiresAt: 0, holders: 0, awaitingAck: false, owned: false, version: 0 };
            entries.set(nodeId, entry);
        }
        return entry;
    }
    function scheduleRenew(nodeId: string, entry: Owned) {
        entry.cancelTimer?.();
        if (!entry.holders || !entry.owned || closed) return;
        entry.cancelTimer = schedule(
            () => {
                entry.cancelTimer = undefined;
                if (!entry.holders || closed) return;
                void ensure(nodeId, true)
                    .catch(() => {})
                    .finally(() => {
                        if (!closed && entry.holders) scheduleRenew(nodeId, entry);
                    });
            },
            Math.max(500, Math.min(10000, entry.expiresAt - now() - 10000)),
        );
    }
    async function ensure(nodeId: string, forceRenew = false) {
        check();
        const entry = record(nodeId);
        if (entry.request) return entry.request;
        if (entry.owned && entry.expiresAt > now() + 5000 && !forceRenew) {
            if (!entry.save && state.nodes[nodeId]?.phase !== "editing") publish(nodeId, { phase: "editing", message: "当前窗口正在编辑", expiresAt: new Date(entry.expiresAt).toISOString() });
            return;
        }
        const operation = (async () => {
            const version = entry.version;
            const renew = entry.owned && entry.expiresAt > now();
            if (!renew && entry.expiresAt > 0) entry.proof = { ...entry.proof, token: nanoid(48) };
            publish(nodeId, { phase: "acquiring", message: "正在获取编辑占用…" });
            try {
                const lease = await (renew ? deps.api.renew(entry.proof, abort.signal) : deps.api.acquire(entry.proof, abort.signal));
                check();
                if (version !== entry.version) return;
                entry.owned = true;
                entry.expiresAt = Date.parse(lease.expiresAt);
                if (entry.expiresAt <= now()) throw new Error("编辑占用已经到期");
                publish(nodeId, { phase: entry.save ? "saving" : "editing", message: entry.save ? "正在确认云端保存…" : "当前窗口正在编辑", expiresAt: lease.expiresAt });
                scheduleRenew(nodeId, entry);
            } catch (error) {
                if (version !== entry.version) return;
                if (closed || !deps.current()) throw error;
                if (error && typeof error === "object" && "reason" in error && (error.reason === "edit_proof_invalid" || error.reason === "node_locked")) {
                    entry.owned = false;
                    entry.expiresAt = Math.max(1, now());
                }
                failure(nodeId, error);
                throw error;
            }
        })();
        entry.request = operation;
        try {
            await operation;
        } finally {
            if (entry.request === operation) entry.request = undefined;
        }
    }
    async function release(nodeId: string) {
        const entry = entries.get(nodeId);
        if (!entry || entry.awaitingAck) return;
        entry.version++;
        entry.cancelTimer?.();
        entry.cancelTimer = undefined;
        if (entry.owned && !closed && deps.current()) {
            try {
                await deps.api.release(entry.proof, abort.signal);
            } catch {
                /* Expiry is the fallback; no durable data depends on release ACK. */
            }
        }
        if (closed || !deps.current()) return;
        entry.owned = false;
        entry.expiresAt = 0;
        publish(nodeId, { phase: "idle", message: "编辑已结束" });
    }
    async function refreshStatus() {
        if (statusFlight) return statusFlight;
        const operation = (async () => {
            check();
            try {
                const activity = await deps.api.status(deps.canvasId, abort.signal);
                check();
                const view: CanvasSyncActivityView = { ...activity, leases: activity.leases.map((lease) => ({ ...lease, isCurrentWindow: lease.sessionId === sessionId })) };
                // The 5s poll usually returns the same activity. Every node reading
                // lease state subscribes to this snapshot, so only publish changes.
                if (state.activityError === undefined && state.activity && JSON.stringify(state.activity) === JSON.stringify(view)) return;
                update({ activity: view, activityError: undefined });
            } catch (error) {
                if (closed || !deps.current()) return;
                const activityError = error instanceof Error ? error.message : "占用状态暂不可用";
                if (state.activityError !== activityError) update({ activityError });
            }
        })();
        statusFlight = operation;
        try {
            await operation;
        } finally {
            if (statusFlight === operation) statusFlight = undefined;
        }
    }
    function poll() {
        if (closed || !statusWatchers) return;
        void refreshStatus()
            .catch(() => {})
            .finally(() => {
                if (!closed && statusWatchers) cancelStatus = schedule(poll, 5000);
            });
    }
    return {
        isCurrent: () => !closed && deps.current(),
        getState: () => state,
        node: (nodeId: string) => state.nodes[nodeId] || idle,
        subscribe(listener: () => void) {
            listeners.add(listener);
            return () => {
                listeners.delete(listener);
            };
        },
        retain(nodeId: string) {
            const entry = record(nodeId);
            entry.holders++;
            if (entry.owned) scheduleRenew(nodeId, entry);
            return () => {
                entry.holders = Math.max(0, entry.holders - 1);
                if (!entry.holders) {
                    entry.cancelTimer?.();
                    entry.cancelTimer = undefined;
                    void release(nodeId);
                }
            };
        },
        async begin(nodeId: string) {
            const saving = entries.get(nodeId)?.save;
            if (saving) await saving;
            await ensure(nodeId);
        },
        markPending(nodeId: string) {
            record(nodeId).awaitingAck = true;
        },
        async save(nodeId: string, persist: () => Promise<void>) {
            const entry = record(nodeId);
            if (entry.save) throw new Error("当前节点正在确认另一笔保存，请等待回执后重试");
            const operation = (async () => {
                await ensure(nodeId);
                check();
                entry.awaitingAck = true;
                publish(nodeId, { phase: "saving", message: "正在确认云端保存…" });
                try {
                    await persist();
                    check();
                    entry.awaitingAck = false;
                    await release(nodeId);
                    check();
                    publish(nodeId, { phase: "saved", message: "已确认保存" });
                } catch (error) {
                    if (!closed && deps.current()) failure(nodeId, error);
                    throw error;
                }
            })();
            entry.save = operation;
            try {
                await operation;
            } finally {
                if (entry.save === operation) entry.save = undefined;
            }
        },
        release,
        async proofHeaders(command: Command, includeAll = false) {
            check();
            const all = includeAll || command.operations.some((op) => ["delete_canvas", "restore_canvas", "undo", "redo"].includes(String(op.type)));
            const ids = new Set(command.operations.map((op) => String(op.id || "")));
            for (const [nodeId, entry] of entries) if ((all || ids.has(nodeId)) && (entry.owned || entry.awaitingAck || (entry.holders && state.nodes[nodeId]?.phase === "editing"))) await ensure(nodeId);
            check();
            const proofs = [...entries.values()].filter((entry) => entry.owned && entry.expiresAt > now()).map((entry) => entry.proof);
            return proofs.length ? { "X-Canvas-Edit-Proofs": JSON.stringify(proofs.slice(0, 128)) } : {};
        },
        watchStatus() {
            statusWatchers++;
            if (statusWatchers === 1) poll();
            return () => {
                statusWatchers = Math.max(0, statusWatchers - 1);
                if (!statusWatchers) {
                    cancelStatus?.();
                    cancelStatus = undefined;
                }
            };
        },
        refreshStatus,
        close() {
            closed = true;
            abort.abort();
            cancelStatus?.();
            for (const entry of entries.values()) entry.cancelTimer?.();
            listeners.clear();
        },
    };
}
export type CanvasEditLeaseController = ReturnType<typeof createCanvasEditLeaseController>;
type Realm = { valid: boolean; controllers: Map<string, CanvasEditLeaseController> };
const realms = new Map<string, Realm>();
function realmFor(scope: string) {
    let realm = realms.get(scope);
    if (!realm) {
        realm = { valid: true, controllers: new Map() };
        realms.set(scope, realm);
    }
    return realm;
}
export function canvasEditLeaseController(scope: string, canvasId: string) {
    const realm = realmFor(scope);
    let controller = realm.controllers.get(canvasId);
    if (!controller) {
        controller = createCanvasEditLeaseController({ scope, canvasId, api: createCanvasEditLeaseAPI(), current: () => realm.valid && getActiveUserScope() === scope });
        realm.controllers.set(canvasId, controller);
    }
    return controller;
}
/** Captures the realm as well as scope. A later account controller cannot lend
 * proofs to an old transport, even when the same account logs in again. */
export function captureCanvasEditProofProvider(scope = getActiveUserScope()) {
    const realm = realmFor(scope);
    return async (command: Command, includeAll = false) => {
        if (!realm.valid || getActiveUserScope() !== scope) throw new DOMException("画布账号已切换", "AbortError");
        return realm.controllers.get(command.canvasId)?.proofHeaders(command, includeAll) || {};
    };
}
export function cancelCanvasEditLeases() {
    for (const realm of realms.values()) {
        realm.valid = false;
        for (const controller of realm.controllers.values()) controller.close();
    }
    realms.clear();
}
