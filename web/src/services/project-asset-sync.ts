import { canvasNodeToAsset, declaredCanvasNodeAssetCategory, findCanvasNodeAsset, type CanvasAssetSource } from "@/lib/canvas/canvas-node-asset";
import { registeredGenerationOutput } from "@/lib/canvas/registered-generation-output";
import { CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, createCanvasProjectAssetLinkQueue, type PendingCanvasProjectAssetLink } from "@/lib/canvas/project-asset-link-queue";
import { projectAssetLinkStorageForScope } from "@/lib/canvas/project-asset-link-storage";
import { appQueryClient } from "@/lib/query-client";
import { parseBackendGenerationResult, type BackendGenerationResult } from "@/services/api/generation-task";
import { ApiError } from "@/services/api/request";
import { linkProjectAsset, moveProjectAsset, updateProjectAssetCategory } from "@/services/api/projects";
import { queryGenerationTask, type GenerationTask, type GenerationTaskOutput } from "@/services/api/task-center";
import { createGenerationTaskMaterializer, createIdempotentMaterializeOutput, type MaterializeGenerationTaskOutput } from "@/services/generation-task-materializer";
import { uploadGeneratedAssetToConfiguredSources } from "@/services/external-asset-sources";
import { createProviderNeutralGenerationTaskEffectStore } from "@/services/provider-neutral-generation-effects";
import { hasRemoteUserDataSyncSession, loadAssetsForUse, saveRemoteUserDataNow, subscribeRemoteUserDataReady, subscribeRemoteAssetDeletion } from "@/services/user-data-sync";
import { getActiveUserScope } from "@/lib/user-scope";
import { normalizeAssetCategory } from "@/lib/asset-category";
import { runGenerationConsumer } from "@/services/generation-consumer-lifecycle";
import { flushAssetStorePersistence, useAssetStore, type AssetCategory, type AssetStatus } from "@/stores/use-asset-store";
import type { CanvasNodeData } from "@/types/canvas";

function throwIfAborted(signal?: AbortSignal) {
    if (signal?.aborted) throw new DOMException("The operation was aborted", "AbortError");
}

type EnsureCanvasNodeAssetOptions = {
    canvasId: string;
    domainProjectId?: string;
    node: CanvasNodeData;
    source: CanvasAssetSource;
    taskId?: string;
    category?: AssetCategory;
    folderId?: string;
    signal?: AbortSignal;
};

export type CanvasNodeAssetResult = {
    assetId: string;
    created: boolean;
    linkedToProject: boolean;
    syncPending: boolean;
};

const pendingAssetSyncs = new Map<string, Promise<CanvasNodeAssetResult>>();
const blockedRetryAt = Number.MAX_SAFE_INTEGER;
const retryDelays = [2_000, 4_000, 8_000, 16_000, 30_000] as const;
const linkFlights = new Map<string, Promise<{ linked: boolean; pending: boolean }>>();
const retryTimers = new Map<string, ReturnType<typeof setTimeout>>();
const DEFAULT_RATE_LIMIT_RETRY_MS = 60_000;
const MAX_RATE_LIMIT_RETRY_MS = 5 * 60_000;

type CanvasAssetSyncRetryOptions = {
    signal?: AbortSignal;
    maxRetries?: number;
    wait?: (delayMs: number, signal?: AbortSignal) => Promise<void>;
};

function waitForCanvasAssetSyncRetry(delayMs: number, signal?: AbortSignal) {
    return new Promise<void>((resolve, reject) => {
        if (signal?.aborted) {
            reject(new DOMException("The operation was aborted", "AbortError"));
            return;
        }
        const timer = globalThis.setTimeout(() => {
            signal?.removeEventListener("abort", onAbort);
            resolve();
        }, delayMs);
        const onAbort = () => {
            globalThis.clearTimeout(timer);
            reject(new DOMException("The operation was aborted", "AbortError"));
        };
        signal?.addEventListener("abort", onAbort, { once: true });
    });
}

