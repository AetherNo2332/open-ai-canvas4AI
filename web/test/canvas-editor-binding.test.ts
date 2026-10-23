import { expect, test } from "bun:test";
import { CanvasCommandSession, type CommandTransport } from "../src/services/canvas-command-session";
import { bindCanvasCommandEditor } from "../src/services/canvas-command-editor-binding";
import type { CanvasDocument } from "../src/lib/canvas/canvas-event-projection";
import type { Command, Receipt, Snapshot } from "../src/lib/canvas/command-contract.generated";
import type { CanvasCapabilities } from "../src/lib/canvas/canvas-editor-commands";
const initial = { id: "canvas", nodes: [{ id: "a", type: "text", metadata: { content: "before", extension: "keep" } }], connections: [] };
const cap = { protocolVersion: 1, nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" } } }], structuredFields: {} };

const fieldCap: CanvasCapabilities = {
    protocolVersion: 1,
    nodes: [{ type: "text", patchFields: { content: { path: "metadata.content" }, richText: { path: "metadata.richText" }, title: { path: "title" }, x: { path: "position.x" } } }],
    structuredFields: {},
};
async function fieldEditor(generation: number | null = 0, capabilities = fieldCap, node: any = { id: "a", type: "text", title: "Old title", position: { x: 0 }, metadata: { content: "before" } }) {
    let host: Snapshot = {
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 1,
        eventSeq: 0,
        document: { id: "canvas", nodes: [structuredClone(node)], connections: [] },
        entityVersions: { "node:a": 1 },
        ...(generation === null ? {} : { entityGenerations: { "node:a": generation } }),
    };
    const sent: Command[] = [];
    const readPath = (value: any, path: string) => {
        let exists = true;
        for (const key of path.split(".")) {
            if (!value || typeof value !== "object" || !Object.hasOwn(value, key)) {
                exists = false;
                value = null;
                break;
            }
            value = value[key];
        }
        return { exists, value: exists ? value : null };
    };
    const session = new CanvasCommandSession(
        {
            snapshot: async () => structuredClone(host),
            events: async () => [],
            receipt: async () => {
                throw { status: 404 };
            },
            execute: async (command) => {
                sent.push(structuredClone(command));
                const current = (host.document.nodes as any[])[0];
                for (const op of command.operations) {
                    const baseline = op.fieldBaselines;
                    const descriptor = capabilities.nodes.find((d) => d.type === current?.type)!;
                    if (
                        baseline &&
                        (baseline.generation !== host.entityGenerations?.["node:a"] ||
                            baseline.nodeType !== current?.type ||
                            Object.entries(baseline.fields).some(([alias, observed]) => JSON.stringify(readPath(current, descriptor.patchFields[alias].path)) !== JSON.stringify(observed)))
                    )
                        throw { status: 409, reason: "entity_version_conflict" };
                    if (!baseline && command.expectedEntityVersions?.["node:a"] !== host.entityVersions["node:a"]) throw { status: 409, reason: "entity_version_conflict" };
                    for (const [alias, value] of Object.entries(op.patch || {})) {
                        const path = descriptor.patchFields[alias].path.split(".");
                        let parent = current;
                        for (const key of path.slice(0, -1)) parent = parent[key] ||= {};
                        parent[path.at(-1)!] = structuredClone(value);
                    }
                }
                const event: Receipt = {
                    protocolVersion: 1,
                    canvasId: "canvas",
                    documentEpoch: "epoch",
                    revision: ++host.revision,
                    eventSeq: ++host.eventSeq,
                    operationId: command.operationId,
                    clientId: "client",
                    actorId: "owner",
                    changes: { entities: [{ key: "node:a", value: structuredClone(current) }] },
                    entityVersions: { "node:a": host.revision },
                    entityGenerations: host.entityGenerations,
                };
                host.entityVersions["node:a"] = host.revision;
                return event;
            },
        },
        "client",
    );
    await session.open("canvas");
    let live: any = structuredClone(host.document);
    const binding = bindCanvasCommandEditor(session, capabilities, {
        refs: { nodes: { current: live.nodes }, connections: { current: [] } },
        read: () => live,
        present: (document) => {
            live = document;
        },
    });
    return {
        session,
        binding,
        sent,
        live: () => live,
        host: () => host,
        remote: async (edit: (value: any) => void, nextGeneration = generation) => {
            edit((host.document.nodes as any[])[0]);
            host.revision++;
            host.eventSeq++;
            host.entityVersions["node:a"] = host.revision;
            if (nextGeneration !== null) (host.entityGenerations ||= {})["node:a"] = nextGeneration;
            await session.receive({
                protocolVersion: 1,
                canvasId: "canvas",
                documentEpoch: "epoch",
                revision: host.revision,
                eventSeq: host.eventSeq,
                operationId: "remote-" + host.eventSeq,
                clientId: "other",
                actorId: "owner",
                changes: { entities: [{ key: "node:a", value: structuredClone((host.document.nodes as any[])[0]) }] },
                entityVersions: host.entityVersions,
                entityGenerations: host.entityGenerations,
            });
        },
    };
}

test("field baseline preserves original content across two remote disjoint updates", async () => {
    const e = await fieldEditor();
    e.live().nodes[0].metadata.content = "local";
    await e.remote((node) => {
        node.title = "Remote title";
    });
    await e.remote((node) => {
        node.metadata.progress = 80;
    });
    await e.binding.commit();
    expect(e.sent[0].expectedEntityVersions?.["node:a"]).toBe(1);
    expect(e.sent[0].operations[0].fieldBaselines).toEqual({ nodeType: "text", workflowKind: "", generation: 0, fields: { content: { exists: true, value: "before" }, richText: { exists: false, value: null } } });
    expect(e.live().nodes[0]).toMatchObject({ title: "Remote title", metadata: { content: "local", progress: 80 } });
    e.binding.dispose();
    e.session.close();
});

test("field baseline keeps same-field conflict draft through repeated remote updates", async () => {
    const e = await fieldEditor();
    e.live().nodes[0].metadata.content = "local";
    await e.remote((node) => {
        node.metadata.content = "remote";
    });
    await e.remote((node) => {
        node.title = "Latest title";
    });
    await expect(e.binding.commit()).rejects.toMatchObject({ reason: "entity_version_conflict" });
    expect(e.sent[0].operations[0].fieldBaselines?.fields.content.value).toBe("before");
    expect(e.live().nodes[0]).toMatchObject({ title: "Latest title", metadata: { content: "local" } });
    expect(e.host().document.nodes).toMatchObject([{ metadata: { content: "remote" } }]);
    e.binding.dispose();
    e.session.close();
});

test("field baseline rejects generation ABA and keeps missing-generation clients strict", async () => {
    const aba = await fieldEditor(0);
    aba.live().nodes[0].metadata.content = "local";
    await aba.remote((node) => {
        node.title = "Recreated";
    }, 3);
    await expect(aba.binding.commit()).rejects.toMatchObject({ reason: "entity_version_conflict" });
    expect(aba.sent[0].operations[0].fieldBaselines?.generation).toBe(0);
    expect(aba.session.state.acknowledged?.entityGenerations?.["node:a"]).toBe(3);
    aba.binding.dispose();
    aba.session.close();
    const legacy = await fieldEditor(null);
    legacy.live().nodes[0].metadata.content = "local";
    await legacy.remote((node) => {
        node.title = "Remote title";
    });
    await expect(legacy.binding.commit()).rejects.toMatchObject({ reason: "entity_version_conflict" });
    expect(legacy.sent[0].operations[0].fieldBaselines).toBeUndefined();
    legacy.binding.dispose();
    legacy.session.close();
});

test("field baseline distinguishes explicit null from missing and guards rich-text mirrors", async () => {
    for (const metadata of [{ content: null }, {}]) {
        const e = await fieldEditor(0, fieldCap, { id: "a", type: "text", title: "Old", metadata });
        e.live().nodes[0].metadata.content = "local";
        await e.remote((node) => {
            node.title = "Remote";
        });
        await e.binding.commit();
        expect(e.sent[0].operations[0].fieldBaselines?.fields.content).toEqual({ exists: Object.hasOwn(metadata, "content"), value: null });
        e.binding.dispose();
        e.session.close();
    }
    const richText = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "before" }] }] };
    const e = await fieldEditor(2, fieldCap, { id: "a", type: "text", title: "Old", metadata: { content: "before", richText } });
    e.live().nodes[0].metadata.richText.content[0].content[0].text = "local";
    e.live().nodes[0].metadata.content = "local";
    await e.remote((node) => {
        node.metadata.status = "success";
        node.metadata.taskProgress = 90;
    });
    await e.binding.commit();
    expect(e.sent[0].operations[0].patch).toEqual({ content: "local", richText: e.live().nodes[0].metadata.richText });
    expect(e.sent[0].operations[0].fieldBaselines?.fields.richText).toEqual({ exists: true, value: richText });
    expect(e.live().nodes[0].metadata.taskProgress).toBe(90);
    e.binding.dispose();
    e.session.close();
});

