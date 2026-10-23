import { useEffect, useState } from "react";
import { App, Button, Modal } from "antd";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, LoaderCircle } from "lucide-react";
import { CachedResourceImage } from "@/components/cached-resource-image";
import { readCanvasMediaVersions, retryTaskMaterialization } from "@/services/api/canvas-commands";
import { cancelGenerationTask, queryGenerationTask, recoverGenerationTaskMedia, subscribeGenerationTasks } from "@/services/api/task-center";
import { canCancelGenerationTask, generationTaskStatusLabel } from "@/lib/generation-task-display";
import { resolveMediaUrl } from "@/services/file-storage";
import { commitCanvasCommandEditor, openCanvasCommandSession, useCanvasSessionStore } from "@/stores/canvas/use-canvas-session-store";
import { getActiveUserScope } from "@/lib/user-scope";
import { producedModelLabel } from "@/lib/canvas/produced-model";
import { useEffectiveConfig } from "@/stores/use-config-store";
import type { Item as MediaVersion } from "@/lib/canvas/command-contract.generated";
import type { CanvasNodeData } from "@/types/canvas";
import "./canvas-media-versions-modal.css";

export function CanvasMediaVersionsModal({ canvasId, node, onClose, onGenerate, onTaskDetails }: { canvasId: string; node: CanvasNodeData; onClose: () => void; onGenerate: () => void; onTaskDetails: () => void }) {
    const { message } = App.useApp();
    const queryClient = useQueryClient();
    const config = useEffectiveConfig();
    const userScope = getActiveUserScope();
    const versionState = node.metadata?.mediaVersion;
    const key = ["canvas-media-versions", userScope, canvasId, node.id, versionState?.epoch, versionState?.generation, versionState?.selectionRevision, versionState?.versionCount, versionState?.pendingVersionId];
    const query = useInfiniteQuery({ queryKey: key, initialPageParam: 0, queryFn: ({ pageParam, signal }) => readCanvasMediaVersions(canvasId, node.id, pageParam, signal), getNextPageParam: (page) => page.nextBeforeVersion || undefined });
    const taskId = versionState?.activeTaskId;
    const task = useQuery({ queryKey: ["media-version-task", userScope, taskId], enabled: Boolean(taskId), queryFn: ({ signal }) => queryGenerationTask(taskId!, { signal }) });
    useEffect(() => {
        if (!taskId) return;
        return subscribeGenerationTasks([taskId], (value) => {
            queryClient.setQueryData(["media-version-task", userScope, taskId], value);
            if (["succeeded", "failed", "cancelled"].includes(value.status)) void queryClient.invalidateQueries({ queryKey: ["canvas-media-versions", userScope, canvasId, node.id] });
        });
    }, [canvasId, node.id, queryClient, taskId, userScope]);
    const [previewId, setPreviewId] = useState<string>();
    const [outputIndex, setOutputIndex] = useState(0);
    const [busy, setBusy] = useState<string>();
    const items = query.data?.pages.flatMap((page) => page.items) || [];
    const page = query.data?.pages[0];
    const preview = items.find((item) => item.versionId === previewId) || items.find((item) => item.isCurrent) || items.find((item) => item.status === "ready");
    const output = preview?.outputs?.[outputIndex];
    const modelLabel = preview?.model ? producedModelLabel(config, preview.model) : "";
    async function perform(id: string, action: () => Promise<unknown>) {
        setBusy(id);
        try {
            await action();
            if (getActiveUserScope() === userScope) await query.refetch();
        } catch (error) {
            if (getActiveUserScope() === userScope) message.error(error instanceof Error ? error.message : "操作未完成");
        } finally {
            setBusy(undefined);
        }
    }
    const useVersion = (versionId: string) =>
        perform(versionId, async () => {
            const scope = useCanvasSessionStore.getState().scope;
            await commitCanvasCommandEditor(canvasId);
            const session = await openCanvasCommandSession(canvasId);
            if (getActiveUserScope() !== userScope || useCanvasSessionStore.getState().scope !== scope) throw new DOMException("画布已切换", "AbortError");
            await session.submit([{ type: "use_media_version", id: node.id, versionId }], { preview: { entities: [] } });
        });
    return (
        <Modal open title="媒体版本" onCancel={onClose} footer={null} width={880} centered destroyOnHidden className="canvas-media-versions-modal">
            <div className="media-version-preview" data-canvas-no-zoom data-canvas-wheel-scroll>
                <MediaVersionPreview key={`${preview?.versionId || node.id}:${outputIndex}`} mode={output?.mode || preview?.mode || node.type} media={output?.media || (preview ? preview.media || {} : node.metadata || {})} />
            </div>
            {preview?.prompt || modelLabel ? (
                <details className="media-version-details">
                    <summary>
                        {modelLabel || "生成信息"}
                        {preview?.options?.size ? ` · ${String(preview.options.size)}` : ""}
                        {preview?.options?.videoSeconds ? ` · ${String(preview.options.videoSeconds)}s` : ""}
                    </summary>
                    {preview?.prompt ? <p>{preview.prompt}</p> : null}
                </details>
            ) : null}
            {(preview?.outputs?.length || 0) > 1 ? (
                <div className="media-version-progress" aria-label="版本内素材">
                    {preview?.outputs?.map((item, index) => (
                        <Button key={item.index} size="small" aria-pressed={index === outputIndex} onClick={() => setOutputIndex(index)}>
                            素材 {index + 1}
                        </Button>
                    ))}
                </div>
            ) : null}
            {taskId ? (
                <div className="media-version-progress" role="status">
                    <span>
                        {task.data ? generationTaskStatusLabel(task.data) : "读取任务…"}
                        {typeof task.data?.progress === "number" ? ` · ${task.data.progress}%` : ""}
                    </span>
                    <Button size="small" onClick={onTaskDetails}>
                        任务详情
                    </Button>
                    {task.data && canCancelGenerationTask(task.data) ? (
                        <Button size="small" loading={busy === "cancel"} onClick={() => void perform("cancel", () => cancelGenerationTask(taskId))}>
                            取消生成
                        </Button>
                    ) : null}
                    {task.data?.canRecoverMedia ? (
                        <Button size="small" loading={busy === "recover"} onClick={() => void perform("recover", () => recoverGenerationTaskMedia(taskId))}>
                            恢复保存
                        </Button>
                    ) : null}
                    {task.data?.status === "succeeded" ? (
                        <Button size="small" loading={busy === "materialize"} onClick={() => void perform("materialize", () => retryTaskMaterialization(taskId))}>
                            同步结果
                        </Button>
                    ) : null}
                </div>
            ) : null}
            {query.isLoading ? (
                <p role="status">读取版本…</p>
            ) : query.isError ? (
                <div role="alert" className="media-version-feedback">
                    <span>版本暂时无法读取</span>
                    <Button size="small" onClick={() => void query.refetch()}>
                        重试
                    </Button>
                </div>
            ) : (
                <CanvasMediaVersionList
                    items={items}
                    previewId={preview?.versionId}
                    busy={busy}
                    locked={Boolean(node.metadata?.locked)}
                    onPreview={(id) => {
                        setPreviewId(id);
                        setOutputIndex(0);
                    }}
                    onUse={(id) => void useVersion(id)}
                />
            )}
            {!query.isLoading && !query.isError && items.length === 0 ? <p className="media-version-empty">当前媒体将在首次生成新版本时保留。</p> : null}
            {!taskId && page?.blockIssue ? <p className="media-version-empty">{page.blockIssue}</p> : null}
            <div className="media-version-footer">
                {query.hasNextPage ? (
                    <Button loading={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
                        更早版本
                    </Button>
                ) : (
                    <span />
                )}
                <Button
                    type="primary"
                    disabled={!page?.canCreateVersion || Boolean(busy) || Boolean(node.metadata?.locked)}
                    onClick={() => {
                        onClose();
                        onGenerate();
                    }}
                >
                    生成新版本
                </Button>
            </div>
        </Modal>
    );
}

export function CanvasMediaVersionList({ items, previewId, busy, locked, onPreview, onUse }: { items: MediaVersion[]; previewId?: string; busy?: string; locked?: boolean; onPreview: (id: string) => void; onUse: (id: string) => void }) {
    return (
        <ol className="media-version-list" aria-label="媒体版本列表" data-canvas-wheel-scroll>
            {items.map((item) => (
                <li key={item.versionId}>
                    <button type="button" className="media-version-entry" aria-pressed={previewId === item.versionId} onClick={() => onPreview(item.versionId)}>
                        <span className="media-version-sequence">V{item.sequence}</span>
                        <span>
                            {item.isCurrent ? (
                                <>
                                    <Check size={14} />
                                    当前
                                </>
                            ) : item.status === "materializing" ? (
                                "保存未完成"
                            ) : item.status === "submission_uncertain" ? (
                                "待确认"
                            ) : item.isPending ? (
                                <>
                                    <LoaderCircle size={14} className="media-version-spinner" />
                                    生成中
                                </>
                            ) : item.status === "ready" ? (
                                "已保存"
                            ) : item.status === "cancelled" ? (
                                "已取消"
                            ) : item.status === "failed" ? (
                                "未完成"
                            ) : (
                                "待确认"
                            )}
                        </span>
                        <time dateTime={item.createdAt}>{new Date(item.createdAt).toLocaleString()}</time>
                    </button>
                    <Button size="small" aria-label={`${item.isCurrent ? "保持" : "采用"}版本 V${item.sequence}`} loading={busy === item.versionId} disabled={item.status !== "ready" || Boolean(busy) || locked} onClick={() => onUse(item.versionId)}>
                        {item.isCurrent ? "保持此版本" : "采用"}
                    </Button>
                </li>
            ))}
        </ol>
    );
}

function MediaVersionPreview({ mode, media }: { mode: string; media: Record<string, unknown> }) {
    const storageKey = typeof media.storageKey === "string" ? media.storageKey : undefined;
    const content = typeof media.content === "string" ? media.content : "";
    const scope = getActiveUserScope();
    const [source, setSource] = useState("");
    const [failed, setFailed] = useState(false);
    useEffect(() => {
        let active = true;
        setSource("");
        setFailed(false);
        if (mode !== "image")
            void resolveMediaUrl(storageKey, content)
                .then((url) => {
                    if (active && getActiveUserScope() === scope) setSource(url);
                })
                .catch(() => {
                    if (active && getActiveUserScope() === scope) setFailed(true);
                });
        return () => {
            active = false;
        };
    }, [content, mode, scope, storageKey]);
    if (!content && !storageKey) return <span>媒体尚未就绪</span>;
    if (mode === "image") return <CachedResourceImage storageKey={storageKey} src={content} alt="版本图片" eager fallback={<span>图片暂不可用</span>} />;
    if (!source) return <span>{failed ? "媒体暂不可用" : "读取媒体…"}</span>;
    return mode === "video" ? <video key={source} src={source} controls preload="metadata" playsInline aria-label="版本视频" /> : <audio key={source} src={source} controls preload="metadata" aria-label="版本音频" />;
}
