import React from "react";
import type { CSSProperties } from "react";

import { FRAME_HEADER_HEIGHT } from "@/lib/canvas/canvas-frame";
import { SUBMAP_COLLAPSED_HEIGHT, SUBMAP_COLLAPSED_WIDTH } from "@/lib/canvas/canvas-submap-minimap";
import { canvasThemes } from "@/lib/canvas-theme";
import { useActiveTheme } from "@/stores/canvas/use-canvas-theme-store";

import { SubmapCollapsedView } from "./canvas-submap-node-collapsed";
import { CanvasSubmapNodeHeader } from "./canvas-submap-node-header";
import { SubmapResizeHandle, useSubmapResize } from "./canvas-submap-node-resize";
import type { CanvasSubmapNodeProps } from "./canvas-submap-node-types";

/**
 * submap 视觉容器：标题栏 + 内容区 + 折叠小地图 + 四角 resize。
 * 折叠只收拢渲染外壳，data.width/height 与子节点绝对坐标都不改。
 */
export const CanvasSubmapNode = React.memo(function CanvasSubmapNode({
    data,
    childNodes,
    dragOffset,
    renderLOD = "full",
    scale,
    isSelected,
    readOnly = false,
    onMouseDown,
    onResize,
    onToggleCollapsed,
    onTitleChange,
    onPresetChange,
    userPresets,
    onSavePreset,
    onContextMenu,
    onSubmapContextMenu,
}: CanvasSubmapNodeProps) {
    const theme = canvasThemes[useActiveTheme()];
    const collapsed = Boolean(data.metadata?.collapsed);
    const startResize = useSubmapResize(data, childNodes, scale, onResize);
    const fullDetail = renderLOD === "full";

    return (
        <div
            data-node-id={data.id}
            data-node-lod={fullDetail ? "full" : renderLOD}
            data-submap-scope={data.id}
            data-submap-collapsed={collapsed ? "true" : undefined}
            className={`absolute z-0 select-none ${dragOffset ? "cursor-grabbing" : "cursor-default"}`}
            style={
                {
                    transform: `translate(${data.position.x + (dragOffset?.x || 0)}px, ${data.position.y + (dragOffset?.y || 0)}px)`,
                    width: collapsed ? SUBMAP_COLLAPSED_WIDTH : data.width,
                    height: collapsed ? SUBMAP_COLLAPSED_HEIGHT : data.height,
                    contain: "layout style",
                } as CSSProperties
            }
            onMouseDown={(event) => onMouseDown(event, data.id)}
            onDoubleClick={(event) => {
                if (event.target instanceof Element && event.target.closest("button,input")) return;
                event.stopPropagation();
                onToggleCollapsed(data.id);
            }}
            onContextMenu={(event) => onContextMenu(event, data.id)}
        >
            <div
                className="canvas-submap-shell h-full w-full overflow-hidden rounded-[var(--dock-radius)] border"
                style={{
                    background: collapsed ? theme.frame.preview : isSelected ? theme.frame.activeFill : theme.frame.fill,
                    borderColor: isSelected ? theme.frame.activeStroke : theme.frame.stroke,
                    borderWidth: 1 / Math.max(scale, 0.05),
                    boxShadow: isSelected ? `0 0 0 ${1 / Math.max(scale, 0.05)}px ${theme.frame.activeStroke}33, 0 24px 72px ${theme.spatial.shadow}` : `0 18px 54px ${theme.spatial.shadow}`,
                }}
            >
                {collapsed ? (
                    <SubmapCollapsedView data={data} nodes={childNodes} theme={theme} isSelected={isSelected} onToggleCollapsed={onToggleCollapsed} />
                ) : !fullDetail ? (
                    <div className="flex h-full items-center justify-center" style={{ color: theme.node.text }}>
                        {data.title} · {childNodes.length}
                    </div>
                ) : (
                    <>
                        <CanvasSubmapNodeHeader
                            data={data}
                            childCount={childNodes.length}
                            collapsed={false}
                            readOnly={readOnly}
                            theme={theme}
                            onToggleCollapsed={onToggleCollapsed}
                            onTitleChange={onTitleChange}
                            onPresetChange={onPresetChange}
                            userPresets={userPresets}
                            onSavePreset={onSavePreset}
                        />
                        <div
                            data-submap-content={data.id}
                            className="absolute inset-x-2 bottom-2 rounded-md border border-dashed"
                            style={{ top: FRAME_HEADER_HEIGHT, background: "transparent", borderColor: theme.frame.stroke }}
                            onContextMenu={(event) => {
                                event.stopPropagation();
                                onSubmapContextMenu(event, data.id);
                            }}
                        />
                    </>
                )}
            </div>
            {!readOnly && !collapsed && isSelected && !data.metadata?.locked ? (
                <>
                    <SubmapResizeHandle corner="top-left" scale={scale} theme={theme} onMouseDown={startResize} />
                    <SubmapResizeHandle corner="top-right" scale={scale} theme={theme} onMouseDown={startResize} />
                    <SubmapResizeHandle corner="bottom-left" scale={scale} theme={theme} onMouseDown={startResize} />
                    <SubmapResizeHandle corner="bottom-right" scale={scale} theme={theme} onMouseDown={startResize} />
                </>
            ) : null}
        </div>
    );
});
