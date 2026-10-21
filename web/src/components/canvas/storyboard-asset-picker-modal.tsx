import { Button, Input, InputNumber, Select } from "antd";
import { Check, Library, Search, X } from "lucide-react";
import { useMemo, useState } from "react";

import { CanvasProjectAssetModal } from "@/components/canvas/canvas-project-asset-modal";
import type { InsertAssetPayload } from "@/components/canvas/asset-picker-modal";
import { AssetThumbnail, STORYBOARD_ASSET_ROLE_LABELS } from "@/components/canvas/storyboard-assets-cell";
import { AppModal } from "@/components/ui/product/app-modal";
import { bindingForConnectedNode, isStoryboardPreviewAsset } from "@/lib/canvas/canvas-storyboard-materializer";
import { normalizeStoryboardAssetBindings } from "@/lib/canvas/canvas-storyboard-assets";
import type { ProjectDetail } from "@/services/api/projects";
import type { CanvasNodeData, StoryboardAssetBinding } from "@/types/canvas";

const ROLE_OPTIONS = Object.entries(STORYBOARD_ASSET_ROLE_LABELS).map(([value, label]) => ({ value, label }));
// 镜头产出（首帧、动作板、成片）不作为本镜头的输入素材候选，避免自引用。
const OUTPUT_WORKFLOW_KINDS = new Set(["shot", "action_board", "final"]);

/**
 * 镜头关联资产选择弹窗：只编辑本镜头自己的 assetBindings。
 * 表级共享与出镜角色继承仅展示，需在各自入口修改；保存结果由调用方转成 set_row_assets 意图。
 */