export async function retryCanvasAssetSyncAfterRateLimit<T>(operation: () => Promise<T>, options: CanvasAssetSyncRetryOptions = {}): Promise<T> {
    const maxRetries = Math.max(0, options.maxRetries ?? 2);
    const wait = options.wait ?? waitForCanvasAssetSyncRetry;
    for (let attempt = 0; ; attempt += 1) {
        throwIfAborted(options.signal);
        try {
            return await operation();
        } catch (error) {
            if (!(error instanceof ApiError) || error.status !== 429 || attempt >= maxRetries) throw error;
            const delayMs = Math.min(MAX_RATE_LIMIT_RETRY_MS, Math.max(0, error.retryAfterMs ?? DEFAULT_RATE_LIMIT_RETRY_MS));
            await wait(delayMs, options.signal);
        }
    }
}

export function ensureCanvasNodeAsset(options: EnsureCanvasNodeAssetOptions) {
    const scope = getActiveUserScope();
    const identity = options.taskId || options.node.metadata?.taskId || options.node.metadata?.storageKey || options.node.id;
    const key = [scope, options.domainProjectId || "personal", options.canvasId, options.node.id, identity, options.category || declaredCanvasNodeAssetCategory(options.node) || "", options.folderId ?? "unchanged"].join(":");
    const pending = pendingAssetSyncs.get(key);
    if (pending) return pending;
    const request = runGenerationConsumer(options.signal, (signal) => persistCanvasNodeAsset({ ...options, signal })).finally(() => pendingAssetSyncs.delete(key));
    pendingAssetSyncs.set(key, request);
    return request;
}

async function persistCanvasNodeAsset(options: EnsureCanvasNodeAssetOptions): Promise<CanvasNodeAssetResult> {
    throwIfAborted(options.signal);
    const store = useAssetStore.getState();
    let asset = findCanvasNodeAsset(store.assets, options.node, options.canvasId, options.taskId);
    const declaredCategory = options.category || declaredCanvasNodeAssetCategory(options.node);
    let created = false;
    const taskId = options.taskId || options.node.metadata?.taskId;
    if (!asset && taskId && options.source === "canvas-generation" && ["image", "video", "audio"].includes(options.node.type)) {
        const task = await queryGenerationTask(taskId, { signal: options.signal });
        const output = registeredGenerationOutput(task, undefined, options.node.metadata?.storageKey);
        await loadAssetsForUse([output.assetId]);
        throwIfAborted(options.signal);
        asset = useAssetStore.getState().assets.find((candidate) => candidate.id === output.assetId);
        if (!asset) throw new Error("生成素材尚未读取完成，未创建重复素材");
    }
    if (!asset) {
        const input = canvasNodeToAsset(options.node, { canvasId: options.canvasId, source: options.source, taskId: options.taskId });
        if (!input) throw new Error("当前节点没有可保存的素材内容");
        const assetId = store.addAsset(options.category ? { ...input, category: options.category } : input);
        asset = useAssetStore.getState().assets.find((item) => item.id === assetId);
        created = true;
    }
    if (!asset) throw new Error("素材写入本地失败");
    if (options.source === "canvas-generation" && (!asset.metadata?.canvasId || !asset.metadata?.nodeId)) {
        store.updateAsset(asset.id, { metadata: { ...asset.metadata, canvasId: asset.metadata?.canvasId || options.canvasId, nodeId: asset.metadata?.nodeId || options.node.id } });
        asset = useAssetStore.getState().assets.find((candidate) => candidate.id === asset?.id) || asset;
    }
    if (declaredCategory && asset.category !== declaredCategory) {
        store.updateAsset(asset.id, { category: declaredCategory });
        asset = useAssetStore.getState().assets.find((item) => item.id === asset?.id) || asset;
    }
    if (!options.domainProjectId) {
        if (!hasRemoteUserDataSyncSession()) return { assetId: asset.id, created, linkedToProject: false, syncPending: true };
        try {
            await saveRemoteUserDataNow([]);
            throwIfAborted(options.signal);
            return { assetId: asset.id, created, linkedToProject: false, syncPending: false };
        } catch (error) {
            if (!isRetryableAssetSyncError(error)) throw error;
            return { assetId: asset.id, created, linkedToProject: false, syncPending: true };
        }
    }
    // A queue entry must never outlive the local asset it describes after a refresh.
    await flushAssetStorePersistence();
    throwIfAborted(options.signal);
    const sync = await syncAssetToProject(asset.id, options.domainProjectId, declaredCategory, options.folderId, options.signal);
    return { assetId: asset.id, created, linkedToProject: sync.linked, syncPending: sync.pending };
}

