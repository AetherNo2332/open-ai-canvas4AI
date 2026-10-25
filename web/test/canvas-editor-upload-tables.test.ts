import { expect, test } from "bun:test";
import hostCapabilities from "./fixtures/canvas-editor-capabilities.json";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
import { canvasPersistentEditorDraft, carryCanvasTaskDisplay } from "../src/lib/canvas/canvas-editor-transient";
import { createCanvasNode } from "../src/lib/canvas/canvas-project-domain";
import { syncBatchColumnReferences } from "../src/lib/canvas/canvas-batch-references";
import { CanvasNodeType } from "../src/types/canvas";
import { copyCanvasNodeGraph } from "../src/lib/canvas/canvas-node-copy";

const capabilities = hostCapabilities;
const empty = () => ({ id: "canvas", nodes: [] as any[], connections: [] });

test("a real persistent draft still recognizes text copies after stripping display metadata", () => {
    const node = createCanvasNode(CanvasNodeType.Text, { x: 0, y: 0 }, { content: "Body", status: "success", producedModel: "model" });
    const base = { ...empty(), nodes: [node] };
    const copy = copyCanvasNodeGraph([node], [], node.id, new Map([[node.id, "copy"]]), () => "edge").nodes[0];
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node, copy] }), hostCapabilities);
    expect(ops[0]).toMatchObject({ type: "add_node", id: "copy", copiedFromNodeId: node.id, content: "Body" });
});

test.each([CanvasNodeType.Image, CanvasNodeType.Video, CanvasNodeType.Audio])("persistent %s copies preserve saved media through source provenance", (type) => {
    const node = createCanvasNode(type, { x: 0, y: 0 }, { content: "/api/resources/source/file", storageKey: "resource:source", status: "success", bytes: 123, mimeType: `${type}/example`, mediaVersion: { version: 1 } as any });
    const base = { ...empty(), nodes: [node] };
    const copy = copyCanvasNodeGraph([node], [], node.id, new Map([[node.id, "copy"]]), () => "edge").nodes[0];
    copy.metadata.content = "blob:local-media-preview";
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node, copy] }), hostCapabilities);
    expect(ops.filter((op) => op.type === "add_node")).toEqual([expect.objectContaining({ type: "add_node", id: "copy", nodeType: type, copiedFromNodeId: node.id })]);
    expect(ops.some((op) => op.type === "bind_resource")).toBe(false);
});

test.each(["image", "video", "audio", "text"])("%s upload progress never blocks unrelated canvas saves and survives host receipts", (type) => {
    const base = { ...empty(), nodes: [{ id: "existing", type: "text", title: "Old", metadata: {} }] };
    const live = {
        ...base,
        nodes: [
            { ...base.nodes[0], title: "Edited" },
            { id: "upload", type, metadata: { fileUpload: "uploading", fileUploadProgress: 50, bytes: 42, mimeType: `${type}/example` } },
        ],
    };
    const persistent = canvasPersistentEditorDraft(base, live);
    expect(canvasEditorCommands(base, persistent, capabilities)).toEqual([
        { type: "add_node", id: "upload", nodeType: type },
        { type: "update_node", id: "existing", patch: { title: "Edited" } },
    ]);
    const receipt = { ...persistent, nodes: structuredClone(persistent.nodes) };
    expect((carryCanvasTaskDisplay(base, live, receipt).nodes as any[])[1].metadata.fileUploadProgress).toBe(50);
    expect(live.nodes[1].metadata.fileUpload).toBe("uploading");
});

