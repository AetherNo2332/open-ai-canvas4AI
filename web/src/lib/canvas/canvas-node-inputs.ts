import { getNodeResourceKind } from "@/lib/canvas/node-registry";
import type { CanvasResourceKind } from "@/lib/canvas/canvas-resource-references";
import { CANVAS_NODE_INPUT_FRAME_FIELDS, resolveCanvasNodeInputIds } from "@/lib/canvas/canvas-node-input-members";
import type { CanvasNodeData } from "@/types/canvas";

/**
 * 关系投影与输入解析的只读入口。
 *
 * 语义真源是节点字段，connections 是渲染期投影。顺序与后端一致：
 * 先按目标节点在 nodes 里的顺序，再按该节点字段的数组顺序。
 */
export type CanvasProjectedRelationFamily = "R1" | "R1-frame" | "R1-text";

export type CanvasProjectedRelation = {
    family: CanvasProjectedRelationFamily;
    fromNodeId: string;
    toNodeId: string;
    /** 产出这条关系的字段名，用于对账与报错定位。 */
    field: string;
};

/**
 * 字段 → 应有边。纯函数：不读 connections，不改节点。
 *
 * 帧字段是 R1 的成员资格子族，与参考集重叠时只保留 R1。
 * 文本来源是并列的独立族，同一目标排在媒体参考与帧之后。
 */
export function projectCanvasRelations(nodes: CanvasNodeData[]): CanvasProjectedRelation[] {
    const relations: CanvasProjectedRelation[] = [];
    for (const node of nodes) {
        if (!node.id) continue;
        const seen = new Set<string>();
        for (const sourceId of node.metadata?.referenceNodeIds || []) {
            if (!sourceId || seen.has(sourceId)) continue;
            seen.add(sourceId);
            relations.push({ family: "R1", fromNodeId: sourceId, toNodeId: node.id, field: "referenceNodeIds" });
        }
        for (const field of CANVAS_NODE_INPUT_FRAME_FIELDS) {
            const sourceId = node.metadata?.[field];
            if (!sourceId || seen.has(sourceId)) continue;
            seen.add(sourceId);
            relations.push({ family: "R1-frame", fromNodeId: sourceId, toNodeId: node.id, field });
        }
        for (const sourceId of node.metadata?.referenceTextNodeIds || []) {
            if (!sourceId || seen.has(sourceId)) continue;
            seen.add(sourceId);
            relations.push({ family: "R1-text", fromNodeId: sourceId, toNodeId: node.id, field: "referenceTextNodeIds" });
        }
    }
    const seenRelations = new Set<string>();
    return relations.filter((relation) => {
        const key = relation.family === "R1-frame" ? `frame:${relation.field}\0${relation.fromNodeId}\0${relation.toNodeId}` : `${relation.fromNodeId}\0${relation.toNodeId}`;
        if (seenRelations.has(key)) return false;
        seenRelations.add(key);
        return true;
    });
}

export type CanvasNodeInputResolver = (nodeId: string, includeSelf?: boolean) => CanvasNodeData[];

/**
 * 建一个输入解析器：一次建表，多次查询。
 *
 * 成员资格完全来自字段；批量子节点不算素材输入，没有字段输入时退回批量根节点；
 * 仍然没有时，资源节点可按 includeSelf 把自身当作引用素材。
 */
export function createCanvasNodeInputResolver(nodes: CanvasNodeData[]): CanvasNodeInputResolver {
    const nodeById = new Map(nodes.map((node) => [node.id, node]));
    const inputsByTargetId = new Map<string, CanvasNodeData[]>();
    const idsByTargetId = new Map<string, string[]>();
    for (const node of nodes) {
        const ids = resolveCanvasNodeInputIds(nodes, node.id);
        if (!ids.length) continue;
        idsByTargetId.set(node.id, ids);
        inputsByTargetId.set(
            node.id,
            ids.flatMap((id) => {
                const source = nodeById.get(id);
                return source && isCanvasResourceNode(source) ? [source] : [];
            }),
        );
    }

    const resolve = (nodeId: string, includeSelf: boolean, visited: Set<string>): CanvasNodeData[] => {
        if (visited.has(nodeId)) return [];
        visited.add(nodeId);
        const node = nodeById.get(nodeId);
        if (!node) return [];
        if (idsByTargetId.get(nodeId)?.length) return uniqueCanvasNodes(inputsByTargetId.get(nodeId) || []);
        const batchRoot = node.metadata?.batchRootId ? nodeById.get(node.metadata.batchRootId) : undefined;
        if (batchRoot?.metadata?.isBatchRoot) return resolve(batchRoot.id, false, visited);
        return includeSelf && isCanvasResourceNode(node) ? [node] : [];
    };

    return (nodeId, includeSelf = false) => resolve(nodeId, includeSelf, new Set<string>());
}

/** 单次查询入口；批量场景请用 createCanvasNodeInputResolver 复用建表结果。 */
export function resolveNodeInputs(nodes: CanvasNodeData[], nodeId: string, options: { includeSelf?: boolean } = {}) {
    return createCanvasNodeInputResolver(nodes)(nodeId, options.includeSelf === true);
}

export function uniqueCanvasNodes(nodes: CanvasNodeData[]) {
    const seen = new Set<string>();
    return nodes.filter((node) => {
        if (seen.has(node.id)) return false;
        seen.add(node.id);
        return true;
    });
}

/** 节点作为 @ 引用素材的类别；角色卡是跨类型覆盖，必须先于类型判定。 */
export function canvasNodeResourceKind(node: CanvasNodeData): CanvasResourceKind | null {
    if (node.metadata?.workflowKind === "character" && node.metadata.characterAssetId) return "character";
    return getNodeResourceKind(node);
}

export function isCanvasResourceNode(node: CanvasNodeData) {
    return Boolean(canvasNodeResourceKind(node));
}

/** 文字化资产进入 referenceTextNodeIds，与媒体参考族并列。 */
export function isCanvasTextReferenceSource(node: CanvasNodeData | undefined) {
    return Boolean(node) && canvasNodeResourceKind(node as CanvasNodeData) === "text";
}
