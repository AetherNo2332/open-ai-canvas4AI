import { expect, test } from "bun:test";
import { createDirectorDeskExportHandler } from "@/integrations/director-desk/export-handler";
import { previsSceneToDirectorDocument, directorDocumentToPrevisScene } from "@/integrations/director-desk/previs-adapter";
import { createPrevisScene } from "@/lib/canvas/previs/previs-scene";
import type { DirectorDeskExportTransport, PrevisExportTask } from "@/integrations/director-desk/export-to-canvas";

function fixture(save = true) {
    const scene = createPrevisScene("最新场景");
    const calls: string[] = [];
    const events: unknown[] = [];
    let finish!: (task: PrevisExportTask) => void;
    const completed = new Promise<PrevisExportTask>((resolve) => { finish = resolve; });
    const transport: DirectorDeskExportTransport = {
        submitPrevis: async () => { calls.push("submit"); return { taskId: "task-1" }; },
        queryTask: async (id) => { calls.push("query"); return { id, status: "succeeded" }; },
        waitForMaterialization: async () => { calls.push("writeback"); return { state: "done", outcome: "applied" }; },
        readCanvas: async () => ({ nodes: ["previs-video-task-1", "previs-preview-task-1"].map((id) => ({ id, metadata: { taskId: "task-1", previsSceneId: scene.id, storageKey: "resource:output", assetId: "asset-1" } })) as any }),
        writeStoryboard: async () => { calls.push("storyboard"); },
    };
    let savedName = "";
    const handler = createDirectorDeskExportHandler({
        canvasId: "canvas-1", sceneId: scene.id, scene: () => scene, transport,
        saveDocument: async (document) => {
            calls.push("save");
            if (!save) throw new Error("保存未完成");
            const next = directorDocumentToPrevisScene(document, scene);
            savedName = next.objects[0]?.name || "";
            return next;
        },
        waitTask: async () => { calls.push("wait"); return completed; },
        onRefresh: () => { calls.push("refresh"); },
    });
    const document = previsSceneToDirectorDocument(scene);
    document.entities.find((item) => item.kind !== "camera")!.name = "应用里刚改的名字";
    const request = { canvasId: "canvas-1", sceneId: scene.id, shotId: scene.activeShotId, kind: "whitebox-video", document };
    return { handler, calls, events, finish, request, savedName: () => savedName };
}

test("export saves the supplied app document before submission and waits for terminal before writeback", async () => {
    const f = fixture();
    const response = await f.handler(f.request, (event) => f.events.push(event));
    expect(response).toEqual({ taskId: "task-1", status: "queued" });
    expect(f.savedName()).toBe("应用里刚改的名字");
    expect(f.calls).toEqual(["save", "submit", "wait"]);
    f.finish({ id: "task-1", status: "succeeded" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(f.calls).toEqual(["save", "submit", "wait", "query", "writeback", "refresh"]);
    expect(f.events[0]).toMatchObject({ taskId: "task-1", status: "succeeded", nodeIds: ["previs-video-task-1", "previs-preview-task-1"] });
});

test("save failure and foreign canvas never submit export tasks", async () => {
    const failed = fixture(false);
    await expect(failed.handler(failed.request)).rejects.toThrow("保存未完成");
    expect(failed.calls).toEqual(["save"]);
    const foreign = fixture();
    await expect(foreign.handler({ ...foreign.request, canvasId: "other" })).rejects.toThrow("scope");
    expect(foreign.calls).toHaveLength(0);
});

test("terminal failure is reported without materializing or refreshing", async () => {
    const f = fixture();
    await f.handler(f.request, (event) => f.events.push(event));
    f.finish({ id: "task-1", status: "failed", error: "渲染失败" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(f.calls).toEqual(["save", "submit", "wait"]);
    expect(f.events[0]).toMatchObject({ taskId: "task-1", status: "failed" });
});
