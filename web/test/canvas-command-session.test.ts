import { expect, test } from "bun:test";
import { CanvasCommandSession, type CommandTransport, type StoredPending } from "../src/services/canvas-command-session";
import { projectCanvasOperations, canvasDocumentChanges } from "../src/lib/canvas/canvas-event-projection";
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

test.each(["missing", "accepted", "offline"])("cold recovery checks old epoch receipt: %s", async (outcome) => {
    let stored: StoredPending[] = [];
    const journal = {
        read: async () => structuredClone(stored),
        write: async (_id: string, pending: StoredPending[]) => {
            stored = structuredClone(pending);
        },
    };
    const first = new CanvasCommandSession(
        transport({
            execute: async () => {
                throw new Error("lost ACK");
            },
        }),
        "client",
        journal,
    );
    await first.open("canvas");
    void first.submit([{ type: "update_node", id: "a", patch: { content: "draft" } }]).catch(() => {});
    await tick();
    await first.whenJournalDurable();
    first.close();
    let lookedUp = 0;
    const current = { ...snapshot(), documentEpoch: "restored", revision: 8, eventSeq: 7 };
    const recovered = new CanvasCommandSession(
        transport({
            snapshot: async () => current,
            receipt: async (command) => {
                lookedUp++;
                if (outcome === "offline") throw new Error("offline");
                if (outcome === "missing") throw Object.assign(new Error("missing"), { status: 404 });
                return { ...receipt(1, "a", "draft", command.operationId), clientId: "client" };
            },
            execute: async () => {
                throw new Error("must not replay across epochs");
            },
        }),
        "client",
        journal,
    );
    await recovered.open("canvas");
    expect(lookedUp).toBe(1);
    expect(recovered.state.acknowledged?.document).toEqual(current.document);
    if (outcome === "accepted") expect(recovered.state.pending).toHaveLength(0);
    else if (outcome === "offline") expect(recovered.state.pending[0].status).toBe("unconfirmed");
    else {
        expect(recovered.state.pending[0].status).toBe("conflicted");
        let backup: unknown;
        await recovered.loadLatest(async (draft) => {
            backup = draft;
        });
        expect((backup as any).nodes[0].metadata.content).toBe("draft");
    }
    recovered.close();
});

test("large edits persist all chunks before sending and retain the unconfirmed remainder", async () => {
    let stored: StoredPending[] = [];
    const sent: Command[] = [];
    let host = snapshot();
    const session = new CanvasCommandSession(
        transport({
            execute: async (command) => {
                sent.push(command);
                if (sent.length === 1) expect(stored.map((p) => p.operations.length)).toEqual([100, 1]);
                else throw new Error("connection lost");
                const document = projectCanvasOperations(host.document, command.operations);
                const result: Receipt = {
                    ...receipt(1),
                    operationId: command.operationId,
                    clientId: command.clientId,
                    changes: canvasDocumentChanges(host.document, document),
                    entityVersions: Object.fromEntries(command.operations.map((op) => ["node:" + op.id, 1])),
                };
                host = { ...host, document, eventSeq: 1, revision: 2, entityVersions: { ...host.entityVersions, ...result.entityVersions } };
                return result;
            },
        }),
        "client",
        {
            read: async () => [],
            write: async (_id, pending) => {
                stored = structuredClone(pending);
            },
        },
    );
    await session.open("canvas");
    void session.submit(Array.from({ length: 101 }, (_, i) => ({ type: "add_node", id: "bulk-" + i, nodeType: "text", content: String(i) }))).catch(() => {});
    await tick();
    await session.whenJournalDurable();
    expect(sent.map((command) => command.operations.length)).toEqual([100, 1]);
    expect(stored).toHaveLength(1);
    expect(stored[0].operations[0].id).toBe("bulk-100");
    expect(stored[0].status).toBe("unconfirmed");
    expect(session.state.projected.nodes).toHaveLength(103);
    session.close();
});

