import { isCanvasTextReferenceSource } from "@/lib/canvas/canvas-node-inputs";
import type { CanvasConnection, CanvasNodeData, CanvasNodeMetadata } from "@/types/canvas";

const REFERENCE_TARGET_TYPES = new Set<string>(["image", "video", "audio", "config", "svg", "html", "chart", "panorama", "compare", "colorgrade", "media-conversion"]);

export function isCanvasReferenceFieldTarget(node: CanvasNodeData | undefined) {
    return Boolean(node && REFERENCE_TARGET_TYPES.has(node.type));
}

/**
 * 外部文档兼容入口：旧导出包、第三方导入和旧分享快照可能只有 connections，
 * 没有节点上的 referenceNodeIds。关系字段已经成为真源，因此先在导入边界反解，
 * 后续投影、保存和重开才不会把旧边丢掉。
 */
export function normalizeImportedCanvasRelations(nodes: CanvasNodeData[], connections: CanvasConnection[]): CanvasNodeData[] {
    if (!connections.length || !nodes.length) return nodes;
    const nodeById = new Map(nodes.map((node) => [node.id, node]));
    const mediaByTargetId = new Map<string, string[]>();
    const textByTargetId = new Map<string, string[]>();
    for (const connection of connections) {
        const { fromNodeId, toNodeId } = connection;
        if (!fromNodeId || !toNodeId || fromNodeId === toNodeId) continue;
        if (connection.fromHandleId || connection.toHandleId || connection.relation) continue;
        const source = nodeById.get(fromNodeId);
        const target = nodeById.get(toNodeId);
        if (!source || !isCanvasReferenceFieldTarget(target)) continue;
        const field = isCanvasTextReferenceSource(source) ? textByTargetId : mediaByTargetId;
        const values = field.get(toNodeId) || [];
        if (!values.includes(fromNodeId)) values.push(fromNodeId);
        field.set(toNodeId, values);
    }

    let changed = false;
    const next = nodes.map((node) => {
        if (!isCanvasReferenceFieldTarget(node)) return node;
        const mediaEdges = mediaByTargetId.get(node.id) || [];
        const textEdges = textByTargetId.get(node.id) || [];
        const declaredMedia = node.metadata?.referenceNodeIds || [];
        const declaredText = node.metadata?.referenceTextNodeIds || [];
        if (!declaredMedia.length && !declaredText.length && !mediaEdges.length && !textEdges.length) return node;

        const media: string[] = [];
        const text: string[] = [];
        const seenMedia = new Set<string>();
        const seenText = new Set<string>();
        const add = (field: string[], seen: Set<string>, id: string | undefined, requireNode: boolean) => {
            if (!id || id === node.id || seen.has(id) || (requireNode && !nodeById.has(id))) return;
            seen.add(id);
            field.push(id);
        };

        for (const id of declaredText) {
            if (id && !nodeById.has(id)) {
                add(text, seenText, id, false);
            } else if (id && isCanvasTextReferenceSource(nodeById.get(id))) {
                add(text, seenText, id, true);
            } else {
                add(media, seenMedia, id, true);
            }
        }
        for (const id of declaredMedia) {
            if (!id) continue;
            if (!nodeById.has(id)) add(media, seenMedia, id, false);
            else if (isCanvasTextReferenceSource(nodeById.get(id))) add(text, seenText, id, true);
            else add(media, seenMedia, id, true);
        }
        for (const id of mediaEdges) add(media, seenMedia, id, true);
        for (const id of textEdges) add(text, seenText, id, true);

        const mediaUnchanged = media.length === declaredMedia.length && media.every((id, index) => id === declaredMedia[index]);
        const textUnchanged = text.length === declaredText.length && text.every((id, index) => id === declaredText[index]);
        if (mediaUnchanged && textUnchanged) return node;
        changed = true;
        return {
            ...node,
            metadata: {
                ...node.metadata,
                referenceNodeIds: media.length ? media : undefined,
                referenceTextNodeIds: text.length ? text : undefined,
            } satisfies CanvasNodeMetadata,
        };
    });
    return changed ? next : nodes;
}
