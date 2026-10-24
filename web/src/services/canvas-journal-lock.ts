const tails = new Map<string, Promise<unknown>>();
let database: Promise<IDBDatabase> | undefined;

function indexedDBLock<T>(work: () => Promise<T>): Promise<T> {
    database ??= new Promise<IDBDatabase>((resolve, reject) => {
        const request = indexedDB.open("canvas-command-journal-locks", 1);
        request.onupgradeneeded = () => request.result.createObjectStore("locks");
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => {
            database = undefined;
            reject(request.error);
        };
    });
    return database.then(
        (db) =>
            new Promise<T>((resolve, reject) => {
                // HTTP deployments have IndexedDB but no Web Locks. A read/write
                // transaction serializes windows without an expiring ownership lease.
                const tx = db.transaction("locks", "readwrite");
                let started = false,
                    finished = false;
                let value: T, failure: unknown;
                tx.oncomplete = () => resolve(value);
                tx.onabort = () => reject(failure || tx.error || new Error("canvas_journal_lock_aborted"));
                const keepAlive = () => {
                    const request = tx.objectStore("locks").get("lock");
                    request.onsuccess = () => {
                        if (!started) {
                            started = true;
                            void Promise.resolve()
                                .then(work)
                                .then(
                                    (result) => {
                                        value = result;
                                        finished = true;
                                    },
                                    (error) => {
                                        failure = error;
                                        finished = true;
                                        tx.abort();
                                    },
                                );
                        }
                        if (!finished) keepAlive();
                    };
                };
                keepAlive();
            }),
    );
}

/** Web Locks serialize IndexedDB read/modify/write across browser windows. */
export function withCanvasJournalLock<T>(key: string, work: () => Promise<T>): Promise<T> {
    if (typeof navigator !== "undefined" && navigator.locks) return navigator.locks.request(key, work);
    if (typeof indexedDB !== "undefined") return indexedDBLock(work);
    // Non-browser/native test hosts still need serialization between controllers.
    const task = (tails.get(key) || Promise.resolve()).catch(() => {}).then(work);
    tails.set(key, task);
    void task
        .finally(() => {
            if (tails.get(key) === task) tails.delete(key);
        })
        .catch(() => {});
    return task;
}
