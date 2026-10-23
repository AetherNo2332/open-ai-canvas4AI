import type { CreateCanvasRequest, Snapshot } from "../lib/canvas/command-contract.generated";
import type { CommandJournalStorage } from "./canvas-command-journal";
import { nanoid } from "nanoid";
type CreateInput = Omit<CreateCanvasRequest, "clientId" | "operationId">;
type Saved = { protocolVersion: 1; scope: string; clientId: string; pending: CreateCanvasRequest[] };
export function createCanvasCommandCreator(options: { scope: string; clientId: string; storage: CommandJournalStorage; create: (request: CreateCanvasRequest) => Promise<Snapshot>; onCreated: (snapshot: Snapshot) => void }) {
    const { scope, clientId, storage } = options;
    if (!scope || !clientId) throw new Error("canvas_creation_scope_missing");
    const key = "infinite-canvas:command-journal:" + encodeURIComponent(scope) + ":creates";
    let closed = false,
        tail: Promise<unknown> = Promise.resolve();
    const check = () => {
        if (closed) throw new DOMException("画布主机已切换", "AbortError");
    };
    const save = (pending: CreateCanvasRequest[]) => storage.setItem<Saved>(key, { protocolVersion: 1, scope, clientId, pending: structuredClone(pending) });
    async function read() {
        const saved = await storage.getItem<Saved>(key);
        check();
        if (saved === null) return [];
        if (saved.protocolVersion !== 1 || saved.scope !== scope || saved.clientId !== clientId || !Array.isArray(saved.pending) || saved.pending.some((p) => !p.operationId || p.clientId !== clientId || typeof p.title !== "string"))
            throw new Error("canvas_creation_journal_invalid");
        return structuredClone(saved.pending);
    }
    async function drain(pending: CreateCanvasRequest[]) {
        const results = new Map<string, Snapshot>();
        while (pending.length) {
            check();
            const request = pending[0],
                snapshot = await options.create(structuredClone(request));
            check();
            if (snapshot.protocolVersion !== 1 || !snapshot.canvasId || !snapshot.documentEpoch || snapshot.document.id !== snapshot.canvasId) throw new Error("canvas_creation_snapshot_invalid");
            options.onCreated(snapshot);
            results.set(request.operationId, snapshot);
            pending.shift();
            await save(pending);
        }
        return results;
    }
    function serial<T>(work: () => Promise<T>): Promise<T> {
        const task = tail
            .catch(() => {})
            .then(() => {
                check();
                return work();
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
                return (await drain(pending)).get(request.operationId)!;
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
