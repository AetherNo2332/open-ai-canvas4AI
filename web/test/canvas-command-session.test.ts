import { expect, test } from "bun:test";
import { CanvasCommandSession, type CommandTransport } from "../src/services/canvas-command-session";
import type { Snapshot, Receipt, Command } from "../src/lib/canvas/command-contract.generated";
const snapshot = (): Snapshot => ({
    protocolVersion: 1,
    canvasId: "canvas",
    documentEpoch: "epoch",
    revision: 1,
    eventSeq: 0,
    document: {
        id: "canvas",
        nodes: [
            { id: "a", type: "text", position: { x: 0, y: 0 }, metadata: { content: "before", extension: "keep" } },
            { id: "b", type: "text", metadata: { content: "B" } },
        ],
        connections: [],
    },
    entityVersions: { "node:a": 1, "node:b": 1 },
});
const receipt = (seq: number, node = "a", content = "remote", op = "remote-op"): Receipt => ({
    protocolVersion: 1,
    canvasId: "canvas",
    documentEpoch: "epoch",
    clientId: "remote",
    actorId: "remote",
    operationId: op,
    revision: seq + 1,
    eventSeq: seq,
    changes: { entities: [{ key: "node:" + node, value: { id: node, type: "text", position: { x: 0, y: 0 }, metadata: { content, extension: "keep" } } }] },
    entityVersions: { ["node:" + node]: seq + 1 },
});
function transport(overrides: Partial<CommandTransport> = {}): CommandTransport {
    return {
        snapshot: async () => snapshot(),
        events: async () => [],
        execute: async (req) => ({ ...receipt(1, "a", "draft", req.operationId), clientId: req.clientId, actorId: "owner" }),
        receipt: async () => {
            throw Object.assign(new Error("missing"), { status: 404 });
        },
        ...overrides,
    };
}
const tick = () => new Promise((r) => setTimeout(r, 0));

test("field baseline wire survives lost ACK, remote sync and journal reload byte-for-byte", async () => {
    let stored: any[] = [],
        attempts: string[] = [],
        recovered: string[] = [];
    const journal = {
        read: async () => structuredClone(stored),
        write: async (_id: string, pending: any[]) => {
            stored = structuredClone(pending);
        },
    };
    const operation = { type: "update_node", id: "a", patch: { content: "local" }, fieldBaselines: { nodeType: "text", generation: 0, fields: { content: { exists: true, value: "before" } } } };
    const first = new CanvasCommandSession(
        transport({
            snapshot: async () => ({ ...snapshot(), entityGenerations: { "node:a": 0 } }),
            execute: async (command) => {
                attempts.push(JSON.stringify(command));
                throw new Error("lost ACK");
            },
        }),
        "client",
        journal,
    );
    await first.open("canvas");
    const pending = first.submit([operation]).catch((error) => error);
    operation.fieldBaselines.fields.content.value = "mutated caller";
    await tick();
    await first.whenJournalDurable();
    expect(stored[0].wire.operations[0].fieldBaselines.fields.content.value).toBe("before");
    first.close();
    await pending;
    const remote = receipt(1, "b", "remote B");
    const restarted = new CanvasCommandSession(
        transport({
            snapshot: async () => ({ ...snapshot(), entityGenerations: { "node:a": 99 } }),
            events: async () => [remote],
            receipt: async (command) => {
                recovered.push(JSON.stringify(command));
                throw { status: 404 };
            },
            execute: async (command) => {
                attempts.push(JSON.stringify(command));
                return { ...receipt(2, "a", "local", command.operationId), clientId: "client", entityGenerations: { "node:a": 0 } };
            },
        }),
        "client",
        journal,
    );
    await restarted.open("canvas");
    await restarted.persistJournal();
    expect(attempts).toHaveLength(2);
    expect(attempts[1]).toBe(attempts[0]);
    expect(recovered[0]).toBe(attempts[0]);
    expect(stored).toEqual([]);
    restarted.close();
});

