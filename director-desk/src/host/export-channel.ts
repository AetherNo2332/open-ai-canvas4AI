import type { SceneDocument } from "../scenes/sequence-project.ts";
import type { HostBridge } from "./bridge";
import { hostBridge, hostCanvasId } from "./runtime.ts";

export type CanvasExportKind = "whitebox-video" | "composition-frame" | "shot-table";
export type CanvasExportResult = { taskId?: string; status: "queued" | "succeeded"; rows?: number };
export type ExportShot = { id: string; name: string; duration: number; fps: number };

export function canvasExportScope(document: SceneDocument): { sceneId: string; activeShotId: string; shots: ExportShot[] } {
    const state = document.scenes.find((scene) => scene.id === document.activeSceneId)?.state;
    const extension = state?.extensions?.["infinite-canvas"] as { sourceSceneId?: string; scene?: { activeShotId?: string }; shots?: ExportShot[] } | undefined;
    if (!extension?.sourceSceneId || !extension.scene?.activeShotId || !Array.isArray(extension.shots) || !extension.shots.length) throw new Error("当前戏段尚未连接画布场景");
    return { sceneId: extension.sourceSceneId, activeShotId: extension.scene.activeShotId, shots: extension.shots };
}

/** 云端只交付到画布;编码、资源登记、源场景校验均由宿主任务链负责。 */
export async function requestCanvasExport(input: { document: SceneDocument; kind: CanvasExportKind; shotId?: string }, context = { bridge: hostBridge(), canvasId: hostCanvasId() } as { bridge?: HostBridge; canvasId?: string }): Promise<CanvasExportResult> {
    if (!context.bridge || !context.canvasId) throw new Error("导演台未连接画布，请从画布重新打开");
    const scope = canvasExportScope(input.document);
    const shotId = input.shotId || scope.activeShotId;
    if (!scope.shots.some((shot) => shot.id === shotId)) throw new Error("导出镜头不存在");
    return await context.bridge.request("desk.export.request", { canvasId: context.canvasId, sceneId: scope.sceneId, shotId, kind: input.kind, document: input.document }, 60000) as CanvasExportResult;
}
