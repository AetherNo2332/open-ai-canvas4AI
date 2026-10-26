import { expect, test } from "bun:test";
import { CanvasCommandSession } from "../src/services/canvas-command-session";
import { bindCanvasCommandEditor } from "../src/services/canvas-command-editor-binding";
import type { Command, Receipt, Snapshot } from "../src/lib/canvas/command-contract.generated";
import type { CanvasCapabilities } from "../src/lib/canvas/canvas-editor-commands";
import capabilities from "./fixtures/canvas-editor-capabilities.json";

// Keep conversion, draft capture and session scheduling real; control only HTTP
// completion so the upload can finish between enqueue and the creation ACK.
async function uploadEditor(type: "video" | "image" | "audio", existing = false) {
    const placeholder = { id: "upload", type, title: "Upload", position: { x: 0, y: 0 }, metadata: {} as Record<string, unknown> };
    let host: Snapshot = {
        protocolVersion: 1,
        canvasId: "canvas",
        documentEpoch: "epoch",
        revision: 1,
        eventSeq: 0,
        entityVersions: existing ? { "node:upload": 1 } : {},
        entityGenerations: existing ? { "node:upload": 0 } : {},
        document: { nodes: existing ? [structuredClone(placeholder)] : [], connections: [] },
    };
    const sent: Command[] = [];
    const arrivals: Array<{ command: Command; finish: () => Receipt | undefined }> = [];
    let wake = () => {};
    const session = new CanvasCommandSession(
        {
            snapshot: async () => structuredClone(host),
            events: async () => [],
            receipt: async () => {
                throw { status: 404 };
            },
            execute: (command) =>
                new Promise<Receipt>((resolve, reject) => {
                    sent.push(structuredClone(command));
                    arrivals.push({
                        command,
                        finish: () => {
                            const current = (host.document.nodes as (typeof placeholder)[])[0];
                            const disjointTitleEdit = command.operations.every((op) => op.type === "update_node" && Object.keys(op.patch || {}).every((key) => key === "title") && op.fieldBaselines?.fields.title?.value === current?.title);
                            if (!disjointTitleEdit && (command.expectedEntityVersions?.["node:upload"] ?? 0) !== (host.entityVersions["node:upload"] ?? 0)) {
                                const error = Object.assign(new Error("目标实体已被其他操作修改"), { status: 409, reason: "entity_version_conflict" });
                                reject(error);
                                return undefined;
                            }
                            const node = structuredClone((host.document.nodes as (typeof placeholder)[])[0] || placeholder);
                            for (const op of command.operations) {
                                if (op.type === "bind_resource") Object.assign(node.metadata, { storageKey: "resource:" + op.resourceId, content: "/api/resources/" + op.resourceId + "/file" });
                                if (op.type === "bind_asset") node.metadata.assetId = op.assetId;
                                if (op.type === "update_node" && op.patch?.title) node.title = String(op.patch.title);
                            }
                            const event = advance(node, command.clientId, command.operationId);
                            resolve(event);
                            return event;
                        },
                    });
                    wake();
                }),
        },
        "client",
    );
    function advance(node: typeof placeholder, clientId: string, operationId: string): Receipt {
        host = {
            ...host,
            revision: host.revision + 1,
            eventSeq: host.eventSeq + 1,
            entityVersions: { "node:upload": (host.entityVersions["node:upload"] ?? 0) + 1 },
            entityGenerations: { "node:upload": 0 },
            document: { nodes: [structuredClone(node)], connections: [] },
        };
        return {
            protocolVersion: 1,
            canvasId: "canvas",
            documentEpoch: "epoch",
            revision: host.revision,
            eventSeq: host.eventSeq,
            clientId,
            operationId,
            actorId: "owner",
            entityVersions: host.entityVersions,
            entityGenerations: host.entityGenerations,
            changes: { entities: [{ key: "node:upload", value: node }] },
        };
    }
    await session.open("canvas");
    let live: any = structuredClone(host.document);
    const binding = bindCanvasCommandEditor(session, capabilities as CanvasCapabilities, {
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
        placeholder,
        next: async () => {
            if (!arrivals.length)
                await new Promise<void>((resolve) => {
                    wake = resolve;
                });
            return arrivals.shift()!;
        },
        external: async (
            clientId = "other",
            edit = (node: typeof placeholder) => {
                node.title = "Remote edit";
            },
        ) => {
            const node = structuredClone((host.document.nodes as (typeof placeholder)[])[0]);
            edit(node);
            await session.receive(advance(node, clientId, "external"));
        },
        close: () => {
            binding.dispose();
            session.close();
        },
    };
}

test("an own field-merged ACK cannot erase an intervening remote resource conflict", async () => {
    const e = await uploadEditor("video", true);
    try {
        e.live().nodes[0].title = "Local title";
        const rename = e.binding.commit();
        const first = await e.next();
        Object.assign(e.live().nodes[0].metadata, { storageKey: "resource:uploaded", content: "/api/resources/uploaded/file" });
        await e.external("other", (node) => {
            Object.assign(node.metadata, { storageKey: "resource:remote", content: "/api/resources/remote/file" });
        });
        first.finish();
        await rename;
        await e.session.flush();
        const result = e.binding.commit().catch((error) => error);
        const request = await e.next();
        request.finish();
        expect(await result).toMatchObject({ reason: "entity_version_conflict" });
        expect(request.command.expectedEntityVersions?.["node:upload"]).toBe(1);
        expect(e.live().nodes[0].metadata.storageKey).toBe("resource:uploaded");
    } finally {
        e.close();
    }
});

test("replayed own receipts cannot rebase an upload over a later remote edit", async () => {
    const e = await uploadEditor("video", true);
    try {
        e.live().nodes[0].title = "Local title";
        const rename = e.binding.commit();
        const first = await e.next();
        const receipt = first.finish()!;
        await rename;
        await e.session.flush();
        Object.assign(e.live().nodes[0].metadata, { storageKey: "resource:uploaded", content: "/api/resources/uploaded/file" });
        await e.external();
        await e.session.receive(receipt);
        const result = e.binding.commit().catch((error) => error);
        const request = await e.next();
        request.finish();
        expect(await result).toMatchObject({ reason: "entity_version_conflict" });
        expect(request.command.expectedEntityVersions?.["node:upload"]).toBe(2);
    } finally {
        e.close();
    }
});

for (const type of ["video", "image", "audio"] as const) {
    for (const existing of [false, true]) {
        test(`${type} upload keeps unsent resource and asset edits valid across its own ACKs (existing=${existing})`, async () => {
            const e = await uploadEditor(type, existing);
            try {
                if (existing) e.live().nodes[0].title = "Replacement";
                else e.live().nodes.push(structuredClone(e.placeholder));
                const creation = e.binding.commit();
                const first = await e.next();
                Object.assign(e.live().nodes[0].metadata, { storageKey: "resource:uploaded", content: "/api/resources/uploaded/file" });
                first.finish();
                await creation;
                await e.session.flush();
                const resource = e.binding.commit().catch((error) => error);
                const second = await e.next();
                expect(second.command.operations).toContainEqual({ type: "bind_resource", id: "upload", resourceId: "uploaded" });
                e.live().nodes[0].metadata.assetId = "asset-uploaded";
                second.finish();
                expect(await resource).toHaveProperty("eventSeq", 2);
                await e.session.flush();
                expect(second.command.expectedEntityVersions?.["node:upload"]).toBe(existing ? 2 : 1);
                const asset = e.binding.commit().catch((error) => error);
                const third = await e.next();
                expect(third.command.operations).toContainEqual({ type: "bind_asset", id: "upload", assetId: "asset-uploaded" });
                third.finish();
                expect(await asset).toHaveProperty("eventSeq", 3);
                expect(third.command.expectedEntityVersions?.["node:upload"]).toBe(existing ? 3 : 2);
                expect(e.session.state.saveState).toBe("saved");
                expect(e.live().nodes[0].metadata).toMatchObject({ storageKey: "resource:uploaded", assetId: "asset-uploaded" });
                await e.binding.commit();
                expect(e.sent).toHaveLength(3);
            } finally {
                e.close();
            }
        });
    }
}

for (const clientId of ["other", "client"]) {
    test(`upload must not advance unsent structural edits for unrelated events (clientId=${clientId})`, async () => {
        const e = await uploadEditor("video", true);
        try {
            Object.assign(e.live().nodes[0].metadata, { storageKey: "resource:uploaded", content: "/api/resources/uploaded/file" });
            await e.external(clientId);
            const result = e.binding.commit().catch((error) => error);
            const request = await e.next();
            request.finish();
            expect(await result).toMatchObject({ reason: "entity_version_conflict" });
            expect(request.command.expectedEntityVersions?.["node:upload"]).toBe(1);
            expect(e.live().nodes[0].metadata.storageKey).toBe("resource:uploaded");
        } finally {
            e.close();
        }
    });
}
