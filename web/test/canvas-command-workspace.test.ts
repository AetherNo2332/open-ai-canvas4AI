import { expect, test } from "bun:test";
import { createCanvasCommandWorkspace } from "../src/services/canvas-command-workspace";
import type { CommandTransport } from "../src/services/canvas-command-session";
test("closing workspace aborts a snapshot that is still opening", async () => {
    let aborted = false;
    const t = {
        snapshot: (_id: string, signal: AbortSignal) =>
            new Promise((_resolve, reject) => {
                signal.addEventListener(
                    "abort",
                    () => {
                        aborted = true;
                        reject(new DOMException("closed", "AbortError"));
                    },
                    { once: true },
                );
            }),
    } as unknown as CommandTransport;
    const workspace = createCanvasCommandWorkspace({ transport: t, clientId: "client" });
    const loading = workspace.open("canvas").catch((e) => e);
    const closing = workspace.close();
    await new Promise((r) => setTimeout(r, 0));
    expect(aborted).toBe(true);
    await closing;
    expect((await loading).name).toBe("AbortError");
});
test("workspace opens one session per canvas and stops all observation on host switch", async () => {
    let snapshots = 0,
        events = 0;
    const scheduled: Array<() => void> = [];
    let cleared = 0;
    const transport = {
        snapshot: async (id: string) => {
            snapshots++;
            return { protocolVersion: 1, canvasId: id, documentEpoch: "e", revision: 0, eventSeq: 0, document: { id, nodes: [], connections: [] }, entityVersions: {} };
        },
        events: async () => {
            events++;
            return [];
        },
        execute: async () => {},
        receipt: async () => {},
    } as unknown as CommandTransport;
    const workspace = createCanvasCommandWorkspace({
        transport,
        clientId: "client",
        schedule: (fn) => {
            scheduled.push(fn);
            return () => cleared++;
        },
    });
    const [one, two] = await Promise.all([workspace.open("one"), workspace.open("one")]);
    expect(one).toBe(two);
    expect(snapshots).toBe(1);
    await workspace.open("two");
    expect(snapshots).toBe(2);
    scheduled[0]();
    await new Promise((r) => setTimeout(r, 0));
    expect(events).toBe(1);
    await workspace.close();
    const seen = events;
    for (const run of scheduled) run();
    await new Promise((r) => setTimeout(r, 0));
    expect(events).toBe(seen);
    expect(one.state.saveState).toBe("closed");
    expect(cleared).toBeGreaterThan(0);
    await expect(workspace.open("three")).rejects.toThrow("canvas_workspace_closed");
});

test("workspace close waits for local journal durability while a network ACK is unknown", async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
        release = resolve;
    });
    let writes = 0,
        network = 0;
    const transport = {
        snapshot: async () => ({ protocolVersion: 1, canvasId: "c", documentEpoch: "e", revision: 0, eventSeq: 0, document: { id: "c", nodes: [], connections: [] }, entityVersions: {} }),
        execute: () => {
            network++;
            return new Promise(() => {});
        },
    } as unknown as CommandTransport;
    const workspace = createCanvasCommandWorkspace({
        transport,
        clientId: "client",
        schedule: () => () => {},
        journal: {
            read: async () => [],
            write: async (_id, pending) => {
                if (pending.length) {
                    writes++;
                    await gate;
                }
            },
        },
    });
    const session = await workspace.open("c");
    void session.submit([{ type: "rename_canvas", id: "c", title: "durable draft" }]).catch(() => {});
    let closed = false;
    const closing = workspace.close().then(() => {
        closed = true;
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(writes).toBeGreaterThan(0);
    expect(network).toBe(0);
    expect(closed).toBe(false);
    release();
    await closing;
    expect(session.state.saveState).toBe("closed");
});
