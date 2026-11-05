/**
 * 创作平台 SDK 客户端：模型、报价、提交、快照、事件续读、动作、额度与资源。
 * 只依赖 contract.ts 与注入的 Transport；不读业务配置、不弹提示、不访问 window 或 store。
 */
import {
    allowsAction,
    isTerminalExecution,
    type Action,
    type BillingAggregate,
    type BusinessRef,
    type Capability,
    type CreditBalance,
    type EventPage,
    type EventQuery,
    type GenerationRequest,
    type ModelCatalog,
    type PlatformError,
    type PlatformErrorCode,
    type Quote,
    type ResourceRef,
    type SubmitReceipt,
    type TaskSnapshot,
    validateGenerationRequest,
} from "./contract";
import type { Transport, TransportRequest } from "./transport";

const PLATFORM_ERROR_CODES: readonly PlatformErrorCode[] = [
    "invalid_request",
    "unauthorized",
    "forbidden",
    "not_found",
    "model_unavailable",
    "model_not_supported",
    "reference_unavailable",
    "insufficient_credits",
    "cost_limit_exceeded",
    "quote_expired",
    "idempotency_conflict",
    "active_task_limit",
    "action_not_allowed",
    "billing_pending_review",
    "media_delivery_failed",
    "unavailable",
    "internal",
    "transport",
];
const EXECUTION_STATES = ["queued", "running", "succeeded", "failed", "cancelled", "uncertain"];

/** 平台调用失败；按 error.code 分支，message 只用于展示。 */
export class PlatformRequestError extends Error {
    readonly error: PlatformError;
    constructor(error: PlatformError) {
        super(error.message);
        this.name = "PlatformRequestError";
        this.error = error;
    }
}

export type CreationPlatformClientOptions = {
    transport: Transport;
    /** 调用方未给幂等键时生成一个；同一次 submit 的所有重投复用它。 */
    newIdempotencyKey?: () => string;
    /** 传输层失败（未拿到响应）时重投次数。只用于幂等的提交与读取。 */
    maxRetries?: number;
    retryDelayMs?: (attempt: number) => number;
    pollIntervalMs?: number;
};

export type WaitOptions = {
    signal?: AbortSignal;
    onSnapshot?: (snapshot: TaskSnapshot) => void;
};

export type ResourceUploadOptions = { kind: "image" | "video" | "audio"; fileName?: string; width?: number; height?: number; durationMs?: number; idempotencyKey?: string; signal?: AbortSignal };

