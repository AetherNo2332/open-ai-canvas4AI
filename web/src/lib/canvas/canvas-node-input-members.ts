import type { CanvasNodeData } from "@/types/canvas";

/** 视频首尾帧字段：R1 的成员资格子族，顺序排在媒体参考之后、文本参考之前。 */
export const CANVAS_NODE_INPUT_FRAME_FIELDS = ["videoStartFrameNodeId", "videoEndFrameNodeId"] as const;

/**
 * 目标节点的输入成员 ID，顺序 = 字段序：referenceNodeIds → 首尾帧 → referenceTextNodeIds。
 *
 * 拖拽连线只写这些字段，connections 是显示投影，因此不作为成员来源。
 * pendingSourceIds 是尚未写进字段的候选成员，只由连线预检显式传入。
 */
export function resolveCanvasNodeInputIds(nodes: CanvasNodeData[], targetId: string, pendingSourceIds: ReadonlyArray<string> = []) {
    const nodeById = new Map(nodes.map((node) => [node.id, node]));
    const target = nodeById.get(targetId);
    if (!target) return [];
    const ids: string[] = [];
    const seen = new Set<string>();
    const push = (sourceId: string | undefined) => {
        if (!sourceId || sourceId === targetId || seen.has(sourceId)) return;
        if (!nodeById.has(sourceId)) return;
        if (target.metadata?.batchRootId === sourceId) return;
        seen.add(sourceId);
        ids.push(sourceId);
    };
    for (const sourceId of target.metadata?.referenceNodeIds || []) push(sourceId);
    for (const field of CANVAS_NODE_INPUT_FRAME_FIELDS) push(target.metadata?.[field]);
    for (const sourceId of target.metadata?.referenceTextNodeIds || []) push(sourceId);
    for (const sourceId of pendingSourceIds) push(sourceId);
    return ids;
}
