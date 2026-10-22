import { InputNumber, Popover, Select } from "antd";
import { AppModal } from "@/components/ui/product/app-modal";
import { Tooltip } from "@/components/ui/base/tooltip";
import { useEffect, useMemo, useState } from "react";

import { Image as ImageIcon, Music2, Play, UserRound, Pencil, Link2 } from "lucide-react";

import { CanvasVideoPreviewImage } from "@/components/canvas/canvas-video-preview-image";
import { isStoryboardPreviewAsset, bindingForConnectedNode } from "@/lib/canvas/canvas-storyboard-materializer";
import { resolveMediaUrl } from "@/services/file-storage";
import { canvasAssetPreviewFallback, resolveCanvasAssetPreviewInput } from "@/services/canvas-asset-preview";
import { CanvasNodeType, type CanvasNodeData, type StoryboardAssetBinding } from "@/types/canvas";
import "./storyboard-assets-cell.css";

export const STORYBOARD_ASSET_ROLE_LABELS: Record<StoryboardAssetBinding["role"], string> = {
    character: "角色",
    environment: "场景",
    wardrobe: "服装",
    prop: "道具",
    weapon: "武器",
    style: "风格",
    motion: "动态",
    audio: "音频",
};
const ROLE_LABELS = STORYBOARD_ASSET_ROLE_LABELS;

export function StoryboardSharedReferencesControl({ node, nodes, onChange }: { node: CanvasNodeData; nodes: CanvasNodeData[]; onChange: (ids: string[]) => void }) {
    const ids = node.metadata?.storyboard?.referenceNodeIds || [];
    return (
        <Popover
            trigger="click"
            title="分镜表共享素材"
            content={
                <div className="max-w-[calc(100vw-3rem)] space-y-3" style={{ width: 352 }} data-canvas-no-zoom data-canvas-wheel-scroll>
                    <p className="max-w-[60ch] text-pretty text-xs leading-relaxed text-foreground/60">所有镜头都会继承这些素材。移除共享素材后，各镜头单独选择的素材仍会保留。</p>
                    <Select
                        className="w-full"
                        mode="multiple"
                        maxCount={100}
                        aria-label="分镜表共享素材"
                        placeholder="选择画布素材或文本"
                        value={ids}
                        options={nodes
                            .filter((candidate) => candidate.id !== node.id && (isStoryboardPreviewAsset(candidate) || candidate.type === CanvasNodeType.Text || candidate.type === CanvasNodeType.Markdown))
                            .map((candidate) => ({ value: candidate.id, label: candidate.title }))}
                        onChange={onChange}
                    />
                </div>
            }
        >
            <button
                type="button"
                className="inline-flex min-h-11 shrink-0 items-center gap-1.5 rounded-[var(--button-radius)] px-2 text-xs text-foreground/65 hover:bg-foreground/5 focus-visible:outline-2 focus-visible:outline-ring md:min-h-8"
                onPointerDown={(event) => event.stopPropagation()}
            >
                <Link2 className="size-3.5" />
                <span>共享素材</span>
                <span className="tabular-nums text-foreground/45">{ids.length}</span>
            </button>
        </Popover>
    );
}