test.each(["image", "video", "audio"])("completed %s upload binds its resource before its optional asset is available", (type) => {
    const base = { ...empty(), nodes: [{ id: "upload", type, metadata: {} }] };
    const done = { ...base, nodes: [{ id: "upload", type, metadata: { content: "/api/resources/media-1/file", storageKey: "resource:media-1", bytes: 123, mimeType: `${type}/example` } }] };
    expect(canvasEditorCommands(base, canvasPersistentEditorDraft(base, done), capabilities)).toEqual([{ type: "bind_resource", id: "upload", resourceId: "media-1" }]);
    const host = { ...done, nodes: [{ ...done.nodes[0], metadata: { ...done.nodes[0].metadata, status: "success" } }] };
    const linked = { ...host, nodes: [{ ...host.nodes[0], metadata: { ...host.nodes[0].metadata, assetId: "asset-1" } }] };
    expect(canvasEditorCommands(host, canvasPersistentEditorDraft(host, linked), capabilities)).toEqual([{ type: "bind_asset", id: "upload", assetId: "asset-1" }]);
});

test("resolved display URLs for the same resource never create another media binding", () => {
    const base = { ...empty(), nodes: [{ id: "image", type: "image", metadata: { content: "/api/resources/media-1/file", storageKey: "resource:media-1" } }] };
    const display = { ...base, nodes: [{ ...base.nodes[0], metadata: { ...base.nodes[0].metadata, content: "https://cdn.example/media.png?token=display" } }] };
    expect(canvasEditorCommands(base, canvasPersistentEditorDraft(base, display), hostCapabilities)).toEqual([]);
});

test("uploaded text retains its body and source prompt while binding the original file", () => {
    const base = empty();
    const node = { id: "text-upload", type: "text", metadata: { content: "Text body", prompt: "Text body", storageKey: "resource:text-file", bytes: 9, mimeType: "text/plain" } };
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node] }), hostCapabilities);
    expect(ops).toContainEqual({ type: "add_node", id: "text-upload", nodeType: "text", content: "Text body" });
    expect(ops).toContainEqual({ type: "bind_resource", id: "text-upload", resourceId: "text-file" });
    expect(ops).toContainEqual({ type: "update_node", id: "text-upload", patch: { prompt: "Text body" } });
});

test("manual storyboard creation sends blank draft rows instead of Agent append validation", () => {
    const base = empty();
    const node = createCanvasNode(CanvasNodeType.Script, { x: 0, y: 0 });
    const operations = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node] }), capabilities);
    const rows = operations.filter((op) => op.type === "edit_storyboard");
    expect(rows).toHaveLength(3);
    expect(rows.every((op) => op.action === "append_draft" && op.patch?.durationSeconds === 6)).toBe(true);
});

test("a real new batch table with synchronized reference columns saves its configuration", () => {
    const base = empty();
    const nodes = syncBatchColumnReferences([createCanvasNode(CanvasNodeType.BatchTable, { x: 0, y: 0 })], []);
    const operations = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes }), capabilities);
    expect(operations).toContainEqual({
        type: "edit_batch_table",
        id: nodes[0].id,
        action: "configure",
        patch: {
            operation: "try_on",
            concurrency: 10,
            referenceColumns: [
                { id: "reference-1", label: "参考图 1" },
                { id: "reference-2", label: "参考图 2" },
                { id: "reference-3", label: "参考图 3" },
            ],
            referenceMode: "cells",
            referenceColumnInputs: { "reference-1": [], "reference-2": [], "reference-3": [] },
        },
    });
});

test("batch configuration edits and explicit empty cell overrides are persisted together", () => {
    const base = { ...empty(), nodes: [{ id: "batch", type: "batch-table", metadata: { batchTable: { operation: "try_on", concurrency: 10, rows: [{ id: "row", enabled: true, inputNodeIds: [""], prompt: "" }] } } }] };
    const live = structuredClone(base) as any;
    live.nodes[0].metadata.batchTable.concurrency = 5;
    live.nodes[0].metadata.batchTable.globalPrompt = "Create";
    live.nodes[0].metadata.batchTable.rows[0].referenceOverrides = ["reference-1"];
    expect(canvasEditorCommands(base, live, capabilities)).toEqual([
        { type: "edit_batch_table", id: "batch", action: "configure", patch: { concurrency: 5, globalPrompt: "Create" } },
        { type: "edit_batch_table", id: "batch", action: "update", rowId: "row", patch: { referenceOverrides: ["reference-1"] } },
    ]);
});

