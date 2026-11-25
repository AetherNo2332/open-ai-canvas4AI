import type { CanvasProjectedRelation } from "@/lib/canvas/canvas-node-inputs";
import type { CanvasConnection } from "@/types/canvas";

/**
 * 关系边身份：边 ID 是关系身份的纯函数。
 *
 * - R1（含文本参考）：`conn:r1:<to>:<from>`
 * - 首尾帧：`conn:frame:<field>:<to>:<from>`
 * - 结构边：`conn:<to>:<from>:-:-`
 *
 * 族必须先于端点出现在键里，R1 与帧指向同一对节点时不会撞 ID。
 * 存量 conn-N 不在这里生成；投影遇到同端点已有边时复用原 ID。
 */
export const CANVAS_VIDEO_FRAME_EDGE_FIELDS = ["videoStartFrameNodeId", "videoEndFrameNodeId"] as const;

export type CanvasVideoFrameEdgeField = (typeof CANVAS_VIDEO_FRAME_EDGE_FIELDS)[number];

type CanvasConnectionEndpoints = Pick<CanvasConnection, "fromNodeId" | "toNodeId">;

export function mediaReferenceEdgeId(fromNodeId: string, toNodeId: string): string {
    return `conn:r1:${toNodeId}:${fromNodeId}`;
}

export function videoFrameEdgeId(field: string, fromNodeId: string, toNodeId: string): string {
    return `conn:frame:${field}:${toNodeId}:${fromNodeId}`;
}

export function structuralEdgeId({ fromNodeId, toNodeId }: CanvasConnectionEndpoints): string {
    return `conn:${toNodeId}:${fromNodeId}:-:-`;
}

/** R8：批量分组归属，只服务折叠/布局视觉连线，不是生成输入。 */
export function batchGroupEdgeId(fromNodeId: string, toNodeId: string): string {
    return `conn:r8:${toNodeId}:${fromNodeId}`;
}

/** 字段投影出的关系按族取 ID。文本族与 R1 共用身份，端点集合不重叠。 */
export function projectedRelationEdgeId(relation: Pick<CanvasProjectedRelation, "family" | "field" | "fromNodeId" | "toNodeId">): string {
    if (relation.family === "R1-frame") {
        return videoFrameEdgeId(relation.field, relation.fromNodeId, relation.toNodeId);
    }
    return mediaReferenceEdgeId(relation.fromNodeId, relation.toNodeId);
}
