import { canvasNodeToAsset, declaredCanvasNodeAssetCategory, findCanvasNodeAsset, type CanvasAssetSource } from "@/lib/canvas/canvas-node-asset";
import { registeredGenerationOutput } from "@/lib/canvas/registered-generation-output";
import { parseBackendGenerationResult, type BackendGenerationResult } from "@/services/api/generation-task";
import { ApiError } from "@/services/api/request";
import { linkProjectAsset, moveProjectAsset, updateProjectAssetCategory } from "@/services/api/projects";
import { queryGenerationTask, type GenerationTask, type GenerationTaskOutput } from "@/services/api/task-center";
import { createGenerationTaskMaterializer, createIdempotentMaterializeOutput, type MaterializeGenerationTaskOutput } from "@/services/generation-task-materializer";
import { uploadGeneratedAssetToConfiguredSources } from "@/services/external-asset-sources";
import { createProviderNeutralGenerationTaskEffectStore } from "@/services/provider-neutral-generation-effects";
import { loadAssetsForUse, saveRemoteUserDataNow } from "@/services/user-data-sync";
import { getActiveUserScope } from "@/lib/user-scope";
import { normalizeAssetCategory } from "@/lib/asset-category";
import { runGenerationConsumer } from "@/services/generation-consumer-lifecycle";
import { useAssetStore, type AssetCategory, type AssetStatus } from "@/stores/use-asset-store";
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
};

const pendingAssetSyncs = new Map<string, Promise<CanvasNodeAssetResult>>();
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
    const key = [scope, options.domainProjectId || "personal", options.canvasId, options.node.id, identity].join(":");
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
        // 个人画布也必须在返回成功前把素材提交到服务端，不能只依赖延迟自动同步。
        await saveRemoteUserDataNow();
        throwIfAborted(options.signal);
        return { assetId: asset.id, created, linkedToProject: false };
    }
    await syncAssetToProject(asset.id, options.domainProjectId, declaredCategory, options.folderId, options.signal);
    return { assetId: asset.id, created, linkedToProject: true };
}

async function syncAssetToProject(assetId: string, domainProjectId: string, category?: AssetCategory, folderId?: string, signal?: AbortSignal) {
    throwIfAborted(signal);
    const asset = useAssetStore.getState().assets.find((candidate) => candidate.id === assetId);
    if (!asset) throw new Error("素材写入本地失败");
    const linkedProjectIds = Array.isArray(asset.metadata?.projectIds) ? asset.metadata.projectIds.filter((id): id is string => typeof id === "string") : [];
    if (linkedProjectIds.includes(domainProjectId)) {
        if (folderId !== undefined) await moveProjectAsset(domainProjectId, asset.id, folderId, signal);
        return;
    }

    // 项目关联依赖后端 assets 记录，先强制完成素材同步，不能依赖延迟自动同步的时序。
    await saveRemoteUserDataNow();
    throwIfAborted(signal);
    const { asset: linkedAsset } = await linkProjectAsset(
        domainProjectId,
        {
            assetId: asset.id,
            category: normalizeAssetCategory(category || asset.category),
            folderId,
        },
        signal,
    );
    throwIfAborted(signal);
    let linked = category && linkedAsset.category !== category ? (await updateProjectAssetCategory(domainProjectId, asset.id, category, signal)).asset : linkedAsset;
    if (folderId !== undefined && (linked.folderId || "") !== folderId) linked = (await moveProjectAsset(domainProjectId, asset.id, folderId, signal)).asset;
    throwIfAborted(signal);
    useAssetStore.getState().updateAsset(asset.id, {
        category: normalizeAssetCategory(linked.category),
        status: linked.status as AssetStatus,
        primaryVersionId: linked.primaryVersionId,
        metadata: { ...asset.metadata, projectIds: [...new Set([...linkedProjectIds, domainProjectId])] },
    });
    await saveRemoteUserDataNow();
    throwIfAborted(signal);
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