test("unmapped authored fields still fail visibly", () => {
    const base = { ...empty(), nodes: [{ id: "image", type: "image", metadata: {} }] };
    expect(() => canvasEditorCommands(base, { ...base, nodes: [{ ...base.nodes[0], metadata: { unknownField: true } }] }, capabilities)).toThrow("unknownField");
});

test("asset-library image insertion preserves the prompt and tags through declared fields", () => {
    const base = empty();
    const node = {
        id: "library-image",
        type: "image",
        title: "Library image",
        position: { x: 0, y: 0 },
        width: 400,
        height: 300,
        metadata: {
            content: "/api/resources/library-resource/file",
            storageKey: "resource:library-resource",
            assetId: "library-asset",
            bytes: 123,
            mimeType: "image/png",
            prompt: "A library scene",
            assetTags: ["scene"],
            status: "success",
        },
    };
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node] }), hostCapabilities);
    expect(ops).toContainEqual({ type: "bind_asset", id: "library-image", assetId: "library-asset" });
    expect(ops).toContainEqual({ type: "update_node", id: "library-image", patch: { width: 400, height: 300, prompt: "A library scene", assetTags: ["scene"] } });
});

test.each(Object.values(CanvasNodeType))("new %s factory defaults are covered by the real host capability contract", (type) => {
    const base = empty();
    const node = createCanvasNode(type, { x: 0, y: 0 });
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node] }), hostCapabilities);
    expect(ops[0]).toMatchObject({ type: "add_node", id: node.id, nodeType: type });
});

test("folder creation persists its timestamp and collapsed geometry", () => {
    const base = empty();
    const node = createCanvasNode(CanvasNodeType.Frame, { x: 0, y: 0 }, { frame: { collapsed: true, expandedWidth: 760, expandedHeight: 520 }, folder: { style: "glass", theme: "aurora", createdAt: "2026-10-25T12:00:00Z" } });
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node] }), hostCapabilities);
    expect(ops).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ folderCreatedAt: "2026-10-25T12:00:00Z", frame: { collapsed: true, expandedWidth: 760, expandedHeight: 520 } }) }));
});

test("project-linked folders preserve the project and asset-folder identities", () => {
    const base = empty();
    const node = createCanvasNode(CanvasNodeType.Frame, { x: 0, y: 0 }, { folder: { style: "glass", theme: "aurora", createdAt: "2026-10-25T12:00:00Z", projectId: "project", assetFolderId: "asset-folder" } });
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node] }), hostCapabilities);
    expect(ops).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ folderProjectId: "project", folderAssetFolderId: "asset-folder" }) }));
});

test("character creation binds asset identity and lets the host reconstruct character snapshots", () => {
    const base = empty();
    const node = {
        id: "character",
        type: "text",
        title: "Hero",
        position: { x: 0, y: 0 },
        width: 320,
        height: 260,
        metadata: {
            workflowKind: "character",
            assetId: "hero-asset",
            characterAssetId: "hero-asset",
            characterVersionId: "v1",
            characterVersionPolicy: "current",
            characterName: "Hero",
            characterPrompt: "【角色卡：Hero】\nA coat",
            characterAliases: ["H"],
            characterDefinition: { aliases: ["H"], clothing: "A coat", appearance: "Short hair", consistencyPrompt: "Keep identity" },
            characterCoverUrl: "/api/resources/hero-image/file",
            characterVisualStatus: "ready",
            characterVoiceStatus: "missing",
            status: "success",
            fontSize: 14,
        },
    };
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node] }), hostCapabilities);
    expect(ops).toContainEqual({ type: "bind_character", id: "character", assetId: "hero-asset", versionId: "v1", action: "current" });
    expect(JSON.stringify(ops)).not.toContain("characterDefinition");
    expect(ops.filter((op) => op.type === "bind_asset")).toHaveLength(0);
});