export function StoryboardAssetPickerModal({
    scriptNodeId,
    nodes,
    bindings,
    inherited,
    shotNumber,
    detail,
    pending = false,
    onClose,
    onSave,
    onImport,
}: {
    scriptNodeId: string;
    nodes: CanvasNodeData[];
    bindings: StoryboardAssetBinding[];
    inherited: Array<StoryboardAssetBinding & { source: string }>;
    shotNumber: number;
    detail?: ProjectDetail;
    pending?: boolean;
    onClose: () => void;
    onSave: (bindings: StoryboardAssetBinding[]) => void;
    onImport: (payloads: InsertAssetPayload[]) => Promise<CanvasNodeData[]>;
}) {
    const [draft, setDraft] = useState(() => normalizeStoryboardAssetBindings(bindings));
    const [query, setQuery] = useState("");
    const [role, setRole] = useState("all");
    const [libraryOpen, setLibraryOpen] = useState(false);
    const [error, setError] = useState("");
    const nodeById = useMemo(() => new Map(nodes.map((node) => [node.id, node])), [nodes]);
    const selectedIds = new Set(draft.map((binding) => binding.nodeId));
    const inheritedIds = new Set(inherited.map((binding) => binding.nodeId));
    const candidates = nodes.filter((node) => {
        if (node.id === scriptNodeId || !isStoryboardPreviewAsset(node) || !bindingForConnectedNode(node)) return false;
        if (selectedIds.has(node.id)) return true;
        if (OUTPUT_WORKFLOW_KINDS.has(node.metadata?.workflowKind || "")) return false;
        return Boolean(node.metadata?.content || node.metadata?.storageKey || node.metadata?.drawingPreviewUrl || (node.metadata?.characterAssetId && node.metadata?.characterVersionId));
    });
    const keyword = query.trim().toLowerCase();
    const filtered = candidates.filter(
        (node) => (role === "all" || bindingForConnectedNode(node)?.role === role) && (!keyword || [node.title, node.metadata?.prompt, ...(node.metadata?.assetTags || [])].join(" ").toLowerCase().includes(keyword)),
    );
    const titleOf = (nodeId: string) => nodeById.get(nodeId)?.title || "失效素材";
    const toggle = (node: CanvasNodeData) =>
        setDraft((current) => {
            if (current.some((binding) => binding.nodeId === node.id)) return current.filter((binding) => binding.nodeId !== node.id);
            const binding = bindingForConnectedNode(node);
            return binding ? normalizeStoryboardAssetBindings([...current, binding]) : current;
        });
    const update = (nodeId: string, patch: Partial<StoryboardAssetBinding>) => setDraft((current) => current.map((item) => (item.nodeId === nodeId ? { ...item, ...patch } : item)));

    return (
        <>
            <AppModal open title={`镜头 ${shotNumber} · 关联素材`} width={760} centered onCancel={onClose} footer={null}>
                <div data-canvas-no-zoom data-canvas-wheel-scroll onPointerDown={(event) => event.stopPropagation()} onWheel={(event) => event.stopPropagation()}>
                    <p className="mb-4 max-w-[60ch] text-pretty text-sm text-foreground/60">选择本镜头使用的角色、场景、道具或参考媒体。画布上还没有的素材，可从项目资产中引入。</p>
                    <div className="mb-4 flex flex-wrap gap-2">
                        <Input className="min-w-40 flex-1" prefix={<Search className="size-4 text-foreground/40" />} allowClear placeholder="搜索素材名称或标签" aria-label="搜索素材" value={query} onChange={(event) => setQuery(event.target.value)} />
                        <Select className="w-32" aria-label="筛选素材用途" value={role} onChange={setRole} options={[{ value: "all", label: "全部用途" }, ...ROLE_OPTIONS]} />
                        <Button icon={<Library className="size-4" />} onClick={() => setLibraryOpen(true)}>
                            引用项目资产
                        </Button>
                    </div>
                    <div className="grid max-h-[38vh] min-h-40 auto-rows-max grid-cols-1 content-start gap-2 overflow-y-auto p-1 sm:grid-cols-2 md:grid-cols-3" aria-label="可选素材" data-canvas-wheel-scroll>
                        {filtered.map((node) => {
                            const selected = selectedIds.has(node.id);
                            return (
                                <button
                                    key={node.id}
                                    type="button"
                                    aria-pressed={selected}
                                    aria-label={`选择素材：${node.title}`}
                                    onClick={() => toggle(node)}
                                    className={`relative flex min-h-11 items-center gap-2 rounded-[var(--card-radius)] border p-2 text-left outline-none focus-visible:outline-2 focus-visible:outline-ring ${selected ? "border-foreground/50 bg-foreground/[0.08]" : "border-foreground/10 hover:bg-foreground/[0.04]"}`}
                                >
                                    <span className="storyboard-assets-cell-preview relative grid size-12 shrink-0 place-items-center rounded-[var(--card-radius)] bg-foreground/[0.04] text-foreground/60">
                                        <AssetThumbnail node={node} />
                                    </span>
                                    <span className="min-w-0 flex-1">
                                        <span className="block truncate text-sm text-foreground" title={node.title}>
                                            {node.title}
                                        </span>
                                        <span className="text-xs text-foreground/50">
                                            {STORYBOARD_ASSET_ROLE_LABELS[bindingForConnectedNode(node)!.role]}
                                            {inheritedIds.has(node.id) && !selected ? " · 已继承" : ""}
                                        </span>
                                    </span>
                                    {selected ? <Check className="size-4 shrink-0" aria-hidden /> : null}
                                </button>
                            );
                        })}
                        {!filtered.length ? (
                            <div className="col-span-full grid place-items-center px-4 py-8 text-center text-sm text-foreground/50">{keyword || role !== "all" ? "没有匹配的素材，试试其他搜索条件" : "当前画布还没有可选素材，点击“引用项目资产”添加"}</div>
                        ) : null}
                    </div>
                    <div className="mt-4 border-t border-foreground/10 pt-3">
                        <div className="mb-2 flex items-center justify-between text-xs text-foreground/60">
                            <span className="tabular-nums">已选 {draft.length} 项</span>
                            <Button type="text" size="small" disabled={!draft.length} onClick={() => setDraft([])}>
                                清空镜头素材
                            </Button>
                        </div>
                        {draft.length ? (
                            <div className="max-h-40 space-y-2 overflow-y-auto" aria-label="已选素材" data-canvas-wheel-scroll>
                                {draft.map((binding) => (
                                    <div key={binding.nodeId} className="grid grid-cols-[minmax(0,1fr)_6rem_4.5rem_2.75rem] items-center gap-2">
                                        <span className="truncate text-xs text-foreground" title={titleOf(binding.nodeId)}>
                                            {titleOf(binding.nodeId)}
                                        </span>
                                        <Select size="small" aria-label={`${titleOf(binding.nodeId)}用途`} value={binding.role} options={ROLE_OPTIONS} onChange={(value: StoryboardAssetBinding["role"]) => update(binding.nodeId, { role: value })} />
                                        <InputNumber
                                            size="small"
                                            className="w-full tabular-nums"
                                            aria-label={`${titleOf(binding.nodeId)}优先级`}
                                            min={0}
                                            max={100}
                                            precision={0}
                                            value={binding.priority}
                                            onChange={(value) => update(binding.nodeId, { priority: Number(value) || 0 })}
                                        />
                                        <button
                                            type="button"
                                            aria-label={`移除素材：${titleOf(binding.nodeId)}`}
                                            className="grid size-11 place-items-center rounded-[var(--button-radius)] text-foreground/60 hover:bg-foreground/5 focus-visible:outline-2 focus-visible:outline-ring md:size-8"
                                            onClick={() => setDraft((current) => current.filter((item) => item.nodeId !== binding.nodeId))}
                                        >
                                            <X className="size-3.5" />
                                        </button>
                                    </div>
                                ))}
                            </div>
                        ) : null}
                        {inherited.length ? (
                            <div className="mt-3 space-y-2">
                                <p className="text-[var(--fs-caption)] text-foreground/50">继承素材 · {inherited.length}（在表级共享素材或出镜角色中修改）</p>
                                <div className="flex flex-wrap gap-1.5">
                                    {inherited.map((binding) => (
                                        <span key={binding.nodeId} className="max-w-full truncate rounded-[var(--card-radius)] bg-foreground/5 px-2 py-1 text-xs text-foreground/70">
                                            {titleOf(binding.nodeId)} · {binding.source}
                                        </span>
                                    ))}
                                </div>
                            </div>
                        ) : null}
                        {pending ? <p className="mt-3 max-w-[60ch] text-pretty text-xs text-foreground/60">任务正在生成。这里的调整用于下次生成，当前任务继续使用提交时的素材。</p> : null}
                    </div>
                    {error ? (
                        <p role="alert" className="mt-2 text-sm text-[var(--status-error)]">
                            {error}
                        </p>
                    ) : null}
                    <div className="mt-4 flex justify-end gap-2">
                        <Button onClick={onClose}>取消</Button>
                        <Button
                            type="primary"
                            onClick={() => {
                                try {
                                    onSave(normalizeStoryboardAssetBindings(draft));
                                } catch (cause) {
                                    setError(cause instanceof Error ? cause.message : "镜头素材保存失败");
                                }
                            }}
                        >
                            保存关联
                        </Button>
                    </div>
                </div>
            </AppModal>
            {libraryOpen ? (
                <CanvasProjectAssetModal
                    open
                    detail={detail}
                    onClose={() => setLibraryOpen(false)}
                    onInsert={async (payloads) => {
                        if (payloads.some((payload) => payload.kind === "text")) throw new Error("镜头关联请选择角色、图片、视频或音频；文本请作为分镜表共享素材引用");
                        const imported = await onImport(payloads);
                        const additions = imported.flatMap((node) => {
                            const binding = bindingForConnectedNode(node);
                            return binding ? [binding] : [];
                        });
                        setError("");
                        setDraft((current) => normalizeStoryboardAssetBindings([...current, ...additions]));
                    }}
                />
            ) : null}
        </>
    );
}
