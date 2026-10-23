export const CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY = "yingce:pending-canvas-project-asset-links";

export type PendingCanvasProjectAssetLink = {
    assetId: string;
    domainProjectId: string;
    category?: string;
    folderId?: string;
    attempts: number;
    nextAttemptAt: number;
    revision: number;
    blockedReason?: "auth";
};

export type CanvasProjectAssetLinkRequest = Pick<PendingCanvasProjectAssetLink, "assetId" | "domainProjectId" | "category" | "folderId">;

type Storage = {
    getItem(name: string): string | null | Promise<string | null>;
    setItem(name: string, value: string): unknown;
    removeItem(name: string): unknown;
    atomicUpdate?<T>(name: string, change: (raw: string | null) => { value: string | null; result: T }): Promise<T>;
};

function identity(entry: Pick<PendingCanvasProjectAssetLink, "assetId" | "domainProjectId">) {
    return `${entry.domainProjectId}\0${entry.assetId}`;
}

function readEntries(raw: string | null): PendingCanvasProjectAssetLink[] {
    if (!raw) return [];
    let value: unknown;
    try {
        value = JSON.parse(raw);
    } catch {
        throw new Error("项目资产待同步记录已损坏，原记录已保留");
    }
    if (!Array.isArray(value) || value.some((entry) => !validEntry(entry))) throw new Error("项目资产待同步记录格式无效，原记录已保留");
    return value;
}

function validEntry(value: unknown): value is PendingCanvasProjectAssetLink {
    if (!value || typeof value !== "object") return false;
    const entry = value as Partial<PendingCanvasProjectAssetLink>;
    return typeof entry.assetId === "string" && !!entry.assetId &&
        typeof entry.domainProjectId === "string" && !!entry.domainProjectId &&
        (entry.category === undefined || typeof entry.category === "string") &&
        (entry.folderId === undefined || typeof entry.folderId === "string") &&
        (entry.blockedReason === undefined || entry.blockedReason === "auth") &&
        Number.isSafeInteger(entry.attempts) && entry.attempts! >= 0 &&
        Number.isFinite(entry.nextAttemptAt) && Number.isSafeInteger(entry.revision) && entry.revision! > 0;
}

const queueTails = new Map<string, Promise<void>>();

export function createCanvasProjectAssetLinkQueue(storage: Storage, storageKey: string, scope = storageKey) {
    const lockName = `infinite-canvas:project-asset-link-queue:${scope}:${storageKey}`;
    function serialize<T>(operation: () => Promise<T>): Promise<T> {
        const previous = queueTails.get(lockName) || Promise.resolve();
        const result = previous.then(() => undefined, () => undefined).then(operation);
        const tail = result.then(() => undefined, () => undefined);
        queueTails.set(lockName, tail);
        void tail.finally(() => { if (queueTails.get(lockName) === tail) queueTails.delete(lockName); });
        return result;
    }
    const read = async () => readEntries(await storage.getItem(storageKey));
    function mutate<T>(change: (entries: PendingCanvasProjectAssetLink[]) => { result: T; changed: boolean }) {
        return serialize(async () => {
            if (storage.atomicUpdate) return storage.atomicUpdate(storageKey, (raw) => {
                const entries = readEntries(raw);
                const next = change(entries);
                return { value: next.changed ? entries.length ? JSON.stringify(entries) : null : raw, result: next.result };
            });
            const entries = await read();
            const next = change(entries);
            if (next.changed) {
                if (entries.length) await storage.setItem(storageKey, JSON.stringify(entries));
                else await storage.removeItem(storageKey);
            }
            return next.result;
        });
    }
    return {
        list: () => serialize(read),
        upsert: (request: CanvasProjectAssetLinkRequest) => mutate((entries) => {
            if (!request.assetId || !request.domainProjectId) throw new Error("项目资产关联缺少目标标识");
            const index = entries.findIndex((entry) => identity(entry) === identity(request));
            const previous = entries[index];
            const category = request.category === undefined ? previous?.category : request.category;
            const folderId = request.folderId === undefined ? previous?.folderId : request.folderId;
            if (previous && previous.category === category && previous.folderId === folderId) return { result: previous, changed: false };
            const next: PendingCanvasProjectAssetLink = {
                assetId: request.assetId, domainProjectId: request.domainProjectId, category, folderId,
                attempts: 0, nextAttemptAt: 0, revision: (previous?.revision || 0) + 1,
            };
            if (index < 0) entries.push(next);
            else entries[index] = next;
            return { result: next, changed: true };
        }),
        update: (entry: PendingCanvasProjectAssetLink, changes: Pick<PendingCanvasProjectAssetLink, "attempts" | "nextAttemptAt"> & { blockedReason?: "auth" }) => mutate((entries) => {
            const index = entries.findIndex((candidate) => identity(candidate) === identity(entry) && candidate.revision === entry.revision);
            if (index < 0) return { result: undefined, changed: false };
            const next = { ...entries[index], ...changes, blockedReason: changes.blockedReason, revision: entries[index].revision + 1 };
            entries[index] = next;
            return { result: next, changed: true };
        }),
        remove: (entry: PendingCanvasProjectAssetLink) => mutate((entries) => {
            const index = entries.findIndex((candidate) => identity(candidate) === identity(entry) && candidate.revision === entry.revision);
            if (index < 0) return { result: false, changed: false };
            entries.splice(index, 1);
            return { result: true, changed: true };
        }),
        removeAssets: (assetIds: readonly string[]) => mutate((entries) => {
            const ids = new Set(assetIds);
            const remaining = entries.filter((entry) => !ids.has(entry.assetId));
            const changed = remaining.length !== entries.length;
            if (changed) entries.splice(0, entries.length, ...remaining);
            return { result: undefined, changed };
        }),
        removeProject: (domainProjectId: string) => mutate((entries) => {
            const remaining = entries.filter((entry) => entry.domainProjectId !== domainProjectId);
            const changed = remaining.length !== entries.length;
            if (changed) entries.splice(0, entries.length, ...remaining);
            return { result: undefined, changed };
        }),
    };
}
