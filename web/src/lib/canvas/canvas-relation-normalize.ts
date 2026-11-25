import { withCanvasEdgePresentation, type CanvasEdgePresentationMap } from "@/lib/canvas/canvas-edge-presentation";
import type { CanvasProjectedRelation } from "@/lib/canvas/canvas-node-inputs";
import { isCanvasReferenceFieldTarget, normalizeImportedCanvasRelations } from "@/lib/canvas/canvas-import-normalize";
import { projectCanvasRelations } from "@/lib/canvas/canvas-node-inputs";
import { ensureMediaNodeMinimumSize } from "@/lib/canvas/canvas-node-size";
import { projectReferenceConnections, REFERENCE_FIELDS_VERSION } from "@/lib/canvas/canvas-reference-fields";
import { projectedRelationEdgeId } from "@/lib/canvas/canvas-relation-edge-id";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData } from "@/types/canvas";

export type CanvasNormalizedRelations = {
    nodes: CanvasNodeData[];
    connections: CanvasConnection[];
};

export type CanvasRelationNormalizeOptions = {
    /** 外部文档需要把「只有边、没有字段」的旧关系反解成字段；内存文档保持字段真源。 */
    inferFieldsFromConnections?: boolean;
    edgePresentation?: CanvasEdgePresentationMap;
};

/**
 * 关系归一：字段是真源，connections 是投影缓存。
 *
 * 输入可以只有字段、只有旧边，或两者混合。先反解旧边，再按目标节点顺序和字段顺序
 * 重排投影；带 handle 的结构边、模板边等未声明关系保持原位。悬空端点统一剔除。
 */
export function normalizeCanvasRelations(nodes: CanvasNodeData[], connections: CanvasConnection[], options: CanvasRelationNormalizeOptions = {}): CanvasNormalizedRelations {
    const normalizedNodes = options.inferFieldsFromConnections === false ? nodes : normalizeImportedCanvasRelations(nodes, connections);
    const nodeIds = new Set(normalizedNodes.map((node) => node.id));
    const nodeById = new Map(normalizedNodes.map((node) => [node.id, node]));
    const alive = connections.filter((connection) => Boolean(connection.fromNodeId && connection.toNodeId && nodeIds.has(connection.fromNodeId) && nodeIds.has(connection.toNodeId)));

    const projectedByTargetId = new Map<string, CanvasProjectedRelation[]>();
    const projectedPairs = new Set<string>();
    for (const relation of projectCanvasRelations(normalizedNodes)) {
        const target = nodeById.get(relation.toNodeId);
        if (!target || !nodeIds.has(relation.fromNodeId)) continue;
        if (relation.family === "R1-frame" ? target.type !== CanvasNodeType.Video : !isCanvasReferenceFieldTarget(target)) continue;
        const pair = pairKey(relation.fromNodeId, relation.toNodeId);
        if (projectedPairs.has(pair)) continue;
        projectedPairs.add(pair);
        const relations = projectedByTargetId.get(relation.toNodeId) || [];
        relations.push(relation);
        projectedByTargetId.set(relation.toNodeId, relations);
    }

    const existingByPair = new Map<string, CanvasConnection>();
    for (const connection of alive) {
        const pair = pairKey(connection.fromNodeId, connection.toNodeId);
        if (!existingByPair.has(pair)) existingByPair.set(pair, connection);
    }

    const blocksAt = new Map<number, string[]>();
    const blockedTargets = new Set<string>();
    const drop = new Array<boolean>(alive.length).fill(false);
    alive.forEach((connection, index) => {
        const targetId = projectedPairs.has(pairKey(connection.fromNodeId, connection.toNodeId)) ? connection.toNodeId : "";
        if (!targetId) return;
        drop[index] = true;
        if (blockedTargets.has(targetId)) return;
        blockedTargets.add(targetId);
        blocksAt.set(index, [...(blocksAt.get(index) || []), targetId]);
    });
    for (const targetId of projectedByTargetId.keys()) {
        if (blockedTargets.has(targetId)) continue;
        blockedTargets.add(targetId);
        blocksAt.set(alive.length, [...(blocksAt.get(alive.length) || []), targetId]);
    }

    const emitTarget = (targetId: string): CanvasConnection[] =>
        (projectedByTargetId.get(targetId) || []).map(
            (relation) =>
                existingByPair.get(pairKey(relation.fromNodeId, relation.toNodeId)) || {
                    id: projectedRelationEdgeId(relation),
                    fromNodeId: relation.fromNodeId,
                    toNodeId: relation.toNodeId,
                },
        );

    const result: CanvasConnection[] = [];
    for (let index = 0; index <= alive.length; index += 1) {
        for (const targetId of blocksAt.get(index) || []) result.push(...emitTarget(targetId));
        if (index === alive.length) break;
        if (!drop[index]) result.push(alive[index]);
    }
    const projected = projectReferenceConnections(normalizedNodes, result, REFERENCE_FIELDS_VERSION);
    const presented = options.edgePresentation ? applyEdgePresentation(projected, options.edgePresentation) : projected;
    const unchanged = presented.length === connections.length && presented.every((connection, index) => connection === connections[index]);
    return { nodes: normalizedNodes, connections: unchanged ? connections : presented };
}

export function normalizeSharedCanvas(project: { nodes?: CanvasNodeData[]; connections?: CanvasConnection[]; edgePresentation?: CanvasEdgePresentationMap }) {
    return normalizeCanvasRelations((project.nodes || []).map(ensureMediaNodeMinimumSize), project.connections || [], {
        inferFieldsFromConnections: true,
        edgePresentation: project.edgePresentation,
    });
}

function pairKey(fromNodeId: string, toNodeId: string) {
    return `${fromNodeId}\u0000${toNodeId}`;
}

function applyEdgePresentation(connections: CanvasConnection[], presentation: CanvasEdgePresentationMap) {
    let changed = false;
    const next = connections.map((connection) => {
        const presented = withCanvasEdgePresentation(connection, presentation);
        if (presented !== connection) changed = true;
        return presented;
    });
    return changed ? next : connections;
}
