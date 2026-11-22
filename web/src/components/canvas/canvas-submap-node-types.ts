import type { MouseEvent as ReactMouseEvent } from "react";

import type { CanvasNodeRenderLOD } from "@/lib/canvas/canvas-node-lod";
import type { CanvasNodeData, Position } from "@/types/canvas";

export type SubmapResizeCorner = "top-left" | "top-right" | "bottom-left" | "bottom-right";

export type SubmapChildBounds = { left: number; top: number; right: number; bottom: number };

export const SUBMAP_RESIZE_MIN_WIDTH = 640;
export const SUBMAP_RESIZE_MIN_HEIGHT = 260;

export type CanvasSubmapNodeProps = {
    data: CanvasNodeData;
    childNodes: CanvasNodeData[];
    dragOffset?: Position;
    renderLOD?: CanvasNodeRenderLOD;
    scale: number;
    isSelected: boolean;
    readOnly?: boolean;
    onMouseDown: (event: ReactMouseEvent, nodeId: string) => void;
    onResize: (nodeId: string, width: number, height: number, position?: Position) => void;
    onToggleCollapsed: (nodeId: string) => void;
    onTitleChange: (nodeId: string, title: string) => void;
    onContextMenu: (event: ReactMouseEvent, nodeId: string) => void;
    onSubmapContextMenu: (event: ReactMouseEvent, nodeId: string) => void;
};