const defaultKey = () => (globalThis.crypto?.randomUUID ? globalThis.crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`);

export function createCreationPlatformClient(options: CreationPlatformClientOptions) {
    const { transport } = options;
    const maxRetries = options.maxRetries ?? 3;
    const retryDelay = options.retryDelayMs ?? ((attempt: number) => Math.min(4000, 250 * 2 ** attempt));
    const pollInterval = options.pollIntervalMs ?? 1500;

    async function call<T>(request: TransportRequest, retryTransport = false): Promise<T> {
        for (let attempt = 0; ; attempt++) {
            let response;
            try {
                response = await transport.request(request);
            } catch (error) {
                if (isAbort(error)) throw error;
                if (retryTransport && attempt < maxRetries) {
                    await sleep(retryDelay(attempt), request.signal);
                    continue;
                }
                throw new PlatformRequestError({ code: "transport", reason: "transport", message: error instanceof Error ? error.message : "网络请求失败", retryable: true });
            }
            const { status, body } = response;
            if (status >= 200 && status < 300 && body.code === 0) return body.data as T;
            throw new PlatformRequestError(envelopeError(status, body));
        }
    }

    async function snapshot(taskId: string, signal?: AbortSignal) {
        return checkedSnapshot(await call<TaskSnapshot>({ method: "GET", path: `/platform/v1/tasks/${encodeURIComponent(taskId)}`, signal }, true));
    }

    async function action(taskId: string, name: Action, path: string) {
        const current = await snapshot(taskId);
        if (!allowsAction(current, name)) {
            throw new PlatformRequestError({ code: "action_not_allowed", reason: "failed_precondition", message: "任务当前不允许该操作", retryable: false, taskId });
        }
        return checkedSnapshot(await call<TaskSnapshot>({ method: "POST", path: `/platform/v1/tasks/${encodeURIComponent(taskId)}/${path}` }));
    }

    return {
        models: (capability: Capability, signal?: AbortSignal) => call<ModelCatalog>({ method: "GET", path: `/platform/v1/models?capability=${encodeURIComponent(capability)}`, signal }, true),

        quote(request: GenerationRequest, signal?: AbortSignal) {
            const prepared = { ...request, idempotencyKey: request.idempotencyKey || "quote" };
            assertValid(prepared);
            return call<Quote>({ method: "POST", path: "/platform/v1/quotes", body: prepared, signal }, true);
        },

        /** 提交生成任务。响应丢失时用同一幂等键重投，服务端返回原任务（replayed=true）。 */
        async submit(request: GenerationRequest, signal?: AbortSignal): Promise<SubmitReceipt> {
            const prepared = { ...request, idempotencyKey: request.idempotencyKey || (options.newIdempotencyKey ?? defaultKey)() };
            assertValid(prepared);
            const receipt = await call<SubmitReceipt>({ method: "POST", path: "/platform/v1/generations", body: prepared, signal }, true);
            return { ...receipt, snapshot: checkedSnapshot(receipt.snapshot) };
        },

        task: (taskId: string, signal?: AbortSignal) => snapshot(taskId, signal),

        events(query: EventQuery, signal?: AbortSignal) {
            const params = new URLSearchParams();
            for (const [key, value] of Object.entries(query)) if (value !== undefined && value !== "") params.set(key, String(value));
            return call<EventPage>({ method: "GET", path: `/platform/v1/events?${params.toString()}`, signal }, true);
        },

        /**
         * 等待任务到达终态且资源交付结束。以快照 revision 为起点续读事件，旧于当前修订的事件被忽略；
         * 游标过期时重新读快照。中止只结束本地等待，不取消服务端任务。
         */
        async waitForTask(taskId: string, wait: WaitOptions = {}): Promise<TaskSnapshot> {
            let current = await snapshot(taskId, wait.signal);
            wait.onSnapshot?.(current);
            let cursor = String(current.revision);
            while (!settled(current)) {
                const page = await this.events({ taskId, after: cursor }, wait.signal);
                if (page.resyncRequired || page.events.some((event) => event.revision > current.revision)) {
                    const next = await snapshot(taskId, wait.signal);
                    if (next.revision > current.revision || page.resyncRequired) {
                        current = next;
                        wait.onSnapshot?.(current);
                    }
                }
                cursor = page.resyncRequired ? String(Math.max(current.revision, Number(page.nextCursor) || 0)) : page.nextCursor;
                if (!settled(current) && page.events.length === 0) await sleep(pollInterval, wait.signal);
            }
            return current;
        },

        cancel: (taskId: string) => action(taskId, "cancel", "cancel"),
        retry: (taskId: string) => action(taskId, "retry", "retry"),
        recoverMedia: (taskId: string) => action(taskId, "recover_media", "recover-media"),

        balance: (signal?: AbortSignal) => call<CreditBalance>({ method: "GET", path: "/platform/v1/billing/balance", signal }, true),

        billingAggregate(business: Pick<BusinessRef, "application" | "projectId">, signal?: AbortSignal) {
            const params = new URLSearchParams({ application: business.application });
            if (business.projectId) params.set("projectId", business.projectId);
            return call<BillingAggregate>({ method: "GET", path: `/platform/v1/billing/aggregate?${params.toString()}`, signal }, true);
        },

        /** 上传参考素材到既有资源接口，返回稳定资源 ID；同一 idempotencyKey 重投得到同一资源。 */
        async uploadResource(file: Blob, upload: ResourceUploadOptions): Promise<ResourceRef> {
            const form = new FormData();
            form.append("file", file, upload.fileName ?? "upload");
            form.append("kind", upload.kind);
            for (const [key, value] of Object.entries({ width: upload.width, height: upload.height, durationMs: upload.durationMs })) if (value) form.append(key, String(value));
            const headers = upload.idempotencyKey ? { "X-Idempotency-Key": upload.idempotencyKey } : undefined;
            const { resource } = await call<{ resource: { id: string; kind: string; mimeType?: string; size?: number; width?: number; height?: number; durationMs?: number } }>(
                { method: "POST", path: "/resources", body: form, headers, signal: upload.signal },
                Boolean(upload.idempotencyKey),
            );
            return { resourceId: resource.id, kind: resource.kind, mimeType: resource.mimeType, bytes: resource.size, width: resource.width, height: resource.height, ...(resource.durationMs ? { durationMs: resource.durationMs } : {}) };
        },

        /** 获取资源的授权访问地址；地址会过期，不能当作持久身份保存。 */
        async resourceAccess(resourceId: string, purpose: "display" | "download" | "provider-input" = "display", signal?: AbortSignal) {
            const { items } = await call<{ items: Array<{ resourceId: string; access?: { url: string; expiresAt?: string | null }; error?: { reason?: string; msg?: string } }> }>(
                { method: "POST", path: "/resources/access", body: [{ resourceId, purpose }], signal },
                true,
            );
            const item = items[0];
            if (!item?.access) throw new PlatformRequestError({ code: "not_found", reason: item?.error?.reason ?? "not_found", message: item?.error?.msg ?? "资源不可访问", retryable: false });
            return item.access;
        },
    };
}

export type CreationPlatformClient = ReturnType<typeof createCreationPlatformClient>;

// 终态之后作品转存与费用结算仍在同一 worker 收尾；两者都结束才算“已落定”，避免读到 succeeded + running 的中间态。
function settled(snapshot: TaskSnapshot) {
    const billingPending = snapshot.billing.status === "reserved" || snapshot.billing.status === "running";
    return isTerminalExecution(snapshot.execution) && snapshot.resources !== "delivering" && !(snapshot.execution === "succeeded" && billingPending);
}

function assertValid(request: GenerationRequest) {
    const invalid = validateGenerationRequest(request);
    if (invalid) throw new PlatformRequestError(invalid);
}

// 未知执行状态不能被当成成功：直接按内部错误抛出。
function checkedSnapshot(snapshot: TaskSnapshot) {
    if (!EXECUTION_STATES.includes(snapshot.execution)) {
        throw new PlatformRequestError({ code: "internal", reason: "internal", message: `未知任务状态：${String(snapshot.execution)}`, retryable: false, taskId: snapshot.taskId });
    }
    return snapshot;
}

function envelopeError(status: number, body: { code: number; msg?: string; reason?: string; details?: Record<string, unknown> }): PlatformError {
    const reason = body.reason ?? "";
    const code = (PLATFORM_ERROR_CODES as readonly string[]).includes(reason) ? (reason as PlatformErrorCode) : status === 401 ? "unauthorized" : status === 404 ? "not_found" : "internal";
    const details = body.details ?? {};
    return {
        code,
        reason,
        message: body.msg || "请求失败",
        retryable: typeof details.retryable === "boolean" ? details.retryable : status >= 500,
        ...(typeof details.taskId === "string" ? { taskId: details.taskId } : {}),
        ...(typeof details.requestId === "string" ? { requestId: details.requestId } : {}),
        httpStatus: status,
    };
}

function isAbort(error: unknown) {
    return (error instanceof DOMException || error instanceof Error) && error.name === "AbortError";
}

function sleep(ms: number, signal?: AbortSignal) {
    return new Promise<void>((resolve, reject) => {
        if (signal?.aborted) return reject(new DOMException("等待已中止", "AbortError"));
        const timer = setTimeout(() => {
            signal?.removeEventListener("abort", onAbort);
            resolve();
        }, ms);
        const onAbort = () => {
            clearTimeout(timer);
            reject(new DOMException("等待已中止", "AbortError"));
        };
        signal?.addEventListener("abort", onAbort, { once: true });
    });
}