test("field baseline resolves media content alias and guards prompt/reference mirrors", async () => {
    const mediaCap: CanvasCapabilities = {
        protocolVersion: 1,
        nodes: [{ type: "image", patchFields: { content: { path: "metadata.composerContent" }, richText: { path: "metadata.richText" }, generationSpec: { path: "metadata.generationSpec" }, inputBindings: { path: "metadata.inputBindings" } } }],
        structuredFields: {},
    };
    const generationSpec = { prompt: "before", inputs: [] };
    const e = await fieldEditor(4, mediaCap, { id: "a", type: "image", metadata: { composerContent: "before", generationSpec, inputBindings: [] } });
    e.live().nodes[0].metadata.composerContent = "local prompt";
    await e.remote((node) => {
        node.metadata.status = "loading";
        node.metadata.taskProgress = 70;
    });
    await e.binding.commit();
    expect(e.sent[0].operations[0].patch).toEqual({ content: "local prompt" });
    expect(e.sent[0].operations[0].fieldBaselines?.fields).toEqual({
        content: { exists: true, value: "before" },
        richText: { exists: false, value: null },
        generationSpec: { exists: true, value: generationSpec },
        inputBindings: { exists: true, value: [] },
    });
    expect(e.live().nodes[0].metadata.taskProgress).toBe(70);
    e.binding.dispose();
    e.session.close();
});