export function StoryboardAssetsCell({
    bindings,
    nodes,
    inherited = [],
    onChange,
    onEdit,
    disabled = false,
    pending = false,
    limit = 2,
}: {
    bindings: StoryboardAssetBinding[];
    nodes: CanvasNodeData[];
    inherited?: Array<StoryboardAssetBinding & { source: string }>;
    onChange?: (bindings: StoryboardAssetBinding[]) => void;
    /** 提供时铅笔入口打开完整的镜头资产选择弹窗（含项目资产引入），否则回退到行内浮层。 */
    onEdit?: () => void;
    disabled?: boolean;
    pending?: boolean;
    limit?: number;
}) {
    const [previewNode, setPreviewNode] = useState<CanvasNodeData | null>(null);
    const nodeById = useMemo(() => new Map(nodes.map((node) => [node.id, node])), [nodes]);
    const assets = [...bindings.map((binding) => ({ ...binding, source: "镜头素材" })), ...inherited].map((binding) => ({ binding, node: nodeById.get(binding.nodeId) })).filter((item) => !item.node || isStoryboardPreviewAsset(item.node));
    const visible = assets.slice(0, limit);
    const hiddenCount = Math.max(0, assets.length - visible.length);

    return (
        <>
            <div className="flex w-full min-w-0 items-center gap-2" aria-label={`已关联 ${assets.length} 个资产`}>
                <div className="min-w-0 flex-1 space-y-1">
                    <div className="flex min-w-0 items-center gap-1.5 overflow-hidden">
                        {!assets.length ? <span className="text-[var(--fs-caption)] text-foreground/35">未关联</span> : null}
                        {visible.map(({ binding, node }) => (
                            <Tooltip key={binding.nodeId} title={`${node?.title || "资产已失效"} · ${ROLE_LABELS[binding.role]} · ${binding.source}`}>
                                <button
                                    type="button"
                                    data-icon-only
                                    disabled={!node}
                                    className="storyboard-assets-cell-preview relative grid size-11 shrink-0 place-items-center overflow-hidden rounded-[var(--button-radius)] border border-foreground/10 bg-foreground/[0.035] text-foreground/60 outline-none enabled:hover:border-foreground/30 enabled:hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed"
                                    aria-label={`预览${node?.title || "失效资产"}`}
                                    onMouseDown={(event) => event.stopPropagation()}
                                    onPointerDown={(event) => event.stopPropagation()}
                                    onClick={(event) => {
                                        event.stopPropagation();
                                        if (node) setPreviewNode(node);
                                    }}
                                >
                                    {node ? <AssetThumbnail node={node} /> : <ImageIcon className="size-4" />}
                                    <span className="absolute bottom-0.5 right-0.5 rounded-[var(--card-radius)] border border-foreground/10 bg-[var(--site-surface)] px-1 text-[8px] leading-3 text-foreground">{ROLE_LABELS[binding.role].slice(0, 1)}</span>
                                </button>
                            </Tooltip>
                        ))}
                        {hiddenCount ? <span className="shrink-0 text-[var(--fs-caption)] font-medium text-foreground/45">+{hiddenCount}</span> : null}
                    </div>
                    {inherited.length || pending ? (
                        <p className="truncate text-[var(--fs-caption)] text-foreground/50" title={pending ? "素材调整用于下次生成，运行中的任务保持原输入" : undefined}>
                            {pending ? "下次生成素材" : `含 ${inherited.length} 个共享素材`}
                        </p>
                    ) : null}
                </div>
                {onEdit ? (
                    <button
                        type="button"
                        aria-label={assets.length ? "管理镜头素材" : "选择镜头素材"}
                        title={disabled ? "请先解锁分镜节点" : undefined}
                        disabled={disabled}
                        className="grid size-11 shrink-0 place-items-center rounded-[var(--button-radius)] text-foreground/60 hover:bg-foreground/5 focus-visible:outline-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-40"
                        onMouseDown={(event) => event.stopPropagation()}
                        onPointerDown={(event) => event.stopPropagation()}
                        onClick={(event) => {
                            event.stopPropagation();
                            onEdit();
                        }}
                    >
                        <Pencil className="size-3.5" />
                    </button>
                ) : onChange ? (
                    <Popover trigger="click" title="镜头素材" content={<StoryboardAssetEditor bindings={bindings} inherited={inherited} nodes={nodes} onChange={onChange} pending={pending} />}>
                        <button
                            type="button"
                            aria-label="编辑镜头素材"
                            className="grid size-11 shrink-0 place-items-center rounded-[var(--button-radius)] text-foreground/60 hover:bg-foreground/5 focus-visible:outline-2 focus-visible:outline-ring"
                            onPointerDown={(event) => event.stopPropagation()}
                        >
                            <Pencil className="size-3.5" />
                        </button>
                    </Popover>
                ) : null}
            </div>
            <AssetPreviewModal node={previewNode} onClose={() => setPreviewNode(null)} />
        </>
    );
}

