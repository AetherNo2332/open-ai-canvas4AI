import { API_ERROR_REASONS } from "@/services/api/error-codes";
import { ApiError } from "@/services/api/request";
import { resourceIdFromStorageKey } from "@/services/api/resources";
import type { AgentRun } from "@/services/api/agent";

/**
 * Agent 调用 `director` 工具时由后端发出的 `director_open` 运行事件载荷。
 * request_approval 下批准后才发；auto 下执行前直接发。preview 带 requestId，
 * 浏览器录制完成后用它回传结果。
 */
export type DirectorOpenEvent = { canvasId: string; nodeId: string; sceneId: string; shotId?: string; action: string; requestId?: string };
/** `seq` 是 director_open 的持久事件序号；宿主只转发 seq>0 的事件，预演挂起查询据此定位请求事件。 */
export type DirectorOpenRunEvent = DirectorOpenEvent & { runId: string; seq?: number };
export type DirectorPreviewResult = { videoResourceId: string } | { error: string };
export type DirectorPreviewJob = { runId: string; requestId: string; sceneId: string; shotId?: string; openSeq?: number };

const DIRECTOR_PREVIEW_ERROR_MAX = 500;
/** 与后端 CANVAS_AGENT_DIRECTOR_PREVIEW_TIMEOUT 默认值一致：更早的事件只可能是回放。 */
export const DIRECTOR_OPEN_MAX_AGE_MS = 120_000;