test("field baseline queued dependency advances only version while retaining semantic observation", async () => {
    let finish!: (event: Receipt) => void;
    const sent: Command[] = [];
    const s = new CanvasCommandSession(
        transport({
            snapshot: async () => ({ ...snapshot(), entityGenerations: { "node:a": 0 } }),
            execute: async (command) => {
                sent.push(structuredClone(command));
                if (sent.length === 1)
                    return new Promise((resolve) => {
                        finish = resolve;
                    });
                return { ...receipt(2, "a", "second", command.operationId), clientId: "client", entityGenerations: { "node:a": 0 } };
            },
        }),
        "client",
    );
    await s.open("canvas");
    const one = s.submit([{ type: "update_node", id: "a", patch: { content: "first" }, fieldBaselines: { nodeType: "text", generation: 0, fields: { content: { exists: true, value: "before" } } } }]);
    const two = s.submit([{ type: "update_node", id: "a", patch: { content: "second" }, fieldBaselines: { nodeType: "text", generation: 0, fields: { content: { exists: true, value: "first" } } } }]);
    await tick();
    finish({ ...receipt(1, "a", "first", sent[0].operationId), clientId: "client", entityGenerations: { "node:a": 0 } });
    await Promise.all([one, two]);
    expect(sent[1].expectedEntityVersions?.["node:a"]).toBe(2);
    expect(sent[1].operations[0].fieldBaselines).toEqual({ nodeType: "text", generation: 0, fields: { content: { exists: true, value: "first" } } });
    s.close();
});

test("journal durability awaits queued storage without waiting for an outstanding network write", async () => {
    let release!: () => void,
        finish!: (event: Receipt) => void,
        blocked = false,
        writes = 0;
    const session = new CanvasCommandSession(
        transport({
            execute: async () =>
                new Promise((resolve) => {
                    finish = resolve;
                }),
        }),
        "client",
        {
            read: async () => [],
            write: async () => {
                writes++;
                if (blocked)
                    await new Promise<void>((resolve) => {
                        release = resolve;
                    });
            },
        },
    );
    await session.open("canvas");
    await session.persistJournal();
    blocked = true;
    const pending = session.submit([{ type: "update_node", id: "a", patch: { content: "local" } }]).catch((error) => error);
    let durable = false;
    const wait = session.whenJournalDurable().then(() => {
        durable = true;
    });
    await tick();
    expect(durable).toBe(false);
    blocked = false;
    release();
    await wait;
    await tick();
    expect(writes).toBeGreaterThan(1);
    expect(typeof finish).toBe("function");
    expect(session.state.pending[0].status).toBe("inflight");
    session.close();
    await pending;
});

