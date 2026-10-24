import { expect, test } from "bun:test";
import { attachCanvasCommandProject } from "../src/services/canvas-command-project-lifecycle";
import { CanvasCommandSession, type StoredPending } from "../src/services/canvas-command-session";
import { canvasDocumentChanges, projectCanvasOperations, type CanvasDocument } from "../src/lib/canvas/canvas-event-projection";
import type { Snapshot } from "../src/lib/canvas/command-contract.generated";

async function conflictedEditor() {
    let host: Snapshot = {
        protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0,
        document: { id: "canvas", title: "Story", nodes: [{ id: "text", type: "text", metadata: { content: "before" } }], connections: [] },
        entityVersions: { "node:text": 1 },
    };
    let live: any = {};
    let writes = 0;
    let stored: StoredPending[] = [];
    let failJournal = false;
    let clearBarrier: (() => Promise<void>) | undefined;
    const session = new CanvasCommandSession({
        snapshot: async () => structuredClone(host),
        events: async () => [],
        receipt: async () => { throw { status: 404 }; },
        execute: async (command) => {
            writes++;
            if (writes === 1) throw { status: 409, reason: "entity_version_conflict", message: "another edit" };
            const next = projectCanvasOperations(host.document, command.operations);
            const changes = canvasDocumentChanges(host.document, next);
            host = { ...host, document: next, revision: host.revision + 1, eventSeq: host.eventSeq + 1 };
            return { ...host, changes, operationId: command.operationId, clientId: command.clientId, actorId: "owner" };
        },
    }, "client", {
        read: async () => structuredClone(stored),
        write: async (_id, pending) => {
            if (pending.length === 0) await clearBarrier?.();
            if (failJournal && pending.length === 0) throw new Error("disk full");
            stored = structuredClone(pending);
        },
    });
    await session.open("canvas");
    const editor = await attachCanvasCommandProject({
        canvasId: "canvas", session,
        capabilities: { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" } } }], structuredFields: {} },
        refs: { nodes: { current: [] }, connections: { current: [] } },
        read: () => live,
        present: (document) => { live = document; },
        view: { read: async () => null, write: async () => {} },
        readViewport: () => ({ x: 120, y: 80, k: 0.9 }), presentViewport: () => {},
    });
    live.nodes[0].metadata.content = "local draft";
    expect(await editor.flush()).toBe(false);
    expect(session.state.saveState).toBe("conflict");
    host = { ...host, revision: 2, eventSeq: 1, document: { ...host.document, nodes: [{ id: "text", type: "text", metadata: { content: "cloud update" } }] }, entityVersions: { "node:text": 2 } };
    return { session, editor, live: () => live, stored: () => stored, writes: () => writes, failJournal: () => { failJournal = true; }, delayClear: (barrier: () => Promise<void>) => { clearBarrier = barrier; } };
}

test("loading the latest canvas backs up conflicted and unsent edits without requiring a successful save, then allows new edits", async () => {
    const e = await conflictedEditor();
    const backups: CanvasDocument[] = [];
    e.live().title = "unsent title";
    try {
        await e.editor.loadLatest(async (draft) => { backups.push(structuredClone(draft)); });
        expect(backups[0].title).toBe("unsent title");
        expect((backups[0].nodes as any[])[0].metadata.content).toBe("local draft");
        expect(e.live().title).toBe("Story");
        expect(e.live().nodes[0].metadata.content).toBe("cloud update");
        expect(e.session.state.pending).toEqual([]);
        expect(e.stored()).toEqual([]);
        expect(e.session.state.saveState).toBe("saved");
        expect(await e.editor.flush()).toBe(true);
        expect(e.writes()).toBe(1);
        e.live().nodes[0].metadata.content = "new edit";
        expect(await e.editor.flush()).toBe(true);
        expect(e.writes()).toBe(2);
        expect(e.live().nodes[0].metadata.content).toBe("new edit");
    } finally { e.editor.dispose(); e.session.close(); }
});

test("a failed backup or command journal write retains the conflicted edits", async () => {
    for (const failure of ["backup", "journal"]) {
        const e = await conflictedEditor();
        const original = structuredClone(e.session.state.pending);
        if (failure === "journal") e.failJournal();
        try {
            await expect(e.editor.loadLatest(async () => { if (failure === "backup") throw new Error("disk full"); })).rejects.toThrow("disk full");
            expect(e.session.state.pending).toEqual(original);
            expect(e.stored()).toEqual(original);
            expect(e.session.state.saveState).toBe("conflict");
            expect(e.live().nodes[0].metadata.content).toBe("local draft");
            expect(e.writes()).toBe(1);
        } finally { e.editor.dispose(); e.session.close(); }
    }
});

test("edits made during the backup are kept instead of replaced by the cloud", async () => {
    const e = await conflictedEditor();
    try {
        await expect(e.editor.loadLatest(async () => { e.live().nodes[0].metadata.content = "edit during backup"; })).rejects.toThrow("仍在更新");
        expect(e.live().nodes[0].metadata.content).toBe("edit during backup");
        expect(e.session.state.pending).toHaveLength(1);
        expect(e.writes()).toBe(1);
    } finally { e.editor.dispose(); e.session.close(); }
});

test("a request with an unknown outcome cannot be discarded by loading the latest canvas", async () => {
    const session = new CanvasCommandSession({
        snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0, document: { nodes: [], connections: [] }, entityVersions: {} }),
        events: async () => [],
        receipt: async () => { throw { status: 503 }; },
        execute: async () => { throw new Error("network lost"); },
    }, "client");
    await session.open("canvas");
    void session.submit([{ type: "rename_canvas", id: "canvas", title: "local" }]).catch(() => {});
    await session.flush();
    const original = structuredClone(session.state.pending);
    try {
        expect(original[0].status).toBe("unconfirmed");
        await expect(session.loadLatest(async () => {})).rejects.toThrow("确认");
        expect(session.state.pending).toEqual(original);
    } finally { session.close(); }
});

test("an edit during journal replacement restores the original journal and leaves the editor unchanged", async () => {
    const e = await conflictedEditor();
    const original = structuredClone(e.session.state.pending);
    let entered!: () => void;
    let release!: () => void;
    const started = new Promise<void>((resolve) => { entered = resolve; });
    const barrier = new Promise<void>((resolve) => { release = resolve; });
    e.delayClear(async () => { entered(); await barrier; });
    try {
        const loading = e.editor.loadLatest(async () => {}).catch((error) => error);
        await started;
        e.live().nodes[0].metadata.content = "edit during journal write";
        release();
        expect((await loading).message).toContain("仍在更新");
        expect(e.session.state.pending).toEqual(original);
        expect(e.stored()).toEqual(original);
        expect(e.live().nodes[0].metadata.content).toBe("edit during journal write");
    } finally { release(); e.editor.dispose(); e.session.close(); }
});