test("field baseline captures a newly edited alias from its actual later observation", async () => {
    const e = await fieldEditor();
    e.live().nodes[0].metadata.content = "local";
    await e.remote((node) => {
        node.title = "Remote title";
    });
    e.live().nodes[0].title = "My title";
    await e.remote((node) => {
        node.metadata.taskProgress = 50;
    });
    await e.binding.commit();
    expect(e.sent[0].operations[0].fieldBaselines?.fields.title).toEqual({ exists: true, value: "Remote title" });
    expect(e.sent[0].operations[0].fieldBaselines?.fields.content.value).toBe("before");
    expect(e.live().nodes[0]).toMatchObject({ title: "My title", metadata: { content: "local", taskProgress: 50 } });
    e.binding.dispose();
    e.session.close();
});

test("field baseline never turns a dirty update into add-node after remote deletion", async () => {
    const e = await fieldEditor();
    e.live().nodes[0].metadata.content = "local";
    const host = e.host();
    host.document.nodes = [];
    host.eventSeq = 1;
    host.revision = 2;
    host.entityVersions["node:a"] = 2;
    await e.session.receive({
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 2,
        eventSeq: 1,
        operationId: "delete",
        clientId: "other",
        actorId: "owner",
        changes: { entities: [{ key: "node:a", delete: true }], nodeOrder: [] },
        entityVersions: { "node:a": 2 },
        entityGenerations: { "node:a": 0 },
    });
    await expect(e.binding.commit()).rejects.toMatchObject({ reason: "entity_version_conflict" });
    expect(e.sent[0].operations.map((op) => op.type)).toEqual(["update_node"]);
    expect(e.sent[0].operations[0].fieldBaselines?.generation).toBe(0);
    expect(e.live().nodes[0].metadata.content).toBe("local");
    e.binding.dispose();
    e.session.close();
});

