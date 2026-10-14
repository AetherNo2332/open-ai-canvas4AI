import type { PrevisScene } from "@/types/previs";
import { directorDocumentToPrevisScene, previsSceneToDirectorDocument } from "./previs-adapter";
import { createDirectorRenderBridge } from "./render-bridge";
import { assertExportPayloadSafe, createDirectorDeskCanvasExporter, type DirectorDeskExportTransport, type PrevisExportTask } from "./export-to-canvas";

export type DirectorExportEvent = { taskId: string; status: "succeeded" | "failed"; nodeIds?: string[]; message?: string };
export type ExportResultPublisher = (event: DirectorExportEvent) => void;

export function createDirectorDeskExportHandler(options: {
    canvasId: string;
    sceneId: string;
    scene: () => PrevisScene | null;
    saveDocument: (document: unknown) => Promise<PrevisScene>;
    transport: DirectorDeskExportTransport;
    waitTask: (taskId: string) => Promise<PrevisExportTask>;
    onRefresh?: () => void | Promise<void>;
}) {
    const exporter = createDirectorDeskCanvasExporter(options.transport);
    const render = createDirectorRenderBridge({ submit: async (input, context) => {
        const result = await exporter.requestPrevisRender({ canvasId: options.canvasId, sceneId: input.sourceSceneId, shotId: input.shotId, kind: context.kind || "whitebox-video", render: input, requestId: context.requestId });
        return { ...result, status: "queued" };
    } });
    return async (payload: unknown, publish?: ExportResultPublisher) => {
        assertExportPayloadSafe(payload);
        if (!payload || typeof payload !== "object" || Array.isArray(payload)) throw new Error("director_export_scope_invalid");
        const value = payload as Record<string, unknown>;
        const current = options.scene();
        if (value.canvasId !== options.canvasId || value.sceneId !== options.sceneId || current?.id !== options.sceneId || typeof value.shotId !== "string") throw new Error("director_export_scope_mismatch");
        const kind = value.kind;
        if (kind !== "whitebox-video" && kind !== "composition-frame" && kind !== "shot-table") throw new Error("director_export_kind_invalid");
        // 校验全部 scope 后才允许保存，避免错误请求产生场景副作用。
        const proposed = directorDocumentToPrevisScene(value.document, current);
        if (proposed.id !== options.sceneId || !proposed.shots.some((shot) => shot.id === value.shotId)) throw new Error("director_export_scope_mismatch");
        const saved = await options.saveDocument(value.document);
        if (saved.id !== options.sceneId || options.scene()?.id !== options.sceneId) throw new Error("director_export_scope_changed");
        const project = previsSceneToDirectorDocument(saved);
        if (kind === "shot-table") {
            const result = await exporter.exportShotTable(project, options.canvasId);
            await options.onRefresh?.();
            return { status: "succeeded", rows: result.rows };
        }
        const submitted = await render.exportToCanvas(saved, kind, { shotId: value.shotId });
        // 应答只等待提交；后台观察持续到终态，再确认资源与画布均已落盘。
        void (async () => {
            try {
                const task = await options.waitTask(submitted.taskId);
                if (task.status !== "succeeded") throw new Error("预演任务未成功，请在任务中心检查");
                const result = await exporter.writeBackPrevisArtifacts({ canvasId: options.canvasId, sceneId: options.sceneId, taskId: submitted.taskId, project });
                if (options.scene()?.id === options.sceneId) await options.onRefresh?.();
                publish?.({ taskId: submitted.taskId, status: "succeeded", nodeIds: result.nodeIds });
            } catch {
                publish?.({ taskId: submitted.taskId, status: "failed", message: "导出或画布写回未完成，请在任务中心检查；场景改动后的旧产物不会覆盖新场景。" });
            }
        })();
        return { taskId: submitted.taskId, status: "queued" };
    };
}
