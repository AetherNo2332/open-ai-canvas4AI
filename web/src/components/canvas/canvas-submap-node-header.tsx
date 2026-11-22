import { useEffect, useState } from "react";
import { ChevronDown, ChevronRight, LayoutPanelTop } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Select } from "@/components/ui/base/select";
import { FRAME_HEADER_HEIGHT } from "@/lib/canvas/canvas-frame";
import { canvasNodeDisplayPath } from "@/lib/canvas/canvas-submap";
import { listSubmapPresetOptions, resolveSubmapPresetId } from "@/lib/canvas/canvas-submap-presets";
import type { CanvasTheme } from "@/lib/canvas-theme";
import type { CanvasNodeData } from "@/types/canvas";

const SUBMAP_PRESET_OPTIONS = listSubmapPresetOptions();

export function CanvasSubmapNodeHeader({
    data,
    childCount,
    collapsed,
    readOnly,
    theme,
    onToggleCollapsed,
    onTitleChange,
    onPresetChange,
}: {
    data: CanvasNodeData;
    childCount: number;
    collapsed: boolean;
    readOnly: boolean;
    theme: CanvasTheme;
    onToggleCollapsed: (nodeId: string) => void;
    onTitleChange: (nodeId: string, title: string) => void;
    onPresetChange?: (nodeId: string, presetId: string) => void;
}) {
    const { t } = useTranslation();
    const [editing, setEditing] = useState(false);
    const [title, setTitle] = useState(data.title);

    useEffect(() => setTitle(data.title), [data.title]);

    const commitTitle = () => {
        const next = title.trim() || t("canvasPage.nodeOps.submapEmptyPreview");
        setTitle(next);
        setEditing(false);
        onTitleChange(data.id, next);
    };

    return (
        <div className="pointer-events-auto absolute inset-x-0 top-0 z-10 flex items-center gap-2 px-2" style={{ height: FRAME_HEADER_HEIGHT, color: theme.node.text }}>
            <button
                type="button"
                className="grid size-8 shrink-0 place-items-center rounded-md transition-colors hover:bg-black/5 focus-visible:outline focus-visible:outline-2 dark:hover:bg-white/10"
                style={{ outlineColor: theme.frame.activeStroke }}
                aria-label={collapsed ? t("canvasPage.nodeOps.submapExpand") : t("canvasPage.nodeOps.submapCollapse")}
                onMouseDown={(event) => event.stopPropagation()}
                onClick={(event) => {
                    event.stopPropagation();
                    onToggleCollapsed(data.id);
                }}
            >
                {collapsed ? <ChevronRight className="size-4" /> : <ChevronDown className="size-4" />}
            </button>
            <LayoutPanelTop className="size-3.5 shrink-0 opacity-60" />
            {editing ? (
                <input
                    autoFocus
                    className="h-7 min-w-0 flex-1 rounded border bg-transparent px-2 text-sm font-semibold outline-none"
                    style={{ borderColor: theme.frame.activeStroke, color: theme.node.text }}
                    value={title}
                    onChange={(event) => setTitle(event.target.value)}
                    onBlur={commitTitle}
                    onMouseDown={(event) => event.stopPropagation()}
                    onKeyDown={(event) => {
                        if (event.key === "Enter") commitTitle();
                        if (event.key === "Escape") {
                            setTitle(data.title);
                            setEditing(false);
                        }
                    }}
                />
            ) : (
                <button
                    type="button"
                    className="min-w-0 truncate text-left text-sm font-semibold"
                    title={data.title}
                    onMouseDown={(event) => event.stopPropagation()}
                    onDoubleClick={(event) => {
                        event.stopPropagation();
                        if (!readOnly) setEditing(true);
                    }}
                >
                    {data.title}
                </button>
            )}
            <span className="shrink-0 text-[var(--fs-label)] tabular-nums opacity-60" title={canvasNodeDisplayPath(data)}>
                {data.id}
            </span>
            {onPresetChange && !readOnly && !data.metadata?.locked ? (
                <span className="shrink-0" title={t("canvasPage.nodeOps.submapPresetHint")} onMouseDown={(event) => event.stopPropagation()} onClick={(event) => event.stopPropagation()} onDoubleClick={(event) => event.stopPropagation()}>
                    <Select
                        size="sm"
                        appearance="soft"
                        ariaLabel={t("canvasPage.nodeOps.submapPresetLabel")}
                        value={resolveSubmapPresetId(data.metadata)}
                        options={SUBMAP_PRESET_OPTIONS}
                        popupMatchSelectWidth={false}
                        onChange={(value) => onPresetChange(data.id, String(value))}
                    />
                </span>
            ) : null}
            <span className="ml-auto shrink-0 pr-1 text-[var(--fs-label)] tabular-nums" style={{ color: theme.node.muted }}>
                {t("canvasPage.nodeOps.submapChildCount", { count: childCount })}
            </span>
        </div>
    );
}
