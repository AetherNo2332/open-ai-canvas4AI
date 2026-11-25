import { isolateCopiedNodeMetadata } from "@/lib/canvas/canvas-node-copy";
import { isSubmapNode } from "@/lib/canvas/canvas-submap";
import { countSubmapExternalReferences, submapCopySources } from "@/lib/canvas/canvas-submap-operations";
import type { CanvasProject } from "@/stores/canvas/use-canvas-store";
import type { CanvasNodeData } from "@/types/canvas";

export type CanvasSubmapExport = {
    /** Exported package carries node fields; the importer rebuilds relations from them. */
    project: Omit<CanvasProject, "connections">;
    droppedConnections: number;
    droppedReferences: number;
};

/**
 * Keep one submap container and its direct children, then remove references and media
 * results that belong outside the exported scope. IDs remain stable so the importer
 * can remap foreign references without breaking the internal graph.
 */
export function exportSubmapProject(project: CanvasProject, submapId: string): CanvasSubmapExport {
    const submap = project.nodes.find((node) => node.id === submapId);
    if (!submap || !isSubmapNode(submap)) throw new Error("找不到要导出的子画布");
    const sources = submapCopySources(submap, project.nodes);
    const scopeIds = new Set(sources.map((node) => node.id));
    const identityMap = new Map(sources.map((node) => [node.id, node.id]));
    const nodes = sources.map((node) => exportNode(node, scopeIds, identityMap));
    const crossingConnections = project.connections.filter((connection) => scopeIds.has(connection.fromNodeId) !== scopeIds.has(connection.toNodeId));
    const { connections: _connections, ...content } = project;
    return {
        project: {
            ...content,
            projectId: undefined,
            title: submap.title,
            nodes,
            chatSessions: [],
            activeChatId: null,
            previsScenes: [],
            timeline: undefined,
            idCounters: project.idCounters,
        },
        droppedConnections: crossingConnections.length,
        droppedReferences: countSubmapExternalReferences(project.nodes, scopeIds),
    };
}

function exportNode(node: CanvasNodeData, scopeIds: ReadonlySet<string>, identityMap: ReadonlyMap<string, string>): CanvasNodeData {
    const metadata = isolateCopiedNodeMetadata(node, identityMap, { clearMediaResult: true, dropExternalReferences: true });
    delete metadata.copiedFromNodeId;
    delete metadata.generationResultPlacement;
    if (metadata.emotionEdit && !scopeIds.has(metadata.emotionEdit.sourceNodeId)) delete metadata.emotionEdit;
    return { ...node, metadata };
}
