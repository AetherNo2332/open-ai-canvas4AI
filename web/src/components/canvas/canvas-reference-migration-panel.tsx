import { useEffect, useState } from "react";
import { Button, Radio } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import { referenceMigrationScope, type CanvasReferenceMigrationChoice } from "@/lib/canvas/canvas-reference-migration";
import type { MigrationConflict } from "@/lib/canvas/command-contract.generated";

export type CanvasReferenceMigrationContentProps = {
    conflicts: MigrationConflict[];
    /** 节点标题覆盖，用于把预览返回的节点 id 显示成用户认识的名称。 */
    nodeTitles?: Record<string, string>;
    choices: Record<string, CanvasReferenceMigrationChoice>;
    onChoose: (scopeKey: string, choice: CanvasReferenceMigrationChoice) => void;
    applying?: boolean;
    error?: string;
    onCancel: () => void;
    onApply: () => void;
};

/** 规范化的真实内容：逐作用域选择权威来源，确认前不改动文档。 */
export function CanvasReferenceMigrationContent({ conflicts, nodeTitles, choices, onChoose, applying = false, error, onCancel, onApply }: CanvasReferenceMigrationContentProps) {
    const titles = nodeTitles || {};
    const name = (nodeId: string) => titles[nodeId] || nodeId;
    const pending = conflicts.filter((conflict) => choices[conflict.scopeKey] !== "fields" && choices[conflict.scopeKey] !== "connections");
    const nodes = (nodeIds: string[]) =>
        nodeIds.length ? (
            nodeIds.map((nodeId) => (
                <span key={nodeId} className="max-w-full truncate rounded-[var(--r-sm)] border border-border/60 px-1.5 py-0.5 text-xs text-foreground/75">
                    {name(nodeId)}
                </span>
            ))
        ) : (
            <span className="text-xs text-foreground/45">无</span>
        );
    return (
        <div className="flex flex-col gap-4 px-5 pb-5 pt-1">
            <p className="text-sm leading-6 text-foreground/65 text-pretty">这张画布来自旧版本：引用既有字段声明，也有旧连线。请为每个作用域选择以哪一侧为准；确认后一次性写入权威字段与托管连线，并标记为已规范化。取消不会改动画布。</p>
            {conflicts.length === 0 ? <div className="rounded-[var(--card-radius)] bg-foreground/[0.04] px-3 py-2 text-sm text-foreground/70">没有需要逐项选择的作用域，直接完成迁移即可。</div> : null}
            <div className="thin-scrollbar flex max-h-[52vh] flex-col gap-3 overflow-y-auto" data-canvas-no-zoom>
                {conflicts.map((conflict) => {
                    const scope = referenceMigrationScope(conflict.scopeKey);
                    return (
                        <article key={conflict.scopeKey} className="rounded-[var(--card-radius)] border border-border/60 px-3 py-3">
                            <header className="flex flex-wrap items-center gap-2">
                                <span className="rounded-[var(--r-sm)] bg-foreground/[0.06] px-1.5 py-0.5 text-xs text-foreground/70">{scope.kindLabel}</span>
                                <span className="min-w-0 truncate text-xs text-foreground/55">
                                    {scope.nodeId ? name(scope.nodeId) : conflict.scopeKey}
                                    {scope.rowId ? ` · 行 ${scope.rowId}` : ""}
                                </span>
                            </header>
                            <div className="mt-2 grid gap-2 sm:grid-cols-2">
                                <section className="flex flex-col gap-1.5 rounded-[var(--r-md)] bg-foreground/[0.035] px-2.5 py-2" aria-label="只有字段声明的引用">
                                    <span className="text-xs text-foreground/55">只有字段声明</span>
                                    <div className="flex flex-wrap gap-1">{nodes(conflict.fieldNodeIds)}</div>
                                </section>
                                <section className="flex flex-col gap-1.5 rounded-[var(--r-md)] bg-foreground/[0.035] px-2.5 py-2" aria-label="只有旧连线声明的引用">
                                    <span className="text-xs text-foreground/55">只有旧连线声明</span>
                                    <div className="flex flex-wrap gap-1">{nodes(conflict.edgeNodeIds)}</div>
                                </section>
                            </div>
                            <Radio.Group
                                className="mt-2.5 flex flex-wrap gap-x-4"
                                value={choices[conflict.scopeKey]}
                                aria-label={`${scope.kindLabel}引用来源`}
                                onChange={(event) => onChoose(conflict.scopeKey, event.target.value as CanvasReferenceMigrationChoice)}
                            >
                                <Radio value="fields">以字段为准</Radio>
                                <Radio value="connections">以旧连线为准</Radio>
                            </Radio.Group>
                        </article>
                    );
                })}
            </div>
            {error ? (
                <div role="alert" className="rounded-[var(--card-radius)] bg-foreground/[0.06] px-3 py-2 text-sm text-foreground">
                    {error}
                </div>
            ) : null}
            <div className="flex flex-wrap items-center justify-end gap-2">
                <Button onClick={onCancel} disabled={applying}>
                    稍后再说
                </Button>
                <Button type="primary" loading={applying} disabled={pending.length > 0} onClick={onApply}>
                    {conflicts.length ? `应用迁移（${conflicts.length} 个作用域）` : "完成迁移"}
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
export function CanvasReferenceMigrationPanel({ open, conflicts, nodeTitles, applying = false, error, onCancel, onApply }: CanvasReferenceMigrationPanelProps) {
    const [choices, setChoices] = useState<Record<string, CanvasReferenceMigrationChoice>>({});
    const signature = conflicts.map((conflict) => `${conflict.scopeKey}:${conflict.fieldNodeIds.join("|")}:${conflict.edgeNodeIds.join("|")}`).join(",");
    // 预览变化（含撤销后重新进入该入口）时丢弃旧选择，避免把过期作用域当成已决策。
    useEffect(() => setChoices({}), [signature]);
    return (
        <AppModal open={open} centered footer={null} width="min(760px, calc(100vw - 24px))" title="完成一次引用规范化" maskClosable={!applying} closable={!applying} onCancel={applying ? undefined : onCancel}>
            <CanvasReferenceMigrationContent
                conflicts={conflicts}
                nodeTitles={nodeTitles}
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
