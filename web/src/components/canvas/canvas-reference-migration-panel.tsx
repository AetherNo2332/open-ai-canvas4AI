import { useEffect, useMemo, useState } from "react";
import { Button, Radio } from "antd";
import { ArrowRight, FileImage, LocateFixed } from "lucide-react";

import { AppModal } from "@/components/ui/product/app-modal";
import { MediaPreview } from "@/components/media-preview";
import { useResolvedCanvasResourceReferences } from "./use-resolved-canvas-resource-references";
import { buildOrderedCanvasResourceReferences, type CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { canvasReferenceMigrationIssues } from "@/lib/canvas/canvas-reference-migration-issues";
import { referenceMigrationScope, type CanvasReferenceMigrationChoice } from "@/lib/canvas/canvas-reference-migration";
import type { MigrationConflict } from "@/lib/canvas/command-contract.generated";
import type { CanvasNodeData } from "@/types/canvas";

function MigrationNodeCard({ node, reference, title, missing = false, onLocate }: { node?: CanvasNodeData; reference?: CanvasResourceReference; title: string; missing?: boolean; onLocate?: (nodeId: string) => void }) {
    const preview = reference && ["image", "video", "character"].includes(reference.kind) ? reference.previewUrl : undefined;
    return (
        <div className="flex min-w-0 items-center gap-3 rounded-[var(--card-radius)] border border-border/60 p-3">
            <div className="flex h-16 w-20 shrink-0 items-center justify-center overflow-hidden rounded-[var(--r-sm)] bg-foreground/[0.04] text-foreground/45">
                {preview ? <MediaPreview kind="image" src={preview} alt={title} loading="lazy" width={80} height={64} className="h-full w-full object-contain" fallbackLabel="预览暂不可用" /> : <FileImage size={24} aria-hidden="true" />}
            </div>
            <div className="min-w-0 flex-1">
                <p className="break-words text-sm font-medium text-foreground">{title}</p>
                <p className="mt-1 text-xs text-foreground/55">{missing ? "素材已不存在，无法显示原图" : preview ? "" : "暂无图片预览"}</p>
                {node && onLocate ? (
                    <Button type="text" size="small" className="mt-1 min-h-11 max-w-full" icon={<LocateFixed size={14} aria-hidden="true" />} onClick={() => onLocate(node.id)} aria-label={`定位到画布：${title}`}>
                        定位到画布
                    </Button>
                ) : null}
            </div>
        </div>
    );
}

export type CanvasReferenceMigrationContentProps = {
    conflicts: MigrationConflict[];
    /** 节点标题覆盖，用于把预览返回的节点 id 显示成用户认识的名称。 */
    nodeTitles?: Record<string, string>;
    nodes?: CanvasNodeData[];
    projectId?: string;
    onLocate?: (nodeId: string) => void;
    choices: Record<string, CanvasReferenceMigrationChoice>;
    onChoose: (scopeKey: string, choice: CanvasReferenceMigrationChoice) => void;
    applying?: boolean;
    error?: string;
    onCancel: () => void;
    onApply: () => void;
};

/** 规范化的真实内容：逐作用域选择权威来源，确认前不改动文档。 */
export function CanvasReferenceMigrationContent({ conflicts, nodeTitles, nodes: canvasNodes, projectId, onLocate, choices, onChoose, applying = false, error, onCancel, onApply }: CanvasReferenceMigrationContentProps) {
    const nodes = canvasNodes || [];
    const byId = new Map(nodes.map((node) => [node.id, node]));
    const titles = nodeTitles || {};
    const name = (nodeId: string) => byId.get(nodeId)?.title || titles[nodeId] || "未命名节点";
    const issues = useMemo(() => canvasReferenceMigrationIssues(canvasNodes || [], choices), [canvasNodes, choices]);
    const references = useMemo(() => {
        const involved = new Set([...conflicts.flatMap((conflict) => [referenceMigrationScope(conflict.scopeKey).nodeId, ...conflict.fieldNodeIds, ...conflict.edgeNodeIds]), ...issues.flatMap((issue) => [issue.ownerNodeId, issue.sourceNodeId])]);
        return buildOrderedCanvasResourceReferences((canvasNodes || []).filter((node) => involved.has(node.id)));
    }, [canvasNodes, conflicts, issues]);
    const previews = new Map(useResolvedCanvasResourceReferences(references, { projectId }).map((ref) => [ref.nodeId, ref]));
    const pending = conflicts.filter((conflict) => choices[conflict.scopeKey] !== "fields" && choices[conflict.scopeKey] !== "connections");
    const renderNodes = (nodeIds: string[]) =>
        nodeIds.length ? (
            nodeIds.map((nodeId) => <MigrationNodeCard key={nodeId} node={byId.get(nodeId)} reference={previews.get(nodeId)} title={name(nodeId)} missing={Boolean(canvasNodes && !byId.has(nodeId))} onLocate={onLocate} />)
        ) : (
            <span className="text-xs text-foreground/45">无</span>
        );
    return (
        <div className="flex flex-col gap-4 px-5 pb-5 pt-1">
            <p className="max-w-[60ch] text-sm leading-6 text-foreground/65 text-pretty">旧版画布分别在素材列表和画布连线中记录素材关联。升级后，以素材列表为准，连线自动跟随更新。确认前不会修改这些关联。</p>
            {issues.length ? (
                <div role="alert" className="rounded-[var(--card-radius)] bg-foreground/[0.04] p-4 text-sm text-foreground">
                    <p className="font-medium">有 {issues.length} 处素材关联需要手动处理</p>
                    <p className="mt-1 leading-6 text-foreground/65">请先定位到下方节点，重新选择素材或移除错误关联，再从顶部「确认素材引用」返回此处继续升级。</p>
                </div>
            ) : null}
            {conflicts.length === 0 && issues.length === 0 && !error ? <p className="text-sm text-foreground/70">无需选择保留哪组素材，可点击「确认并升级」。</p> : null}
            <div className="thin-scrollbar flex max-h-[52vh] flex-col gap-4 overflow-y-auto overscroll-contain" data-canvas-no-zoom>
                {issues.map((issue) => (
                    <article key={issue.key} className="rounded-[var(--card-radius)] border border-border/60 p-4">
                        <p className="text-sm text-foreground">
                            {issue.location} · {issue.fieldLabel}
                        </p>
                        <div className="mt-3 grid min-w-0 grid-cols-1 items-center gap-2 sm:grid-cols-[1fr_auto_1fr]">
                            <div className="min-w-0">
                                <p className="mb-2 text-xs text-foreground/55">需要修改的节点</p>
                                <MigrationNodeCard node={byId.get(issue.ownerNodeId)} reference={previews.get(issue.ownerNodeId)} title={name(issue.ownerNodeId)} onLocate={onLocate} />
                            </div>
                            <ArrowRight size={16} className="hidden text-foreground/45 sm:block" aria-label="引用" />
                            <div className="min-w-0">
                                <p className="mb-2 text-xs text-foreground/55">当前关联的素材</p>
                                <MigrationNodeCard
                                    node={byId.get(issue.sourceNodeId)}
                                    reference={previews.get(issue.sourceNodeId)}
                                    title={issue.kind === "missing" ? issue.sourceTitle || "已丢失的素材" : name(issue.sourceNodeId)}
                                    missing={issue.kind === "missing"}
                                />
                            </div>
                        </div>
                        <p className="mt-3 text-sm leading-6 text-foreground/70">
                            {issue.kind === "self" ? "这个节点引用了自己。请在上述素材设置中移除它，或重新选择其他节点。" : `这份素材已不在画布中。请在「${issue.fieldLabel}」中重新选择素材，或移除这条关联。`}
                        </p>
                        <details className="mt-2 text-xs text-foreground/45">
                            <summary className="cursor-pointer">查看节点编号</summary>
                            <p className="mt-1 break-all">
                                使用素材的节点：{issue.ownerNodeId}
                                <br />
                                被引用的节点：{issue.sourceNodeId}
                            </p>
                        </details>
                    </article>
                ))}
                {conflicts.length ? <p className="text-sm leading-6 text-foreground/70">以下位置的记录不一致。两侧只列出不同的素材，请选择要保留的记录方式；两边相同的素材仍会保留。</p> : null}
                {conflicts.map((conflict) => {
                    const scope = referenceMigrationScope(conflict.scopeKey);
                    const owner = byId.get(scope.nodeId);
                    const rowIndex = (owner?.metadata?.storyboard?.rows || owner?.metadata?.batchTable?.rows || []).findIndex((row) => row.id === scope.rowId);
                    return (
                        <article key={conflict.scopeKey} className="rounded-[var(--card-radius)] border border-border/60 px-3 py-3">
                            <header className="flex flex-wrap items-center gap-2">
                                <span className="rounded-[var(--r-sm)] bg-foreground/[0.06] px-1.5 py-0.5 text-xs text-foreground/70">{scope.kindLabel}</span>
                                <span className="min-w-0 truncate text-xs text-foreground/55">
                                    {scope.nodeId ? name(scope.nodeId) : conflict.scopeKey}
                                    {rowIndex >= 0 ? ` · 第 ${rowIndex + 1} 行` : ""}
                                </span>
                            </header>
                            {owner ? (
                                <div className="mt-3">
                                    <MigrationNodeCard node={owner} reference={previews.get(owner.id)} title={name(owner.id)} onLocate={onLocate} />
                                </div>
                            ) : null}
                            <div className="mt-2 grid gap-2 sm:grid-cols-2">
                                <section className="flex min-w-0 flex-col gap-2" aria-label="仅素材列表中有的素材">
                                    <span className="text-xs text-foreground/55">仅素材列表中有</span>
                                    <div className="flex flex-col gap-2">{renderNodes(conflict.fieldNodeIds)}</div>
                                </section>
                                <section className="flex min-w-0 flex-col gap-2" aria-label="仅旧连线中有的素材">
                                    <span className="text-xs text-foreground/55">仅旧连线中有</span>
                                    <div className="flex flex-col gap-2">{renderNodes(conflict.edgeNodeIds)}</div>
                                </section>
                            </div>
                            <Radio.Group
                                className="mt-2.5 flex flex-wrap gap-x-4"
                                value={choices[conflict.scopeKey]}
                                disabled={applying}
                                aria-label={`${scope.kindLabel}引用来源`}
                                onChange={(event) => onChoose(conflict.scopeKey, event.target.value as CanvasReferenceMigrationChoice)}
                            >
                                <Radio className="min-h-11 items-center" value="fields">
                                    保留素材列表
                                </Radio>
                                <Radio className="min-h-11 items-center" value="connections">
                                    沿用旧连线
                                </Radio>
                            </Radio.Group>
                        </article>
                    );
                })}
            </div>
            {error && !issues.length ? (
                <div role="alert" className="rounded-[var(--card-radius)] bg-foreground/[0.06] px-3 py-2 text-sm text-foreground">
                    <p className="font-medium">暂时无法完成升级</p>
                    <p className="mt-1 break-words leading-6 text-foreground/70">{error}</p>
                </div>
            ) : null}
            <div className="flex flex-wrap items-center justify-end gap-2">
                <Button className="min-h-11" onClick={onCancel} disabled={applying}>
                    稍后再说
                </Button>
                <Button className="min-h-11" type="primary" loading={applying} disabled={pending.length > 0 || issues.length > 0} onClick={onApply}>
                    确认并升级
                </Button>
            </div>
        </div>
    );
}

export type CanvasReferenceMigrationPanelProps = Omit<CanvasReferenceMigrationContentProps, "choices" | "onChoose" | "onApply"> & {
    open: boolean;
    onApply: (choices: Record<string, CanvasReferenceMigrationChoice>) => void;
};

/** 历史画布的一次规范化入口；预览可取消，确认后由命令会话提交。 */
export function CanvasReferenceMigrationPanel({ open, conflicts, nodeTitles, nodes, projectId, onLocate, applying = false, error, onCancel, onApply }: CanvasReferenceMigrationPanelProps) {
    const [choices, setChoices] = useState<Record<string, CanvasReferenceMigrationChoice>>({});
    const signature = conflicts.map((conflict) => `${conflict.scopeKey}:${conflict.fieldNodeIds.join("|")}:${conflict.edgeNodeIds.join("|")}`).join(",");
    // 预览变化（含撤销后重新进入该入口）时丢弃旧选择，避免把过期作用域当成已决策。
    useEffect(() => setChoices({}), [signature]);
    return (
        <AppModal open={open} centered footer={null} width="min(840px, calc(100vw - 24px))" title="确认画布的素材关联" maskClosable={!applying} closable={!applying} onCancel={applying ? undefined : onCancel}>
            <CanvasReferenceMigrationContent
                conflicts={conflicts}
                nodeTitles={nodeTitles}
                nodes={nodes}
                projectId={projectId}
                onLocate={applying ? undefined : onLocate}
                choices={choices}
                onChoose={(scopeKey, choice) => setChoices((current) => ({ ...current, [scopeKey]: choice }))}
                applying={applying}
                error={error}
                onCancel={onCancel}
                onApply={() => onApply(choices)}
            />
        </AppModal>
    );
}
