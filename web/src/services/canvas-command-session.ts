import type { Command, Operation, Snapshot, Receipt, ChangeSet, SubmitRequest, PrevisSubmitRequest, TaskHandle } from "../lib/canvas/command-contract.generated";
import { applyCanvasEventChanges, applyCanvasDraftChanges, canvasDocumentChanges, projectCanvasOperations, type CanvasDocument } from "../lib/canvas/canvas-event-projection";

export type CommandTransport = {
    snapshot(canvasId: string, signal?: AbortSignal): Promise<Snapshot>;
    events(canvasId: string, after: number, signal?: AbortSignal): Promise<Receipt[]>;
    execute(command: Command, signal?: AbortSignal): Promise<Receipt>;
    receipt(command: Command, signal?: AbortSignal): Promise<Receipt>;
    invoke?(command: Command, effect: CommandEffect, signal?: AbortSignal): Promise<Receipt | TaskHandle>;
    recover?(command: Command, effect: CommandEffect, signal?: AbortSignal): Promise<Receipt | TaskHandle>;
};
export type CommandEffect =
    | { type: "task"; request: Omit<SubmitRequest, "command"> }
    | { type: "previs"; request: Omit<PrevisSubmitRequest, "command"> }
    | { type: "undo" | "redo" }
    | { type: "resolve_uncertainty"; taskId: string }
    | { type: "restore"; document: CanvasDocument; expectedRevision: number };
export type StoredPending = {
    operationId: string;
    documentEpoch: string;
    operations: Operation[];
    effect?: CommandEffect;
    acknowledgedReceipt?: Receipt;
    result?: TaskHandle;
    preview: ChangeSet;
    baseEntities?: Record<string, Record<string, unknown>>;
    baseFields?: Record<string, unknown>;
    expectedEntityVersions: Record<string, number>;
    dependencies: string[];
    wire?: Command;
    status: "queued" | "inflight" | "unconfirmed" | "conflicted";
    reason?: string;
    message?: string;
};
type Pending = StoredPending & { resolve: (receipt: Receipt | TaskHandle) => void; reject: (error: unknown) => void };
export type CommandJournal = { read(canvasId: string): Promise<StoredPending[]>; write(canvasId: string, pending: StoredPending[]): Promise<void> };
export type CommandSessionState = {
    acknowledged: Snapshot | null;
    pending: StoredPending[];
    projected: CanvasDocument;
    conflicts: Array<{ operationId: string; reason: string; message: string; operations: Operation[] }>;
    saveState: "saved" | "saving" | "offline" | "conflict" | "closed";
};
export type CommandPreconditions = { expectedEntityVersions?: Record<string, number>; preview?: ChangeSet; operationId?: string };
const copy = <T>(v: T): T => structuredClone(v);
const errorStatus = (e: unknown) => (e && typeof e === "object" ? ("status" in e ? Number(e.status) : undefined) : undefined);
const errorReason = (e: unknown) => (e && typeof e === "object" && "reason" in e ? String(e.reason) : undefined);
const aborted = () => new DOMException("画布会话已关闭", "AbortError");

