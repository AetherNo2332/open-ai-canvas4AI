import { useLayoutEffect, useRef, useState, type RefObject } from "react";

import { avoidCanvasNodePanels, canvasNodePanelBounds, canvasToolbarLayoutLevel, observeCanvasOverlayBounds, subscribeCanvasNodePanelBounds, type CanvasOverlayBounds } from "@/lib/canvas/canvas-overlay-layout";
import { subscribeCanvasGraphicsViewportPreview, subscribeCanvasNodeDragPreview } from "@/lib/canvas/canvas-live-viewport";
import type { CanvasNodeData, CanvasWorkspaceMode, Position, ViewportTransform } from "@/types/canvas";
import { useCanvasNodeResizeMotion } from "./canvas-node-resize-motion";

export function useCanvasNodeToolbarPosition(node: CanvasNodeData | null, viewport: ViewportTransform, containerRef: RefObject<HTMLDivElement | null>, toolbarRef: RefObject<HTMLDivElement | null>, workspaceMode: CanvasWorkspaceMode) {
    const [anchor, setAnchor] = useState<{ left: number; top: number } | null>(null);
    const [containerWidth, setContainerWidth] = useState(0);
    const [layoutLevel, setLayoutLevel] = useState(0);
    const measuredWidths = useRef<number[]>([0, 0, 0]);
    const latest = useRef({ node, viewport, layoutLevel });
    const syncPosition = useRef(() => {});
    const measureToolbar = useRef(() => {});
    const resizeMotion = useCanvasNodeResizeMotion();

    // 提交坐标变化只更新位置，不重新读取布局或重建观察器。
    useLayoutEffect(() => {
        latest.current = { node, viewport, layoutLevel };
        syncPosition.current();
    }, [node, viewport.x, viewport.y, viewport.k, layoutLevel]);

    useLayoutEffect(() => {
        measuredWidths.current = [0, 0, 0];
        setLayoutLevel(0);
        measureToolbar.current();
    }, [node?.id, workspaceMode]);

    useLayoutEffect(() => {
        const container = containerRef.current;
        if (!node || !container) {
            setAnchor(null);
            return;
        }
        let disposed = false;
        let queued = false;
        let liveViewport = latest.current.viewport;
        let dragOffset: Position | null = null;
        let bounds: CanvasOverlayBounds | null = null;
        let toolbarWidth = 0;
        let toolbarHeight = 44;
        const update = () => {
            const current = latest.current.node;
            if (!current || !bounds || disposed) return;
            const visual = resizeMotion?.resolveNode(current) || current;
            const scale = Math.max(liveViewport.k, 0.05);
            const center = liveViewport.x + (visual.position.x + (dragOffset?.x || 0) + visual.width / 2) * scale;
            const top = liveViewport.y + (visual.position.y + (dragOffset?.y || 0)) * scale - 30;
            const halfWidth = toolbarWidth / 2;
            const position = avoidCanvasNodePanels({
                left: Math.min(Math.max(center, bounds.left + halfWidth), Math.max(bounds.left + halfWidth, bounds.right - halfWidth)),
                top: Math.max(bounds.top + toolbarHeight, Math.min(top, bounds.bottom)),
            }, { width: toolbarWidth, height: toolbarHeight }, bounds, canvasNodePanelBounds(container));
            if (toolbarRef.current) toolbarRef.current.style.transform = `translate3d(${position.left}px, ${position.top}px, 0)`;
            else setAnchor(position);
        };
        const scheduleUpdate = (nextViewport?: ViewportTransform) => {
            if (nextViewport) liveViewport = nextViewport;
            if (queued || disposed) return;
            queued = true;
            queueMicrotask(() => {
                queued = false;
                update();
            });
        };
        const fitToolbar = () => {
            if (!bounds) return;
            const availableWidth = Math.max(0, bounds.right - bounds.left);
            setContainerWidth(availableWidth);
            setLayoutLevel(canvasToolbarLayoutLevel(availableWidth, measuredWidths.current, latest.current.layoutLevel));
        };
        const measure = () => {
            const toolbar = toolbarRef.current;
            if (toolbar) {
                toolbarWidth = toolbar.offsetWidth;
                toolbarHeight = toolbar.offsetHeight;
                const dock = toolbar.firstElementChild as HTMLElement | null;
                if (dock) measuredWidths.current[latest.current.layoutLevel] = dock.scrollWidth;
            }
            fitToolbar();
            update();
        };
        syncPosition.current = () => {
            liveViewport = latest.current.viewport;
            update();
        };
        measureToolbar.current = measure;
        const stopBounds = observeCanvasOverlayBounds(container, (next) => {
            bounds = next;
            fitToolbar();
            update();
        });
        measure();
        const observer = new ResizeObserver(measure);
        if (toolbarRef.current) {
            observer.observe(toolbarRef.current);
            if (toolbarRef.current.firstElementChild) observer.observe(toolbarRef.current.firstElementChild);
        }
        const stopPanels = subscribeCanvasNodePanelBounds(container, () => scheduleUpdate());
        const stopViewport = subscribeCanvasGraphicsViewportPreview(container, scheduleUpdate);
        const stopResize = resizeMotion?.subscribeNode(node.id, () => scheduleUpdate());
        const stopDrag = subscribeCanvasNodeDragPreview(container, (preview) => {
            dragOffset = preview?.nodeIds.has(node.id) ? { x: preview.x, y: preview.y } : null;
            scheduleUpdate();
        });
        return () => {
            disposed = true;
            syncPosition.current = () => {};
            measureToolbar.current = () => {};
            observer.disconnect();
            stopBounds();
            stopPanels();
            stopViewport();
            stopResize?.();
            stopDrag();
        };
    }, [node?.id, anchor !== null, containerRef, toolbarRef, resizeMotion]);

    useLayoutEffect(() => measureToolbar.current(), [layoutLevel]);
    return { anchor, containerWidth, layoutLevel };
}
