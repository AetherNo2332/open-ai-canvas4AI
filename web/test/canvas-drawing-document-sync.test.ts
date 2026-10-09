import { expect, test } from "bun:test";
import { hydrateCanvasDrawingDocument, publishCanvasDrawingDocument, persistCanvasDrawingEditorSave, selectCanvasDrawingDocument } from "@/lib/canvas/canvas-drawing-document-sync";
import type { CanvasDrawingSnapshot } from "@/lib/canvas/canvas-drawing-storage";
import { copyCanvasNodeGraph } from "@/lib/canvas/canvas-node-copy";
import { drawingEngineForNode } from "@/lib/canvas/canvas-drawing-engine";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

const document = (snapshot: unknown, revision = 1, engine: "excalidraw" | "tldraw" = "excalidraw"): CanvasDrawingSnapshot => ({ version: 2, engine, snapshot, revision, updatedAt: `2026-10-07T10:00:0${revision}Z`, shapeCount: 1, pageCount: 1 });
const native = {
    type: "excalidraw",
    version: 2,
    source: "canvas",
    elements: [{ id: "shape", type: "image", fileId: "file", isDeleted: false }],
    appState: { viewBackgroundColor: "#ffffff" },
    files: { file: { id: "file", dataURL: "data:image/png;base64,YQ==", mimeType: "image/png", created: 1 } },
};
const drawingNode = (doc?: CanvasDrawingSnapshot): CanvasNodeData => ({
    id: "drawing",
    type: CanvasNodeType.Drawing,
    title: "绘图",
    position: { x: 0, y: 0 },
    width: 320,
    height: 240,
    metadata: { drawingId: "drawing-document", drawingEngine: "excalidraw", drawingDocument: doc },
});

test("native images are published as owned resource references and hydrated only for the editor", async () => {
    const published: string[] = [];
    const safe = await publishCanvasDrawingDocument(document(native), async (source, key) => {
        published.push(`${key}:${source}`);
        return "resource:image";
    });
    expect(published).toHaveLength(1);
    expect(JSON.stringify(safe)).not.toContain("data:image");
    expect((safe.snapshot as typeof native).files.file.dataURL).toBe("resource:image");
    expect(native.files.file.dataURL).toContain("data:image");
    const hydrated = await hydrateCanvasDrawingDocument(safe, async () => "data:image/png;base64,YQ==");
    expect(hydrated.snapshot).toEqual(native);
    expect(JSON.stringify(safe)).not.toContain("data:image");
});

test("tldraw document records remain native while embedded asset bytes and session state leave the synced payload", async () => {
    const safe = await publishCanvasDrawingDocument(
        document(
            {
                document: {
                    store: { "asset:a": { id: "asset:a", typeName: "asset", type: "image", props: { src: "data:image/png;base64,YQ==", w: 1, h: 1 } }, "shape:a": { id: "shape:a", typeName: "shape", type: "image", props: { assetId: "asset:a" } } },
                    schema: { schemaVersion: 2 },
                },
                session: { selectedShapeIds: ["shape:a"] },
            },
            1,
            "tldraw",
        ),
        async () => "resource:image",
    );
    expect((safe.snapshot as { session?: unknown }).session).toBeUndefined();
    expect(JSON.stringify(safe)).toContain("resource:image");
    const hydrated = await hydrateCanvasDrawingDocument(safe, async () => "data:image/png;base64,YQ==");
    expect(JSON.stringify(hydrated)).toContain("data:image");
});

test("publication rejects credentials, ephemeral URLs and overlarge native documents", async () => {
    const publish = async () => "resource:image";
    await expect(publishCanvasDrawingDocument(document({ ...native, token: "private" }), publish)).rejects.toThrow();
    await expect(publishCanvasDrawingDocument(document({ ...native, files: { file: { ...native.files.file, dataURL: "https://host/image?token=private" } } }), publish)).rejects.toThrow();
    await expect(publishCanvasDrawingDocument(document({ ...native, elements: [{ id: "a", text: "a".repeat(4 * 1024 * 1024) }], files: {} }), publish)).rejects.toThrow("过大");
});

test("remote native documents restore on a new machine while newer local drafts survive", () => {
    const remote = document(native, 2),
        local = document(native, 3);
    expect(selectCanvasDrawingDocument(null, remote)).toBe(remote);
    expect(selectCanvasDrawingDocument(document(native), remote)).toBe(remote);
    expect(selectCanvasDrawingDocument(local, remote)).toBe(local);
    expect(drawingEngineForNode({ metadata: { drawingEngine: "tldraw", drawingDocument: remote } })).toBe("excalidraw");
});