function optionalString(value: unknown) {
    return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

export function parseDirectorOpenEvent(event: { type: string; runId: string; seq?: number; payload?: Record<string, unknown> }): DirectorOpenRunEvent | null {
    if (event.type !== "director_open") return null;
    const payload = event.payload || {};
    const canvasId = optionalString(payload.canvasId);
    const nodeId = optionalString(payload.nodeId);
    const sceneId = optionalString(payload.sceneId);
    const action = optionalString(payload.action);
    if (!canvasId || !nodeId || !sceneId || !action) return null;
    const shotId = optionalString(payload.shotId);
    const requestId = optionalString(payload.requestId);
    const seq = typeof event.seq === "number" && Number.isInteger(event.seq) && event.seq > 0 ? event.seq : undefined;
    return { runId: event.runId, canvasId, nodeId, sceneId, action, ...(shotId ? { shotId } : {}), ...(requestId ? { requestId } : {}), ...(seq ? { seq } : {}) };
}

/** 页面重连会回放游标之后的事件；过期或时间无法解析的打开请求不再弹出导演台（fail closed）。 */
export function isDirectorOpenStale(createdAt: string | undefined, now = Date.now()) {
    const at = createdAt ? Date.parse(createdAt) : Number.NaN;
    return !Number.isFinite(at) || now - at > DIRECTOR_OPEN_MAX_AGE_MS;
}

/** 画布不匹配或 read（后端本不发，纵深防御）返回 ignored；已打开同一节点不重复打开。 */
export function handleDirectorOpen(event: DirectorOpenEvent, deps: { openPrevisWorkbench(nodeId: string): void; currentCanvasId: string; openNodeId?: string | null }): "opened" | "ignored" {
    if (!event.nodeId || event.action === "read" || event.canvasId !== deps.currentCanvasId) return "ignored";
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
 * 运行仍挂起在这次预演上：运行未结束、事件里有该 requestId 的 director_preview_requested，
 * 且同一调用之后还没有 tool_completed / tool_failed（浏览器回传、超时都会写回执）。
 * 前端运行视图不暴露 waitKind/waitId，所以按事件判定；找不到请求事件时按未挂起处理。
 */
export function directorPreviewPending(run: Pick<AgentRun, "status" | "events"> | null | undefined, requestId: string) {
    if (!run || !["running", "queued"].includes(run.status)) return false;
    const events = run.events || [];
    const index = events.findLastIndex((event) => event.type === "director_preview_requested" && event.payload?.requestId === requestId);
    if (index < 0) return false;
    const callId = events[index].payload?.callId;
    return !events.slice(index + 1).some((event) => (event.type === "tool_completed" || event.type === "tool_failed") && event.payload?.callId === callId);
}

/** 与后端 agentRunEventQueryLimit 一致的单页事件上限。 */
const DIRECTOR_PREVIEW_EVENT_PAGE = 500;
type DirectorRunFetchOptions = { sinceSeq?: number; eventLimit?: number; fromStart?: boolean };

/**
 * 挂起查询的事件窗口：从 director_open 的前一条开始分页读取，请求事件（紧随 director_open 写入）
 * 与之后的工具回执都落在页内；默认视图只返回尾部一窗，请求事件之后事件一多就会漏判为未挂起。
 * 缺少序号时从头读取。
 */
export function directorPreviewRunQuery(job: Pick<DirectorPreviewJob, "openSeq">): DirectorRunFetchOptions {
    const sinceSeq = Math.max(0, (job.openSeq ?? 0) - 1);
    return sinceSeq > 0 ? { sinceSeq, eventLimit: DIRECTOR_PREVIEW_EVENT_PAGE } : { fromStart: true, eventLimit: DIRECTOR_PREVIEW_EVENT_PAGE };
}

export async function directorPreviewJobPending(job: Pick<DirectorPreviewJob, "runId" | "requestId" | "openSeq">, fetchRun: (runId: string, options: DirectorRunFetchOptions) => Promise<{ run: Pick<AgentRun, "status" | "events"> }>) {
    return directorPreviewPending((await fetchRun(job.runId, directorPreviewRunQuery(job))).run, job.requestId);
}

type SyncStorage = { getItem(key: string): string | null; setItem(key: string, value: string): void };
type LeaseStorage = SyncStorage & { removeItem(key: string): void };
type LockManager = { request(name: string, options: { ifAvailable: boolean }, callback: (lock: object | null) => Promise<unknown>): Promise<unknown> };
export type DirectorPreviewLock = <T>(name: string, fn: () => Promise<T>) => Promise<{ acquired: true; value: T } | { acquired: false }>;

const HANDLED_STORAGE_KEY = "director-preview-handled";
const HANDLED_LIMIT = 64;
const LEASE_PREFIX = "director-preview-lock:";
/** 录制最长 60s，加上转码和上传；租约过期后另一个标签页才可接手。 */
const LEASE_MS = 5 * 60_000;

/**
 * 本标签页已处理过的 requestId，存 sessionStorage：刷新或导演台卸载后回放的旧请求不再录制。
 * 存储不可用时退回内存，只影响跨刷新去重，不阻塞录制。
 */
export function directorPreviewHandledStore(storage: SyncStorage | undefined = safeSessionStorage()) {
    const memory = new Set<string>();
    const read = () => {
        try {
            const parsed = JSON.parse(storage?.getItem(HANDLED_STORAGE_KEY) || "[]");
            return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === "string") : [];
        } catch {
            return [];
        }
    };
    return {
        isHandled: (requestId: string) => memory.has(requestId) || read().includes(requestId),
        markHandled: (requestId: string) => {
            memory.add(requestId);
            try {
                storage?.setItem(HANDLED_STORAGE_KEY, JSON.stringify([...read().filter((id) => id !== requestId), requestId].slice(-HANDLED_LIMIT)));
            } catch {
                // 隐私模式或存储被禁用：保留内存记录即可。
            }
        },
    };
}

function safeSessionStorage(): SyncStorage | undefined {
    try {
        return typeof sessionStorage === "undefined" ? undefined : sessionStorage;
    } catch {
        return undefined;
    }
}

function safeLocalStorage(): LeaseStorage | undefined {
    try {
        return typeof localStorage === "undefined" ? undefined : localStorage;
    } catch {
        return undefined;
    }
}

/**
 * 同一 requestId 只允许一个标签页录制与回传。优先 Web Locks（ifAvailable，拿不到立即放弃）；
 * 没有 navigator.locks 时用 localStorage 租约兜底（写后回读确认归属，过期租约可接手）。
 * 两者都不可用时直接执行：单标签页场景不能因此失去预演能力。
 */
export async function withDirectorPreviewLock<T>(requestId: string, fn: () => Promise<T>, deps: { locks?: LockManager; storage?: LeaseStorage; owner?: string; now?: () => number } = defaultLockDeps()): Promise<{ acquired: true; value: T } | { acquired: false }> {
    const name = `${LEASE_PREFIX}${requestId}`;
    if (deps.locks) {
        let outcome: { acquired: true; value: T } | { acquired: false } = { acquired: false };
        await deps.locks.request(name, { ifAvailable: true }, async (lock) => {
            if (lock) outcome = { acquired: true, value: await fn() };
        });
        return outcome;
    }
    const storage = deps.storage;
    if (!storage) return { acquired: true, value: await fn() };
    const now = deps.now ?? Date.now;
    const owner = deps.owner || `tab-${Math.random().toString(36).slice(2)}`;
    try {
        const current = JSON.parse(storage.getItem(name) || "null") as { owner?: string; expiresAt?: number } | null;
        if (current && current.owner !== owner && Number(current.expiresAt) > now()) return { acquired: false };
        storage.setItem(name, JSON.stringify({ owner, expiresAt: now() + LEASE_MS }));
        if ((JSON.parse(storage.getItem(name) || "null") as { owner?: string } | null)?.owner !== owner) return { acquired: false };
    } catch {
        return { acquired: true, value: await fn() };
    }
    try {
        return { acquired: true, value: await fn() };
    } finally {
        try {
            if ((JSON.parse(storage.getItem(name) || "null") as { owner?: string } | null)?.owner === owner) storage.removeItem(name);
        } catch {
            // 租约会自然过期。
        }
    }
}

function defaultLockDeps() {
    const locks = typeof navigator !== "undefined" ? (navigator as Navigator & { locks?: LockManager }).locks : undefined;
    return locks ? { locks } : { storage: safeLocalStorage() };
}

/**
 * 录制并回传一次预演。
 * - 后台标签页（hidden）直接放弃：录制会因页面不可见中止，不能抢先回传失败。
 * - 本标签页处理过、其他标签页持锁、或运行已不再等待这次请求时跳过。
 * - 录制失败回传 { error }（submitted_error），让 Agent 立即拿到结论而不是等超时；
 *   请求已超时或已完成（director_preview_not_found）视为 expired，其余提交失败向上抛。
 */
export async function runDirectorPreview(input: {
    runId: string;
    requestId: string;
    record: () => Promise<string>;
    submit: (runId: string, requestId: string, result: DirectorPreviewResult) => Promise<void>;
    isHidden?: () => boolean;
    isPending?: () => Promise<boolean>;
    handled?: { isHandled(requestId: string): boolean; markHandled(requestId: string): void };
    withLock?: DirectorPreviewLock;
}): Promise<DirectorPreviewOutcome> {
    if (input.isHidden?.()) return "abandoned";
    if (input.handled?.isHandled(input.requestId)) return "skipped";
    const lock: DirectorPreviewLock = input.withLock ?? (async (_name, fn) => ({ acquired: true, value: await fn() }));
    const locked = await lock(input.requestId, async (): Promise<DirectorPreviewOutcome> => {
        if (input.isPending && !(await input.isPending())) return "skipped";
        if (input.handled?.isHandled(input.requestId)) return "skipped";
        input.handled?.markHandled(input.requestId);
        let result: DirectorPreviewResult;
        try {
            result = { videoResourceId: await input.record() };
        } catch (cause) {
            if (cause instanceof DirectorPreviewAbandonedError) return "abandoned";
            result = { error: directorPreviewErrorMessage(cause) };
        }
        try {
            await input.submit(input.runId, input.requestId, result);
            return "error" in result ? "submitted_error" : "submitted";
        } catch (cause) {
            if (cause instanceof ApiError && cause.reason === API_ERROR_REASONS.directorPreviewNotFound) return "expired";
            throw cause;
        }
    });
    return locked.acquired ? locked.value : "skipped";
}

export type DirectorPreviewOutcome = "submitted" | "submitted_error" | "expired" | "abandoned" | "skipped";

/** 结果提示：只有真正回传了视频才报成功；录制失败回传给 Agent 时用警告；未录制即放弃时用信息提示。 */
export function directorPreviewOutcomeNotice(outcome: DirectorPreviewOutcome): { level: "success" | "warning" | "info"; text: string } | null {
    if (outcome === "submitted") return { level: "success", text: "预演视频已回传给 Agent" };
    if (outcome === "submitted_error") return { level: "warning", text: "预演录制失败，已把失败原因告知 Agent" };
    if (outcome === "expired") return { level: "warning", text: "Agent 的预演请求已超时或已结束" };
    if (outcome === "abandoned") return { level: "info", text: "未录制导演台预演（页面在后台或导演台已关闭）；Agent 将在超时后收到可重试的提示" };
    return null;
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
