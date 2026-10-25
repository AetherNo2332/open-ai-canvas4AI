import { expect, test } from "bun:test";
import capabilities from "./fixtures/canvas-editor-capabilities.json";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
import { canvasPersistentEditorDraft } from "../src/lib/canvas/canvas-editor-transient";
import { applyNodeConfigPatch, createCanvasNode } from "../src/lib/canvas/canvas-project-domain";
import { CanvasNodeType } from "../src/types/canvas";
import { uploadReplacementPatch } from "../src/lib/canvas/canvas-file-upload";

test.each([CanvasNodeType.Image, CanvasNodeType.Video, CanvasNodeType.Audio, CanvasNodeType.Text])("%s replacement clears saved settings without introducing foreign fields", (type) => {
    const node = createCanvasNode(type, { x: 0, y: 0 }, { content: "old", composerContent: "old", prompt: "old", model: "old", count: 2, size: "16:9", storageKey: "resource:old" });
    const base = { id: "canvas", nodes: [node], connections: [] };
    const patch = uploadReplacementPatch(type);
    expect(Object.hasOwn(patch, "freeResize")).toBe(type === CanvasNodeType.Image);
    const next = { ...node, metadata: { ...node.metadata, ...patch, storageKey: "resource:new", content: "new" } };
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [next] }), capabilities);
    expect(ops).toContainEqual({ type: "bind_resource", id: node.id, resourceId: "new" });
    expect(ops).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ model: null, count: null, size: null }) }));
});

test("workflow configuration and template reset survive the real config-node converter", () => {
    const node = createCanvasNode(CanvasNodeType.Config, { x: 0, y: 0 }, { promptTemplateVariables: { shot: "wide" } });
    const base = { id: "canvas", nodes: [node], connections: [] };
    const changed = applyNodeConfigPatch(node, { workflowProvider: "runninghub", workflowTitle: "Workflow", workflowParameters: { seed: 42, random: true }, promptTemplateVariables: undefined });
    const operations = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [changed] }), capabilities);
    expect(operations).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ workflowTitle: "Workflow", workflowParameters: { seed: 42, random: true }, promptTemplateVariables: null }) }));
});

test("asset category is persisted by the canvas command contract", () => {
    const node = createCanvasNode(CanvasNodeType.Image, { x: 0, y: 0 });
    const base = { id: "canvas", nodes: [node], connections: [] };
    const operations = canvasEditorCommands(base, { ...base, nodes: [applyNodeConfigPatch(node, { assetCategory: "environment" })] }, capabilities);
    expect(operations).toContainEqual({ type: "update_node", id: node.id, patch: { assetCategory: "environment" } });
});

test("uploading over a copied media result retires its provenance through the resource binding", () => {
    const node = createCanvasNode(CanvasNodeType.Image, { x: 0, y: 0 }, { content: "old", storageKey: "resource:old", copiedFromNodeId: "source", versionOfNodeId: "source", imageBatchExpanded: true });
    const base = { id: "canvas", nodes: [node], connections: [] };
    const next = { ...node, metadata: { ...node.metadata, ...uploadReplacementPatch(node.type), storageKey: "resource:new", content: "new" } };
    const ops = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [next] }), capabilities);
    expect(ops).toContainEqual({ type: "bind_resource", id: node.id, resourceId: "new" });
    expect(ops.filter((op) => op.type === "update_node").some((op) => Object.hasOwn(op.patch || {}, "imageBatchExpanded"))).toBe(false);
});

test("changing an old folder theme can remove its legacy cover", () => {
    const node = createCanvasNode(CanvasNodeType.Frame, { x: 0, y: 0 }, { folder: { style: "glass", theme: "aurora", themeCover: "https://example.test/old.jpg", createdAt: "2026-10-01T00:00:00Z" } });
    const base = { id: "canvas", nodes: [node], connections: [] };
    const next = { ...node, metadata: { ...node.metadata, folder: { ...node.metadata.folder!, theme: "pearl", themeCover: undefined } } };
    expect(canvasEditorCommands(base, { ...base, nodes: [next] }, capabilities)).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ folderTheme: "pearl", folderThemeCover: null }) }));
});
