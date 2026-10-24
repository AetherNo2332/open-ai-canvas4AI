import { expect, test } from "bun:test";
import { CanvasCommandSession, type CommandTransport, type StoredPending } from "../src/services/canvas-command-session";
import { attachCanvasCommandProject } from "../src/services/canvas-command-project-lifecycle";
import { createCanvasCommandJournal } from "../src/services/canvas-command-journal";
import { canvasDocumentChanges, projectCanvasOperations, type CanvasDocument } from "../src/lib/canvas/canvas-event-projection";
import type { Command, Snapshot } from "../src/lib/canvas/command-contract.generated";

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
async function fixture(status = 400, reason = "invalid_reference") {
    let host: Snapshot = {
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 1,
        eventSeq: 0,
        document: { id: "canvas", nodes: [{ id: "node", type: "text", title: "Original", metadata: { content: "before" } }], connections: [] },
        entityVersions: { "node:node": 1 },
        entityGenerations: { "node:node": 0 },
    };
    let fail = true,
        live: any;
    let journalBarrier: ((pending: StoredPending[]) => Promise<void>) | undefined;
    const sent: Command[] = [];
    const saved = new Map<string, any>();
    const journal = createCanvasCommandJournal(
        {
            getItem: async (key) => structuredClone(saved.get(key) ?? null),
            setItem: async (key, value) => {
                saved.set(key, structuredClone(value));
                if ((value as any).pending) await journalBarrier?.((value as any).pending);
            },
        },
        "account",
    );
    const transport: CommandTransport = {
        snapshot: async () => structuredClone(host),
        events: async () => [],
        receipt: async () => {
            throw { status: 404 };
        },
        execute: async (command) => {
            sent.push(structuredClone(command));
            if (fail) throw Object.assign(new Error("参考内容需要修正"), { status, reason });
            for (const op of command.operations)
                if (op.fieldBaselines) {
                    const node = (host.document.nodes as any[]).find((node) => node.id === op.id);
                    for (const [alias, observed] of Object.entries(op.fieldBaselines.fields)) {
                        const parent = alias === "content" ? node.metadata : node;
                        if (observed.exists !== Object.hasOwn(parent, alias) || (observed.exists && JSON.stringify(observed.value) !== JSON.stringify(parent[alias]))) {
                            throw Object.assign(new Error("same field changed"), { status: 409, reason: "entity_version_conflict" });
                        }
                    }
                } else if (op.id && (command.expectedEntityVersions["node:" + op.id] ?? 0) !== (host.entityVersions["node:" + op.id] ?? 0)) {
                    throw Object.assign(new Error("entity changed"), { status: 409, reason: "entity_version_conflict" });
                }
            const next = projectCanvasOperations(host.document, command.operations);
            const changes = canvasDocumentChanges(host.document, next);
            host = { ...host, document: next, revision: host.revision + 1, eventSeq: host.eventSeq + 1, entityVersions: { "node:node": host.revision + 1 } };
            return { ...host, changes, operationId: command.operationId, clientId: command.clientId, actorId: "owner" };
        },
    };
    const session = new CanvasCommandSession(transport, "client", journal);
    await session.open("canvas");
    const editor = await attachCanvasCommandProject({
        canvasId: "canvas",
        session,
        capabilities: { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" }, title: { path: "title" } } }], structuredFields: {} },
        refs: { nodes: { current: [] }, connections: { current: [] } },
        read: () => live,
        present: (doc) => {
            live = doc;
        },
        view: { read: async () => null, write: async () => {} },
        readViewport: () => ({ x: 0, y: 0, k: 1 }),
        presentViewport: () => {},
    });
    return {
        host: () => host,
        live: () => live,
        session,
        editor,
        sent,
        journal,
        transport,
        succeed: () => {
            fail = false;
        },
        onJournalWrite: (barrier: typeof journalBarrier) => {
            journalBarrier = barrier;
        },
        remote: async (edit: (node: any) => void) => {
            edit((host.document.nodes as any[])[0]);
            host.revision++;
            host.eventSeq++;
            host.entityVersions["node:node"] = host.revision;
            await session.receive({ ...host, clientId: "other", actorId: "other", operationId: "remote-" + host.revision, changes: { entities: [{ key: "node:node", value: structuredClone((host.document.nodes as any[])[0]) }] } });
        },
    };
}

test.each([
    [400, "invalid_reference"],
    [400, "invalid_operation"],
    [401, "unauthenticated"],
    [403, "permission_denied"],
    [409, "node_locked"],
    [409, "operation_id_reused"],
])("HTTP %i %s retains the draft and original status without a version conflict", async (status, reason) => {
    const e = await fixture(status as number, reason as string);
    e.live().nodes[0].metadata.content = "local edit";
    const error = await e.editor.commit().catch((error) => error);
    expect(error.status).toBe(status);
    expect(e.session.state.saveState).toBe("error");
    expect(e.session.state.conflicts).toEqual([]);
    expect(e.session.state.rejections[0].reason).toBe(reason);
    expect(e.live().nodes[0].metadata.content).toBe("local edit");
    expect(e.host().revision).toBe(1);
    e.session.close();
});

test("a known rejection survives the real journal and reopening never resends it", async () => {
    const e = await fixture();
    e.live().nodes[0].metadata.content = "rejected";
    await e.editor.commit().catch(() => {});
    await e.session.whenJournalDurable();
    const stored = await e.journal.read("canvas");
    expect(stored[0].status).toBe("rejected");
    const reopened = new CanvasCommandSession(e.transport, "client", e.journal);
    await reopened.open("canvas");
    expect(reopened.state.saveState).toBe("error");
    expect(e.sent.length).toBe(1);
    reopened.close();
    e.session.close();
});

test("historical invalid_operation conflicts become rejections while unknown wires still recover", async () => {
    const e = await fixture();
    e.live().nodes[0].metadata.content = "old rejected edit";
    await e.editor.commit().catch(() => {});
    await e.session.whenJournalDurable();
    const old = await e.journal.read("canvas");
    old[0].status = "conflicted";
    old[0].reason = "invalid_operation";
    delete old[0].failureStatus;
    await e.journal.write("canvas", old);
    const reopened = new CanvasCommandSession(e.transport, "client", e.journal);
    await reopened.open("canvas");
    expect(reopened.state.saveState).toBe("error");
    expect(e.sent.length).toBe(1);
    reopened.close();
    e.session.close();
});

test("explicitly retrying the same rejected request preserves its operation identity", async () => {
    const e = await fixture();
    e.live().nodes[0].metadata.content = "retry me";
    await e.editor.commit().catch(() => {});
    e.succeed();
    await e.session.retryRejected(e.sent[0].operationId);
    expect(e.sent[1]).toEqual(e.sent[0]);
    expect(e.session.state.saveState).toBe("saved");
    e.session.close();
});

test("corrected edits replace a rejected batch after backup, use a new identity, and survive the next edit", async () => {
    const e = await fixture();
    e.live().nodes[0].metadata.content = "bad input";
    await e.editor.commit().catch(() => {});
    e.live().nodes[0].metadata.content = "corrected";
    e.live().nodes[0].title = "Keep this too";
    e.succeed();
    const backups: CanvasDocument[] = [];
    await e.editor.retryRejected(async (draft) => {
        backups.push(structuredClone(draft));
    });
    expect((backups[0].nodes as any[])[0].metadata.content).toBe("corrected");
    expect(e.sent[1].operationId).not.toBe(e.sent[0].operationId);
    expect((e.host().document.nodes as any[])[0]).toMatchObject({ title: "Keep this too", metadata: { content: "corrected" } });
    expect(e.session.state.pending).toEqual([]);
    e.live().nodes[0].metadata.content = "next";
    await e.editor.commit();
    expect((e.host().document.nodes as any[])[0].metadata.content).toBe("next");
    e.session.close();
});

test("backup failure and edits during backup retain rejected commands and the latest local content", async () => {
    for (const editDuringBackup of [false, true]) {
        const e = await fixture();
        e.live().nodes[0].metadata.content = "keep";
        await e.editor.commit().catch(() => {});
        const before = structuredClone(await e.journal.read("canvas"));
        await expect(
            e.editor.retryRejected(async () => {
                if (editDuringBackup) e.live().nodes[0].metadata.content = "newer";
                else throw new Error("disk full");
            }),
        ).rejects.toThrow();
        expect(e.sent.length).toBe(1);
        expect(e.session.state.pending[0].operationId).toBe(before[0].operationId);
        expect((await e.journal.read("canvas"))[0].operationId).toBe(before[0].operationId);
        expect(e.live().nodes[0].metadata.content).toBe(editDuringBackup ? "newer" : "keep");
        e.session.close();
    }
});

test("real version conflicts and unknown outcomes keep their distinct recovery paths", async () => {
    for (const [status, reason, expected] of [
        [409, "entity_version_conflict", "conflict"],
        [503, "unavailable", "offline"],
    ] as const) {
        const e = await fixture(status, reason);
        e.live().nodes[0].metadata.content = "keep";
        void e.editor.commit().catch(() => {});
        await tick();
        expect(e.session.state.saveState).toBe(expected);
        await expect(e.editor.retryRejected(async () => {})).rejects.toThrow();
        expect(e.sent.length).toBe(1);
        e.session.close();
    }
});

test("failed replacement journal writes and concurrent edits restore the original durable rejection", async () => {
    for (const edit of [false, true]) {
        const e = await fixture();
        e.live().nodes[0].metadata.content = "keep";
        await e.editor.commit().catch(() => {});
        await e.session.whenJournalDurable();
        const operation = e.sent[0].operationId;
        e.onJournalWrite(async (pending) => {
            if (!pending.length || pending[0].operationId === operation) return;
            if (edit) e.live().nodes[0].metadata.content = "changed during write";
            else throw new Error("disk error after write");
        });
        await expect(e.editor.retryRejected(async () => {})).rejects.toThrow();
        expect((await e.journal.read("canvas"))[0].operationId).toBe(operation);
        expect(e.session.state.pending[0].operationId).toBe(operation);
        expect(e.live().nodes[0].metadata.content).toBe(edit ? "changed during write" : "keep");
        expect(e.sent.length).toBe(1);
        e.session.close();
    }
});

test("corrected retries do not turn an unrelated remote title into an authored patch", async () => {
    const e = await fixture();
    e.live().nodes[0].metadata.content = "bad";
    await e.editor.commit().catch(() => {});
    await e.remote((node) => {
        node.title = "Remote title";
    });
    e.live().nodes[0].metadata.content = "corrected";
    e.succeed();
    await e.editor.retryRejected(async () => {});
    expect(e.sent[1].operations[0].patch).toEqual({ content: "corrected" });
    expect((e.host().document.nodes as any[])[0]).toMatchObject({ title: "Remote title", metadata: { content: "corrected" } });
    e.session.close();
});

test("retry after an authentication rejection preserves the original same-field observation", async () => {
    const e = await fixture(401, "unauthenticated");
    e.live().nodes[0].metadata.content = "local";
    await e.remote((node) => {
        node.metadata.content = "remote";
    });
    await e.editor.commit().catch(() => {});
    expect(e.sent[0].operations[0].fieldBaselines!.fields.content.value).toBe("before");
    e.succeed();
    await expect(e.editor.retryRejected(async () => {})).rejects.toMatchObject({ reason: "entity_version_conflict" });
    expect(e.sent[1].operations[0].fieldBaselines!.fields.content.value).toBe("before");
    expect((e.host().document.nodes as any[])[0].metadata.content).toBe("remote");
    expect(e.live().nodes[0].metadata.content).toBe("local");
    e.session.close();
});

test("unsent field observations survive a remote edit during rejection recovery", async () => {
    const e = await fixture();
    e.live().nodes[0].metadata.content = "bad";
    await e.editor.commit().catch(() => {});
    e.live().nodes[0].title = "My title";
    await e.remote((node) => {
        node.title = "Other title";
    });
    e.succeed();
    await expect(e.editor.retryRejected(async () => {})).rejects.toMatchObject({ reason: "entity_version_conflict" });
    expect((e.host().document.nodes as any[])[0].title).toBe("Other title");
    expect(e.live().nodes[0].title).toBe("My title");
    e.session.close();
});

test("runtime progress is backed up but is excluded from corrected save operations", async () => {
    const e = await fixture();
    e.live().nodes[0].metadata.content = "bad";
    await e.editor.commit().catch(() => {});
    e.live().nodes[0].metadata.content = "corrected";
    e.live().nodes[0].metadata.taskProgress = 75;
    e.succeed();
    let backup: any;
    await e.editor.retryRejected(async (draft) => {
        backup = structuredClone(draft);
    });
    expect(backup.nodes[0].metadata.taskProgress).toBe(75);
    expect(e.sent[1].operations[0].patch).toEqual({ content: "corrected" });
    expect((e.host().document.nodes as any[])[0].metadata.taskProgress).toBeUndefined();
    e.session.close();
});

test("a corrected field and queued new node save together while structural batches keep strict concurrency checks", async () => {
    for (const remote of [false, true]) {
        const e = await fixture();
        e.live().nodes[0].metadata.content = "bad";
        await e.editor.commit().catch(() => {});
        e.live().nodes.push({ id: "new", type: "text", title: "New node", metadata: { content: "new text" } });
        void e.editor.commit().catch(() => {});
        await tick();
        if (remote)
            await e.remote((node) => {
                node.title = "Remote title";
            });
        e.live().nodes[0].metadata.content = "corrected";
        e.succeed();
        if (remote) {
            await expect(e.editor.retryRejected(async () => {})).rejects.toMatchObject({ reason: "entity_version_conflict" });
            expect((e.host().document.nodes as any[]).some((node) => node.id === "new")).toBe(false);
            expect(e.live().nodes.some((node: any) => node.id === "new")).toBe(true);
        } else {
            await e.editor.retryRejected(async () => {});
            expect((e.host().document.nodes as any[]).find((node) => node.id === "node").metadata.content).toBe("corrected");
            expect((e.host().document.nodes as any[]).some((node) => node.id === "new")).toBe(true);
        }
        e.session.close();
    }
});
