import type { CanvasConnection } from "@/types/canvas";

/** 锚点默认落点：节点侧边的垂直中点。 */
export const CANVAS_DEFAULT_ANCHOR_RATIO = 0.5;

export type CanvasEdgePresentation = {
    fromAnchorRatio?: number;
    toAnchorRatio?: number;
};

export type CanvasEdgePresentationMap = Record<string, CanvasEdgePresentation>;

/**
 * 把 edgePresentation[id] 合并进投影产出的边。
 * 关系由节点字段决定，锚点只决定线落在侧边的哪个位置。
 */
export function withCanvasEdgePresentation(connection: CanvasConnection, presentation: CanvasEdgePresentationMap | undefined): CanvasConnection {
    const value = presentation?.[connection.id];
    if (!value) return connection;
    const fromAnchorRatio = value.fromAnchorRatio ?? connection.fromAnchorRatio;
    const toAnchorRatio = value.toAnchorRatio ?? connection.toAnchorRatio;
    if (fromAnchorRatio === connection.fromAnchorRatio && toAnchorRatio === connection.toAnchorRatio) return connection;
    return { ...connection, fromAnchorRatio, toAnchorRatio };
}

/**
 * 从运行时连线抽出要落库的锚点；默认落点不写。
 * connections 仍是投影缓存，锚点比例只是视觉附属数据。
 */
export function canvasEdgePresentationFromConnections(connections: readonly CanvasConnection[]): CanvasEdgePresentationMap | undefined {
    let result: CanvasEdgePresentationMap | undefined;
    for (const connection of connections) {
        const entry: CanvasEdgePresentation = {};
        if (connection.fromAnchorRatio !== undefined && connection.fromAnchorRatio !== CANVAS_DEFAULT_ANCHOR_RATIO) {
            entry.fromAnchorRatio = connection.fromAnchorRatio;
        }
        if (connection.toAnchorRatio !== undefined && connection.toAnchorRatio !== CANVAS_DEFAULT_ANCHOR_RATIO) {
            entry.toAnchorRatio = connection.toAnchorRatio;
        }
        if (entry.fromAnchorRatio === undefined && entry.toAnchorRatio === undefined) continue;
        result ??= {};
        result[connection.id] = entry;
    }
    return result;
}
