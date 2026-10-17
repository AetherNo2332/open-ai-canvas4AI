import { expect, test } from "bun:test";
import { createCanvasCommandCreator } from "../src/services/canvas-command-create";
import type { CreateCanvasRequest, Snapshot } from "../src/lib/canvas/command-contract.generated";
const snapshot: Snapshot = { protocolVersion: 1, canvasId: "created", documentEpoch: "epoch", revision: 1, eventSeq: 0, document: { id: "created", title: "Draft", nodes: [], connections: [] }, entityVersions: {} };
test("creation journal preserves original operation across lost ACK and restart", async () => {
    const values = new Map<string, any>();
    const storage = {
        getItem: async <T>(key: string) => structuredClone(values.get(key) ?? null) as T | null,
        setItem: async (key: string, value: any) => {
            values.set(key, structuredClone(value));
        },
    };
    const calls: CreateCanvasRequest[] = [];
    const received: string[] = [];
    const first = createCanvasCommandCreator({
        scope: "host:owner",
        clientId: "client",
        storage,
        create: async (request) => {
            calls.push(request);
            throw new Error("lost ACK");
        },
        onCreated: (s) => {
            received.push(s.canvasId);
        },
    });
    await expect(first.create({ title: "Draft" })).rejects.toThrow("lost ACK");
    first.close();
    const reopened = createCanvasCommandCreator({
        scope: "host:owner",
        clientId: "client",
        storage,
        create: async (request) => {
            calls.push(request);
            return snapshot;
        },
        onCreated: (s) => {
            received.push(s.canvasId);
        },
    });
    await reopened.recover();
    expect(calls).toHaveLength(2);
    expect(calls[1]).toEqual(calls[0]);
    expect(received).toEqual(["created"]);
    await reopened.recover();
    expect(calls).toHaveLength(2);
    reopened.close();
});
test("creation cannot reach host before its durable intent is saved", async () => {
    let calls = 0;
    const creator = createCanvasCommandCreator({
        scope: "host:owner",
        clientId: "client",
        storage: {
            getItem: async () => null,
            setItem: async () => {
                throw new Error("disk full");
            },
        },
        create: async () => {
            calls++;
            return snapshot;
        },
        onCreated: () => {},
    });
    await expect(creator.create({ title: "Draft" })).rejects.toThrow("disk full");
    expect(calls).toBe(0);
    creator.close();
});