function StoryboardAssetEditor({
    bindings,
    inherited,
    nodes,
    onChange,
    pending,
}: {
    bindings: StoryboardAssetBinding[];
    inherited: Array<StoryboardAssetBinding & { source: string }>;
    nodes: CanvasNodeData[];
    onChange: (bindings: StoryboardAssetBinding[]) => void;
    pending: boolean;
}) {
    const nodeById = new Map(nodes.map((node) => [node.id, node]));
    const update = (nodeId: string, patch: Partial<StoryboardAssetBinding>) => onChange(bindings.map((binding) => (binding.nodeId === nodeId ? { ...binding, ...patch } : binding)));
    return (
        <div className="max-w-[calc(100vw-3rem)] space-y-4" style={{ width: 384 }} data-canvas-no-zoom data-canvas-wheel-scroll>
            <p className="max-w-[60ch] text-pretty text-xs leading-relaxed text-foreground/60">选择本镜头使用的画布素材，并设置用途与优先级。数字越大，排列越靠前。</p>
            <Select
                mode="multiple"
                maxCount={100}
                aria-label="镜头引用素材"
                className="w-full"
                placeholder="选择画布素材"
                value={bindings.map((binding) => binding.nodeId)}
                options={nodes.filter(isStoryboardPreviewAsset).map((node) => ({ value: node.id, label: node.title }))}
                onChange={(ids: string[]) =>
                    onChange(
                        ids.flatMap((id) => {
                            const binding = bindings.find((binding) => binding.nodeId === id) || (nodeById.has(id) ? bindingForConnectedNode(nodeById.get(id)!) : null);
                            return binding ? [binding] : [];
                        }),
                    )
                }
            />
            {bindings.length ? (
                <div className="max-h-64 space-y-2 overflow-y-auto" data-canvas-wheel-scroll>
                    <div className="grid grid-cols-[minmax(0,1fr)_6rem_4rem] gap-2 text-[var(--fs-caption)] text-foreground/45">
                        <span>素材</span>
                        <span>用途</span>
                        <span>优先级</span>
                    </div>
                    {bindings.map((binding) => (
                        <div key={binding.nodeId} className="grid grid-cols-[minmax(0,1fr)_6rem_4rem] items-center gap-2">
                            <span className="truncate text-xs text-foreground" title={nodeById.get(binding.nodeId)?.title}>
                                {nodeById.get(binding.nodeId)?.title || "失效素材"}
                            </span>
                            <Select
                                aria-label={`${nodeById.get(binding.nodeId)?.title || "素材"}用途`}
                                value={binding.role}
                                options={Object.entries(ROLE_LABELS).map(([value, label]) => ({ value, label }))}
                                onChange={(role: StoryboardAssetBinding["role"]) => update(binding.nodeId, { role })}
                            />
                            <InputNumber
                                className="w-full tabular-nums"
                                aria-label={`${nodeById.get(binding.nodeId)?.title || "素材"}优先级`}
                                min={0}
                                max={100}
                                precision={0}
                                value={binding.priority}
                                onChange={(value) => update(binding.nodeId, { priority: Number(value) || 0 })}
                            />
                        </div>
                    ))}
                </div>
            ) : null}
            {inherited.length ? (
                <div className="space-y-2 border-t border-foreground/10 pt-3">
                    <p className="text-[var(--fs-caption)] text-foreground/50">表级共享 · {inherited.length}</p>
                    <div className="flex flex-wrap gap-1.5">
                        {inherited.map((binding) => (
                            <span key={binding.nodeId} className="max-w-full truncate rounded-[var(--card-radius)] bg-foreground/5 px-2 py-1 text-xs text-foreground/70">
                                {nodeById.get(binding.nodeId)?.title || "失效素材"}
                            </span>
                        ))}
                    </div>
                    <p className="max-w-[60ch] text-pretty text-xs text-foreground/50">共享素材在表级管理，清空镜头素材后仍会保留。</p>
                </div>
            ) : null}
            {pending ? <p className="max-w-[60ch] text-pretty text-xs leading-relaxed text-foreground/60">任务正在生成。这里的调整用于下次生成，当前任务继续使用提交时的素材。</p> : null}
            {bindings.length ? (
                <button type="button" className="min-h-11 rounded-[var(--button-radius)] px-2 text-xs text-foreground/60 hover:bg-foreground/5 focus-visible:outline-2 focus-visible:outline-ring" onClick={() => onChange([])}>
                    清空镜头素材
                </button>
            ) : null}
        </div>
    );
}

