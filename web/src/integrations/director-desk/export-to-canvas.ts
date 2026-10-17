import { createStoryboardRow } from "@/lib/canvas/canvas-project-domain";
import type { CanvasNodeData, StoryboardRow } from "@/types/canvas";
import type { DirectorProject } from "./previs-runtime-types";
import { DIRECTOR_CANVAS_EXTENSION_KEY } from "./previs-runtime-types";

export type PrevisRenderInput = {
    project: DirectorProject;
    sourceSceneId: string;
    shotId: string;
    time: number;
    duration?: number;
    fps?: number;
};

export type PrevisExportKind = "whitebox-video" | "composition-frame";

export type PrevisExportTask = {
    id: string;
    status: "queued" | "running" | "succeeded" | "failed" | "cancelled" | "submission_uncertain";
    resultJson?: string;
    error?: string;
};

export type DirectorDeskExportTransport = {
    submitPrevis(input: {
        canvasId: string;
        sceneId: string;
        shotId: string;
        kind: PrevisExportKind;
        options: { duration: number; fps: number };
    }, operationId: string): Promise<{ taskId: string }>;
    queryTask(taskId: string): Promise<PrevisExportTask>;
    readCanvas(canvasId: string): Promise<{ nodes: CanvasNodeData[] }>;
    /** 后台完成资源登记与幂等画布写回;前端不得绕过源场景校验自行补建节点。 */
    waitForMaterialization(taskId: string): Promise<{ state: string; outcome?: string }>;
    writeStoryboard(canvasId: string, rows: StoryboardRow[]): Promise<void>;
};

export type PrevisArtifactWriteInput = {
    canvasId: string;
    sceneId: string;
    taskId: string;
    project: DirectorProject;
};

const isRecord = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === "object" && !Array.isArray(value);

export function assertExportPayloadSafe(value: unknown, path = "payload"): void {
    if (typeof Blob !== "undefined" && value instanceof Blob) throw new Error("director_export_blob_forbidden");
    if (typeof value === "string") {
        if (/^blob:/iu.test(value) || /^(?:[a-z]:\\|\\\\|\/tmp\/|file:\/\/)/iu.test(value)) throw new Error(`director_export_path_forbidden:${path}`);
        return;
    }
    if (Array.isArray(value)) {
        value.forEach((item, index) => assertExportPayloadSafe(item, `${path}[${index}]`));
        return;
    }
    if (!isRecord(value)) return;
    for (const [key, child] of Object.entries(value)) {
        if (/^(?:filePath|directory|filename|blob|dataUrl|localPath|destination)$/iu.test(key)) throw new Error(`director_export_field_forbidden:${path}.${key}`);
        assertExportPayloadSafe(child, `${path}.${key}`);
    }
}

function extensionOf(project: DirectorProject) {
    const extension = project.extensions?.[DIRECTOR_CANVAS_EXTENSION_KEY];
    if (!extension || extension.scene.id !== extension.sourceSceneId) throw new Error("director_export_scene_extension_invalid");
    return extension;
}

function shotOf(project: DirectorProject, sceneId: string, shotId: string) {
    const extension = extensionOf(project);
    if (extension.sourceSceneId !== sceneId) throw new Error("director_export_scene_mismatch");
    const shot = extension.shots.find((item) => item.id === shotId);
    if (!shot) throw new Error("director_export_shot_missing");
    return { extension, shot };
}

export async function requestPrevisRender(
    input: { canvasId: string; sceneId: string; shotId: string; kind: PrevisExportKind; render: PrevisRenderInput; requestId?: string },
    transport: DirectorDeskExportTransport,
): Promise<{ taskId: string }> {
    if (!input.canvasId.trim() || !input.sceneId.trim() || !input.shotId.trim()) throw new Error("director_export_scope_invalid");
    assertExportPayloadSafe(input);
    if (input.render.sourceSceneId !== input.sceneId || input.render.shotId !== input.shotId) throw new Error("director_export_scope_mismatch");
    const { shot } = shotOf(input.render.project, input.sceneId, input.shotId);
    const duration = input.render.duration ?? shot.duration;
    const fps = input.render.fps ?? shot.fps;
    if (!Number.isFinite(duration) || duration <= 0 || !Number.isFinite(fps) || fps <= 0) throw new Error("director_export_options_invalid");
    const operationId = input.requestId || `director-export:${crypto.randomUUID()}`;
    return transport.submitPrevis({ canvasId: input.canvasId, sceneId: input.sceneId, shotId: input.shotId, kind: input.kind, options: { duration, fps } }, operationId);
}

export async function writeBackPrevisArtifacts(input: PrevisArtifactWriteInput, transport: DirectorDeskExportTransport): Promise<{ nodeIds: string[]; storyboardUpdated: boolean }> {
    if (extensionOf(input.project).sourceSceneId !== input.sceneId) throw new Error("director_export_scene_mismatch");
    const task = await transport.queryTask(input.taskId);
    if (task.status !== "succeeded") return { nodeIds: [], storyboardUpdated: false };
    const materialization = await transport.waitForMaterialization(input.taskId);
    if (materialization.state !== "done") throw new Error("director_export_writeback_pending");
    if (materialization.outcome !== "applied") throw new Error(`director_export_writeback:${materialization.outcome || "unknown"}`);
    const current = await transport.readCanvas(input.canvasId);
    const nodeIds = [`previs-video-${input.taskId}`, `previs-preview-${input.taskId}`];
    if (!nodeIds.every((id) => current.nodes.some((node) => node.id === id && node.metadata?.taskId === input.taskId && node.metadata?.previsSceneId === input.sceneId && node.metadata.storageKey && node.metadata.assetId))) throw new Error("director_export_artifacts_missing");
    return { nodeIds, storyboardUpdated: false };
}

function storyboardRows(project: DirectorProject): StoryboardRow[] {
    const extension = extensionOf(project);
    return extension.shots.map((shot, index) => createStoryboardRow(index + 1, {
        id: `shot-${shot.id}`,
        durationSeconds: shot.duration,
        videoMotionPrompt: shot.prompt || `${shot.name} ${shot.cameraMove}`.trim(),
        camera: shot.cameraId,
        motion: shot.cameraMove,
        shotSize: shot.shotSize,
        previsSceneId: extension.scene.id,
    }));
}

export async function exportShotTable(project: DirectorProject, canvasId: string, transport: DirectorDeskExportTransport): Promise<{ rows: number }> {
    const rows = storyboardRows(project);
    await transport.writeStoryboard(canvasId, rows);
    return { rows: rows.length };
}

export function createDirectorDeskCanvasExporter(transport: DirectorDeskExportTransport) {
    return {
        requestPrevisRender: (input: Parameters<typeof requestPrevisRender>[0]) => requestPrevisRender(input, transport),
        writeBackPrevisArtifacts: (input: PrevisArtifactWriteInput) => writeBackPrevisArtifacts(input, transport),
        exportShotTable: (project: DirectorProject, canvasId: string) => exportShotTable(project, canvasId, transport),
    };
}
