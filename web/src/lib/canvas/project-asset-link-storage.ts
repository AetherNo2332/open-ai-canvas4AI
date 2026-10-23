import { openDB, type IDBPDatabase } from "idb";

import { localForageStorageForScope } from "@/lib/localforage-storage";
import { scopedStorageKey } from "@/lib/user-scope";

const databaseName = "infinite-canvas-project-asset-links";
const storeName = "queues";
let connection: Promise<IDBPDatabase> | null = null;

function queueDatabase() {
    if (typeof indexedDB === "undefined") throw new Error("浏览器存储不可用，项目关联待办未持久化；本地素材已保留，请恢复存储后重试");
    if (!connection) {
        connection = openDB(databaseName, 1, {
            upgrade(db) {
                db.createObjectStore(storeName);
            },
        }).catch((error) => {
            connection = null;
            throw error;
        });
    }
    return connection;
}

/** A readwrite IndexedDB transaction serializes queue mutations across tabs on HTTP LAN too. */
export function projectAssetLinkStorageForScope(scope: string) {
    // Non-browser callers use the existing scoped storage adapter for isolated tests.
    if (typeof document === "undefined") return localForageStorageForScope(scope);
    const keyFor = (name: string) => scopedStorageKey(name, scope);
    return {
        async getItem(name: string) {
            const db = await queueDatabase();
            return ((await db.get(storeName, keyFor(name))) as string | undefined) ?? null;
        },
        async setItem(name: string, value: string) {
            const db = await queueDatabase();
            await db.put(storeName, value, keyFor(name));
        },
        async removeItem(name: string) {
            const db = await queueDatabase();
            await db.delete(storeName, keyFor(name));
        },
        async atomicUpdate<T>(name: string, change: (raw: string | null) => { value: string | null; result: T }): Promise<T> {
            const db = await queueDatabase();
            const transaction = db.transaction(storeName, "readwrite");
            try {
                const raw = ((await transaction.store.get(keyFor(name))) as string | undefined) ?? null;
                const next = change(raw);
                if (next.value === null) await transaction.store.delete(keyFor(name));
                else if (next.value !== raw) await transaction.store.put(next.value, keyFor(name));
                await transaction.done;
                return next.result;
            } catch (error) {
                try {
                    transaction.abort();
                } catch {
                    /* Transaction may already be closed. */
                }
                throw error;
            }
        },
    };
}