function queueForScope(scope: string) {
    return createCanvasProjectAssetLinkQueue(projectAssetLinkStorageForScope(scope), CANVAS_PROJECT_ASSET_LINK_QUEUE_KEY, scope);
}

function isRetryableAssetSyncError(error: unknown) {
    if (error instanceof DOMException && error.name === "AbortError") return false;
    if (error instanceof ApiError) return error.status === 401 || error.retryable || error.status === undefined;
    if (typeof error === "object" && error !== null && "permanent" in error) return (error as { permanent?: unknown }).permanent !== true;
    return true;
}

function isOnline() {
    return typeof navigator === "undefined" || navigator.onLine !== false;
}

function scheduleRetry(scope: string, delayMs: number) {
    const previous = retryTimers.get(scope);
    if (previous) clearTimeout(previous);
    const timer = setTimeout(() => {
        retryTimers.delete(scope);
        void retryPendingCanvasProjectAssetLinks(scope).catch((error) => console.warn("云端项目资产关联重试失败", error));
    }, Math.max(0, delayMs));
    retryTimers.set(scope, timer);
}

async function scheduleNextRetry(scope: string) {
    if (scope !== getActiveUserScope() || !hasRemoteUserDataSyncSession() || !isOnline()) return;
    const entries = await queueForScope(scope).list();
    const next = entries.reduce((minimum, entry) => Math.min(minimum, entry.nextAttemptAt), blockedRetryAt);
    const timer = retryTimers.get(scope);
    if (timer) clearTimeout(timer);
    retryTimers.delete(scope);
    if (next !== blockedRetryAt) scheduleRetry(scope, next - Date.now());
}

