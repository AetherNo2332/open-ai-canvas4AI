import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { requestCanvasExport } from "@desk/host/export-channel";
import { previsSceneToDirectorDocument } from "@/integrations/director-desk/previs-adapter";
import { createPrevisScene } from "@/lib/canvas/previs/previs-scene";
import { readSceneDocument } from "@desk/scenes/sequence-project";
import type { HostBridge } from "@desk/host/bridge";
import { createVideoPanel } from "@desk/ui/video-panel";

test("app export sends scoped document to host without creating a Blob or file", async () => {
    const scene = createPrevisScene();
    const document = readSceneDocument(previsSceneToDirectorDocument(scene));
    const calls: unknown[] = [];
    const bridge: HostBridge = { notify() {}, request: async (type, payload) => {
        calls.push({ type, payload }); return { taskId: "task-1", status: "queued" };
    }, on: () => () => {}, dispose() {} };
    const result = await requestCanvasExport({ document, kind: "whitebox-video", shotId: scene.activeShotId }, { bridge, canvasId: "canvas-1" });
    expect(result).toEqual({ taskId: "task-1", status: "queued" });
    expect(calls[0]).toMatchObject({ type: "desk.export.request", payload: { canvasId: "canvas-1", sceneId: scene.id, shotId: scene.activeShotId, kind: "whitebox-video", document } });
    expect(JSON.stringify(calls)).not.toMatch(/blob:|filePath|directory|filename/);
    await expect(requestCanvasExport({ document, kind: "composition-frame" }, { canvasId: "canvas-1" })).rejects.toThrow("未连接");
});

test("export panel routes video, frame and shot table to canvas; local download code is absent", () => {
    const source = readFileSync(resolve(import.meta.dir, "../../director-desk/src/ui/video-panel.ts"), "utf8");
    expect(source).toContain("requestCanvasExport");
    expect(source).toContain("shot-table");
    expect(source).not.toMatch(/\bdownload\b|videoDestination|runVideoExports|toBlob|directorDesktop|production-delivery/);
});

test("export panel escapes scene labels and blocks repeat submission while busy", async () => {
    const scene = createPrevisScene();
    scene.shots[0].name = '<img src=x onerror="boom()">';
    const document = readSceneDocument(previsSceneToDirectorDocument(scene));
    let html = "";
    let reads = 0;
    const previous = globalThis.document;
    globalThis.document = { querySelector: () => ({ value: scene.activeShotId, addEventListener() {} }) } as any;
    try {
        const ctx = { busy: false, scenes: { document: () => { reads++; return document; } }, showModal: (_title: string, body: string) => { html = body; }, toast() {} } as any;
        const panel = createVideoPanel(ctx);
        panel.exportDialog();
        expect(html).toContain("&lt;img");
        expect(html).not.toContain("<img src=x");
        ctx.busy = true;
        const before = reads;
        await panel.startExport();
        expect(reads).toBe(before);
    } finally {
        globalThis.document = previous;
    }
});
