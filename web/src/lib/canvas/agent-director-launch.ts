import { API_ERROR_REASONS } from "@/services/api/error-codes";
import { ApiError } from "@/services/api/request";
import { resourceIdFromStorageKey } from "@/services/api/resources";

/**
 * Agent 调用 `director` 工具时由后端发出的 `director_open` 运行事件载荷。
 * request_approval 下批准后才发；auto 下执行前直接发。preview 带 requestId，
 * 浏览器录制完成后用它回传结果。
 */
export type DirectorOpenEvent = { canvasId: string; nodeId: string; sceneId: string; shotId?: string; action: string; requestId?: string };
export type DirectorOpenRunEvent = DirectorOpenEvent & { runId: string };
export type DirectorPreviewResult = { videoResourceId: string } | { error: string };
export type DirectorPreviewJob = { runId: string; requestId: string; sceneId: string; shotId?: string };

const DIRECTOR_PREVIEW_ERROR_MAX = 500;
/** 与后端 CANVAS_AGENT_DIRECTOR_PREVIEW_TIMEOUT 默认值一致：更早的事件只可能是回放。 */
export const DIRECTOR_OPEN_MAX_AGE_MS = 120_000;

function optionalString(value: unknown) {
    return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

export function parseDirectorOpenEvent(event: { type: string; runId: string; payload?: Record<string, unknown> }): DirectorOpenRunEvent | null {
    if (event.type !== "director_open") return null;
    const payload = event.payload || {};
    const canvasId = optionalString(payload.canvasId);
    const nodeId = optionalString(payload.nodeId);
    const sceneId = optionalString(payload.sceneId);
    const action = optionalString(payload.action);
    if (!canvasId || !nodeId || !sceneId || !action) return null;
    const shotId = optionalString(payload.shotId);
    const requestId = optionalString(payload.requestId);
    return { runId: event.runId, canvasId, nodeId, sceneId, action, ...(shotId ? { shotId } : {}), ...(requestId ? { requestId } : {}) };
}

/** 页面重连会回放游标之后的事件；过期的打开请求不再弹出导演台。 */
export function isDirectorOpenStale(createdAt: string | undefined, now = Date.now()) {
    const at = createdAt ? Date.parse(createdAt) : Number.NaN;
    return Number.isFinite(at) && now - at > DIRECTOR_OPEN_MAX_AGE_MS;
}

/** 画布不匹配返回 ignored；已打开同一节点不重复打开。 */
export function handleDirectorOpen(event: DirectorOpenEvent, deps: { openPrevisWorkbench(nodeId: string): void; currentCanvasId: string; openNodeId?: string | null }): "opened" | "ignored" {
    if (!event.nodeId || event.canvasId !== deps.currentCanvasId) return "ignored";
    if (deps.openNodeId === event.nodeId) return "ignored";
    deps.openPrevisWorkbench(event.nodeId);
    return "opened";
}

/**
 * 事件在 Agent 写入提交后立刻到达，画布实时合并可能稍晚：节点与场景都同步到本地后
 * 才能打开，否则 openPrevisWorkbench 会把缺场景的节点当成孤儿并补一个空场景。
 */
export function directorOpenTargetReady(event: DirectorOpenEvent, nodes: ReadonlyArray<{ id: string; metadata?: { previsSceneId?: string } }>, scenes: ReadonlyArray<{ id: string }>) {
    const node = nodes.find((item) => item.id === event.nodeId);
    return Boolean(node && node.metadata?.previsSceneId === event.sceneId && scenes.some((scene) => scene.id === event.sceneId));
}

/** director 工具调用参数里的 action；解析失败返回空串。 */
export function directorToolAction(detail: unknown): string {
    const raw = detail && typeof detail === "object" ? (detail as Record<string, unknown>).arguments : undefined;
    let args: unknown = raw;
    if (typeof raw === "string") {
        try {
            args = JSON.parse(raw);
        } catch {
            return "";
        }
    }
    const action = args && typeof args === "object" ? (args as Record<string, unknown>).action : undefined;
    return typeof action === "string" ? action : "";
}

/** 录制前导演台被关闭或切走：不回传，让后端按超时给出 director_viewport_unavailable。 */
export class DirectorPreviewAbandonedError extends Error {
    constructor() {
        super("导演台已关闭，预演未录制");
        this.name = "DirectorPreviewAbandonedError";
    }
}

/** 回传必须是服务端视频资源；直传失败退回本机缓存的结果不能交给 Agent。 */
export function directorPreviewVideoResourceId(storageKey: string | undefined) {
    const id = resourceIdFromStorageKey(storageKey);
    if (!id) throw new Error("预演视频未上传到服务端，请检查网络后重试");
    return id;
}

/** 后端只接受不含控制字符、最多 500 字的错误说明。 */
export function directorPreviewErrorMessage(cause: unknown) {
    const raw = cause instanceof Error ? cause.message : typeof cause === "string" ? cause : "";
    const compact = raw.replace(/[\u0000-\u001f\u007f]+/gu, " ").replace(/\s+/gu, " ").trim();
    return Array.from(compact || "导演台预演录制失败").slice(0, DIRECTOR_PREVIEW_ERROR_MAX).join("");
}

/**
 * 录制并回传一次预演。录制失败回传 { error }，让 Agent 立即拿到结论而不是等超时；
 * 请求已超时或已完成（director_preview_not_found）视为已结束，其余提交失败向上抛。
 */
export async function runDirectorPreview(input: { runId: string; requestId: string; record: () => Promise<string>; submit: (runId: string, requestId: string, result: DirectorPreviewResult) => Promise<void> }): Promise<"submitted" | "expired" | "abandoned"> {
    let result: DirectorPreviewResult;
    try {
        result = { videoResourceId: await input.record() };
    } catch (cause) {
        if (cause instanceof DirectorPreviewAbandonedError) return "abandoned";
        result = { error: directorPreviewErrorMessage(cause) };
    }
    try {
        await input.submit(input.runId, input.requestId, result);
        return "submitted";
    } catch (cause) {
        if (cause instanceof ApiError && cause.reason === API_ERROR_REASONS.directorPreviewNotFound) return "expired";
        throw cause;
    }
}

/** 导演台刚挂载时 WebGL 上下文尚未登记；轮询到可捕捉再录制。 */
export async function waitForDirectorViewport(isReady: () => boolean, options: { timeoutMs?: number; intervalMs?: number; signal?: AbortSignal } = {}) {
    const deadline = Date.now() + (options.timeoutMs ?? 15_000);
    while (!isReady()) {
        options.signal?.throwIfAborted();
        if (Date.now() >= deadline) throw new Error("导演台 3D 视口未就绪，无法录制预演");
        await new Promise((resolve) => setTimeout(resolve, options.intervalMs ?? 100));
    }
}
