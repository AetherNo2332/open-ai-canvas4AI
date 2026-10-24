import { expect, test } from "bun:test";
import { indexedDB as testIndexedDB } from "fake-indexeddb";
import { withCanvasJournalLock } from "../src/services/canvas-journal-lock";

test("HTTP IndexedDB fallback holds the lock across async work and releases rejected work", async () => {
    const previous = Object.getOwnPropertyDescriptor(globalThis, "indexedDB");
    Object.defineProperty(globalThis, "indexedDB", { configurable: true, value: testIndexedDB });
    try {
        const order: string[] = [];
        let release!: () => void;
        const gate = new Promise<void>((resolve) => {
            release = resolve;
        });
        let entered!: () => void;
        const started = new Promise<void>((resolve) => {
            entered = resolve;
        });
        const first = withCanvasJournalLock("same-canvas", async () => {
            order.push("first");
            entered();
            await gate;
            order.push("first-done");
        });
        await started;
        const second = withCanvasJournalLock("same-canvas", async () => {
            order.push("second");
        });
        await new Promise((resolve) => setTimeout(resolve, 10));
        expect(order).toEqual(["first"]);
        release();
        await Promise.all([first, second]);
        expect(order).toEqual(["first", "first-done", "second"]);
        await expect(
            withCanvasJournalLock("same-canvas", async () => {
                throw new Error("disk failure");
            }),
        ).rejects.toThrow("disk failure");
        expect(await withCanvasJournalLock("same-canvas", async () => "recovered")).toBe("recovered");
    } finally {
        if (previous) Object.defineProperty(globalThis, "indexedDB", previous);
        else delete (globalThis as { indexedDB?: unknown }).indexedDB;
    }
});
