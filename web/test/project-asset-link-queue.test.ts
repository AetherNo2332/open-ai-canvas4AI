import { describe, expect, test } from "bun:test";

import { createCanvasProjectAssetLinkQueue } from "@/lib/canvas/project-asset-link-queue";

function memoryStorage() {
    const values = new Map<string, string>();
    return {
        values,
        getItem: async (key: string) => values.get(key) ?? null,
        setItem: async (key: string, value: string) => { values.set(key, value); },
        removeItem: async (key: string) => { values.delete(key); },
    };
}

describe("project asset link journal", () => {
    test("independent tab adapters preserve concurrent intents in a shared atomic store", async () => {
        const values = new Map<string, string>();
        let transactionTail = Promise.resolve();
        let transactions = 0;
        const adapter = () => ({
            getItem: async (key: string) => values.get(key) ?? null,
            setItem: async (key: string, value: string) => { values.set(key, value); },
            removeItem: async (key: string) => { values.delete(key); },
            atomicUpdate: async <T>(key: string, change: (raw: string | null) => { value: string | null; result: T }) => {
                const previous = transactionTail;
                let release!: () => void;
                transactionTail = new Promise<void>((resolve) => { release = resolve; });
                await previous;
                try {
                    transactions += 1;
                    const next = change(values.get(key) ?? null);
                    await new Promise((resolve) => setTimeout(resolve, 1));
                    if (next.value === null) values.delete(key);
                    else values.set(key, next.value);
                    return next.result;
                } finally { release(); }
            },
        });
        // Distinct tail scopes simulate separate JS realms; only the shared transaction protects the record.
        const first = createCanvasProjectAssetLinkQueue(adapter(), "pending-links", "realm-a");
        const second = createCanvasProjectAssetLinkQueue(adapter(), "pending-links", "realm-b");
        await Promise.all([
            first.upsert({ assetId: "asset-a", domainProjectId: "project" }),
            second.upsert({ assetId: "asset-b", domainProjectId: "project" }),
        ]);
        expect((await first.list()).map((entry) => entry.assetId).sort()).toEqual(["asset-a", "asset-b"]);
        expect(transactions).toBe(2);
    });

    test("reloads an interrupted category and folder intent from durable storage", async () => {
        const storage = memoryStorage();
        const first = createCanvasProjectAssetLinkQueue(storage, "user-a");
        const persisted = await first.upsert({ assetId: "asset", domainProjectId: "project", category: "character", folderId: "folder" });
        const restored = createCanvasProjectAssetLinkQueue(storage, "user-a");
        expect(await restored.list()).toEqual([{
            assetId: "asset", domainProjectId: "project", category: "character", folderId: "folder",
            attempts: 0, nextAttemptAt: 0, revision: 1, intentToken: persisted.intentToken, createAssetIfMissing: false,
        }]);
    });

    test("coalesces repeated intent and rejects stale completion or backoff", async () => {
        const queue = createCanvasProjectAssetLinkQueue(memoryStorage(), "user-a");
        const first = await queue.upsert({ assetId: "asset", domainProjectId: "project", category: "character" });
        expect(await queue.upsert({ assetId: "asset", domainProjectId: "project", category: "character" })).toEqual(first);
        const moved = await queue.upsert({ assetId: "asset", domainProjectId: "project", folderId: "new-folder" });
        expect(moved.category).toBe("character");
        expect(moved.revision).toBe(2);
        expect(await queue.remove(first)).toBe(false);
        expect(await queue.update(first, { attempts: 1, nextAttemptAt: 123 })).toBeUndefined();
        expect(await queue.list()).toEqual([moved]);
    });

    test("an old completion or failure cannot affect a removed and recreated intent", async () => {
        const queue = createCanvasProjectAssetLinkQueue(memoryStorage(), "recreated");
        const old = await queue.upsert({ assetId: "asset", domainProjectId: "project", folderId: "old" });
        await queue.remove(old);
        const current = await queue.upsert({ assetId: "asset", domainProjectId: "project", folderId: "new" });

        expect(await queue.remove(old)).toBe(false);
        expect(await queue.update(old, { attempts: 5, nextAttemptAt: Number.MAX_SAFE_INTEGER })).toBeUndefined();
        expect(await queue.list()).toEqual([current]);
    });

    test("a blocked target stays blocked until the intent changes", async () => {
        const queue = createCanvasProjectAssetLinkQueue(memoryStorage(), "user-a");
        const first = await queue.upsert({ assetId: "asset", domainProjectId: "project" });
        const blocked = await queue.update(first, { attempts: 1, nextAttemptAt: Number.MAX_SAFE_INTEGER });
        expect(await queue.upsert({ assetId: "asset", domainProjectId: "project" })).toEqual(blocked);
        const manuallyRetried = await queue.upsert({ assetId: "asset", domainProjectId: "project" }, { retryBlocked: true });
        expect(manuallyRetried.nextAttemptAt).toBe(0);
        const changed = await queue.upsert({ assetId: "asset", domainProjectId: "project", category: "prop" });
        expect(changed.nextAttemptAt).toBe(0);
    });

    test("asset deletion removes all pending links without affecting another user's scope", async () => {
        const storage = memoryStorage();
        const alice = createCanvasProjectAssetLinkQueue(storage, "user-a");
        const bob = createCanvasProjectAssetLinkQueue(storage, "user-b");
        await alice.upsert({ assetId: "asset", domainProjectId: "one" });
        await alice.upsert({ assetId: "asset", domainProjectId: "two" });
        await bob.upsert({ assetId: "asset", domainProjectId: "one" });
        await alice.removeAssets(["asset"]);
        expect(await alice.list()).toEqual([]);
        expect(await bob.list()).toHaveLength(1);
    });

    test("keeps a corrupt journal intact for recovery", async () => {
        const storage = memoryStorage();
        storage.values.set("user-a", "broken-json");
        const queue = createCanvasProjectAssetLinkQueue(storage, "user-a");
        await expect(queue.upsert({ assetId: "asset", domainProjectId: "project" })).rejects.toThrow("项目资产待同步记录已损坏");
        expect(storage.values.get("user-a")).toBe("broken-json");
    });
});