test("field baseline retains generation through delete and same-ID recreation before commit", async () => {
    const e = await fieldEditor();
    const original = structuredClone(e.live().nodes[0]);
    e.live().nodes[0].metadata.content = "local";
    const host = e.host();
    host.document.nodes = [];
    host.eventSeq = 1;
    host.revision = 2;
    host.entityVersions["node:a"] = 2;
    await e.session.receive({
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 2,
        eventSeq: 1,
        operationId: "delete",
        clientId: "other",
        actorId: "owner",
        changes: { entities: [{ key: "node:a", delete: true }], nodeOrder: [] },
        entityVersions: { "node:a": 2 },
        entityGenerations: { "node:a": 0 },
    });
    host.document.nodes = [original];
    await e.remote((node) => {
        node.title = "Recreated";
    }, 3);
    await expect(e.binding.commit()).rejects.toMatchObject({ reason: "entity_version_conflict" });
    expect(e.sent[0].operations[0].fieldBaselines?.generation).toBe(0);
    expect(e.sent[0].operations.map((op) => op.type)).toEqual(["update_node"]);
    expect(e.live().nodes[0].metadata.content).toBe("local");
    e.binding.dispose();
    e.session.close();
});

test("field baseline binding captures queued edit after its own optimistic predecessor", async () => {
    const sent: Command[] = [];
    let finish!: (event: Receipt) => void;
    const session = new CanvasCommandSession(
        {
            snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0, entityVersions: { "node:a": 1 }, entityGenerations: { "node:a": 0 }, document: structuredClone(initial) }),
            events: async () => [],
            receipt: async () => {
                throw { status: 404 };
            },
            execute: async (command) => {
                sent.push(structuredClone(command));
                if (sent.length === 1)
                    return new Promise((resolve) => {
                        finish = resolve;
                    });
                return {
                    protocolVersion: 1,
                    canvasId: "canvas",
                    documentEpoch: "epoch",
                    revision: 3,
                    eventSeq: 2,
                    clientId: "client",
                    actorId: "owner",
                    operationId: command.operationId,
                    changes: { entities: [{ key: "node:a", value: { ...initial.nodes[0], metadata: { ...initial.nodes[0].metadata, content: "second" } } }] },
                    entityVersions: { "node:a": 3 },
                    entityGenerations: { "node:a": 0 },
                };
            },
        },
        "client",
    );
    await session.open("canvas");
    let live: any = structuredClone(initial);
    const binding = bindCanvasCommandEditor(session, fieldCap, {
        refs: { nodes: { current: live.nodes }, connections: { current: [] } },
        read: () => live,
        present: (document) => {
            live = document;
        },
    });
    live.nodes[0].metadata.content = "first";
    const first = binding.commit();
    live.nodes[0].metadata.content = "second";
    const second = binding.commit();
    await new Promise((resolve) => setTimeout(resolve, 0));
    finish({
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 2,
        eventSeq: 1,
        clientId: "client",
        actorId: "owner",
        operationId: sent[0].operationId,
        changes: { entities: [{ key: "node:a", value: { ...initial.nodes[0], metadata: { ...initial.nodes[0].metadata, content: "first" } } }] },
        entityVersions: { "node:a": 2 },
        entityGenerations: { "node:a": 0 },
    });
    await Promise.all([first, second]);
    expect(sent[1].expectedEntityVersions?.["node:a"]).toBe(2);
    expect(sent[1].operations[0].fieldBaselines?.fields.content).toEqual({ exists: true, value: "first" });
    expect(live.nodes[0].metadata.content).toBe("second");
    binding.dispose();
    session.close();
});

