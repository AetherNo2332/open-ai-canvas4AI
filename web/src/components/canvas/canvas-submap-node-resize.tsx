import { useCallback, useEffect, useMemo, useRef } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";
import { useTranslation } from "react-i18next";

import { FRAME_HEADER_HEIGHT, FRAME_PADDING } from "@/lib/canvas/canvas-frame";
import type { CanvasTheme } from "@/lib/canvas-theme";
import type { CanvasNodeData, Position } from "@/types/canvas";

import { SUBMAP_RESIZE_MIN_HEIGHT, SUBMAP_RESIZE_MIN_WIDTH, type SubmapChildBounds, type SubmapResizeCorner } from "./canvas-submap-node-types";

type OnResize = (nodeId: string, width: number, height: number, position?: Position) => void;

type ResizeState = {
    active: boolean;
    corner: SubmapResizeCorner;
    startX: number;
    startY: number;
    startLeft: number;
    startTop: number;
    startWidth: number;
    startHeight: number;
    nodeId: string;
    scale: number;
    childBounds: SubmapChildBounds | null;
    onResize: OnResize;
};

/** submap 四角 resize：子节点包围盒既是下限，也是缩放基线。 */
export function useSubmapResize(data: CanvasNodeData, childNodes: CanvasNodeData[], scale: number, onResize: OnResize) {
    const childBounds = useMemo<SubmapChildBounds | null>(() => {
        if (!childNodes.length) return null;
        return {
            left: Math.min(...childNodes.map((node) => node.position.x)),
            top: Math.min(...childNodes.map((node) => node.position.y)),
            right: Math.max(...childNodes.map((node) => node.position.x + node.width)),
            bottom: Math.max(...childNodes.map((node) => node.position.y + node.height)),
        };
    }, [childNodes]);

    const resizeRef = useRef<ResizeState>({
        active: false,
        corner: "bottom-right",
        startX: 0,
        startY: 0,
        startLeft: 0,
        startTop: 0,
        startWidth: 0,
        startHeight: 0,
        nodeId: data.id,
        scale,
        childBounds: null,
        onResize,
    });

    const handleResizeMove = useCallback((event: MouseEvent) => {
        const state = resizeRef.current;
        if (!state.active) return;
        const dx = (event.clientX - state.startX) / state.scale;
        const dy = (event.clientY - state.startY) / state.scale;
        const fromLeft = state.corner.includes("left");
        const fromTop = state.corner.includes("top");
        const startRight = state.startLeft + state.startWidth;
        const startBottom = state.startTop + state.startHeight;
        let left = fromLeft ? Math.min(state.startLeft + dx, startRight - SUBMAP_RESIZE_MIN_WIDTH) : state.startLeft;
        let top = fromTop ? Math.min(state.startTop + dy, startBottom - SUBMAP_RESIZE_MIN_HEIGHT) : state.startTop;
        let right = fromLeft ? startRight : Math.max(state.startLeft + SUBMAP_RESIZE_MIN_WIDTH, startRight + dx);
        let bottom = fromTop ? startBottom : Math.max(state.startTop + SUBMAP_RESIZE_MIN_HEIGHT, startBottom + dy);
        if (state.childBounds) {
            if (fromLeft) left = Math.min(left, state.childBounds.left - FRAME_PADDING);
            else right = Math.max(right, state.childBounds.right + FRAME_PADDING);
            if (fromTop) top = Math.min(top, state.childBounds.top - FRAME_HEADER_HEIGHT - FRAME_PADDING);
            else bottom = Math.max(bottom, state.childBounds.bottom + FRAME_PADDING);
        }
        state.onResize(state.nodeId, right - left, bottom - top, { x: left, y: top });
    }, []);

    const handleResizeUp = useCallback(() => {
        resizeRef.current.active = false;
        window.removeEventListener("mousemove", handleResizeMove);
        window.removeEventListener("mouseup", handleResizeUp);
    }, [handleResizeMove]);

    useEffect(
        () => () => {
            window.removeEventListener("mousemove", handleResizeMove);
            window.removeEventListener("mouseup", handleResizeUp);
        },
        [handleResizeMove, handleResizeUp],
    );

    return useCallback(
        (event: ReactMouseEvent, corner: SubmapResizeCorner) => {
            event.preventDefault();
            event.stopPropagation();
            resizeRef.current = {
                active: true,
                corner,
                startX: event.clientX,
                startY: event.clientY,
                startLeft: data.position.x,
                startTop: data.position.y,
                startWidth: data.width,
                startHeight: data.height,
                nodeId: data.id,
                scale,
                childBounds,
                onResize,
            };
            window.addEventListener("mousemove", handleResizeMove);
            window.addEventListener("mouseup", handleResizeUp);
        },
        [childBounds, data.height, data.id, data.position.x, data.position.y, data.width, handleResizeMove, handleResizeUp, onResize, scale],
    );
}

export function SubmapResizeHandle({ corner, scale, theme, onMouseDown }: { corner: SubmapResizeCorner; scale: number; theme: CanvasTheme; onMouseDown: (event: ReactMouseEvent, corner: SubmapResizeCorner) => void }) {
    const { t } = useTranslation();
    const inverseScale = 1 / Math.max(scale, 0.05);
    const size = 14 * inverseScale;
    const offset = -size / 2;
    const fromTop = corner.includes("top");
    const fromLeft = corner.includes("left");
    const cursor = corner === "top-left" || corner === "bottom-right" ? "nwse-resize" : "nesw-resize";
    return (
        <button
            type="button"
            aria-label={t("canvasPage.nodeOps.submapResizeHandle")}
            className="pointer-events-auto absolute z-20 rounded-sm shadow-sm"
            style={{
                top: fromTop ? offset : undefined,
                bottom: fromTop ? undefined : offset,
                left: fromLeft ? offset : undefined,
                right: fromLeft ? undefined : offset,
                width: size,
                height: size,
                cursor,
                background: theme.frame.activeStroke,
                border: `${2 * inverseScale}px solid ${theme.canvas.background}`,
            }}
            onMouseDown={(event) => onMouseDown(event, corner)}
        />
    );
}