async function performProjectAssetLink(job: PendingCanvasProjectAssetLink, scope: string, signal?: AbortSignal) {
    const ensureScope = () => {
        throwIfAborted(signal);
        if (scope !== getActiveUserScope()) throw new DOMException("账号已切换", "AbortError");
    };
    ensureScope();
    let asset = useAssetStore.getState().assets.find((candidate) => candidate.id === job.assetId);
    if (!asset) {
        try {
            await loadAssetsForUse([job.assetId]);
        } catch (error) {
            if (error instanceof ApiError || (error instanceof DOMException && error.name === "AbortError")) throw error;
            throw Object.assign(new Error("素材不存在或无权访问，无法关联到项目"), { permanent: true, cause: error });
        }
        ensureScope();
        asset = useAssetStore.getState().assets.find((candidate) => candidate.id === job.assetId);
    }
    if (!asset) throw Object.assign(new Error("素材已删除，无法关联到项目"), { permanent: true });
    const category = job.category as AssetCategory | undefined;
    const linkedProjectIds = Array.isArray(asset.metadata?.projectIds) ? asset.metadata.projectIds.filter((id): id is string => typeof id === "string") : [];
    // The remote asset record must exist before the project link is created.
    await saveRemoteUserDataNow([]);
    ensureScope();
    let linked;
    if (linkedProjectIds.includes(job.domainProjectId)) {
        if (category) linked = (await updateProjectAssetCategory(job.domainProjectId, asset.id, category, signal)).asset;
        if (job.folderId !== undefined) linked = (await moveProjectAsset(job.domainProjectId, asset.id, job.folderId, signal)).asset;
    } else {
        linked = (await linkProjectAsset(job.domainProjectId, {
            assetId: asset.id,
            category: normalizeAssetCategory(category || asset.category),
            folderId: job.folderId,
            source: "canvas",
        }, signal)).asset;
        ensureScope();
        if (category && linked.category !== category) linked = (await updateProjectAssetCategory(job.domainProjectId, asset.id, category, signal)).asset;
        if (job.folderId !== undefined && (linked.folderId || "") !== job.folderId) linked = (await moveProjectAsset(job.domainProjectId, asset.id, job.folderId, signal)).asset;
    }
    ensureScope();
    const current = useAssetStore.getState().assets.find((candidate) => candidate.id === asset.id);
    if (!current) throw Object.assign(new Error("素材已删除，无法关联到项目"), { permanent: true });
    useAssetStore.getState().updateAsset(asset.id, {
        ...(linked ? { category: normalizeAssetCategory(linked.category), status: linked.status as AssetStatus, primaryVersionId: linked.primaryVersionId } : {}),
        metadata: { ...current.metadata, projectIds: [...new Set([...linkedProjectIds, job.domainProjectId])] },
    });
    await saveRemoteUserDataNow([]);
    ensureScope();
}

async function attemptProjectAssetLink(job: PendingCanvasProjectAssetLink, scope: string, signal?: AbortSignal) {
    const key = `${scope}\0${job.domainProjectId}\0${job.assetId}`;
    const existing = linkFlights.get(key);
    if (existing) return existing;
    const queue = queueForScope(scope);
    const operation = (async () => {
        throwIfAborted(signal);
        if (scope !== getActiveUserScope()) throw new DOMException("账号已切换", "AbortError");
        if (!hasRemoteUserDataSyncSession() || !isOnline()) return { linked: false, pending: true };
        try {
            await performProjectAssetLink(job, scope, signal);
            if (!(await queue.remove(job))) {
                await scheduleNextRetry(scope);
                return { linked: false, pending: true };
            }
            void appQueryClient.invalidateQueries({ queryKey: ["project", job.domainProjectId] });
            await scheduleNextRetry(scope);
            return { linked: true, pending: false };
        } catch (error) {
            if ((error instanceof DOMException && error.name === "AbortError") || scope !== getActiveUserScope()) throw error;
            if (error instanceof ApiError && error.status === 401) {
                await queue.update(job, { attempts: job.attempts, nextAttemptAt: blockedRetryAt, blockedReason: "auth" });
                await scheduleNextRetry(scope);
                return { linked: false, pending: true };
            }
            if (isRetryableAssetSyncError(error)) {
                const backoff = retryDelays[Math.min(job.attempts, retryDelays.length - 1)];
                const retryAfter = error instanceof ApiError ? error.retryAfterMs || 0 : 0;
                await queue.update(job, { attempts: job.attempts + 1, nextAttemptAt: Date.now() + Math.max(backoff, retryAfter) });
                await scheduleNextRetry(scope);
                return { linked: false, pending: true };
            }
            await queue.update(job, { attempts: job.attempts, nextAttemptAt: blockedRetryAt });
            await scheduleNextRetry(scope);
            throw error;
        }
    })();
    linkFlights.set(key, operation);
    try { return await operation; }
    finally { if (linkFlights.get(key) === operation) linkFlights.delete(key); }
}

export async function retryPendingCanvasProjectAssetLinks(scope = getActiveUserScope()) {
    if (scope !== getActiveUserScope() || !hasRemoteUserDataSyncSession() || !isOnline()) return;
    for (const entry of await queueForScope(scope).list()) {
        if (scope !== getActiveUserScope()) return;
        if (entry.nextAttemptAt > Date.now()) continue;
        try { await attemptProjectAssetLink(entry, scope); }
        catch (error) { console.warn("云端项目资产关联需要处理", error); }
    }
    await scheduleNextRetry(scope);
}

