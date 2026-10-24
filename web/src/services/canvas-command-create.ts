import type { CreateCanvasRequest, Snapshot } from "../lib/canvas/command-contract.generated";
import type { CommandJournalStorage } from "./canvas-command-journal";
import { nanoid } from "nanoid";
import { withCanvasJournalLock } from "./canvas-journal-lock";
import { classifyCanvasCommandFailure } from "../lib/canvas/canvas-command-failure";
type CreateInput = Omit<CreateCanvasRequest, "clientId" | "operationId">;
type FailedCreate = { request: CreateCanvasRequest; message: string; status: number; reason?: string };
type Saved = { protocolVersion: 1; scope: string; clientId: string; pending: CreateCanvasRequest[]; failed?: FailedCreate[] };
export function createCanvasCommandCreator(options: { scope: string; clientId: string; storage: CommandJournalStorage; create: (request: CreateCanvasRequest) => Promise<Snapshot>; onCreated: (snapshot: Snapshot) => void }) {
    const { scope, clientId, storage } = options;
    if (!scope || !clientId) throw new Error("canvas_creation_scope_missing");
    const key = "infinite-canvas:command-journal:" + encodeURIComponent(scope) + ":creates";
    let closed = false,
        tail: Promise<unknown> = Promise.resolve();
    const check = () => {
        if (closed) throw new DOMException("画布主机已切换", "AbortError");
    };
    let failed: FailedCreate[] = [];
    const save = (pending: CreateCanvasRequest[]) => storage.setItem<Saved>(key, { protocolVersion: 1, scope, clientId, pending: structuredClone(pending), failed: structuredClone(failed) });
    async function read() {
        const saved = await storage.getItem<Saved>(key);
        check();
        if (saved === null) {
            failed = [];
            return [];
        }
        if (saved.protocolVersion !== 1 || saved.scope !== scope || saved.clientId !== clientId || !Array.isArray(saved.pending) || saved.pending.some((p) => !p.operationId || p.clientId !== clientId || typeof p.title !== "string"))
            throw new Error("canvas_creation_journal_invalid");
        failed = structuredClone(saved.failed || []);
        return structuredClone(saved.pending);
    }
    async function drain(pending: CreateCanvasRequest[]) {
        const results = new Map<string, Snapshot>();
        const rejections = new Map<string, unknown>();
        while (pending.length) {
            check();
            const request = pending[0];
            let snapshot: Snapshot;
            try {
                snapshot = await options.create(structuredClone(request));
            } catch (error) {
                const status = error && typeof error === "object" && "status" in error ? Number(error.status) : undefined;
                const reason = error && typeof error === "object" && "reason" in error ? String(error.reason) : undefined;
                if (classifyCanvasCommandFailure(status, reason) === "unconfirmed" || status === 401) throw error;
                check();
                failed.push({ request, message: error instanceof Error ? error.message : "画布创建被拒绝", status: status!, reason });
                rejections.set(request.operationId, error);
                pending.shift();
                await save(pending);
                continue;
            }
            check();
            if (snapshot.protocolVersion !== 1 || !snapshot.canvasId || !snapshot.documentEpoch || snapshot.document.id !== snapshot.canvasId) throw new Error("canvas_creation_snapshot_invalid");
            options.onCreated(snapshot);
            results.set(request.operationId, snapshot);
            pending.shift();
            await save(pending);
        }
        return { results, rejections };
    }
    function serial<T>(work: () => Promise<T>): Promise<T> {
        const task = tail
            .catch(() => {})
            .then(() => {
                check();
                return withCanvasJournalLock(key, work);
            });
        tail = task;
        return task;
    }
    return {
        create(input: CreateInput) {
            return serial(async () => {
                const pending = await read();
                const matches = (request: CreateCanvasRequest) => JSON.stringify({ ...request, operationId: undefined, clientId: undefined }) === JSON.stringify({ ...input, operationId: undefined, clientId: undefined });
                const request = pending.find(matches) || { ...structuredClone(input), clientId, operationId: nanoid(48) };
                if (!pending.includes(request)) pending.push(request);
                // Persist before every network attempt, including a previous failed save.
                await save(pending);
                check();
                const { results, rejections } = await drain(pending);
                if (rejections.has(request.operationId)) throw rejections.get(request.operationId);
                return results.get(request.operationId)!;
            });
        },
        recover: () =>
            serial(async () => {
                await drain(await read());
            }),
        close() {
            closed = true;
        },
    };
}
