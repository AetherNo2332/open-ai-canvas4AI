import { FRAME_HEADER_HEIGHT, FRAME_PADDING } from "@/lib/canvas/canvas-frame";
import type { CanvasNodeData, Position } from "@/types/canvas";

import { getSubmapChildren, isSubmapNode } from "./canvas-submap";

/** 子节点之间的最小间距；与背板折叠网格的 gap 同口径。 */
export const SUBMAP_CHILD_GAP = 24;

const SUBMAP_MIN_WIDTH = 640;
const SUBMAP_MIN_HEIGHT = 260;

/**
 * 把 submap 尺寸撑到刚好容纳全部子节点（含标题栏与内边距）。
 * 尺寸只增不减，手动拉大的框不会被子节点回收，空容器保留原尺寸。
 */
export function fitSubmapToChildren(nodes: CanvasNodeData[], submapId: string): CanvasNodeData[] {
    const submap = nodes.find((node) => node.id === submapId && isSubmapNode(node));
    if (!submap) return nodes;
    const children = getSubmapChildren(submapId, nodes);
    if (!children.length) return nodes;

    const left = Math.min(submap.position.x, ...children.map((node) => node.position.x - FRAME_PADDING));
    const top = Math.min(submap.position.y, ...children.map((node) => node.position.y - FRAME_HEADER_HEIGHT - FRAME_PADDING));
    const right = Math.max(submap.position.x + submap.width, ...children.map((node) => node.position.x + node.width + FRAME_PADDING));
    const bottom = Math.max(submap.position.y + submap.height, ...children.map((node) => node.position.y + node.height + FRAME_PADDING));
    const width = Math.max(right - left, SUBMAP_MIN_WIDTH);
    const height = Math.max(bottom - top, SUBMAP_MIN_HEIGHT);
    if (left === submap.position.x && top === submap.position.y && width === submap.width && height === submap.height) return nodes;
    return nodes.map((node) => (node.id === submapId ? { ...node, position: { x: left, y: top }, width, height } : node));
}

/** 给 submap 里新加的子节点选一个不重叠的落点；返回左上角坐标。 */
export function nextSubmapChildPosition(submap: CanvasNodeData, children: CanvasNodeData[]): Position {
    if (!children.length) {
        return { x: submap.position.x + FRAME_PADDING, y: submap.position.y + FRAME_HEADER_HEIGHT + FRAME_PADDING };
    }
    const left = Math.min(...children.map((node) => node.position.x));
    const bottom = Math.max(...children.map((node) => node.position.y + node.height));
    return { x: left, y: bottom + SUBMAP_CHILD_GAP };
}

/** v1 不允许把子节点拖出 submap 内容区；只夹紧本次被拖动的子节点。 */
export function clampSubmapChildPositions(nodes: CanvasNodeData[], movedIds: ReadonlySet<string>) {
    let changed = false;
    const byId = new Map(nodes.map((node) => [node.id, node]));
    const next = nodes.map((node) => {
        if (!node.scopeId || !movedIds.has(node.id)) return node;
        const submap = byId.get(node.scopeId);
        if (!submap || !isSubmapNode(submap) || submap.metadata?.collapsed) return node;
        const minX = submap.position.x + FRAME_PADDING;
        const minY = submap.position.y + FRAME_HEADER_HEIGHT + FRAME_PADDING;
        const maxX = submap.position.x + submap.width - FRAME_PADDING - node.width;
        const maxY = submap.position.y + submap.height - FRAME_PADDING - node.height;
        const x = Math.min(Math.max(node.position.x, minX), Math.max(minX, maxX));
        const y = Math.min(Math.max(node.position.y, minY), Math.max(minY, maxY));
        if (x === node.position.x && y === node.position.y) return node;
        changed = true;
        return { ...node, position: { x, y } };
    });
    return changed ? next : nodes;
}