export class CanvasCommandSession {
    private acknowledged: Snapshot | null = null;
    private pending: Pending[] = [];
    private listeners = new Set<(state: CommandSessionState) => void>();
    private buffer = new Map<number, Receipt>();
    private generation = 0;
    private abort = new AbortController();
    private offline = false;
    private pumping: Promise<void> | null = null;
    private synchronizing: Promise<void> | null = null;
    private journalTail: Promise<void> = Promise.resolve();
    state: CommandSessionState = { acknowledged: null, pending: [], projected: {}, conflicts: [], saveState: "closed" };
    constructor(
        private transport: CommandTransport,
        readonly clientId: string,
        private journal?: CommandJournal,
    ) {}
    subscribe(listener: (state: CommandSessionState) => void) {
        this.listeners.add(listener);
        listener(this.state);
        return () => this.listeners.delete(listener);
    }
    async open(canvasId: string): Promise<void> {
        this.close();
        const generation = this.generation;
        this.abort = new AbortController();
        const signal = this.abort.signal;
        const snapshot = await this.transport.snapshot(canvasId, signal);
        if (generation !== this.generation) throw aborted();
        this.validateSnapshot(snapshot, canvasId);
        const stored = (await this.journal?.read(canvasId)) || [];
        if (generation !== this.generation) throw aborted();
        for (const p of stored) {
            if (p.wire && (p.wire.protocolVersion !== 1 || p.wire.canvasId !== canvasId || p.wire.clientId !== this.clientId || p.wire.operationId !== p.operationId || p.wire.documentEpoch !== p.documentEpoch))
                throw new Error("canvas_command_journal_scope_mismatch");
        }
        this.acknowledged = copy(snapshot);
        this.offline = false;
        this.pending = stored.map((p) => ({ ...copy(p), status: p.wire ? "unconfirmed" : p.status === "conflicted" ? "conflicted" : "queued", resolve: () => {}, reject: () => {} }));
        this.emit();
        if (this.pending.length) await this.reconnect();
    }
    submit(operations: Operation[], preconditions: CommandPreconditions = {}): Promise<Receipt> {
        return this.enqueue(operations, preconditions) as Promise<Receipt>;
    }
    submitTask(request: Omit<SubmitRequest, "command">, preconditions: CommandPreconditions = {}): Promise<TaskHandle> {
        const doc = copy(this.state.projected),
            node = ((doc.nodes as Array<Record<string, unknown>>) || []).find((n) => n.id === request.targetNodeId);
        if (node) {
            const metadata = (node.metadata ||= {}) as Record<string, unknown>;
            const kind = request.consumer?.kind;
            const rows = (metadata[kind === "batch" ? "batchTable" : "storyboard"] as { rows?: Array<Record<string, unknown>> } | undefined)?.rows;
            const target = kind === "batch" || kind === "storyboard" ? rows?.find((row) => row.id === request.consumer?.rowId) : metadata;
            if (target) {
                delete target.taskId;
                delete target.errorDetails;
                Object.assign(target, { status: "loading", taskStatus: "queued", taskProgress: 0 });
            }
        }
        return this.enqueue([], { ...preconditions, preview: preconditions.preview || canvasDocumentChanges(this.state.projected, doc) }, { type: "task", request: copy(request) }) as Promise<TaskHandle>;
    }
    submitPrevis(request: Omit<PrevisSubmitRequest, "command">, preconditions: CommandPreconditions = {}): Promise<TaskHandle> {
        return this.enqueue([], preconditions, { type: "previs", request: copy(request) }) as Promise<TaskHandle>;
    }
    history(action: "undo" | "redo"): Promise<Receipt> {
        return this.enqueue([], {}, { type: action }) as Promise<Receipt>;
    }
    restoreVersion(document: CanvasDocument, expectedRevision: number): Promise<Receipt> {
        if (!Number.isSafeInteger(expectedRevision) || expectedRevision < 0) return Promise.reject(new Error("canvas_restore_revision_required"));
        return this.enqueue([], {}, { type: "restore", document: copy(document), expectedRevision }) as Promise<Receipt>;
    }
    resolveUncertainty(taskId: string): Promise<Receipt> {
        return this.enqueue([], {}, { type: "resolve_uncertainty", taskId }) as Promise<Receipt>;
    }
    private enqueue(operations: Operation[], preconditions: CommandPreconditions = {}, effect?: CommandEffect): Promise<Receipt | TaskHandle> {
        if (!this.acknowledged) return Promise.reject(aborted());
        if (effect && (!this.transport.invoke || !this.transport.recover)) return Promise.reject(new Error("canvas_effect_transport_unavailable"));
        if ((!operations.length && !effect) || operations.length > 100) return Promise.reject(new Error("每条命令需要1至100项操作"));
        const projected = this.state.projected;
        const preview = preconditions.preview || canvasDocumentChanges(projected, projectCanvasOperations(projected, operations));
        const expected = { ...this.acknowledged.entityVersions, ...preconditions.expectedEntityVersions };
        for (const key of Object.keys(this.acknowledged.document)) expected["project:" + key] ??= 0;
        for (const key of ["project:nodeOrder", "project:connectionOrder", ...this.touched(preview, operations)]) expected[key] ??= 0;
        return new Promise<Receipt | TaskHandle>((resolve, reject) => {
            const baseEntities: Record<string, Record<string, unknown>> = {},
                baseFields: Record<string, unknown> = {};
            for (const e of preview.entities) {
                const field = e.key.startsWith("node:") ? "nodes" : "connections";
                const before = ((projected[field] as Array<Record<string, unknown>>) || []).find((n) => n.id === e.key.slice(e.key.indexOf(":") + 1));
                if (before) baseEntities[e.key] = copy(before);
            }
            for (const key of Object.keys(preview.fields || {})) if (key in projected) Object.defineProperty(baseFields, key, { value: copy(projected[key]), enumerable: true, writable: true, configurable: true });
            const p: Pending = {
                operationId: preconditions.operationId || crypto.randomUUID(),
                documentEpoch: this.acknowledged!.documentEpoch,
                operations: copy(operations),
                effect,
                preview: copy(preview),
                baseEntities,
                baseFields,
                expectedEntityVersions: expected,
                dependencies: this.pending.map((p) => p.operationId),
                status: "queued",
                resolve,
                reject,
            };
            if (this.pending.some((other) => other.operationId === p.operationId)) {
                reject(new Error("操作 ID 重复"));
                return;
            }
            this.pending.push(p);
            this.emit();
            void this.pump();
        });
    }
    async receive(event: Receipt): Promise<void> {
        const ack = this.acknowledged;
        if (!ack) return;
        if (event.protocolVersion !== 1 || event.canvasId !== ack.canvasId) throw new Error("canvas_event_scope_mismatch");
        // A reset carries the new snapshot. It is valid only as the next event;
        // missing preceding events must still be fetched before resetting drafts.
        if (event.eventSeq <= ack.eventSeq) {
            this.settle(event);
            this.emit();
            return;
        }
        this.buffer.set(event.eventSeq, copy(event));
        this.drainBuffer();
        if (this.buffer.size) await this.synchronize();
    }
    private drainBuffer() {
        while (this.acknowledged) {
            const event = this.buffer.get(this.acknowledged.eventSeq + 1);
            if (!event) break;
            this.buffer.delete(event.eventSeq);
            if (event.reset) {
                this.validateSnapshot(event.reset, event.canvasId);
                this.settle(event);
                this.adopt(event.reset);
            } else {
                if (event.documentEpoch !== this.acknowledged.documentEpoch) throw new Error("event_epoch_missing_reset");
                const before = this.acknowledged;
                const next: Snapshot = { ...before, document: applyCanvasEventChanges(before.document, event.changes), revision: event.revision, eventSeq: event.eventSeq, entityVersions: { ...before.entityVersions, ...event.entityVersions } };
                this.acknowledged = next;
                if (event.clientId === this.clientId && this.pending.some((p) => p.operationId === event.operationId)) {
                    for (const p of this.pending) {
                        if (p.wire || !p.dependencies.includes(event.operationId)) continue;
                        for (const [key, version] of Object.entries(event.entityVersions)) {
                            if ((p.expectedEntityVersions[key] ?? 0) === (before.entityVersions[key] ?? 0)) p.expectedEntityVersions[key] = version;
                        }
                    }
                }
            }
            this.settle(event);
            this.emit();
        }
    }
    private settle(receipt: Receipt) {
        if (receipt.clientId !== this.clientId) return;
        const index = this.pending.findIndex((p) => p.operationId === receipt.operationId && (p.documentEpoch === receipt.documentEpoch || (p.effect?.type === "restore" && !!receipt.reset)));
        if (index < 0) return;
        const p = this.pending[index];
        if ((p.effect?.type === "task" || p.effect?.type === "previs") && !p.result) {
            p.acknowledgedReceipt = copy(receipt);
            return;
        }
        this.pending.splice(index, 1);
        p.resolve(p.result || receipt);
    }
    private adopt(snapshot: Snapshot) {
        const previous = this.acknowledged;
        this.acknowledged = copy(snapshot);
        if (previous && snapshot.documentEpoch !== previous.documentEpoch) {
            for (const p of this.pending) {
                if (p.documentEpoch !== snapshot.documentEpoch) this.conflict(p, "document_epoch_mismatch", "画布已恢复到新的世代，原编辑草稿已保留");
            }
        }
    }
    private validateSnapshot(snapshot: Snapshot, canvasId: string) {
        if (snapshot.protocolVersion !== 1 || snapshot.canvasId !== canvasId || !snapshot.documentEpoch || !Number.isSafeInteger(snapshot.eventSeq) || snapshot.eventSeq < 0) throw new Error("canvas_snapshot_invalid");
    }
    private synchronize(): Promise<void> {
        if (this.synchronizing) return this.synchronizing;
        const generation = this.generation;
        const run = async () => {
            while (this.acknowledged) {
                const after = this.acknowledged.eventSeq;
                const events = await this.transport.events(this.acknowledged.canvasId, after, this.abort.signal);
                if (generation !== this.generation) return;
                for (const event of events) {
                    if (event.protocolVersion !== 1 || event.canvasId !== this.acknowledged.canvasId) throw new Error("canvas_event_scope_mismatch");
                    if (event.eventSeq > this.acknowledged.eventSeq) this.buffer.set(event.eventSeq, event);
                    else this.settle(event);
                }
                this.drainBuffer();
                if (this.acknowledged.eventSeq === after && this.buffer.size) {
                    const snapshot = await this.transport.snapshot(this.acknowledged.canvasId, this.abort.signal);
                    if (generation !== this.generation) return;
                    this.validateSnapshot(snapshot, this.acknowledged.canvasId);
                    if (snapshot.eventSeq >= after) {
                        this.adopt(snapshot);
                        for (const seq of this.buffer.keys()) if (seq <= snapshot.eventSeq) this.buffer.delete(seq);
                        this.emit();
                    }
                    break;
                }
                if (events.length < 250) break;
            }
        };
        const result = run().finally(() => {
            if (this.synchronizing === result) this.synchronizing = null;
        });
        this.synchronizing = result;
        return result;
    }
    async reconnect(): Promise<void> {
        if (!this.acknowledged) return;
        const generation = this.generation;
        try {
            await this.synchronize();
            if (generation !== this.generation) return;
            for (const p of [...this.pending]) {
                if (p.wire && p.effect?.type === "restore" && p.documentEpoch !== this.acknowledged!.documentEpoch) {
                    try {
                        const result = await this.transport.recover!(p.wire, p.effect, this.abort.signal);
                        if (generation !== this.generation) return;
                        await this.receive("receipt" in result ? result.receipt : result);
                    } catch (error) {
                        if (errorStatus(error) !== 404) throw error;
                        this.conflict(p, "document_epoch_mismatch", "画布已恢复到新的世代，原恢复请求已保留");
                    }
                    continue;
                }
                if (p.documentEpoch !== this.acknowledged!.documentEpoch || p.status === "conflicted") continue;
                if (p.wire) {
                    try {
                        const result = p.effect ? await this.transport.recover!(p.wire, p.effect, this.abort.signal) : await this.transport.receipt(p.wire, this.abort.signal);
                        if (generation !== this.generation) return;
                        const receipt = "receipt" in result ? result.receipt : result;
                        if ("receipt" in result) p.result = result;
                        await this.receive(receipt);
                        continue;
                    } catch (error) {
                        if (generation !== this.generation) return;
                        if (errorStatus(error) !== 404) throw error;
                    }
                }
                p.status = "queued";
            }
            this.offline = false;
            this.emit();
            await this.pump();
        } catch (error) {
            if (generation !== this.generation) return;
            this.offline = true;
            this.emit();
        }
    }
    private pump(): Promise<void> {
        if (this.pumping) return this.pumping;
        const generation = this.generation;
        const run = async () => {
            while (this.acknowledged && !this.offline) {
                const p = this.pending.find((p) => p.status === "queued");
                if (!p) break;
                if (this.pending.some((other) => other.status === "conflicted" && p.dependencies.includes(other.operationId))) break;
                if (p.documentEpoch !== this.acknowledged.documentEpoch) {
                    this.conflict(p, "document_epoch_mismatch", "画布世代已变化，编辑草稿已保留");
                    this.emit();
                    continue;
                }
                p.wire ??= {
                    protocolVersion: 1,
                    canvasId: this.acknowledged.canvasId,
                    documentEpoch: p.documentEpoch,
                    clientId: this.clientId,
                    operationId: p.operationId,
                    baseRevision: this.acknowledged.revision,
                    expectedEntityVersions: copy(p.expectedEntityVersions),
                    operations: copy(p.operations),
                };
                p.status = "inflight";
                this.emit();
                try {
                    // Write original operation identity/envelope before any host mutation.
                    await this.journalTail;
                    if (generation !== this.generation) return;
                    const result = p.effect ? await this.transport.invoke!(p.wire, p.effect, this.abort.signal) : await this.transport.execute(p.wire, this.abort.signal);
                    if (generation !== this.generation) return;
                    const receipt = "receipt" in result ? result.receipt : result;
                    if ("receipt" in result) p.result = result;
                    await this.receive(receipt);
                    if (generation !== this.generation) return;
                    if (this.pending.includes(p)) {
                        p.status = "unconfirmed";
                        this.offline = true;
                        this.emit();
                        break;
                    }
                } catch (error) {
                    if (generation !== this.generation) return;
                    const status = errorStatus(error);
                    if (status !== undefined && status >= 400 && status < 500 && ![408, 425, 429].includes(status)) {
                        this.conflict(p, errorReason(error) || "command_rejected", error instanceof Error ? error.message : "操作被主机拒绝");
                        this.emit();
                        continue;
                    }
                    p.status = "unconfirmed";
                    this.offline = true;
                    this.emit();
                    break;
                }
            }
        };
        const result = run().finally(() => {
            if (this.pumping === result) this.pumping = null;
        });
        this.pumping = result;
        return result;
    }
    private conflict(p: Pending, reason: string, message: string) {
        p.status = "conflicted";
        p.reason = reason;
        p.message = message;
        p.reject(Object.assign(new Error(message), { reason, status: 409 }));
    }
    private touched(preview: ChangeSet, ops: Operation[]): string[] {
        const keys = new Set(preview.entities.map((e) => e.key));
        for (const field of Object.keys(preview.fields || {})) keys.add("project:" + field);
        if (preview.nodeOrder) keys.add("project:nodeOrder");
        if (preview.connectionOrder) keys.add("project:connectionOrder");
        for (const op of ops) {
            for (const id of [op.fromNodeId, op.toNodeId, op.parentId, op.sourceNodeId]) if (id) keys.add("node:" + id);
            if (op.type === "edit_drawing") keys.add("drawing:" + op.id);
        }
        return [...keys];
    }
    private emit() {
        let projected = this.acknowledged ? copy(this.acknowledged.document) : {};
        const conflicts: CommandSessionState["conflicts"] = [];
        for (const p of this.pending) {
            try {
                if (!p.acknowledgedReceipt) projected = applyCanvasDraftChanges(projected, p.preview, p.baseEntities, p.baseFields);
            } catch {
                if (p.status === "conflicted") continue;
                this.conflict(p, "projection_conflict", "原编辑草稿不能直接投影到最新画布，已保留待处理");
            }
            let reason = p.reason,
                message = p.message;
            if (!reason && this.acknowledged) {
                for (const key of this.touched(p.preview, p.operations)) {
                    if ((p.expectedEntityVersions[key] ?? 0) !== (this.acknowledged.entityVersions[key] ?? 0)) {
                        reason = "entity_version_conflict";
                        message = "其他编辑已修改相关对象，原草稿已保留";
                        break;
                    }
                }
            }
            if (reason) conflicts.push({ operationId: p.operationId, reason, message: message || "编辑草稿需要处理", operations: copy(p.operations) });
        }
        const stored = this.pending.map(({ resolve: _, reject: __, ...p }) => copy(p));
        this.state = { acknowledged: this.acknowledged, pending: stored, projected, conflicts, saveState: !this.acknowledged ? "closed" : conflicts.length ? "conflict" : this.offline ? "offline" : this.pending.length ? "saving" : "saved" };
        if (this.journal && this.acknowledged) {
            const id = this.acknowledged.canvasId;
            this.journalTail = this.journalTail.catch(() => {}).then(() => this.journal!.write(id, stored));
            void this.journalTail.catch(() => {});
        }
        for (const listener of this.listeners) listener(this.state);
    }
    discard(operationId: string) {
        const index = this.pending.findIndex((p) => p.operationId === operationId);
        if (index < 0) return;
        if (this.pending[index].wire && ["inflight", "unconfirmed"].includes(this.pending[index].status)) throw new Error("提交回执尚未确认，请先同步回执");
        const [p] = this.pending.splice(index, 1);
        p.reject(new DOMException("已丢弃未确认草稿", "AbortError"));
        this.emit();
        void this.pump();
    }
    async flush(): Promise<boolean> {
        await this.pumping;
        try {
            await this.journalTail;
        } catch {
            return false;
        }
        return this.pending.length === 0;
    }
    close() {
        this.generation++;
        this.abort.abort();
        for (const p of this.pending) p.reject(aborted());
        this.pending = [];
        this.acknowledged = null;
        this.buffer.clear();
        this.offline = false;
        this.pumping = null;
        this.synchronizing = null;
        this.emit();
    }
}