test.each([1, 3])("receipt-only no-op at host cursor %i synchronizes real events before settling and allows the next real edit", async (cursor) => {
    const remoteEvents = Array.from({ length: cursor }, (_, index) => receipt(index + 1, "a", "REMOTE " + (index + 1)));
    const sent: Command[] = [];
    const reads: number[] = [];
    const session = new CanvasCommandSession(
        transport({
            events: async (_id, after) => {
                reads.push(after);
                return remoteEvents.filter((event) => event.eventSeq > after);
            },
            execute: async (command) => {
                sent.push(command);
                if (sent.length === 1) return { ...receipt(cursor, "a", "unused", command.operationId), clientId: command.clientId, changes: { entities: [], fields: {} }, entityVersions: {} };
                return { ...receipt(cursor + 1, "a", "EDITED", command.operationId), clientId: command.clientId };
            },
        }),
        "client",
    );
    await session.open("canvas");
    const done = session.submit([{ type: "edit_canvas_settings", id: "canvas", patch: { appearance: { custom: {} } } }]).catch((error) => error);
    try {
        await tick();
        expect(session.state.acknowledged?.eventSeq).toBe(cursor);
        expect((session.state.projected.nodes as any[])[0].metadata.content).toBe("REMOTE " + cursor);
        expect(reads).toEqual([0]);
        expect(session.state.saveState).toBe("saved");
        expect(session.state.pending).toEqual([]);
        await done;
        await session.submit([{ type: "update_node", id: "a", patch: { content: "EDITED" } }]);
        expect(sent[1].baseRevision).toBe(cursor + 1);
        expect(session.state.acknowledged?.eventSeq).toBe(cursor + 1);
        expect((session.state.projected.nodes as any[])[0].metadata.content).toBe("EDITED");
    } finally {
        session.close();
    }
});

test("receipt recovery synchronizes events that appeared after the initial reconnect read", async () => {
    let command!: Command;
    let eventsVisible = false;
    const remote = receipt(1, "a", "REMOTE");
    const session = new CanvasCommandSession(
        transport({
            execute: async (request) => {
                command = request;
                throw new Error("lost ACK");
            },
            events: async (_id, after) => (eventsVisible && after === 0 ? [remote] : []),
            receipt: async (request) => {
                expect(request).toEqual(command);
                eventsVisible = true;
                return { ...remote, clientId: request.clientId, operationId: request.operationId, changes: { entities: [], fields: {} }, entityVersions: {} };
            },
        }),
        "client",
    );
    await session.open("canvas");
    const done = session.submit([{ type: "edit_canvas_settings", id: "canvas", patch: { appearance: { custom: {} } } }]);
    try {
        await tick();
        expect(session.state.saveState).toBe("offline");
        await session.reconnect();
        await done;
        expect((session.state.projected.nodes as any[])[0].metadata.content).toBe("REMOTE");
        expect(session.state.acknowledged?.eventSeq).toBe(1);
        expect(session.state.pending).toEqual([]);
    } finally {
        session.close();
    }
});

test("receipt-only no-op falls back to an authoritative snapshot when the event log cannot cover its cursor", async () => {
    let snapshotReads = 0;
    const session = new CanvasCommandSession(
        transport({
            snapshot: async () => {
                snapshotReads++;
                if (snapshotReads === 1) return snapshot();
                const latest = snapshot();
                (latest.document.nodes as any[])[0].metadata.content = "SNAPSHOT REMOTE";
                return { ...latest, revision: 4, eventSeq: 3, entityVersions: { "node:a": 4, "node:b": 1 } };
            },
            execute: async (command) => ({ ...receipt(3, "a", "unused", command.operationId), clientId: command.clientId, changes: { entities: [], fields: {} }, entityVersions: {} }),
        }),
        "client",
    );
    await session.open("canvas");
    const done = session.submit([{ type: "edit_canvas_settings", id: "canvas", patch: { appearance: { custom: {} } } }]).catch((error) => error);
    try {
        await tick();
        expect(snapshotReads).toBe(2);
        expect(session.state.acknowledged?.eventSeq).toBe(3);
        expect((session.state.projected.nodes as any[])[0].metadata.content).toBe("SNAPSHOT REMOTE");
        expect(session.state.pending).toEqual([]);
        await done;
    } finally {
        session.close();
    }
});

