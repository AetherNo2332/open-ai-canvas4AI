import { expect, test } from "bun:test";
import { migrateCanvasReferences } from "../src/services/canvas-reference-migration";
import type { Command, Snapshot } from "../src/lib/canvas/command-contract.generated";
import type { CommandTransport } from "../src/services/canvas-command-session";

const legacy = (): Snapshot => ({
    protocolVersion: 1,
    canvasId: "canvas",
    documentEpoch: "epoch",
    revision: 8,
    eventSeq: 2,
    entityVersions: {},
    document: { id: "canvas", nodes: [], connections: [] },
    referenceMigration: { required: true, conflicts: [] },
});

function fixture() {
    let current = legacy();
    const commands: Command[] = [];
    const order: string[] = [];
    const transport = {
        snapshot: async () => {
            order.push("snapshot");
            return structuredClone(current);
        },
        execute: async (command: Command) => {
            order.push("execute");
            commands.push(command);
            current = { ...current, revision: current.revision + 1, referenceMigration: undefined, document: { ...current.document, referenceFieldsVersion: 1 } };
            return {};
        },
    } as unknown as CommandTransport;
    const options = {
        snapshot: legacy(),
        choices: {},
        transport,
        save: async () => {
            order.push("save");
        },
        refresh: async () => {
            order.push("refresh");
        },
    };
    return {
        options,
        commands,
        order,
        update: (snapshot: Snapshot) => {
            current = snapshot;
        },
    };
}

test("ordinary web canvas saves edits then migrates through the host without a desktop workspace", async () => {
    const f = fixture();
    const result = await migrateCanvasReferences(f.options);
    expect(f.order).toEqual(["save", "snapshot", "execute", "refresh", "snapshot"]);
    expect(f.commands).toHaveLength(1);
    expect(f.commands[0]).toMatchObject({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", baseRevision: 8, operations: [{ type: "migrate_references", id: "canvas", patch: { scopeChoices: {} } }] });
    expect(f.commands[0].operationId).toBeTruthy();
    expect(f.commands[0].clientId).toBeTruthy();
    expect(result.document.referenceFieldsVersion).toBe(1);
});

test("changed reference conflicts require a fresh user choice", async () => {
    const f = fixture();
    f.update({ ...legacy(), revision: 9, referenceMigration: { required: true, conflicts: [{ scopeKey: "node:image", fieldNodeIds: ["a"], edgeNodeIds: ["b"] }] } });
    await expect(migrateCanvasReferences(f.options)).rejects.toThrow("引用来源已变化");
    expect(f.commands).toEqual([]);
});

test("a migration completed in another tab refreshes without a second command", async () => {
    const f = fixture();
    f.update({ ...legacy(), referenceMigration: undefined, document: { referenceFieldsVersion: 1 } });
    const result = await migrateCanvasReferences(f.options);
    expect(result.document.referenceFieldsVersion).toBe(1);
    expect(f.commands).toEqual([]);
    expect(f.order).toEqual(["save", "snapshot", "refresh"]);
});

test("failed remote save prevents migration", async () => {
    const f = fixture();
    f.options.save = async () => {
        throw new Error("本地草稿未同步");
    };
    await expect(migrateCanvasReferences(f.options)).rejects.toThrow("本地草稿未同步");
    expect(f.order).toEqual([]);
    expect(f.commands).toEqual([]);
});
