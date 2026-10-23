import { nanoid } from "nanoid";

export const CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY = "yingce:pending-canvas-project-asset-links";

export type PendingCanvasProjectAssetLink = {
    assetId: string;
    domainProjectId: string;
    category?: string;
    folderId?: string;
    /** Only a newly created local body can be uploaded after an asset404. */
    createAssetIfMissing?: boolean;
    attempts: number;
    nextAttemptAt: number;
    revision: number;
    /** Completion identity survives retries and is never reused after deletion. */
    intentToken?: string;
    blockedReason?: "auth" | "permanent";
    blockedMessage?: string;
};

export type CanvasProjectAssetLinkRequest = Pick<PendingCanvasProjectAssetLink, "assetId" | "domainProjectId" | "category" | "folderId" | "createAssetIfMissing">;

type Storage = {
    getItem(name: string): string | null | Promise<string | null>;
    setItem(name: string, value: string): unknown;
    removeItem(name: string): unknown;
    atomicUpdate?<T>(name: string, change: (raw: string | null) => { value: string | null; result: T }): Promise<T>;
};

function identity(entry: Pick<PendingCanvasProjectAssetLink, "assetId" | "domainProjectId">) {
    return `${entry.domainProjectId}\0${entry.assetId}`;
}

type QueueDocument = { version: 1; entries: PendingCanvasProjectAssetLink[]; confirmedAssetIds: string[] };

function readDocument(raw: string | null): QueueDocument {
    if (!raw) return { version: 1, entries: [], confirmedAssetIds: [] };
    let value: unknown;
    try {
        value = JSON.parse(raw);
    } catch {
        throw new Error("项目资产待同步记录已损坏，原记录已保留");
    }
    if (Array.isArray(value)) {
        if (value.some((entry) => !validEntry(entry))) throw new Error("项目资产待同步记录格式无效，原记录已保留");
        // Old arrays cannot distinguish an acknowledged body from a conservative
        // sealed permission. Preserve that denial across deletion/recreation.
        return { version: 1, entries: value, confirmedAssetIds: [...new Set(value.filter((entry) => entry.createAssetIfMissing === false).map((entry) => entry.assetId))] };
    }
    const document = value as Partial<QueueDocument> | null;
    if (!document || document.version !== 1 || !Array.isArray(document.entries) || document.entries.some((entry) => !validEntry(entry)) || !Array.isArray(document.confirmedAssetIds) || document.confirmedAssetIds.some((id) => typeof id !== "string" || !id)) throw new Error("项目资产待同步记录格式无效，原记录已保留");
    const confirmed = new Set(document.confirmedAssetIds);
    return { version: 1, entries: document.entries.map((entry) => confirmed.has(entry.assetId) ? { ...entry, createAssetIfMissing: false } : entry), confirmedAssetIds: [...confirmed] };
}

function validEntry(value: unknown): value is PendingCanvasProjectAssetLink {
    if (!value || typeof value !== "object") return false;
    const entry = value as Partial<PendingCanvasProjectAssetLink>;
    return typeof entry.assetId === "string" && !!entry.assetId &&
        typeof entry.domainProjectId === "string" && !!entry.domainProjectId &&
        (entry.category === undefined || typeof entry.category === "string") &&
        (entry.folderId === undefined || typeof entry.folderId === "string") &&
        (entry.createAssetIfMissing === undefined || typeof entry.createAssetIfMissing === "boolean") &&
        (entry.intentToken === undefined || typeof entry.intentToken === "string" && !!entry.intentToken) &&
        (entry.blockedReason === undefined || entry.blockedReason === "auth" || entry.blockedReason === "permanent") &&
        (entry.blockedMessage === undefined || typeof entry.blockedMessage === "string") &&
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
    const read = async () => readDocument(await storage.getItem(storageKey));
    const encode = (document: QueueDocument) => document.entries.length || document.confirmedAssetIds.length ? JSON.stringify(document) : null;
    function mutate<T>(change: (entries: PendingCanvasProjectAssetLink[], confirmedAssetIds: string[]) => { result: T; changed: boolean }) {
        return serialize(async () => {
            if (storage.atomicUpdate) return storage.atomicUpdate(storageKey, (raw) => {
                const document = readDocument(raw);
                const next = change(document.entries, document.confirmedAssetIds);
                return { value: next.changed ? encode(document) : raw, result: next.result };
            });
            const document = await read();
            const next = change(document.entries, document.confirmedAssetIds);
            if (next.changed) {
                const value = encode(document);
                if (value !== null) await storage.setItem(storageKey, value);
                else await storage.removeItem(storageKey);
            }
            return next.result;
        });
    }
    return {
        list: () => serialize(async () => (await read()).entries),
        upsert: (request: CanvasProjectAssetLinkRequest, options: { retryBlocked?: boolean } = {}) => mutate((entries, confirmedAssetIds) => {
            if (!request.assetId || !request.domainProjectId) throw new Error("项目资产关联缺少目标标识");
            const index = entries.findIndex((entry) => identity(entry) === identity(request));
            const previous = entries[index];
            const category = request.category === undefined ? previous?.category : request.category;
            const folderId = request.folderId === undefined ? previous?.folderId : request.folderId;
            if (previous && previous.category === category && previous.folderId === folderId && !(options.retryBlocked && (previous.blockedReason || previous.nextAttemptAt === Number.MAX_SAFE_INTEGER))) return { result: previous, changed: false };
            const next: PendingCanvasProjectAssetLink = {
                assetId: request.assetId, domainProjectId: request.domainProjectId, category, folderId,
                createAssetIfMissing: !confirmedAssetIds.includes(request.assetId) && (previous ? previous.createAssetIfMissing === true : request.createAssetIfMissing === true),
                attempts: 0, nextAttemptAt: 0, revision: (previous?.revision || 0) + 1, intentToken: nanoid(),
            };
            if (index < 0) entries.push(next);
            else entries[index] = next;
            return { result: next, changed: true };
        }),
        markAssetPersisted: (entry: Pick<PendingCanvasProjectAssetLink, "assetId">) => mutate((entries, confirmedAssetIds) => {
            let changed = !confirmedAssetIds.includes(entry.assetId);
            if (changed) confirmedAssetIds.push(entry.assetId);
            for (const current of entries) {
                if (current.assetId !== entry.assetId || current.createAssetIfMissing === false) continue;
                current.createAssetIfMissing = false;
                changed = true;
            }
            // Asset existence is a monotonic account+asset lifecycle fact. It
            // cannot inherit the folder/category job's replaceable token.
            return { result: undefined, changed };
        }),
        update: (entry: PendingCanvasProjectAssetLink, changes: Pick<PendingCanvasProjectAssetLink, "attempts" | "nextAttemptAt"> & Pick<PendingCanvasProjectAssetLink, "blockedReason" | "blockedMessage">) => mutate((entries) => {
            const index = entries.findIndex((candidate) => identity(candidate) === identity(entry) && candidate.intentToken === entry.intentToken && candidate.revision === entry.revision);
            if (index < 0) return { result: undefined, changed: false };
            const next = { ...entries[index], ...changes, blockedReason: changes.blockedReason, blockedMessage: changes.blockedMessage, revision: entries[index].revision + 1 };
            entries[index] = next;
            return { result: next, changed: true };
        }),
        remove: (entry: PendingCanvasProjectAssetLink) => mutate((entries) => {
            const index = entries.findIndex((candidate) => identity(candidate) === identity(entry) && candidate.intentToken === entry.intentToken && candidate.revision === entry.revision);
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