async function resumePendingCanvasProjectAssetLinks(scope = getActiveUserScope()) {
    if (scope !== getActiveUserScope() || !hasRemoteUserDataSyncSession()) return;
    for (const entry of await queueForScope(scope).list()) {
        if (entry.blockedReason === "auth") await queueForScope(scope).update(entry, { attempts: 0, nextAttemptAt: 0 });
    }
    await retryPendingCanvasProjectAssetLinks(scope);
}

subscribeRemoteUserDataReady(() => resumePendingCanvasProjectAssetLinks());
subscribeRemoteAssetDeletion(async (scope, assetIds) => {
    await queueForScope(scope).removeAssets(assetIds);
    await scheduleNextRetry(scope);
});
if (typeof window !== "undefined") window.addEventListener("online", () => void retryPendingCanvasProjectAssetLinks());

async function syncAssetToProject(assetId: string, domainProjectId: string, category?: AssetCategory, folderId?: string, signal?: AbortSignal) {
    throwIfAborted(signal);
    const scope = getActiveUserScope();
    let job: PendingCanvasProjectAssetLink;
    try {
        job = await queueForScope(scope).upsert({ assetId, domainProjectId, category, folderId });
    } catch (error) {
        throw new Error(`素材已保存在本地，但项目关联待办未能持久化：${error instanceof Error ? error.message : "请检查浏览器存储后重试"}`);
    }
    throwIfAborted(signal);
    if (job.nextAttemptAt > Date.now()) return { linked: false, pending: true };
    return attemptProjectAssetLink(job, scope, signal);
}

function generationTaskResult(task: GenerationTask): BackendGenerationResult {
    if (task.resultJson) return parseBackendGenerationResult(task);
    if (!task.previewUrl) return {};
    return task.previewKind === "video" ? { mode: "video", video: { dataUrl: task.previewUrl } } : { mode: "image", images: [{ dataUrl: task.previewUrl }] };
}

export function projectGenerationTaskResult(task: GenerationTask, result?: BackendGenerationResult): GenerationTask {
    const projectedResult = result ?? generationTaskResult(task);
    const outputs: GenerationTaskOutput[] = projectedResult.images?.length
        ? projectedResult.images.map((image, outputIndex) => ({
              outputIndex,
              mediaType: "image" as const,
              ...(image.storageKey ? { providerArtifactRef: image.storageKey } : {}),
          }))
        : projectedResult.video
          ? [
                {
                    outputIndex: 0,
                    mediaType: "video" as const,
                    ...(projectedResult.video.storageKey ? { providerArtifactRef: projectedResult.video.storageKey } : {}),
                },
            ]
          : projectedResult.audio
            ? [
                  {
                      outputIndex: 0,
                      mediaType: "audio" as const,
                      ...(projectedResult.audio.storageKey ? { providerArtifactRef: projectedResult.audio.storageKey } : {}),
                  },
              ]
            : (task.outputs?.map((output) => ({ ...output })) ?? []);

    return {
        ...task,
        ...(result ? { resultJson: JSON.stringify(result) } : {}),
        outputs,
        ...(task.status === "succeeded" && outputs.length && !outputs.every((output) => output.materializedAssetId) ? { resultState: "PENDING_MATERIALIZATION" as const } : {}),
    };
}

