import { expect, test } from "bun:test";
import { createCanvasCommandJournal, readCanvasClientId, createCanvasPersonalViewStorage } from "../src/services/canvas-command-journal";
test("command journals and stable client identities are isolated by captured host scope", async () => {
    const values = new Map<string, unknown>();
    const kv = {
        getItem: async <T>(key: string) => (values.get(key) ?? null) as T | null,
        setItem: async <T>(key: string, value: T) => {
            values.set(key, structuredClone(value));
            return value;
        },
    };
    const a = createCanvasCommandJournal(kv, "host-a:owner"),
        b = createCanvasCommandJournal(kv, "host-b:guest");
    const pending = [{ operationId: "op", documentEpoch: "epoch", operations: [], preview: { entities: [] }, expectedEntityVersions: {}, dependencies: [], status: "unconfirmed" as const }];
    await a.write("canvas", pending);
    expect(await b.read("canvas")).toEqual([]);
    expect(await a.read("canvas")).toEqual(pending);
    const id = await readCanvasClientId(kv, "host-a:owner");
    expect(await readCanvasClientId(kv, "host-a:owner")).toBe(id);
    expect(await readCanvasClientId(kv, "host-b:guest")).not.toBe(id);
    await b.write("canvas", []);
    expect(await a.read("canvas")).toEqual(pending);
});
test("corrupt durable journal is never silently discarded", async () => {
    const kv = { getItem: async () => ({ protocolVersion: 99 }), setItem: async () => {} };
    await expect(createCanvasCommandJournal(kv as any, "scope").read("canvas")).rejects.toThrow("canvas_command_journal_invalid");
});
test("personal viewport survives local host port changes and stays outside command journals", async () => {
    const data = new Map<string, unknown>();
    const storage = {
        getItem: async <T>(key: string) => (data.get(key) ?? null) as T | null,
        setItem: async <T>(key: string, value: T) => {
            data.set(key, structuredClone(value));
        },
    };
    const first = createCanvasPersonalViewStorage(storage, "profile:owner"),
        reopened = createCanvasPersonalViewStorage(storage, "profile:owner"),
        guest = createCanvasPersonalViewStorage(storage, "profile:guest");
    await first.write("canvas", { x: 32, y: -21, k: 0.5 });
    expect(await reopened.read("canvas")).toEqual({ x: 32, y: -21, k: 0.5 });
    expect(await guest.read("canvas")).toBeNull();
    expect(await createCanvasCommandJournal(storage, "profile:owner").read("canvas")).toEqual([]);
    await expect(first.write("canvas", { x: 0, y: 0, k: NaN })).rejects.toThrow("canvas_viewport_invalid");
    const key = [...data.keys()][0];
    data.set(key, { protocolVersion: 1, scope: "other", canvasId: "canvas", viewport: { x: 0, y: 0, k: 1 } });
    await expect(reopened.read("canvas")).rejects.toThrow("canvas_viewport_invalid");
});
