import type { CanvasNodeDragPreview } from "./canvas-live-viewport";
import type { ViewportTransform } from "../../types/canvas";
import type { RealtimeDrag, RealtimePoint } from "../../services/canvas-realtime-client";

const finitePoint = (point: RealtimePoint) => Number.isFinite(point.x) && Number.isFinite(point.y) && Math.abs(point.x) <= 10_000_000 && Math.abs(point.y) <= 10_000_000;
export function realtimeCursorWorldPoint(client: RealtimePoint, bounds: { left: number; top: number }, viewport: ViewportTransform): RealtimePoint | null {
    if (!Number.isFinite(viewport.k) || viewport.k <= 0) return null;
    const point = { x: (client.x - bounds.left - viewport.x) / viewport.k, y: (client.y - bounds.top - viewport.y) / viewport.k };
    return finitePoint(point) ? point : null;
}
export function realtimeDragPositions(nodes: readonly { id: string; position: RealtimePoint }[], preview: CanvasNodeDragPreview | null): RealtimeDrag[] {
    if (!preview) return [];
    const result: RealtimeDrag[] = [];
    for (const node of nodes) {
        if (!preview.nodeIds.has(node.id) || !node.id || new TextEncoder().encode(node.id).length > 128) continue;
        const position = { nodeId: node.id, x: node.position.x + preview.x, y: node.position.y + preview.y };
        if (finitePoint(position)) result.push(position);
        if (result.length === 32) break;
    }
    return result;
}