test("a real empty host event from the log advances the cursor and keeps the preceding remote update", async () => {
    const remote = receipt(1, "a", "REMOTE");
    const emptyHostEvent = { ...receipt(2), changes: { entities: [], fields: {} }, entityVersions: { "node:a": 7 } };
    const log = [remote, emptyHostEvent];
    const session = new CanvasCommandSession(
        transport({
            events: async (_id, after) => log.filter((event) => event.eventSeq > after),
            execute: async (command) => {
                const ownHostEvent = { ...emptyHostEvent, eventSeq: 3, revision: 4, clientId: command.clientId, operationId: command.operationId, entityVersions: { "node:a": 8 } };
                log.push(ownHostEvent);
                return ownHostEvent;
            },
        }),
        "client",
    );
    await session.open("canvas");
    try {
        await session.reconnect();
        expect(session.state.acknowledged?.eventSeq).toBe(2);
        expect(session.state.acknowledged?.revision).toBe(3);
        expect(session.state.acknowledged?.entityVersions["node:a"]).toBe(7);
        expect((session.state.projected.nodes as any[])[0].metadata.content).toBe("REMOTE");
        await session.submit([{ type: "edit_canvas_settings", id: "canvas", patch: { appearance: { custom: {} } } }]);
        expect(session.state.acknowledged?.eventSeq).toBe(3);
        expect(session.state.acknowledged?.revision).toBe(4);
        expect(session.state.acknowledged?.entityVersions["node:a"]).toBe(8);
        expect(session.state.pending).toEqual([]);
    } finally {
        session.close();
    }
});

test("an empty receipt whose cursor is not yet available stays unconfirmed until real events cover it", async () => {
    let available = false;
    let writes = 0;
    let original!: Command;
    const remote = receipt(1, "a", "REMOTE");
    const noOp = (command: Command): Receipt => ({ ...remote, clientId: command.clientId, operationId: command.operationId, changes: { entities: [], fields: {} }, entityVersions: {} });
    const session = new CanvasCommandSession(
        transport({
            events: async (_id, after) => (available && after === 0 ? [remote] : []),
            execute: async (command) => {
                writes++;
                original = command;
                return noOp(command);
            },
            receipt: async (command) => {
                expect(command).toEqual(original);
                return noOp(command);
            },
        }),
        "client",
    );
    await session.open("canvas");
    const done = session.submit([{ type: "edit_canvas_settings", id: "canvas", patch: { appearance: { custom: {} } } }]).catch((error) => error);
    try {
        await tick();
        expect(session.state.acknowledged?.eventSeq).toBe(0);
        expect((session.state.projected.nodes as any[])[0].metadata.content).toBe("before");
        expect(session.state.saveState).toBe("offline");
        expect(session.state.pending[0].status).toBe("unconfirmed");
        expect(session.state.pending[0].wire).toEqual(original);
        available = true;
        await session.reconnect();
        await done;
        expect(writes).toBe(1);
        expect(session.state.acknowledged?.eventSeq).toBe(1);
        expect((session.state.projected.nodes as any[])[0].metadata.content).toBe("REMOTE");
        expect(session.state.pending).toEqual([]);
    } finally {
        session.close();
    }
});

test.each(["command", "task"] as const)("reconnect leaves a live %s request inflight without looking up an early receipt", async (kind) => {
    let finish!: () => void;
    let started!: () => void;
    let writes = 0;
    let lookups = 0;
    const requestStarted = new Promise<void>((resolve) => {
        started = resolve;
    });
    const heldRequest = (command: Command) => {
        writes++;
        started();
        return new Promise<any>((resolve) => {
            finish = () => {
                const event = { ...receipt(1, "a", kind === "task" ? "before" : "draft", command.operationId), clientId: command.clientId, actorId: "owner" };
                if (kind === "task") event.changes.entities[0].value!.metadata = { content: "before", taskId: "one-task", taskStatus: "queued", status: "loading" };
                resolve(kind === "task" ? { taskId: "one-task", receipt: event } : event);
            };
        });
    };
    const prematureLookup = async () => {
        lookups++;
        throw Object.assign(new Error("the active request has not committed yet"), { status: 404 });
    };
    const session = new CanvasCommandSession(transport({ execute: heldRequest, invoke: heldRequest, receipt: prematureLookup, recover: prematureLookup }), "client");
    await session.open("canvas");
    const done =
        kind === "task" ? session.submitTask({ targetNodeId: "a", task: { mode: "text", prompt: "generate", input: { config: { channelId: "managed", model: "text" } } } }) : session.submit([{ type: "update_node", id: "a", patch: { content: "draft" } }]);
    await requestStarted;
    const wire = structuredClone(session.state.pending[0].wire);
    const polling = session.reconnect();
    let settledState = "";
    let remaining = -1;
    try {
        await tick();
        expect(lookups).toBe(0);
        expect(writes).toBe(1);
        expect(session.state.pending[0].status).toBe("inflight");
        expect(session.state.pending[0].wire).toEqual(wire);
        expect(() => session.discard(wire!.operationId)).toThrow("提交回执尚未确认");
        expect(session.state.pending).toHaveLength(1);
    } finally {
        finish();
        await Promise.all([done, polling]);
        settledState = session.state.saveState;
        remaining = session.state.pending.length;
        session.close();
    }
    expect(lookups).toBe(0);
    expect(writes).toBe(1);
    expect(settledState).toBe("saved");
    expect(remaining).toBe(0);
});

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

