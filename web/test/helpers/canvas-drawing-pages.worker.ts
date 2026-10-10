import localforage from "localforage";

localforage.createInstance = (({ storeName }: { storeName: string }) => {
    const values = new Map<string, unknown>();
    return {
        getItem: async (key: string) => values.get(key) ?? null,
        setItem: async (key: string, value: unknown) => {
            values.set(key, structuredClone(value));
            return value;
        },
        removeItem: async (key: string) => {
            values.delete(key);
        },
    };
}) as typeof localforage.createInstance;

self.onmessage = async () => {
    try {
        const { saveCanvasDrawing, loadCanvasDrawing, markCanvasDrawingPublished, restoreCanvasDrawingDocument } = await import("../../src/lib/canvas/canvas-drawing-storage");
        const snapshot = {
            document: {
                store: {
                    "page:first": { id: "page:first", typeName: "page", index: "a1" },
                    "page:second": { id: "page:second", typeName: "page", index: "a2" },
                    "shape:first": { id: "shape:first", typeName: "shape", parentId: "page:first" },
                    "shape:second": { id: "shape:second", typeName: "shape", parentId: "page:second" },
                },
            },
        };
        const saved = await saveCanvasDrawing("project", "drawing", "tldraw", snapshot);
        const reloaded = await loadCanvasDrawing("project", "drawing");
        await markCanvasDrawingPublished("project", "drawing", saved);
        const ancestor = { ...saved, revision: 0, updatedAt: "2026-01-01T00:00:00Z", snapshot: { document: { store: {} } }, shapeCount: 0, pageCount: 1 };
        const undone = await restoreCanvasDrawingDocument("project", "drawing", ancestor);
        const draft = await saveCanvasDrawing("project", "drawing", "tldraw", snapshot, undone);
        const retainedDraft = await restoreCanvasDrawingDocument("project", "drawing", ancestor);
        self.postMessage({ saved, reloaded, undone, draft, retainedDraft });
    } catch (error) {
        self.postMessage({ error: String(error) });
    }
};