test("authoritative drawing undo replaces a published cache but preserves unpublished drafts", () => {
    const previous = document({ ...native, elements: [] }, 1);
    const cached = document(native, 2);
    expect(selectCanvasDrawingDocument(cached, previous, true)).toBe(previous);
    expect(selectCanvasDrawingDocument(cached, previous, false)).toBe(cached);
});

test("actual editor save publishes full document after preserving a local draft, and fences concurrent remote changes", async () => {
    const start = drawingNode(document({ ...native, files: {} }));
    let current = start;
    let local: CanvasDrawingSnapshot | null = null;
    let receipt: CanvasDrawingSnapshot | null = null;
    const saveLocal = async () => (local = document(native, 2));
    const publish = async (saved: CanvasDrawingSnapshot) => publishCanvasDrawingDocument(saved, async () => "resource:image");
    const saved = await persistCanvasDrawingEditorSave({
        node: start,
        previous: start.metadata?.drawingDocument || null,
        createSave: async () => ({ snapshot: native, preview: null, render: null }),
        saveLocal,
        publish,
        currentNode: () => current,
        onSaved: (doc) => {
            receipt = doc;
        },
    });
    expect(local?.snapshot).toBe(native);
    expect(receipt).toBe(saved);
    expect(JSON.stringify(receipt)).toContain("resource:image");
    receipt = null;
    await expect(
        persistCanvasDrawingEditorSave({
            node: start,
            previous: local,
            createSave: async () => ({ snapshot: native, preview: null, render: null }),
            saveLocal,
            publish: async (saved) => {
                current = drawingNode(document({ ...native, elements: [] }, 3));
                return publish(saved);
            },
            currentNode: () => current,
            onSaved: (doc) => {
                receipt = doc;
            },
        }),
    ).rejects.toThrow("冲突");
    expect(receipt).toBeNull();
    expect(local).not.toBeNull();
});

test("asset upload failure preserves the native local draft without advancing synced metadata", async () => {
    const node = drawingNode();
    let local: CanvasDrawingSnapshot | null = null;
    let notified = false;
    await expect(
        persistCanvasDrawingEditorSave({
            node,
            previous: null,
            createSave: async () => ({ snapshot: native, preview: null, render: null }),
            saveLocal: async () => (local = document(native)),
            publish: async () => {
                throw new Error("network unavailable");
            },
            currentNode: () => node,
            onSaved: () => {
                notified = true;
            },
        }),
    ).rejects.toThrow("network unavailable");
    expect(local?.snapshot).toBe(native);
    expect(notified).toBe(false);
});

test("a document arriving after an empty editor opened is a conflict even before save starts", async () => {
    const current = drawingNode(document(native, 2));
    let notified = false;
    await expect(
        persistCanvasDrawingEditorSave({
            node: current,
            expectedDocument: undefined,
            previous: null,
            createSave: async () => ({ snapshot: { ...native, elements: [] }, preview: null, render: null }),
            saveLocal: async () => document({ ...native, elements: [] }, 3),
            publish: async (saved) => saved,
            currentNode: () => current,
            onSaved: () => {
                notified = true;
            },
        }),
    ).rejects.toThrow("冲突");
    expect(notified).toBe(false);
});

test("drawing copies retain independent document identities and full content", () => {
    const source = drawingNode(document({ ...native, files: {} }, 4));
    const copied = copyCanvasNodeGraph([source], [], source.id, new Map([[source.id, "copy"]]), () => "edge").nodes[0];
    expect(copied.metadata?.drawingId).toBe("copy-document");
    expect(copied.metadata?.drawingDocument?.snapshot).toEqual(source.metadata?.drawingDocument?.snapshot);
    expect(copied.metadata?.drawingRevision).toBe(copied.metadata?.drawingDocument?.revision);
});

test("copy publication uses the cloned native draft and never stamps stale synced content with its revision", async () => {
    const target = drawingNode(document({ ...native, elements: [] }, 1));
    const cloned = document(native, 2);
    let receipt: CanvasDrawingSnapshot | undefined;
    let marked: CanvasDrawingSnapshot | undefined;
    await persistCanvasDrawingEditorSave({
        node: target,
        previous: cloned,
        createSave: async () => ({ snapshot: cloned.snapshot, preview: null, render: null }),
        saveLocal: async () => cloned,
        publish: (saved) => publishCanvasDrawingDocument(saved, async () => "resource:image"),
        currentNode: () => target,
        onSaved: (document) => {
            receipt = document;
        },
        markPublished: async (document) => {
            marked = document;
        },
    });
    expect((receipt?.snapshot as typeof native).elements).toHaveLength(1);
    expect((receipt?.snapshot as typeof native).files.file.dataURL).toBe("resource:image");
    expect(marked).toBe(receipt);
});