const materializeGenerationOutput: MaterializeGenerationTaskOutput = createIdempotentMaterializeOutput({
    async insertOrReturnAsset(input) {
        const task = await queryGenerationTask(input.task.id, { signal: input.signal });
        const { assetId } = registeredGenerationOutput(task, input.output.outputIndex);
        await loadAssetsForUse([assetId]);
        throwIfAborted(input.signal);
        const storedAsset = useAssetStore.getState().assets.find((candidate) => candidate.id === assetId);
        if (storedAsset) {
            const syncResult = await uploadGeneratedAssetToConfiguredSources(storedAsset, input.signal);
            throwIfAborted(input.signal);
            if (syncResult.records.length) {
                const currentAsset = useAssetStore.getState().assets.find((candidate) => candidate.id === assetId) || storedAsset;
                useAssetStore.getState().updateAsset(assetId, {
                    metadata: { ...currentAsset.metadata, externalSync: syncResult.records },
                });
            }
        }
        return assetId;
    },
});

function remoteGenerationTaskMaterializer() {
    return createGenerationTaskMaterializer({
        // 每个页面实例持有自己的租约；共享浏览器 storage + Web Lock 提供跨标签原子权威。
        effects: createProviderNeutralGenerationTaskEffectStore(),
        materializeOutput: materializeGenerationOutput,
    });
}

function generationTaskMaterializer(task: GenerationTask) {
    return remoteGenerationTaskMaterializer();
}

export async function materializeGenerationTaskAssets(task: GenerationTask, signal?: AbortSignal): Promise<GenerationTask> {
    return generationTaskMaterializer(task).materialize(projectGenerationTaskResult(task), signal);
}

export function attachGenerationTaskNode(task: GenerationTask, nodeId: string, outputIndex: number, consumer: Parameters<ReturnType<typeof generationTaskMaterializer>["attachNode"]>[3], signal?: AbortSignal) {
    return generationTaskMaterializer(task).attachNode(task, nodeId, outputIndex, consumer, signal);
}

export function attachGenerationTaskMessage(task: GenerationTask, messageId: string, outputIndex: number, consumer: Parameters<ReturnType<typeof generationTaskMaterializer>["attachMessage"]>[3], signal?: AbortSignal) {
    return generationTaskMaterializer(task).attachMessage(task, messageId, outputIndex, consumer, signal);
}

export function resumeGenerationTaskAgent(task: GenerationTask, continuationId: string, consumer: Parameters<ReturnType<typeof generationTaskMaterializer>["resumeAgent"]>[2], signal?: AbortSignal) {
    return generationTaskMaterializer(task).resumeAgent(task, continuationId, consumer, signal);
}

export async function consumeGenerationTaskNode(
    task: GenerationTask,
    nodeId: string,
    outputIndex: number,
    consumer: (input: { task: GenerationTask; output: GenerationTaskOutput; effectKey: string; signal?: AbortSignal }) => Promise<void> | void,
    dependencies: {
        signal?: AbortSignal;
        managed?: true;
        materialize?: typeof materializeGenerationTaskAssets;
        attachNode?: typeof attachGenerationTaskNode;
    } = {},
): Promise<GenerationTask> {
    if (!dependencies.managed) {
        return runGenerationConsumer(dependencies.signal, (signal) => consumeGenerationTaskNode(task, nodeId, outputIndex, consumer, { ...dependencies, signal, managed: true }));
    }
    const materialized = await (dependencies.materialize ?? materializeGenerationTaskAssets)(task, dependencies.signal);
    const output = materialized.outputs?.find((candidate) => candidate.outputIndex === outputIndex);
    if (!output?.materializedAssetId) throw new Error("生成任务输出尚未物化");
    await (dependencies.attachNode ?? attachGenerationTaskNode)(
        materialized,
        nodeId,
        outputIndex,
        async ({ effectKey, signal }) => {
            await consumer({ task: materialized, output, effectKey, signal });
        },
        dependencies.signal,
    );
    return materialized;
}

