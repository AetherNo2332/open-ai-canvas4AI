import type { Command, Operation, Snapshot, Receipt, ChangeSet, SubmitRequest, PrevisSubmitRequest, TaskHandle } from "../lib/canvas/command-contract.generated";
import { applyCanvasEventChanges, applyCanvasDraftChanges, canvasDocumentChanges, projectCanvasOperations, type CanvasDocument } from "../lib/canvas/canvas-event-projection";
import { nanoid } from "nanoid";
import { classifyCanvasCommandFailure, historicalCanvasRejection } from "../lib/canvas/canvas-command-failure";

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
    status: "queued" | "inflight" | "unconfirmed" | "conflicted" | "rejected";
    failureStatus?: number;
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
    rejections: Array<{ operationId: string; reason: string; message: string; status?: number }>;
    saveState: "saved" | "saving" | "offline" | "conflict" | "error" | "closed";
};
export type CommandPreconditions = {
    expectedEntityVersions?: Record<string, number>;
    preview?: ChangeSet;
    operationId?: string;
    // Original semantic observations used by the binding's registry-based
    // baseline assembly. Projection bases below intentionally use current state.
    baseEntities?: Record<string, Record<string, unknown>>;
    baseFields?: Record<string, unknown>;
    baseEntityGenerations?: Record<string, number>;
};
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
    private loadingLatest = false;
    state: CommandSessionState = { acknowledged: null, pending: [], projected: {}, conflicts: [], rejections: [], saveState: "closed" };
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
        this.pending = stored.map((p) => ({
            ...copy(p),
            status: p.status === "rejected" || (p.status === "conflicted" && historicalCanvasRejection(p.reason)) ? "rejected" : p.status === "conflicted" ? "conflicted" : p.wire ? "unconfirmed" : "queued",
            resolve: () => {},
            reject: () => {},
        }));
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
            if (target && (kind === "batch" || kind === "storyboard" || request.intent !== "new_version")) {
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
        if (this.loadingLatest) return Promise.reject(new Error("正在加载云端版本，请稍后再编辑"));
        if (effect && (!this.transport.invoke || !this.transport.recover)) return Promise.reject(new Error("canvas_effect_transport_unavailable"));
        if ((!operations.length && !effect) || operations.length > 100) return Promise.reject(new Error("每条命令需要1至100项操作"));
        const entry = this.preparePending(operations, preconditions, effect);
        if (this.pending.some((other) => other.operationId === entry.pending.operationId)) {
            entry.pending.reject(new Error("操作 ID 重复"));
            return entry.completion;
        }
        this.pending.push(entry.pending);
        this.emit();
        void this.pump();
        return entry.completion;
    }
    private preparePending(operations: Operation[], preconditions: CommandPreconditions, effect?: CommandEffect, projected = this.state.projected) {
        if (!this.acknowledged) throw aborted();
        const preview = preconditions.preview || canvasDocumentChanges(projected, projectCanvasOperations(projected, operations));
        const expected = { ...this.acknowledged.entityVersions, ...preconditions.expectedEntityVersions };
        for (const key of Object.keys(this.acknowledged.document)) expected["project:" + key] ??= 0;
        for (const key of ["project:nodeOrder", "project:connectionOrder", ...this.touched(preview, operations)]) expected[key] ??= 0;
        let pending!: Pending;
        const completion = new Promise<Receipt | TaskHandle>((resolve, reject) => {
            const baseEntities: Record<string, Record<string, unknown>> = {},
                baseFields: Record<string, unknown> = {};
            for (const e of preview.entities) {
                const field = e.key.startsWith("node:") ? "nodes" : "connections";
                const before = ((projected[field] as Array<Record<string, unknown>>) || []).find((n) => n.id === e.key.slice(e.key.indexOf(":") + 1));
                if (before) baseEntities[e.key] = copy(before);
            }
            for (const key of Object.keys(preview.fields || {})) if (key in projected) Object.defineProperty(baseFields, key, { value: copy(projected[key]), enumerable: true, writable: true, configurable: true });
            pending = {
                operationId: preconditions.operationId || nanoid(),
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
        });
        return { pending, completion };
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
    private async receiveReceipt(receipt: Receipt): Promise<void> {
        const ack = this.acknowledged;
        const changes = receipt.changes;
        const empty = !receipt.reset && !changes.entities.length && !Object.keys(changes.fields || {}).length && changes.nodeOrder === undefined && changes.connectionOrder === undefined;
        if (!ack || !empty || receipt.eventSeq <= ack.eventSeq) return this.receive(receipt);
        if (receipt.protocolVersion !== 1 || receipt.canvasId !== ack.canvasId) throw new Error("canvas_event_scope_mismatch");
        // A no-op receipt points at the host's current cursor without adding an
        // event. Never let that empty ACK stand in for an unseen real event.
        const generation = this.generation;
        await this.synchronize();
        if (generation !== this.generation || !this.acknowledged) return;
        if (this.acknowledged.eventSeq < receipt.eventSeq) {
            const snapshot = await this.transport.snapshot(this.acknowledged.canvasId, this.abort.signal);
            if (generation !== this.generation) return;
            this.validateSnapshot(snapshot, this.acknowledged.canvasId);
            if (snapshot.eventSeq < receipt.eventSeq) throw new Error("canvas_receipt_cursor_unconfirmed");
            this.adopt(snapshot);
            for (const seq of this.buffer.keys()) if (seq <= snapshot.eventSeq) this.buffer.delete(seq);
            this.emit();
        }
        await this.receive(receipt);
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
                const next: Snapshot = {
                    ...before,
                    document: applyCanvasEventChanges(before.document, event.changes),
                    revision: event.revision,
                    eventSeq: event.eventSeq,
                    entityVersions: { ...before.entityVersions, ...event.entityVersions },
                    entityGenerations: { ...before.entityGenerations, ...event.entityGenerations },
                };
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
        if (!this.acknowledged || this.loadingLatest) return;
        const generation = this.generation;
        try {
            await this.synchronize();
            if (generation !== this.generation) return;
            for (const p of [...this.pending]) {
                // The live pump still owns this request's ACK. Recovery before
                // it finishes can observe a normal 404 and mark it queued.
                // Restored journal wires enter as unconfirmed, so still recover.
                if (p.status === "inflight" && this.pumping) continue;
                if (p.wire && p.effect?.type === "restore" && p.documentEpoch !== this.acknowledged!.documentEpoch) {
                    try {
                        const result = await this.transport.recover!(p.wire, p.effect, this.abort.signal);
                        if (generation !== this.generation) return;
                        await this.receiveReceipt("receipt" in result ? result.receipt : result);
                    } catch (error) {
                        if (errorStatus(error) !== 404) throw error;
                        this.conflict(p, "document_epoch_mismatch", "画布已恢复到新的世代，原恢复请求已保留");
                    }
                    continue;
                }
                if (p.documentEpoch !== this.acknowledged!.documentEpoch || p.status === "conflicted" || p.status === "rejected") continue;
                if (p.wire) {
                    try {
                        const result = p.effect ? await this.transport.recover!(p.wire, p.effect, this.abort.signal) : await this.transport.receipt(p.wire, this.abort.signal);
                        if (generation !== this.generation) return;
                        const receipt = "receipt" in result ? result.receipt : result;
                        if ("receipt" in result) p.result = result;
                        await this.receiveReceipt(receipt);
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
        if (this.loadingLatest) return Promise.resolve();
        if (this.pumping) return this.pumping;
        const generation = this.generation;
        const run = async () => {
            while (this.acknowledged && !this.offline && !this.loadingLatest) {
                const p = this.pending.find((p) => p.status === "queued");
                if (!p) break;
                if (this.pending.some((other) => ["conflicted", "rejected"].includes(other.status) && p.dependencies.includes(other.operationId))) break;
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
                    await this.receiveReceipt(receipt);
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
                    const kind = classifyCanvasCommandFailure(status, errorReason(error));
                    if (kind !== "unconfirmed") {
                        const reason = errorReason(error) || "command_rejected";
                        const message = error instanceof Error ? error.message : "修改未保存，请检查后重试";
                        if (kind === "conflict") this.conflict(p, reason, message);
                        else {
                            p.status = "rejected";
                            p.failureStatus = status;
                            p.reason = reason;
                            p.message = message;
                            p.reject(Object.assign(new Error(message), { reason, status }));
                        }
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
    private emit(persist = true) {
        let projected = this.acknowledged ? copy(this.acknowledged.document) : {};
        const conflicts: CommandSessionState["conflicts"] = [];
        const rejections: CommandSessionState["rejections"] = [];
        for (const p of this.pending) {
            try {
                if (!p.acknowledgedReceipt) projected = applyCanvasDraftChanges(projected, p.preview, p.baseEntities, p.baseFields);
            } catch {
                if (p.status === "conflicted") continue;
                if (p.status !== "rejected") this.conflict(p, "projection_conflict", "原编辑草稿不能直接投影到最新画布，已保留待处理");
            }
            if (p.status === "rejected") {
                rejections.push({ operationId: p.operationId, reason: p.reason || "command_rejected", message: p.message || "修改未保存，请检查后重试", status: p.failureStatus });
                continue;
            }
            let reason = p.reason,
                message = p.message;
            if (!reason && this.acknowledged) {
                for (const key of this.touched(p.preview, p.operations)) {
                    if ((p.expectedEntityVersions[key] ?? 0) !== (this.acknowledged.entityVersions[key] ?? 0)) {
                        // Field comparisons are authoritative on the host. Do
                        // not report disjoint updates as conflicts pre-emptively.
                        const targets = p.operations.filter((op) => "node:" + op.id === key);
                        if (p.operations.every((op) => op.type === "update_node") && targets.length && targets.every((op) => op.fieldBaselines?.generation !== undefined && op.fieldBaselines.generation === this.acknowledged!.entityGenerations?.[key]))
                            continue;
                        reason = "entity_version_conflict";
                        message = "其他编辑已修改相关对象，原草稿已保留";
                        break;
                    }
                }
            }
            if (reason) conflicts.push({ operationId: p.operationId, reason, message: message || "编辑草稿需要处理", operations: copy(p.operations) });
        }
        const stored = this.pending.map(({ resolve: _, reject: __, ...p }) => copy(p));
        this.state = {
            acknowledged: this.acknowledged,
            pending: stored,
            projected,
            conflicts,
            rejections,
            saveState: !this.acknowledged ? "closed" : conflicts.length ? "conflict" : this.offline ? "offline" : rejections.length ? "error" : this.pending.length ? "saving" : "saved",
        };
        if (persist && this.journal && this.acknowledged) {
            const id = this.acknowledged.canvasId;
            this.journalTail = this.journalTail.catch(() => {}).then(() => this.journal!.write(id, stored));
            void this.journalTail.catch(() => {});
        }
        for (const listener of this.listeners) listener(this.state);
    }
    async replaceRejectedDraft(operations: Operation[], preconditions: CommandPreconditions, preserveDraft: () => Promise<void>, validateLocalDraft: () => void): Promise<void> {
        if (!this.acknowledged) throw aborted();
        if (this.loadingLatest) throw new Error("正在处理本地草稿，请稍候");
        if (!this.pending.some((p) => p.status === "rejected") || this.pending.some((p) => p.effect || !["rejected", "queued"].includes(p.status))) {
            throw new Error("仍有冲突、任务或未确认的提交，请先确认保存结果；本地修改已保留");
        }
        if (operations.length > 100) throw new Error("本次修改过多，请先下载草稿后分批处理");
        const generation = this.generation,
            acknowledged = this.acknowledged,
            pending = [...this.pending];
        const original = pending.map(({ resolve: _, reject: __, ...p }) => copy(p));
        const unchanged = () => {
            if (generation !== this.generation) throw aborted();
            if (this.acknowledged !== acknowledged || this.pending.length !== pending.length || this.pending.some((p, index) => p !== pending[index])) throw new Error("画布仍在更新，本地修改已保留，请稍后再重试");
            validateLocalDraft();
        };
        this.loadingLatest = true;
        let entry: ReturnType<CanvasCommandSession["preparePending"]> | undefined;
        try {
            await this.whenJournalDurable();
            await preserveDraft();
            unchanged();
            if (operations.length) {
                entry = this.preparePending(operations, preconditions, undefined, acknowledged.document);
                entry.pending.dependencies = [];
                void entry.completion.catch(() => {});
            }
            const stored = entry ? [copy((({ resolve: _, reject: __, ...p }) => p)(entry.pending))] : [];
            if (this.journal) {
                try {
                    this.journalTail = this.journalTail.then(() => this.journal!.write(acknowledged.canvasId, stored));
                    await this.journalTail;
                    unchanged();
                } catch (error) {
                    // Restore identities even if storage reports failure after a write.
                    this.journalTail = this.journalTail.catch(() => {}).then(() => this.journal!.write(acknowledged.canvasId, original));
                    await this.journalTail;
                    throw error;
                }
            } else unchanged();
            this.pending = entry ? [entry.pending] : [];
            for (const old of pending) old.reject(new DOMException("原修改已备份，正在保存修正后的内容", "AbortError"));
            this.offline = false;
            this.emit(false);
        } finally {
            if (generation === this.generation) this.loadingLatest = false;
        }
        await this.pump();
        if (entry) {
            if (this.pending.includes(entry.pending) && ["inflight", "unconfirmed", "queued"].includes(entry.pending.status)) throw new Error("保存结果尚未确认，请先重新同步；本地修改已保留");
            await entry.completion;
        }
    }
    async retryRejected(operationId: string): Promise<void> {
        if (this.loadingLatest) throw new Error("正在处理本地草稿，请稍候");
        const pending = this.pending.find((p) => p.operationId === operationId);
        if (!pending || pending.status !== "rejected") throw new Error("此提交不能直接重试，请先确认保存结果");
        pending.status = "queued";
        delete pending.reason;
        delete pending.message;
        delete pending.failureStatus;
        this.emit();
        await this.pump();
        if (this.pending.includes(pending)) throw Object.assign(new Error(pending.message || "保存结果尚未确认，本地修改已保留"), { status: pending.failureStatus, reason: pending.reason });
    }
    async loadLatest(preserveDraft: (document: CanvasDocument) => Promise<void>, validateLocalDraft: () => void = () => {}): Promise<void> {
        if (!this.acknowledged) throw aborted();
        if (this.loadingLatest) throw new Error("正在加载云端版本，请稍候");
        if (this.pending.some((p) => p.status === "inflight" || p.status === "unconfirmed")) {
            throw new Error("仍有提交正在等待服务端确认，请先点击「重新同步」，确认结果后再加载云端版本；本地修改已保留");
        }
        const generation = this.generation;
        const acknowledged = this.acknowledged;
        const pending = [...this.pending];
        const unchanged = () => generation === this.generation && this.acknowledged === acknowledged && this.pending.length === pending.length && this.pending.every((p, index) => p === pending[index]);
        const requireUnchanged = () => {
            if (generation !== this.generation) throw aborted();
            if (!unchanged()) throw new Error("画布仍在更新，本地修改已保留，请稍后再加载云端版本");
        };
        const stored = pending.map(({ resolve: _, reject: __, ...p }) => copy(p));
        this.loadingLatest = true;
        try {
            await this.whenJournalDurable();
            const snapshot = await this.transport.snapshot(acknowledged.canvasId, this.abort.signal);
            requireUnchanged();
            this.validateSnapshot(snapshot, acknowledged.canvasId);
            if (snapshot.eventSeq < acknowledged.eventSeq || snapshot.revision < acknowledged.revision) throw new Error("云端版本尚未更新完成，请稍后重试");
            // Conflicted previews are absent from the live projection. Include them
            // in the independent backup before removing any pending command.
            let draft = copy(acknowledged.document);
            for (const p of pending) draft = applyCanvasDraftChanges(draft, p.preview, p.baseEntities, p.baseFields);
            await preserveDraft(draft);
            requireUnchanged();
            validateLocalDraft();
            if (this.journal) {
                this.journalTail = this.journalTail.then(() => this.journal!.write(acknowledged.canvasId, []));
                await this.journalTail;
                try {
                    requireUnchanged();
                    validateLocalDraft();
                } catch (error) {
                    this.journalTail = this.journalTail.then(() => this.journal!.write(acknowledged.canvasId, stored));
                    await this.journalTail;
                    throw error;
                }
            }
            this.pending = [];
            this.adopt(snapshot);
            this.buffer.clear();
            this.offline = false;
            for (const p of pending) p.reject(new DOMException("未同步修改已保留为本地草稿，已采用云端版本", "AbortError"));
            this.emit(false);
        } finally {
            if (generation === this.generation) this.loadingLatest = false;
        }
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
    whenJournalDurable(): Promise<void> {
        return this.journalTail;
    }
    persistJournal(): Promise<void> {
        return this.whenJournalDurable();
    }
    close() {
        this.generation++;
        this.loadingLatest = false;
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
