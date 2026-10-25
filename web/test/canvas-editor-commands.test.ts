import { expect, test } from "bun:test";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
import { copyCanvasNodeGraph, isolateCopiedNodeMetadata } from "../src/lib/canvas/canvas-node-copy";
import { applyNodeConfigPatch } from "../src/lib/canvas/canvas-project-domain";
const capabilities = {
    protocolVersion: 1,
    nodes: [
        { type: "text", patchFields: { title: { path: "title" }, x: { path: "position.x" }, y: { path: "position.y" }, content: { path: "metadata.content" }, locked: { path: "metadata.locked" }, width: { path: "width" }, height: { path: "height" } } },
        { type: "script", patchFields: { title: { path: "title" } } },
    ],
    structuredFields: { storyboard: { dialogue: {}, durationSeconds: {} }, batch: { prompt: {}, enabled: {} } },
};
const before = () => ({
    id: "canvas",
    nodes: [
        { id: "a", type: "text", title: "A", position: { x: 0, y: 0 }, metadata: { content: "old", extension: "keep" } },
        { id: "board", type: "script", title: "board", metadata: { storyboard: { rows: [{ id: "row", dialogue: "old", durationSeconds: 5, extension: "preserve" }] } } },
    ],
    connections: [],
});
test("actual editor node and row changes map to canonical field and native row operations", () => {
    const source = before(),
        next = structuredClone(source);
    next.nodes[0].metadata.content = "draft";
    next.nodes[0].position.x = 20;
    next.nodes[1].metadata.storyboard.rows[0].dialogue = "new";
    const operations = canvasEditorCommands(source, next, capabilities as any);
    expect(operations).toEqual([
        { type: "update_node", id: "a", patch: { x: 20, content: "draft" } },
        { type: "edit_storyboard", id: "board", action: "update", rowId: "row", patch: { dialogue: "new" } },
    ]);
    expect(source.nodes[0].metadata.content).toBe("old");
    expect(JSON.stringify(operations)).not.toContain("extension");
});
test("graph add delete parent and reorder use semantic operations rather than entity replacement", () => {
    const source = before(),
        next = structuredClone(source) as any;
    next.nodes = next.nodes.filter((n: any) => n.id !== "board");
    next.nodes.push({ id: "new", type: "text", title: "New", position: { x: 10, y: 30 }, metadata: { content: "hello" } });
    next.nodes[0].parentId = "new";
    next.nodes.reverse();
    next.connections.push({ id: "edge", fromNodeId: "a", toNodeId: "new" });
    const ops = canvasEditorCommands(source, next, capabilities as any);
    expect(ops).toContainEqual({ type: "delete_node", id: "board" });
    expect(ops).toContainEqual({ type: "add_node", id: "new", nodeType: "text", x: 10, y: 30, title: "New", content: "hello" });
    expect(ops).toContainEqual({ type: "set_parent", id: "a", parentId: "new" });
    expect(ops).toContainEqual({ type: "connect_nodes", id: "edge", fromNodeId: "a", toNodeId: "new" });
    expect(ops.at(-1)).toEqual({ type: "reorder_nodes", id: "canvas", nodeIds: ["new", "a"] });
});
test("unmapped editing fields are surfaced with their paths instead of silently lost", () => {
    const source = before(),
        next = structuredClone(source) as any;
    next.nodes[0].metadata.extension = "changed";
    expect(() => canvasEditorCommands(source, next, capabilities as any)).toThrow("canvas_editor_unmapped:node:a:metadata.extension");

    const provenanceEdit = structuredClone(source) as any;
    provenanceEdit.nodes[0].metadata.copiedFromNodeId = "board";
    expect(() => canvasEditorCommands(source, provenanceEdit, capabilities as any)).toThrow("canvas_editor_unmapped:node:a:metadata.copiedFromNodeId");
});
test("object-valued node fields ignore key order but preserve array edits and reject unknown changes", () => {
    const richText = {
        type: "doc",
        content: [
            { type: "paragraph", attrs: { align: "left", indent: 0 } },
            { type: "paragraph", attrs: { align: "right", indent: 1 } },
        ],
    };
    const source = { id: "canvas", nodes: [{ id: "a", type: "text", metadata: { richText, extension: { first: 1, second: 2 } } }], connections: [] };
    const next = {
        ...source,
        nodes: [{ ...source.nodes[0], metadata: { extension: { second: 2, first: 1 }, richText: { content: richText.content.map((entry) => ({ attrs: { indent: entry.attrs.indent, align: entry.attrs.align }, type: entry.type })), type: "doc" } } }],
    };
    const caps = { protocolVersion: 1, nodes: [{ type: "text", patchFields: { richText: { path: "metadata.richText" } } }], structuredFields: {} };
    expect(canvasEditorCommands(source, next, caps)).toEqual([]);
    next.nodes[0].metadata.richText.content.reverse();
    expect(canvasEditorCommands(source, next, caps)).toEqual([{ type: "update_node", id: "a", patch: { richText: next.nodes[0].metadata.richText } }]);
    next.nodes[0].metadata.extension.first = 3;
    expect(() => canvasEditorCommands(source, next, caps)).toThrow("canvas_editor_unmapped:node:a:metadata.extension.first");
});
test("shared appearance uses a small settings patch and viewport stays personal", () => {
    const source = { ...before(), appearance: { mode: "light", extension: "keep" }, backgroundMode: "blank", viewport: { x: 0, y: 0, k: 1 } },
        next = structuredClone(source);
    next.appearance.mode = "dark";
    next.viewport.x = 20;
    expect(canvasEditorCommands(source, next, capabilities as any)).toEqual([{ type: "edit_canvas_settings", id: "canvas", patch: { appearance: { mode: "dark" } } }]);
    const edges = {
        ...before(),
        connections: [
            { id: "a", fromNodeId: "a", toNodeId: "board" },
            { id: "b", fromNodeId: "board", toNodeId: "a" },
        ],
    };
    const order = { ...edges, connections: [...edges.connections].reverse() };
    expect(canvasEditorCommands(edges, order, capabilities as any)).toEqual([{ type: "reorder_connections", id: "canvas", nodeIds: ["b", "a"] }]);
});
test("new node creative options use the registry instead of dropping whole metadata branches", () => {
    const source = before(),
        next = structuredClone(source) as any;
    next.nodes.push({ id: "new", type: "text", title: "New", position: { x: 0, y: 0 }, metadata: { content: "new", locked: true, status: "idle" } });
    expect(canvasEditorCommands(source, next, capabilities as any)).toContainEqual({ type: "update_node", id: "new", patch: { locked: true } });
});
test("new text copies carry copy provenance through the add command", () => {
    const source = {
        id: "canvas",
        nodes: [{ id: "source", type: "text", title: "Source", position: { x: 10, y: 20 }, width: 360, height: 220, metadata: { content: "hello" } }],
        connections: [],
    };
    const copied = copyCanvasNodeGraph(source.nodes as any, [], "source", new Map([["source", "copy"]]), () => "edge").nodes[0];
    const next = { ...source, nodes: [...source.nodes, copied] };

    expect(canvasEditorCommands(source, next, capabilities as any)).toEqual([
        { type: "add_node", id: "copy", nodeType: "text", x: 46, y: 56, title: "Source_copy1", content: "hello", copiedFromNodeId: "source" },
        { type: "update_node", id: "copy", patch: { width: 360, height: 220 } },
    ]);
});
test("generated text copies preserve prompt metadata through copy provenance", () => {
    const source = {
        id: "canvas",
        nodes: [
            {
                id: "source",
                type: "text",
                title: "Source",
                position: { x: 10, y: 20 },
                width: 360,
                height: 220,
                metadata: {
                    content: "answer",
                    prompt: "question resource",
                    composerContent: "question @图片1",
                    model: "gpt",
                    generationMode: "text",
                    status: "success",
                    taskId: "completed-task",
                    extension: { keep: true },
                },
            },
        ],
        connections: [],
    };
    const copied = copyCanvasNodeGraph(source.nodes as any, [], "source", new Map([["source", "copy"]]), () => "edge").nodes[0];
    const next = { ...source, nodes: [...source.nodes, copied] };

    expect(canvasEditorCommands(source, next, capabilities as any)).toEqual([
        { type: "add_node", id: "copy", nodeType: "text", x: 46, y: 56, title: "Source_copy1", content: "answer", copiedFromNodeId: "source" },
        { type: "update_node", id: "copy", patch: { width: 360, height: 220 } },
    ]);

    const altered = structuredClone(next) as any;
    altered.nodes.at(-1).metadata.prompt = "changed after copying";
    expect(() => canvasEditorCommands(source, altered, capabilities as any)).toThrow("metadata.copiedFromNodeId");
});
test("multiple text copies of one source keep their own remapped self references", () => {
    const source = {
        id: "canvas",
        nodes: [{ id: "source", type: "text", title: "Source", position: { x: 10, y: 20 }, width: 360, height: 220, metadata: { content: "hello", referenceSetId: "source" } }],
        connections: [],
    };
    const first = copyCanvasNodeGraph(source.nodes as any, [], "source", new Map([["source", "copy-a"]]), () => "edge-a").nodes[0];
    const second = copyCanvasNodeGraph([...source.nodes, first] as any, [], "source", new Map([["source", "copy-b"]]), () => "edge-b").nodes[0];
    const next = { ...source, nodes: [...source.nodes, first, second] };

    expect(canvasEditorCommands(source, next, capabilities as any)).toEqual([
        { type: "add_node", id: "copy-a", nodeType: "text", x: 46, y: 56, title: "Source_copy1", content: "hello", copiedFromNodeId: "source" },
        { type: "add_node", id: "copy-b", nodeType: "text", x: 46, y: 56, title: "Source_copy2", content: "hello", copiedFromNodeId: "source" },
        { type: "update_node", id: "copy-a", patch: { width: 360, height: 220 } },
        { type: "update_node", id: "copy-b", patch: { width: 360, height: 220 } },
    ]);
});
test("text copies remap declared peer references without rewriting opaque NodeId extensions", () => {
    const source = {
        id: "canvas",
        nodes: [
            {
                id: "source-a",
                type: "text",
                title: "A",
                position: { x: 10, y: 20 },
                metadata: {
                    content: "A",
                    pluginNodeId: "source-a",
                    extension: { peerNodeId: "source-a" },
                    videoFrameSourceNodeId: "source-b",
                    batchInputNodeIds: ["source-a", "source-b", "external"],
                    generationSpec: { referenceBindings: [{ nodeId: "source-b" }] },
                    inputBindings: [{ nodeId: "source-b" }],
                },
            },
            { id: "source-b", type: "text", title: "B", position: { x: 100, y: 20 }, metadata: { content: "B" } },
        ],
        connections: [],
    };
    const copyIDs = new Map([
        ["source-a", "copy-a"],
        ["source-b", "copy-b"],
    ]);
    const copyA = {
        ...copyCanvasNodeGraph(source.nodes as any, [], "source-a", copyIDs, () => "edge-a").nodes[0],
        metadata: isolateCopiedNodeMetadata(source.nodes[0] as any, copyIDs),
    };
    const copyB = {
        ...copyCanvasNodeGraph(source.nodes as any, [], "source-b", copyIDs, () => "edge-b").nodes[0],
        metadata: isolateCopiedNodeMetadata(source.nodes[1] as any, copyIDs),
    };
    const next = { ...source, nodes: [...source.nodes, copyA, copyB] };

    expect(copyA.metadata?.pluginNodeId).toBe("source-a");
    expect((copyA.metadata?.extension as any).peerNodeId).toBe("source-a");
    expect(copyA.metadata?.videoFrameSourceNodeId).toBe("copy-b");
    expect(copyA.metadata?.batchInputNodeIds).toEqual(["copy-a", "copy-b", "external"]);
    expect(canvasEditorCommands(source, next, capabilities as any)).toEqual([
        { type: "add_node", id: "copy-a", nodeType: "text", x: 46, y: 56, title: "A_copy1", content: "A", copiedFromNodeId: "source-a" },
        { type: "add_node", id: "copy-b", nodeType: "text", x: 136, y: 56, title: "B_copy1", content: "B", copiedFromNodeId: "source-b" },
    ]);
});
test("text copy commits fail closed before command chunking can split provenance", () => {
    const source = {
        id: "canvas",
        nodes: [{ id: "source", type: "text", title: "Source", position: { x: 0, y: 0 }, metadata: { content: "hello" } }],
        connections: [],
    };
    const copies = Array.from({ length: 101 }, (_, index) => copyCanvasNodeGraph(source.nodes as any, [], "source", new Map([["source", `copy-${index}`]]), () => `edge-${index}`, `Copy ${index}`).nodes[0]);

    expect(() => canvasEditorCommands(source, { ...source, nodes: [...source.nodes, ...copies] }, capabilities as any)).toThrow("canvas_editor_copy_batch_too_large:101");
});
test("new media copies remain fail-closed instead of being reinterpreted as generation prompts", () => {
    const mediaCapabilities = {
        ...capabilities,
        nodes: [...capabilities.nodes, { type: "image", patchFields: { generationResultPlacement: { path: "metadata.generationResultPlacement" } } }],
    };
    const source = {
        id: "canvas",
        nodes: [{ id: "source", type: "image", title: "Source", position: { x: 10, y: 20 }, metadata: { content: "resource:image" } }],
        connections: [],
    };
    const copied = copyCanvasNodeGraph(source.nodes as any, [], "source", new Map([["source", "copy"]]), () => "edge").nodes[0];
    const next = { ...source, nodes: [...source.nodes, copied] };

    expect(() => canvasEditorCommands(source, next, mediaCapabilities as any)).toThrow("canvas_editor_unmapped:node:copy:metadata.copiedFromNodeId");
});
test("native anchors and structured output bindings travel through connection commands", () => {
    const source = before(),
        next = structuredClone(source) as any;
    next.nodes[1].metadata.storyboard.rows[0].imageNodeId = "a";
    next.connections.push({ id: "output", fromNodeId: "board", toNodeId: "a", fromHandleId: "row:row", fromAnchorRatio: 0.3, toAnchorRatio: 0.7, relation: "storyboard-output", storyboardRowId: "row" });
    const ops = canvasEditorCommands(source, next, capabilities as any);
    expect(ops).toEqual([{ type: "connect_nodes", ...next.connections[0] }]);
    const detached = structuredClone(next);
    detached.connections = [];
    delete detached.nodes[1].metadata.storyboard.rows[0].imageNodeId;
    expect(canvasEditorCommands(next, detached, capabilities as any)).toEqual([{ type: "delete_connection", id: "output" }]);
    const forged = structuredClone(source) as any;
    forged.nodes[1].metadata.storyboard.rows[0].imageNodeId = "a";
    expect(() => canvasEditorCommands(source, forged, capabilities as any)).toThrow("imageNodeId");
});
test("timeline edits map to native track and clip operations while retaining extensions", () => {
    const caps = { ...capabilities, timelineFields: { track: { kind: "kind", label: "string", order: "number" }, clip: { kind: "kind", nodeId: "string", trackId: "string", startMs: "number", durationMs: "positive", title: "string" } } };
    const source = {
        ...before(),
        timeline: {
            version: 2,
            tracks: [{ id: "track", kind: "video", label: "视频", order: 0, extension: "keep" }],
            clips: [{ id: "clip", kind: "video", nodeId: "a", trackId: "track", startMs: 0, durationMs: 1000, extension: "keep" }],
            durationMs: 1000,
        },
    };
    const next = structuredClone(source);
    next.timeline.clips[0].startMs = 1200;
    next.timeline.durationMs = 2200;
    expect(canvasEditorCommands(source, next, caps as any)).toEqual([{ type: "edit_timeline", id: "canvas", timelineOperations: [{ type: "upsert_clip", id: "clip", patch: { startMs: 1200 } }] }]);
    const missing = { ...next, timeline: undefined };
    expect(canvasEditorCommands(source, missing, caps as any)).toEqual([{ type: "edit_timeline", id: "canvas", timelineOperations: [{ type: "clear" }] }]);
    next.timeline.clips[0].extension = "changed";
    expect(() => canvasEditorCommands(source, next, caps as any)).toThrow("extension");
});
test("drawing saves publish native records and explicit layer order instead of metadata replacement", () => {
    const caps = { ...capabilities, nodes: [...capabilities.nodes, { type: "drawing", patchFields: { drawingEngine: { path: "metadata.drawingEngine" } } }] };
    const source = { id: "canvas", nodes: [{ id: "drawing", type: "drawing", metadata: { drawingId: "document", drawingEngine: "excalidraw" } }], connections: [] } as any;
    const next = structuredClone(source),
        shape = { id: "shape", type: "rectangle", x: 0, y: 0, width: 40, height: 30 };
    next.nodes[0].metadata.drawingDocument = { version: 2, engine: "excalidraw", snapshot: { elements: [shape], files: {}, appState: {} }, revision: 3, updatedAt: "local", shapeCount: 1, pageCount: 1 };
    next.nodes[0].metadata.drawingRevision = 3;
    next.nodes[0].metadata.drawingShapeCount = 1;
    const ops = canvasEditorCommands(source, next, caps as any);
    expect(ops).toEqual([
        { type: "edit_drawing", id: "drawing", engine: "excalidraw", drawingId: "document", drawingSnapshot: { elements: [], files: {}, appState: {} }, drawingOperations: [{ type: "upsert", id: "shape", record: shape }], drawingElementIds: ["shape"] },
    ]);
    const changed = structuredClone(next);
    changed.nodes[0].metadata.drawingDocument.snapshot.elements[0].x = 20;
    expect(canvasEditorCommands(next, changed, caps as any)[0].drawingOperations).toEqual([{ type: "upsert", id: "shape", record: { ...shape, x: 20 } }]);
});
test("editor-drawn managed edges on a legacy canvas leave the field operation to the host gate", () => {
    const source = before(),
        next = structuredClone(source) as any;
    // A legacy canvas has no referenceFieldsVersion; the host answers the field edit with reference_migration_required.
    next.connections.push({ id: "drawn", fromNodeId: "a", toNodeId: "board", toHandleId: "storyboard:context", managedBy: "reference-fields-v1", referenceOwnerNodeId: "board", referenceBindingId: "node:a:source-text" });
    expect(canvasEditorCommands(source, next, capabilities as any)).toEqual([]);
    const removed = structuredClone(next);
    removed.connections = [];
    expect(canvasEditorCommands(next, removed, capabilities as any)).toEqual([]);
});
// A generated image as the host stores it after applying its spec: flat mirrors included.
const generatedImage = (): any => ({
    id: "image",
    type: "image",
    title: "image",
    position: { x: 0, y: 0 },
    width: 720,
    height: 405,
    metadata: {
        content: "/api/resources/result/file",
        status: "success",
        generationMode: "image",
        prompt: "雨夜街道",
        composerContent: "雨夜街道",
        size: "16:9",
        count: 1,
        channelId: "relay",
        channelModelKey: "image-a",
        model: "relay::image-a",
        generationSpec: {
            version: 1,
            mode: "image",
            prompt: "雨夜街道",
            modelSelection: { kind: "channel", channelId: "relay", modelKey: "image-a" },
            options: { size: "16:9", count: 1 },
            referenceBindings: [{ id: "node:character", nodeId: "character", mediaType: "image", role: "reference", order: 0, resolution: "latest" }],
            textInputMode: "append-sources",
        },
    },
});
const imageCapabilities = {
    protocolVersion: 1,
    nodes: [
        {
            type: "image",
            patchFields: {
                content: { path: "metadata.composerContent" },
                model: { path: "metadata.model" },
                size: { path: "metadata.size" },
                quality: { path: "metadata.quality" },
                count: { path: "metadata.count" },
                transparentBackground: { path: "metadata.transparentBackground" },
                generationSpec: { path: "metadata.generationSpec" },
            },
        },
    ],
    structuredFields: {},
};
test("switching a generated image to another model sends the spec without its host-derived mirrors", () => {
    const source = { id: "canvas", nodes: [generatedImage()], connections: [] };
    const next = { ...source, nodes: [applyNodeConfigPatch(generatedImage(), { model: "relay::image-b", size: "16:9" })] };
    const spec = { ...generatedImage().metadata.generationSpec, modelSelection: { kind: "channel", channelId: "relay", modelKey: "image-b" }, options: { size: "16:9" } };
    // The host rewrites channel mirrors and drops the cleared count when it applies the spec.
    expect(canvasEditorCommands(source, next, imageCapabilities as any)).toEqual([{ type: "update_node", id: "image", patch: { model: "relay::image-b", generationSpec: spec } }]);
});
test("generation mirrors that disagree with the applied spec still fail visibly", () => {
    const source = { id: "canvas", nodes: [generatedImage()], connections: [] };
    const erased = applyNodeConfigPatch(generatedImage(), { model: "other::image-b" });
    delete erased.metadata.channelId;
    delete erased.metadata.channelModelKey;
    expect(() => canvasEditorCommands(source, { ...source, nodes: [erased] }, imageCapabilities as any)).toThrow("canvas_editor_unmapped:node:image:metadata.channelId,metadata.channelModelKey");
    const mirrorOnly = generatedImage();
    mirrorOnly.metadata.channelModelKey = "image-b";
    expect(() => canvasEditorCommands(source, { ...source, nodes: [mirrorOnly] }, imageCapabilities as any)).toThrow("canvas_editor_unmapped:node:image:metadata.channelModelKey");
    const cleared = generatedImage();
    delete cleared.metadata.count;
    cleared.metadata.generationSpec.prompt = "改写";
    cleared.metadata.composerContent = "改写";
    // The spec still carries count, so clearing its mirror is a real edit the host must judge.
    expect(canvasEditorCommands(source, { ...source, nodes: [cleared] }, imageCapabilities as any)[0]).toMatchObject({ patch: { count: null } });
});