export async function consumeGenerationTaskAgent(
    task: GenerationTask,
    continuationId: string,
    consumer: (input: { task: GenerationTask; effectKey: string; signal?: AbortSignal }) => Promise<void> | void,
    dependencies: {
        signal?: AbortSignal;
        managed?: true;
        materialize?: typeof materializeGenerationTaskAssets;
        resumeAgent?: typeof resumeGenerationTaskAgent;
    } = {},
): Promise<GenerationTask> {
    if (!dependencies.managed) {
        return runGenerationConsumer(dependencies.signal, (signal) => consumeGenerationTaskAgent(task, continuationId, consumer, { ...dependencies, signal, managed: true }));
    }
    const materialized = task.outputs?.length ? await (dependencies.materialize ?? materializeGenerationTaskAssets)(task, dependencies.signal) : task;
    await (dependencies.resumeAgent ?? resumeGenerationTaskAgent)(
        materialized,
        continuationId,
        async ({ effectKey, signal }) => {
            await consumer({ task: materialized, effectKey, signal });
        },
        dependencies.signal,
    );
    return materialized;
}

export async function consumeGenerationTaskMessage(
    task: GenerationTask,
    messageId: string,
    consumer: (input: { task: GenerationTask; resultUrls: string[]; resultStorageKeys: string[]; effectKey: string; signal?: AbortSignal }) => Promise<void> | void,
    dependencies: {
        signal?: AbortSignal;
        managed?: true;
        materialize?: typeof materializeGenerationTaskAssets;
        materializedUrls?: typeof generationTaskMaterializedUrls;
        materializedStorageKeys?: typeof generationTaskMaterializedStorageKeys;
        attachMessage?: typeof attachGenerationTaskMessage;
    } = {},
): Promise<GenerationTask> {
    if (!dependencies.managed) {
        return runGenerationConsumer(dependencies.signal, (signal) => consumeGenerationTaskMessage(task, messageId, consumer, { ...dependencies, signal, managed: true }));
    }
    const materialized = await (dependencies.materialize ?? materializeGenerationTaskAssets)(task, dependencies.signal);
    const resultUrls = (dependencies.materializedUrls ?? generationTaskMaterializedUrls)(materialized);
    const resultStorageKeys = (dependencies.materializedStorageKeys ?? generationTaskMaterializedStorageKeys)(materialized);
    const attach = dependencies.attachMessage ?? attachGenerationTaskMessage;
    const outputs = materialized.outputs?.filter((output) => output.materializedAssetId) ?? [];
    for (const output of outputs) {
        await attach(
            materialized,
            messageId,
            output.outputIndex,
            async ({ effectKey, signal }) => {
                await consumer({ task: materialized, resultUrls, resultStorageKeys, effectKey, signal });
            },
            dependencies.signal,
        );
    }
    return materialized;
}

export function generationTaskMaterializedUrls(task: GenerationTask): string[] {
    const assets = useAssetStore.getState().assets;
    return (task.outputs || []).flatMap((output) => {
        const asset = output.materializedAssetId ? assets.find((candidate) => candidate.id === output.materializedAssetId) : undefined;
        if (!asset) return [];
        if (asset.kind === "image") return [asset.data.dataUrl || asset.coverUrl].filter(Boolean);
        if (asset.kind === "video" || asset.kind === "audio") return [asset.data.url].filter(Boolean);
        return [];
    });
}

/**
 * 返回生成结果的稳定素材定位符。对于远程资源是 resource:<id>，对于本地降级素材是
 * 本地 IndexedDB storageKey。它可以持久化到历史记录，不能被短时效签名 URL 替代。
 */
export function generationTaskMaterializedStorageKeys(task: GenerationTask): string[] {
    const assets = useAssetStore.getState().assets;
    return (task.outputs || []).flatMap((output) => {
        const asset = output.materializedAssetId ? assets.find((candidate) => candidate.id === output.materializedAssetId) : undefined;
        if (!asset || (asset.kind !== "image" && asset.kind !== "video" && asset.kind !== "audio")) return [];
        return asset.data.storageKey ? [asset.data.storageKey] : [];
    });
}
