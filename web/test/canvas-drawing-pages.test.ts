import { expect, test } from "bun:test";
import type { CanvasDrawingSnapshot } from "../src/lib/canvas/canvas-drawing-storage";

test("native drawing save and reload retain every page and its shapes", async () => {
    const result = await new Promise<{ saved: CanvasDrawingSnapshot; reloaded: CanvasDrawingSnapshot; undone: CanvasDrawingSnapshot; draft: CanvasDrawingSnapshot; retainedDraft: CanvasDrawingSnapshot }>((resolve, reject) => {
        const worker = new Worker(new URL("./helpers/canvas-drawing-pages.worker.ts", import.meta.url).href, { type: "module" });
        worker.onmessage = ({ data }) => {
            worker.terminate();
            if (data.error) reject(new Error(data.error));
            else resolve(data);
        };
        worker.onerror = (event) => {
            worker.terminate();
            reject(event.error ?? new Error(event.message));
        };
        worker.postMessage(null);
    });
    for (const document of [result.saved, result.reloaded]) {
        expect(document.pageCount).toBe(2);
        expect(document.shapeCount).toBe(2);
        expect(Object.keys((document.snapshot as { document: { store: object } }).document.store)).toHaveLength(4);
    }
    expect(result.undone.revision).toBe(0);
    expect(result.undone.shapeCount).toBe(0);
    expect(result.retainedDraft).toEqual(result.draft);
    expect(result.retainedDraft.shapeCount).toBe(2);
});

test("opening a synced native drawing never deletes pages during editor mount", async () => {
    const source = await Bun.file(new URL("../src/components/canvas/canvas-drawing-tldraw-editor.tsx", import.meta.url)).text();
    expect(source).not.toContain("editor.deletePage(");
});
