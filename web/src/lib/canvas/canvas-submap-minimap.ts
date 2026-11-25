import { isFrameNode } from "@/lib/canvas/canvas-frame";
import type { CanvasTheme } from "@/lib/canvas-theme";
import type { CanvasNodeData } from "@/types/canvas";

import { isSubmapNode } from "./canvas-submap";

/** 折叠后收拢成的固定小窗尺寸；data.width/height 与子节点坐标都不改。 */
export const SUBMAP_COLLAPSED_WIDTH = 240;
export const SUBMAP_COLLAPSED_HEIGHT = 160;

const MINIMAP_PADDING = 500;

export type SubmapMinimapTransform = {
    worldBounds: { x: number; y: number; w: number; h: number };
    scale: number;
    offset: { x: number; y: number };
    toMinimap: (worldX: number, worldY: number) => { x: number; y: number };
};

/** 把一组世界坐标节点装进 width×height 小窗的线性变换。 */
export function submapMinimapTransform(nodes: CanvasNodeData[], width: number, height: number): SubmapMinimapTransform {
    if (!nodes.length) {
        const scale = Math.min(width / 1000, height / 1000);
        const worldBounds = { x: -500, y: -500, w: 1000, h: 1000 };
        return {
            worldBounds,
            scale,
            offset: { x: (width - worldBounds.w * scale) / 2, y: (height - worldBounds.h * scale) / 2 },
            toMinimap: (worldX, worldY) => ({ x: (worldX - worldBounds.x) * scale, y: (worldY - worldBounds.y) * scale }),
        };
    }
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const node of nodes) {
        minX = Math.min(minX, node.position.x);
        minY = Math.min(minY, node.position.y);
        maxX = Math.max(maxX, node.position.x + node.width);
        maxY = Math.max(maxY, node.position.y + node.height);
    }
    minX -= MINIMAP_PADDING;
    minY -= MINIMAP_PADDING;
    maxX += MINIMAP_PADDING;
    maxY += MINIMAP_PADDING;
    const boundsWidth = Math.max(maxX - minX, 1);
    const boundsHeight = Math.max(maxY - minY, 1);
    const scale = Math.min(width / boundsWidth, height / boundsHeight);
    const worldBounds = { x: minX, y: minY, w: boundsWidth, h: boundsHeight };
    const offset = { x: (width - boundsWidth * scale) / 2, y: (height - boundsHeight * scale) / 2 };
    return {
        worldBounds,
        scale,
        offset,
        toMinimap: (worldX, worldY) => ({ x: (worldX - minX) * scale + offset.x, y: (worldY - minY) * scale + offset.y }),
    };
}

/** 在小窗 canvas 上绘制内部节点色块：容器描边、普通节点填充。 */
export function drawSubmapMinimap(canvas: HTMLCanvasElement | null, nodes: CanvasNodeData[], transform: SubmapMinimapTransform, theme: CanvasTheme) {
    if (!canvas) return;
    const context = canvas.getContext("2d");
    if (!context) return;
    context.clearRect(0, 0, canvas.width, canvas.height);
    for (const node of nodes) {
        const container = isFrameNode(node) || isSubmapNode(node);
        const color = container ? theme.frame.stroke : theme.node.muted;
        const p1 = transform.toMinimap(node.position.x, node.position.y);
        const p2 = transform.toMinimap(node.position.x + node.width, node.position.y + node.height);
        const rectWidth = Math.max(p2.x - p1.x, 2);
        const rectHeight = Math.max(p2.y - p1.y, 2);
        context.globalAlpha = container ? 0.95 : 0.8;
        context.fillStyle = color;
        context.strokeStyle = color;
        context.lineWidth = container ? 1 : 0;
        context.fillRect(p1.x, p1.y, rectWidth, rectHeight);
        if (container) context.strokeRect(p1.x, p1.y, rectWidth, rectHeight);
    }
    context.globalAlpha = 1;
}