export function AssetThumbnail({ node }: { node: CanvasNodeData }) {
    return (
        <span className="storyboard-asset-thumbnail">
            <AssetThumbnailContent node={node} />
        </span>
    );
}

function AssetThumbnailContent({ node }: { node: CanvasNodeData }) {
    const { source } = useNodeMediaSource(node.type === CanvasNodeType.Video ? null : node);
    if (node.type === CanvasNodeType.Audio) return <Music2 className="size-4" />;
    if (node.metadata?.workflowKind === "character" && !source) return <UserRound className="size-4" />;
    if (node.type === CanvasNodeType.Video) {
        return (
            <>
                <CanvasVideoPreviewImage node={node} alt="" loading="lazy" decoding="async" draggable={false} className="size-full object-cover" fallback={<Play className="size-4" />} />
                <span className="absolute inset-0 grid place-items-center bg-foreground/10">
                    <span className="grid size-6 place-items-center rounded-[var(--button-radius)] bg-[var(--site-surface)] text-foreground">
                        <Play className="size-3.5 fill-current" />
                    </span>
                </span>
            </>
        );
    }
    return source ? <img src={source} alt="" loading="lazy" decoding="async" draggable={false} className="size-full object-cover" /> : <ImageIcon className="size-4" />;
}

function AssetPreviewModal({ node, onClose }: { node: CanvasNodeData | null; onClose: () => void }) {
    const { source, loading, error } = useNodeMediaSource(node);
    return (
        <AppModal flush title={node?.title || "资产预览"} open={Boolean(node)} onCancel={onClose} footer={null} width={880} centered destroyOnHidden>
            {node ? (
                <div className="grid min-h-56 place-items-center overflow-hidden rounded-[var(--card-radius)] bg-foreground/[0.035] p-4" data-canvas-no-zoom>
                    {node.type === CanvasNodeType.Video && source ? (
                        <video src={source} controls autoPlay playsInline className="max-h-[68vh] max-w-full rounded-[var(--card-radius)]" />
                    ) : node.type === CanvasNodeType.Audio && source ? (
                        <audio src={source} controls autoPlay className="w-full max-w-xl" />
                    ) : source ? (
                        <img src={source} alt={node.title || "资产预览"} className="max-h-[68vh] max-w-full object-contain" />
                    ) : loading ? (
                        <span role="status" className="text-sm text-foreground/45">正在加载资产预览…</span>
                    ) : error ? (
                        <span role="alert" className="max-w-[60ch] text-pretty text-sm text-foreground/60">{error}</span>
                    ) : (
                        <span className="text-sm text-foreground/45">当前资产没有可预览的媒体内容</span>
                    )}
                </div>
            ) : null}
        </AppModal>
    );
}

function useNodeMediaSource(node: CanvasNodeData | null) {
    const fallback = canvasAssetPreviewFallback(node);
    const metadata = node?.metadata;
    const key = JSON.stringify([node?.id, node?.type, metadata?.workflowKind, fallback, metadata?.storageKey, metadata?.characterAssetId, metadata?.characterVersionId, metadata?.characterVersionPolicy]);
    const [resolved, setResolved] = useState({ key, source: fallback, loading: Boolean(node), error: "" });
    useEffect(() => {
        let cancelled = false;
        const currentNode = node;
        setResolved({ key, source: fallback, loading: Boolean(currentNode), error: "" });
        if (currentNode) void resolveCanvasAssetPreviewInput(currentNode)
            .then(({ storageKey, fallback: mediaFallback }) => resolveMediaUrl(storageKey, mediaFallback))
            .then((source) => {
                if (!cancelled) setResolved({ key, source, loading: false, error: "" });
            })
            .catch((cause) => {
                if (!cancelled) setResolved({ key, source: fallback, loading: false, error: cause instanceof Error ? cause.message : "资产预览加载失败，请稍后重试" });
            });
        return () => {
            cancelled = true;
        };
    // The key tracks only media identity; moving a node must not restart its request.
    }, [key]);
    return resolved.key === key ? resolved : { key, source: fallback, loading: Boolean(node), error: "" };
}
