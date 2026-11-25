import { ChevronRight, LayoutPanelTop } from "lucide-react";
import { useTranslation } from "react-i18next";

import type { CanvasTheme } from "@/lib/canvas-theme";
import type { CanvasNodeData } from "@/types/canvas";

import { SubmapNodeMinimap } from "./canvas-submap-node-minimap";

/** 折叠态外壳：小地图铺底，整块区域可点击展开。 */
export function SubmapCollapsedView({ data, nodes, theme, isSelected, onToggleCollapsed }: { data: CanvasNodeData; nodes: CanvasNodeData[]; theme: CanvasTheme; isSelected: boolean; onToggleCollapsed: (nodeId: string) => void }) {
    const { t } = useTranslation();
    const expandLabel = t("canvasPage.nodeOps.submapExpand");
    return (
        <div className="absolute inset-0 overflow-hidden rounded-[var(--dock-radius)]" style={{ background: theme.frame.preview }}>
            <SubmapNodeMinimap nodes={nodes} theme={theme} />
            <button
                type="button"
                aria-label={expandLabel}
                title={expandLabel}
                className="absolute inset-0 z-10 flex flex-col transition-colors hover:brightness-95 focus-visible:outline focus-visible:outline-2"
                style={{ background: isSelected ? theme.frame.activeFill : theme.frame.fill, outlineColor: theme.frame.activeStroke }}
                onMouseDown={(event) => event.stopPropagation()}
                onDoubleClick={(event) => event.stopPropagation()}
                onClick={(event) => {
                    event.stopPropagation();
                    onToggleCollapsed(data.id);
                }}
            >
                <span className="flex min-h-0 items-center gap-1.5 px-2 py-1 text-[var(--fs-label)] font-semibold shadow-sm" style={{ background: theme.toolbar.panel, color: theme.node.text }}>
                    <LayoutPanelTop className="size-3 shrink-0 opacity-60" />
                    <span className="min-w-0 truncate">{data.title}</span>
                    <span className="ml-auto shrink-0 tabular-nums opacity-60">{t("canvasPage.nodeOps.submapChildCount", { count: nodes.length })}</span>
                </span>
                <span className="grid min-h-0 flex-1 place-items-center">
                    {nodes.length ? (
                        <span className="grid size-8 place-items-center rounded-full shadow-sm backdrop-blur-sm transition-transform hover:scale-105" style={{ background: theme.toolbar.panel, color: theme.node.text }}>
                            <ChevronRight className="size-5" />
                        </span>
                    ) : (
                        <span className="px-2 text-center text-[var(--fs-label)]" style={{ color: theme.node.faint }}>
                            {t("canvasPage.nodeOps.submapEmptyPreview")}
                        </span>
                    )}
                </span>
            </button>
        </div>
    );
}