test("own restore settles before adopting the new epoch and recovers after restart", async () => {
    const reset = { ...snapshot(), documentEpoch: "restored", revision: 2, eventSeq: 1 };
    let saved: any[] = [];
    let executed: Command | undefined;
    const makeReceipt = (command: Command): Receipt => ({ ...receipt(1), documentEpoch: "restored", clientId: command.clientId, operationId: command.operationId, reset });
    const t = transport({
        invoke: async (command) => {
            executed = command;
            return makeReceipt(command);
        },
        recover: async (command) => makeReceipt(command),
    });
    const s = new CanvasCommandSession(t, "client", {
        read: async () => saved,
        write: async (_, pending) => {
            saved = structuredClone(pending);
        },
    });
    await s.open("canvas");
    await s.restoreVersion(snapshot().document, 1);
    expect(s.state.acknowledged?.documentEpoch).toBe("restored");
    expect(s.state.conflicts).toEqual([]);
    expect(s.state.pending).toEqual([]);
    s.close();
    saved = [
        {
            operationId: executed!.operationId,
            documentEpoch: "epoch",
            operations: [],
            effect: { type: "restore", document: snapshot().document, expectedRevision: 1 },
            preview: { entities: [] },
            expectedEntityVersions: {},
            dependencies: [],
            baseEntities: {},
            baseFields: {},
            status: "unconfirmed",
            wire: executed,
        },
    ];
    const recovered = new CanvasCommandSession({ ...t, snapshot: async () => reset }, "client", {
        read: async () => saved,
        write: async (_, pending) => {
            saved = structuredClone(pending);
        },
    });
    await recovered.open("canvas");
    expect(recovered.state.pending).toEqual([]);
    expect(recovered.state.conflicts).toEqual([]);
    recovered.close();
});
test("task effects without an available transport reject before creating a pending draft", async () => {
    const s = new CanvasCommandSession(transport(), "client");
    await s.open("canvas");
    const result = s.submitTask({ targetNodeId: "a", task: { mode: "text", prompt: "x" } }).catch((e) => e);
    await tick();
    s.close();
    expect((await result).message).toBe("canvas_effect_transport_unavailable");
    expect(s.state.pending).toEqual([]);
    s.close();
});
test("restored journal cannot resend another canvas or client command", async () => {
    let writes = 0;
    const t = transport({
        execute: async () => {
            writes++;
            return receipt(1);
        },
    });
    const wrong = {
        operationId: "op",
        documentEpoch: "epoch",
        operations: [],
        preview: { entities: [] },
        expectedEntityVersions: {},
        dependencies: [],
        status: "unconfirmed",
        wire: { protocolVersion: 1, canvasId: "other", documentEpoch: "epoch", clientId: "other-client", operationId: "op", baseRevision: 1, operations: [] },
    };
    const s = new CanvasCommandSession(t, "client", { read: async () => [wrong as any], write: async () => {} });
    await expect(s.open("canvas")).rejects.toThrow("canvas_command_journal_scope_mismatch");
    expect(writes).toBe(0);
    s.close();
});
test("structured task optimism updates only its bound row and clears old task identity", async () => {
    const doc = snapshot();
    doc.document.nodes = [
        {
            id: "board",
            type: "batch-table",
            metadata: {
                batchTable: {
                    rows: [
                        { id: "row-a", taskId: "old", status: "failed", prompt: "A" },
                        { id: "row-b", status: "idle", prompt: "B" },
                    ],
                },
            },
        },
    ];
    const s = new CanvasCommandSession(
        transport({
            snapshot: async () => doc,
            invoke: async () => {
                throw new Error("offline");
            },
            recover: async () => {
                throw new Error("offline");
            },
        }),
        "client",
    );
    await s.open("canvas");
    const done = s.submitTask({ targetNodeId: "board", consumer: { kind: "batch", rowId: "row-a", outputNodeId: "output" }, task: { mode: "image", prompt: "A" } }).catch((e) => e);
    await tick();
    const meta = (s.state.projected.nodes as any[])[0].metadata;
    expect(meta.status).toBeUndefined();
    expect(meta.batchTable.rows[0].status).toBe("loading");
    expect(meta.batchTable.rows[0].taskId).toBeUndefined();
    expect(meta.batchTable.rows[1].status).toBe("idle");
    s.close();
    await done;
});
test("duplicate event ignored and cursor gap fetched before applying later event", async () => {
    let reads: number[] = [];
    const s = new CanvasCommandSession(
        transport({
            events: async (_id, after) => {
                reads.push(after);
                return [receipt(1, "a", "first"), receipt(2, "b", "second")];
            },
        }),
        "client",
    );
    await s.open("canvas");
    await s.receive(receipt(2, "b", "second"));
    await s.receive(receipt(1, "a", "first"));
    expect(reads).toEqual([0]);
    expect(s.state.acknowledged?.eventSeq).toBe(2);
    expect((s.state.projected.nodes as any[])[0].metadata.content).toBe("first");
    expect((s.state.projected.nodes as any[])[1].metadata.content).toBe("second");
    s.close();
});
test("remote changes to another node reproject pending edit; same-node conflict retains draft", async () => {
    let reject!: (e: unknown) => void;
    const s = new CanvasCommandSession(transport({ execute: () => new Promise((_, r) => (reject = r)) }), "client");
    await s.open("canvas");
    const done = s.submit([{ type: "update_node", id: "a", patch: { content: "my draft" } }]).catch((e) => e);
    await tick();
    await s.receive(receipt(1, "b", "other edit"));
    expect((s.state.projected.nodes as any[])[0].metadata.content).toBe("my draft");
    expect((s.state.projected.nodes as any[])[1].metadata.content).toBe("other edit");
    expect(s.state.conflicts.length).toBe(0);
    await s.receive(receipt(2, "a", "foreign draft"));
    reject(Object.assign(new Error("conflict"), { status: 409, reason: "entity_version_conflict" }));
    await done;
    expect(s.state.conflicts.length).toBe(1);
    expect((s.state.projected.nodes as any[])[0].metadata.content).toBe("my draft");
    expect((s.state.acknowledged?.document.nodes as any[])[0].metadata.content).toBe("foreign draft");
    s.close();
});
test("lost ACK recovers original receipt without repeating write", async () => {
    let persisted!: Receipt;
    let calls = 0;
    const s = new CanvasCommandSession(
        transport({
            execute: async (req) => {
                calls++;
                persisted = { ...receipt(1, "a", "draft", req.operationId), clientId: req.clientId, actorId: "owner" };
                throw new Error("lost ACK");
            },
            receipt: async () => persisted,
        }),
        "client",
    );
    await s.open("canvas");
    const done = s.submit([{ type: "update_node", id: "a", patch: { content: "draft" } }]);
    await tick();
    expect(s.state.saveState).toBe("offline");
    const id = s.state.pending[0].operationId;
    await s.reconnect();
    const out = await done;
    expect(out.operationId).toBe(id);
    expect(calls).toBe(1);
    expect(s.state.pending.length).toBe(0);
    expect(s.state.saveState).toBe("saved");
    s.close();
});
test("retry after absent receipt uses identical command and operation id", async () => {
    const bodies: Command[] = [];
    const s = new CanvasCommandSession(
        transport({
            execute: async (req) => {
                bodies.push(structuredClone(req));
                if (bodies.length === 1) throw new Error("disconnect");
                return { ...receipt(1, "a", "draft", req.operationId), clientId: req.clientId, actorId: "owner" };
            },
        }),
        "client",
    );
    await s.open("canvas");
    const done = s.submit([{ type: "update_node", id: "a", patch: { content: "draft" } }]);
    await tick();
    await s.reconnect();
    await done;
    expect(bodies.length).toBe(2);
    expect(bodies[0]).toEqual(bodies[1]);
    s.close();
});
test("epoch reset preserves old draft as conflict and never resubmits into new epoch", async () => {
    let calls = 0;
    const s = new CanvasCommandSession(
        transport({
            execute: async () => {
                calls++;
                throw new Error("offline");
            },
        }),
        "client",
    );
    await s.open("canvas");
    const done = s.submit([{ type: "update_node", id: "a", patch: { content: "old draft" } }]).catch((e) => e);
    await tick();
    const reset = { ...snapshot(), documentEpoch: "new-epoch", revision: 4, eventSeq: 1 };
    await s.receive({ ...receipt(1), documentEpoch: "new-epoch", reset });
    await s.reconnect();
    expect(s.state.conflicts[0].reason).toBe("document_epoch_mismatch");
    expect(calls).toBe(1);
    expect(s.state.pending[0].documentEpoch).toBe("epoch");
    s.close();
    await done;
});
test("close and host scope switch ignore late response", async () => {
    let finish!: (r: Receipt) => void;
    let request!: Command;
    const s = new CanvasCommandSession(
        transport({
            execute: (req) => {
                request = req;
                return new Promise((r) => (finish = r));
            },
        }),
        "client",
    );
    await s.open("canvas");
    const done = s.submit([{ type: "update_node", id: "a", patch: { content: "old host" } }]).catch((e) => e);
    await tick();
    s.close();
    finish({ ...receipt(1, "a", "old host", request.operationId), clientId: "client" });
    await done;
    await tick();
    expect(s.state.acknowledged).toBeNull();
    expect(s.state.pending).toEqual([]);
});
test("pending text keeps task result metadata and another new node while retaining the draft", async () => {
    const s = new CanvasCommandSession(
        transport({
            execute: async () => {
                throw new Error("offline");
            },
        }),
        "client",
    );
    await s.open("canvas");
    const done = s.submit([{ type: "update_node", id: "a", patch: { content: "my draft" } }]).catch((e) => e);
    await tick();
    const result = receipt(1, "a", "generated");
    result.changes.entities[0].value!.metadata = { content: "generated", extension: "keep", taskId: "host-task", taskStatus: "succeeded" };
    await s.receive(result);
    const node = (s.state.projected.nodes as any[])[0];
    expect(node.metadata.content).toBe("my draft");
    expect(node.metadata.taskId).toBe("host-task");
    expect(node.metadata.taskStatus).toBe("succeeded");
    s.close();
    await done;
    const add = new CanvasCommandSession(
        transport({
            execute: async () => {
                throw new Error("offline");
            },
        }),
        "client",
    );
    await add.open("canvas");
    const adding = add.submit([{ type: "add_node", id: "local-new", nodeType: "text" }]).catch((e) => e);
    await tick();
    const remote = receipt(1, "remote-new", "remote");
    remote.changes.nodeOrder = ["a", "b", "remote-new"];
    await add.receive(remote);
    expect((add.state.projected.nodes as any[]).map((n) => n.id).sort()).toEqual(["a", "b", "local-new", "remote-new"]);
    add.close();
    await adding;
});
test("queued edit after own ACK advances preconditions, without treating it as another actor", async () => {
    let first!: (r: Receipt) => void;
    const sent: Command[] = [];
    const s = new CanvasCommandSession(
        transport({
            execute: async (req) => {
                sent.push(req);
                if (sent.length === 1) return new Promise((r) => (first = r));
                return { ...receipt(2, "a", "second", req.operationId), clientId: "client", actorId: "owner" };
            },
        }),
        "client",
    );
    await s.open("canvas");
    const one = s.submit([{ type: "update_node", id: "a", patch: { content: "first" } }]);
    const two = s.submit([{ type: "update_node", id: "a", patch: { content: "second" } }]);
    await tick();
    first({ ...receipt(1, "a", "first", sent[0].operationId), clientId: "client", actorId: "owner" });
    await Promise.all([one, two]);
    expect(sent[1].expectedEntityVersions!["node:a"]).toBe(2);
    expect(s.state.conflicts).toEqual([]);
    expect((s.state.projected.nodes as any[])[0].metadata.content).toBe("second");
    s.close();
});
test("persistent journal queries an already committed receipt after renderer restart", async () => {
    let stored: any[] = [];
    let persisted!: Receipt;
    let calls = 0;
    const journal = {
        read: async () => structuredClone(stored),
        write: async (_id: string, pending: any[]) => {
            stored = structuredClone(pending);
        },
    };
    const t = transport({
        execute: async (req) => {
            calls++;
            persisted = { ...receipt(1, "a", "durable", req.operationId), clientId: "client", actorId: "owner" };
            throw new Error("lost ACK");
        },
        receipt: async () => persisted,
    });
    const s = new CanvasCommandSession(t, "client", journal);
    await s.open("canvas");
    const done = s.submit([{ type: "update_node", id: "a", patch: { content: "durable" } }]).catch((e) => e);
    await tick();
    await s.flush();
    s.close();
    await done;
    expect(stored.length).toBe(1);
    expect(stored[0].wire.operationId).toBe(persisted.operationId);
    const reopened = new CanvasCommandSession(
        {
            ...t,
            snapshot: async () => ({ ...snapshot(), revision: 2, eventSeq: 1, document: { ...snapshot().document, nodes: [persisted.changes.entities[0].value!, (snapshot().document.nodes as any[])[1]] }, entityVersions: { "node:a": 2, "node:b": 1 } }),
        },
        "client",
        journal,
    );
    await reopened.open("canvas");
    await reopened.flush();
    expect(calls).toBe(1);
    expect(stored).toEqual([]);
    expect(reopened.state.saveState).toBe("saved");
    reopened.close();
});
test("task submission journal recovers task id from durable handle after lost ACK", async () => {
    let submitted = 0,
        recovered = 0;
    let handle: any;
    const s = new CanvasCommandSession(
        transport({
            invoke: async (req, effect) => {
                submitted++;
                expect(effect.type).toBe("task");
                const result = receipt(1, "a", "before", req.operationId);
                result.clientId = "client";
                result.changes.entities[0].value!.metadata = { content: "before", taskId: "one-task", taskStatus: "queued", status: "loading" };
                handle = { taskId: "one-task", receipt: result };
                throw new Error("lost task ACK");
            },
            recover: async () => {
                recovered++;
                return handle;
            },
        }),
        "client",
    );
    await s.open("canvas");
    const done = s.submitTask({ targetNodeId: "a", task: { mode: "text", prompt: "generate", input: { config: { channelId: "managed", model: "text" } } } });
    await tick();
    expect(s.state.pending[0].effect?.type).toBe("task");
    await s.reconnect();
    const result = await done;
    expect(result.taskId).toBe("one-task");
    expect(submitted).toBe(1);
    expect(recovered).toBe(1);
    expect((s.state.projected.nodes as any[])[0].metadata.taskId).toBe("one-task");
    expect(s.state.pending).toEqual([]);
    s.close();
});