test("restart recovers a journaled inflight task with its original wire while the old HTTP ACK is still absent", async () => {
    let stored: any[] = [];
    let writes = 0;
    let recoveries = 0;
    let started!: () => void;
    let finish!: () => void;
    let handle: any;
    let originalWire = "";
    let recoveredWire = "";
    const requestStarted = new Promise<void>((resolve) => {
        started = resolve;
    });
    const journal = {
        read: async () => structuredClone(stored),
        write: async (_id: string, pending: any[]) => {
            stored = structuredClone(pending);
        },
    };
    const t = transport({
        invoke: async (command, effect) => {
            writes++;
            originalWire = JSON.stringify({ command, effect });
            const event = { ...receipt(1, "a", "before", command.operationId), clientId: command.clientId };
            event.changes.entities[0].value!.metadata = { content: "before", taskId: "durable-task", taskStatus: "queued", status: "loading" };
            handle = { taskId: "durable-task", receipt: event };
            started();
            return new Promise((resolve) => {
                finish = () => resolve(handle);
            });
        },
        recover: async (command, effect) => {
            recoveries++;
            recoveredWire = JSON.stringify({ command, effect });
            return handle;
        },
    });
    const first = new CanvasCommandSession(t, "client", journal);
    await first.open("canvas");
    const pending = first.submitTask({ targetNodeId: "a", task: { mode: "text", prompt: "generate", input: { config: { channelId: "managed", model: "text" } } } }).catch((error) => error);
    await requestStarted;
    await first.whenJournalDurable();
    expect(stored[0].status).toBe("inflight");
    first.close();
    await pending;
    const restarted = new CanvasCommandSession(t, "client", journal);
    try {
        await restarted.open("canvas");
        await restarted.whenJournalDurable();
        expect(writes).toBe(1);
        expect(recoveries).toBe(1);
        expect(recoveredWire).toBe(originalWire);
        expect(restarted.state.saveState).toBe("saved");
        expect(restarted.state.pending).toEqual([]);
        expect((restarted.state.projected.nodes as any[])[0].metadata.taskId).toBe("durable-task");
        expect(stored).toEqual([]);
    } finally {
        finish();
        restarted.close();
    }
});
test("idle reconnect polls leave the published state untouched until something changes", async () => {
    let failing = false;
    let events: Receipt[] = [];
    let journalWrites = 0;
    const journal = {
        read: async () => [],
        write: async () => {
            journalWrites++;
        },
    };
    const s = new CanvasCommandSession(
        transport({
            events: async (_canvasId, after) => {
                if (failing) throw new Error("offline");
                return events.filter((event) => event.eventSeq > after);
            },
        }),
        "client",
        journal,
    );
    await s.open("canvas");
    await s.whenJournalDurable();
    const published: string[] = [];
    s.subscribe((state) => published.push(state.saveState));
    published.length = 0;
    const writes = journalWrites;
    const idle = s.state;
    for (let i = 0; i < 3; i++) await s.reconnect();
    expect(published).toEqual([]);
    expect(s.state).toBe(idle);
    expect(journalWrites).toBe(writes);

    failing = true;
    await s.reconnect();
    await s.reconnect();
    expect(published).toEqual(["offline"]);

    failing = false;
    await s.reconnect();
    expect(published).toEqual(["offline", "saved"]);

    events = [receipt(1)];
    await s.reconnect();
    expect(published.length).toBeGreaterThan(2);
    expect((s.state.projected.nodes as any[])[0].metadata.content).toBe("remote");
    s.close();
});
