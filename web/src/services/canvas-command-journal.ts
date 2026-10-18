import type { CommandJournal, StoredPending } from "./canvas-command-session";
export type CommandJournalStorage = { getItem<T>(key: string): Promise<T | null>; setItem<T>(key: string, value: T): Promise<unknown> };
const scopeKey = (scope: string) => "infinite-canvas:command-journal:" + encodeURIComponent(scope) + ":";
type PersonalViewport = { x: number; y: number; k: number };
const validViewport = (value: unknown): value is PersonalViewport =>
    !!value &&
    typeof value === "object" &&
    Object.entries(value).length === 3 &&
    ["x", "y", "k"].every((key) => typeof (value as Record<string, unknown>)[key] === "number" && Number.isFinite((value as Record<string, number>)[key])) &&
    (value as PersonalViewport).k > 0;
export function createCanvasPersonalViewStorage(storage: CommandJournalStorage, scope: string) {
    if (!scope) throw new Error("canvas_command_journal_scope_missing");
    const key = (canvasId: string) => scopeKey(scope) + "viewport:" + encodeURIComponent(canvasId);
    return {
        async read(canvasId: string): Promise<PersonalViewport | null> {
            const saved = await storage.getItem<{ protocolVersion: number; scope: string; canvasId: string; viewport: PersonalViewport }>(key(canvasId));
            if (saved === null) return null;
            if (saved.protocolVersion !== 1 || saved.scope !== scope || saved.canvasId !== canvasId || !validViewport(saved.viewport)) throw new Error("canvas_viewport_invalid");
            return structuredClone(saved.viewport);
        },
        async write(canvasId: string, viewport: PersonalViewport) {
            if (!validViewport(viewport)) throw new Error("canvas_viewport_invalid");
            await storage.setItem(key(canvasId), { protocolVersion: 1, scope, canvasId, viewport: structuredClone(viewport) });
        },
    };
}
export function createCanvasCommandJournal(storage: CommandJournalStorage, scope: string): CommandJournal {
    if (!scope) throw new Error("canvas_command_journal_scope_missing");
    const prefix = scopeKey(scope);
    return {
        async read(canvasId) {
            const value = await storage.getItem<{ protocolVersion: number; scope: string; canvasId: string; pending: StoredPending[] }>(prefix + "canvas:" + encodeURIComponent(canvasId));
            if (value === null) return [];
            if (value.protocolVersion !== 1 || value.scope !== scope || value.canvasId !== canvasId || !Array.isArray(value.pending) || value.pending.some((p) => !validPending(p))) throw new Error("canvas_command_journal_invalid");
            return structuredClone(value.pending);
        },
        async write(canvasId, pending) {
            await storage.setItem(prefix + "canvas:" + encodeURIComponent(canvasId), { protocolVersion: 1, scope, canvasId, pending: structuredClone(pending) });
        },
    };
}
function validPending(p: StoredPending) {
    return (
        p &&
        typeof p === "object" &&
        typeof p.operationId === "string" &&
        !!p.operationId &&
        typeof p.documentEpoch === "string" &&
        !!p.documentEpoch &&
        Array.isArray(p.operations) &&
        p.preview &&
        Array.isArray(p.preview.entities) &&
        p.expectedEntityVersions &&
        typeof p.expectedEntityVersions === "object" &&
        Array.isArray(p.dependencies) &&
        ["queued", "inflight", "unconfirmed", "conflicted"].includes(p.status)
    );
}
export async function readCanvasClientId(storage: CommandJournalStorage, scope: string): Promise<string> {
    if (!scope) throw new Error("canvas_command_journal_scope_missing");
    const key = scopeKey(scope) + "client";
    const existing = await storage.getItem<string>(key);
    if (existing !== null) {
        if (typeof existing !== "string" || !existing || existing.length > 128) throw new Error("canvas_command_client_invalid");
        return existing;
    }
    const id = crypto.randomUUID();
    await storage.setItem(key, id);
    return id;
}