test("local progress survives unrelated events but host terminal state wins", async () => {
    const document = { ...structuredClone(initial), nodes: [{ ...initial.nodes[0], metadata: { content: "before", taskId: "task", taskStatus: "running", status: "loading", taskProgress: 0 } }] };
    const s = new CanvasCommandSession(
        {
            snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0, entityVersions: { "node:a": 1 }, document }),
            events: async () => [],
            execute: async () => {
                throw new Error("unexpected command");
            },
            receipt: async () => {
                throw new Error("missing");
            },
        } as CommandTransport,
        "client",
    );
    await s.open("canvas");
    let live: any = structuredClone(document);
    const binding = bindCanvasCommandEditor(s, cap, {
        refs: { nodes: { current: live.nodes }, connections: { current: live.connections } },
        read: () => live,
        present: (doc) => {
            live = doc;
        },
    });
    live.nodes[0].metadata.taskProgress = 45;
    await s.receive({
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 2,
        eventSeq: 1,
        clientId: "other",
        actorId: "other",
        operationId: "title",
        changes: { entities: [], fields: { title: { value: "Other edit" } } },
        entityVersions: { "project:title": 2 },
    });
    expect(live.nodes[0].metadata.taskProgress).toBe(45);
    await binding.commit();
    expect(s.state.pending).toEqual([]);
    await s.receive({
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 3,
        eventSeq: 2,
        clientId: "worker",
        actorId: "owner",
        operationId: "finished",
        changes: { entities: [{ key: "node:a", value: { ...document.nodes[0], metadata: { content: "result", taskId: "task", taskStatus: "succeeded", status: "success", taskProgress: 100 } } }] },
        entityVersions: { "node:a": 3 },
    });
    expect(live.nodes[0].metadata.taskProgress).toBe(100);
    expect(live.nodes[0].metadata.content).toBe("result");
    binding.dispose();
    s.close();
});
test("editor binding updates refs before rendering and preserves text not yet submitted across task events", async () => {
    const s = new CanvasCommandSession(
        {
            snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0, entityVersions: { "node:a": 1 }, document: structuredClone(initial) }),
            events: async () => [],
            execute: async () => {
                throw new Error("offline");
            },
            receipt: async () => {
                throw new Error("offline");
            },
        } as CommandTransport,
        "client",
    );
    await s.open("canvas");
    let live: CanvasDocument = structuredClone(initial);
    const refs = { nodes: { current: initial.nodes as any[] }, connections: { current: [] as any[] } };
    let presentations = 0;
    const binding = bindCanvasCommandEditor(s, cap, {
        refs,
        read: () => live,
        present: (doc) => {
            expect(refs.nodes.current).toBe(doc.nodes as any[]);
            expect(refs.connections.current).toBe(doc.connections as any[]);
            live = doc;
            presentations++;
        },
    });
    (live.nodes as any[])[0].metadata.content = "unsent draft";
    await s.receive({
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 2,
        eventSeq: 1,
        clientId: "worker",
        actorId: "owner",
        operationId: "result",
        entityVersions: { "node:a": 2 },
        changes: { entities: [{ key: "node:a", value: { id: "a", type: "text", metadata: { content: "generated", taskId: "task", taskStatus: "succeeded", extension: "keep" } } }] },
    });
    expect((live.nodes as any[])[0].metadata.content).toBe("unsent draft");
    expect((live.nodes as any[])[0].metadata.taskId).toBe("task");
    const saved = binding.commit().catch((e) => e);
    await new Promise((r) => setTimeout(r, 0));
    expect(s.state.pending[0].operations).toEqual([{ type: "update_node", id: "a", patch: { content: "unsent draft" } }]);
    expect(s.state.pending[0].expectedEntityVersions["node:a"]).toBe(1);
    expect(presentations).toBeGreaterThan(1);
    binding.dispose();
    s.close();
    await saved;
});
