import { expect, test } from "bun:test";
import capabilities from "./fixtures/canvas-editor-capabilities.json";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
import { canvasPersistentEditorDraft } from "../src/lib/canvas/canvas-editor-transient";
import { createNineGridNode, createPortraitTextureNode } from "../src/lib/canvas/canvas-image-source";
import { applyBatchPrimaryImage, createCanvasNode } from "../src/lib/canvas/canvas-project-domain";
import { buildImageGenerationMetadata } from "../src/lib/canvas/canvas-project-generation";
import { defaultConfig } from "../src/stores/use-config-store";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";

const source: CanvasNodeData = {
    id: "source",
    type: CanvasNodeType.Image,
    title: "Source",
    position: { x: 0, y: 0 },
    width: 400,
    height: 300,
    metadata: { content: "/api/resources/source-image/file", storageKey: "resource:source-image", status: "success" },
};
const base = () => ({ id: "canvas", nodes: [structuredClone(source)], connections: [] as any[] });
function commands(child: CanvasNodeData) {
    const before = base(),
        after = { ...before, nodes: [...before.nodes, child], connections: [{ id: "edge", fromNodeId: source.id, toNodeId: child.id }] };
    return canvasEditorCommands(before, canvasPersistentEditorDraft(before, after), capabilities);
}

test.each(["crop", "annotation", "split", "upscale"])("%s image output saves its uploaded resource and inherited prompt", (name) => {
    const child = { ...source, id: name, title: name, metadata: { content: "/api/resources/output/file", storageKey: "resource:output", bytes: 123, mimeType: "image/png", prompt: "Keep identity", manualSize: true } };
    expect(commands(child)).toContainEqual({ type: "bind_resource", id: name, resourceId: "output" });
});
test("portrait and nine-grid helper nodes map through real capabilities", () => {
    for (const child of [createPortraitTextureNode(source, "portrait"), createNineGridNode(source, "nine", 1, "Grid", "image", "grid")]) expect(commands(child).some((op) => op.type === "update_node")).toBe(true);
});
test.each(["mask", "angle", "lighting", "image-edit"])("%s generation preparation keeps request snapshots out of editor writes", (name) => {
    const metadata = buildImageGenerationMetadata("edit", { ...defaultConfig, model: "model", size: "1024x1024", quality: "high" }, 1, [
        { id: source.id, name: "source", type: "image/png", dataUrl: source.metadata!.content!, storageKey: source.metadata!.storageKey },
    ]);
    const ops = commands({ ...source, id: name, metadata: { ...metadata, prompt: "Edit image", status: "loading" } });
    expect(ops).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ generationType: "edit", model: "model", prompt: "Edit image" }) }));
    expect(JSON.stringify(ops)).not.toContain("producedModelCandidate");
    expect(JSON.stringify(ops)).not.toContain('"references"');
});
test("built-in annotation and layer tools save their bounded tool context", () => {
    const ops = commands({ ...source, id: "layer", metadata: { prompt: "Split layers", pluginId: "image-tools", pluginNodeId: "layer-decomposition", pluginData: { layerIndex: 1, sourceNodeId: source.id } } });
    expect(ops).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ imageToolPlugin: "image-tools", imageToolKind: "layer-decomposition", imageToolData: { layerIndex: 1, sourceNodeId: source.id } }) }));
});
test("emotion edit regions are persisted for host admission", () => {
    const emotionEdit = {
        sourceNodeId: source.id,
        characterName: "Hero",
        presetId: "happy",
        intimacy: 1,
        arousal: 1,
        label: "Happy",
        faceBox: { id: "face", x: 1, y: 1, width: 20, height: 20, source: "manual" as const },
        editRegion: { x: 0, y: 0, width: 30, height: 30 },
        sourceWidth: 400,
        sourceHeight: 300,
        providerSize: "1024x1024",
        editMode: "provider-mask" as const,
    };
    const ops = commands({ ...source, id: "emotion", metadata: { prompt: "Smile", emotionEdit } });
    expect(ops).toContainEqual({ type: "update_node", id: "emotion", patch: expect.objectContaining({ emotionEdit }) });
});
test("reverse-prompt result nodes retain their composer draft", () => {
    const node = createCanvasNode(CanvasNodeType.Text, { x: 0, y: 0 }, { content: "", composerContent: "Analyze @图片1", generationMode: "text" });
    expect(commands(node)).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ composerContent: "Analyze @图片1" }) }));
});
test("image batch grouping, fold state and primary selection use semantic writes", () => {
    const before = base(),
        root = { ...source, id: "root", metadata: { isBatchRoot: true, batchChildIds: ["child"], imageBatchExpanded: true } },
        child = { ...source, id: "child", metadata: { ...source.metadata, batchRootId: "root" } };
    const grouped = { ...before, nodes: [...before.nodes, root, child] };
    expect(canvasEditorCommands(before, canvasPersistentEditorDraft(before, grouped), capabilities)).toContainEqual({ type: "edit_image_batch", id: "root", action: "set_children", nodeIds: ["child"] });
    const primary = applyBatchPrimaryImage(root, child);
    expect(canvasEditorCommands(grouped, canvasPersistentEditorDraft(grouped, { ...grouped, nodes: [source, primary, child] }), capabilities)).toContainEqual({ type: "edit_image_batch", id: "root", action: "select_primary", sourceNodeId: "child" });
});
