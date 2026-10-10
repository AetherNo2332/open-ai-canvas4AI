import { resetGenerationTaskMetadata } from "@/lib/canvas/canvas-project-generation";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata, type StoryboardRow } from "@/types/canvas";
import { getFrameChildren, isFrameNode } from "@/lib/canvas/canvas-frame";

const COPY_TITLE_SUFFIX = /^(.*)_copy(\d+)$/i;

export function nextCopiedNodeTitle(sourceTitle: string, existingTitles: Iterable<string>) {
    const sourceMatch = sourceTitle.match(COPY_TITLE_SUFFIX);
    const baseTitle = (sourceMatch?.[1] || sourceTitle.replace(/ Copy$/i, "")).trim() || sourceTitle.trim() || "未命名节点";
    let maxCopyIndex = 0;
    for (const title of existingTitles) {
        const match = title.match(COPY_TITLE_SUFFIX);
        if (!match || match[1] !== baseTitle) continue;
        const copyIndex = Number(match[2]);
        if (Number.isSafeInteger(copyIndex)) maxCopyIndex = Math.max(maxCopyIndex, copyIndex);
    }
    return `${baseTitle}_copy${maxCopyIndex + 1}`;
}

function remapReferenceId(nodeId: string | undefined, idMap: ReadonlyMap<string, string>) {
    return nodeId ? idMap.get(nodeId) || nodeId : undefined;
}

function remapOwnedNodeId(nodeId: string | undefined, idMap: ReadonlyMap<string, string>) {
    return nodeId ? idMap.get(nodeId) : undefined;
}

function remapReferenceIds(nodeIds: string[] | undefined, idMap: ReadonlyMap<string, string>) {
    return nodeIds ? Array.from(new Set(nodeIds.map((nodeId) => idMap.get(nodeId) || nodeId))) : undefined;
}

function copyStoryboardRow(row: StoryboardRow, idMap: ReadonlyMap<string, string>): StoryboardRow {
    const imageNodeId = remapOwnedNodeId(row.imageNodeId, idMap);
    const videoNodeId = remapOwnedNodeId(row.videoNodeId, idMap);
    const hasCopiedOutput = Boolean(imageNodeId || videoNodeId);
    return {
        ...row,
        characters: (row.characters || []).map((character) => ({
            ...character,
            characterImageNodeId: remapReferenceId(character.characterImageNodeId, idMap),
        })),
        assetBindings: (row.assetBindings || []).map((binding) => ({ ...binding, nodeId: remapReferenceId(binding.nodeId, idMap)! })),
        imageNodeId,
        videoNodeId,
        status: hasCopiedOutput ? row.status : "idle",
        errorDetails: undefined,
    };
}

// 副本只能继承内容和用户引用，运行中任务、批次及指向源生成结果的关系必须隔离。
export function isolateCopiedNodeMetadata(node: CanvasNodeData, idMap: ReadonlyMap<string, string>): CanvasNodeMetadata {
    const metadata = resetGenerationTaskMetadata(node.metadata, node.metadata?.content ? "success" : "idle");
    delete metadata.generationBatches;
    delete metadata.batchRootId;
    delete metadata.batchChildIds;
    delete metadata.batchFailedCount;
    delete metadata.isBatchRoot;
    delete metadata.primaryImageId;
    delete metadata.imageBatchExpanded;
    delete metadata.batchUsesReferenceImages;
    delete metadata.versionOfNodeId;
    delete metadata.versionLabel;
    delete metadata.versionPrimary;
    for (const key of Object.keys(metadata)) if (key.startsWith("generationApplied") || key.startsWith("generationPersistence")) delete (metadata as Record<string, unknown>)[key];

    metadata.copiedFromNodeId = node.id;
    if (node.type === CanvasNodeType.Image || node.type === CanvasNodeType.Video || node.type === CanvasNodeType.Audio) {
        metadata.generationResultPlacement = "replace-node";
    }
    metadata.frame = node.metadata?.frame ? { ...node.metadata.frame } : undefined;
    metadata.referenceSetId = remapOwnedNodeId(node.metadata?.referenceSetId, idMap);
    metadata.referenceAssetNodeIds = node.metadata?.referenceAssetNodeIds
        ?.map((nodeId) => remapOwnedNodeId(nodeId, idMap))
        .filter((nodeId): nodeId is string => Boolean(nodeId));
    metadata.videoStartFrameNodeId = remapReferenceId(node.metadata?.videoStartFrameNodeId, idMap);
    metadata.videoEndFrameNodeId = remapReferenceId(node.metadata?.videoEndFrameNodeId, idMap);
    metadata.previsPreviewNodeId = remapOwnedNodeId(node.metadata?.previsPreviewNodeId, idMap);
    metadata.previsDepthNodeId = remapOwnedNodeId(node.metadata?.previsDepthNodeId, idMap);
    metadata.previsNormalNodeId = remapOwnedNodeId(node.metadata?.previsNormalNodeId, idMap);

    const characterViewNodeIds = node.metadata?.characterViewNodeIds;
    const copiedCharacterViewNodeIds = characterViewNodeIds ? {
        front: remapOwnedNodeId(characterViewNodeIds.front, idMap),
        side: remapOwnedNodeId(characterViewNodeIds.side, idMap),
        back: remapOwnedNodeId(characterViewNodeIds.back, idMap),
    } : undefined;
    metadata.characterViewNodeIds = copiedCharacterViewNodeIds && Object.values(copiedCharacterViewNodeIds).some(Boolean)
        ? copiedCharacterViewNodeIds
        : undefined;
    metadata.emotionEdit = node.metadata?.emotionEdit ? {
        ...node.metadata.emotionEdit,
        sourceNodeId: remapReferenceId(node.metadata.emotionEdit.sourceNodeId, idMap)!,
        faceBox: { ...node.metadata.emotionEdit.faceBox },
        editRegion: node.metadata.emotionEdit.editRegion ? { ...node.metadata.emotionEdit.editRegion } : undefined,
    } : undefined;
    metadata.storyboard = node.metadata?.storyboard ? {
        rows: node.metadata.storyboard.rows.map((row) => copyStoryboardRow(row, idMap)),
        visibleColumns: [...node.metadata.storyboard.visibleColumns],
        referenceNodeIds: remapReferenceIds(node.metadata.storyboard.referenceNodeIds, idMap) || [],
    } : undefined;
    return metadata;
}

export function copyCanvasNodeGraph(nodes: CanvasNodeData[], connections: CanvasConnection[], sourceId: string, idMap: ReadonlyMap<string, string>, connectionId: () => string, title?: string) {
    const source = nodes.find((node) => node.id === sourceId);
    if (!source) throw new Error("复制来源节点不存在");
    const sources = isFrameNode(source) ? [source, ...getFrameChildren(source.id, nodes)] : [source];
    const copiedIds = new Set(sources.map((node) => node.id));
    const copiedMap = new Map([...idMap].filter(([id]) => copiedIds.has(id)));
    const copiedNodes = sources.map((node) => {
        const id = copiedMap.get(node.id);
        if (!id || nodes.some((item) => item.id === id)) throw new Error("复制节点 ID 缺失或重复");
        const metadata = isolateCopiedNodeMetadata(node, copiedMap);
        if (node.type === CanvasNodeType.Drawing) {
            metadata.drawingId = `${id}-document`;
            metadata.drawingRevision = 0;
            metadata.drawingUpdatedAt = undefined;
            metadata.drawingShapeCount = 0;
            metadata.drawingPageCount = 1;
            if (metadata.drawingDocument) {
                metadata.drawingDocument = { ...structuredClone(metadata.drawingDocument), revision: 1, updatedAt: new Date().toISOString() };
                metadata.drawingEngine = metadata.drawingDocument.engine;
                metadata.drawingRevision = metadata.drawingDocument.revision;
                metadata.drawingUpdatedAt = metadata.drawingDocument.updatedAt;
                metadata.drawingShapeCount = metadata.drawingDocument.shapeCount;
                metadata.drawingPageCount = metadata.drawingDocument.pageCount;
            }
        }
        return { ...node, id, title: node.id === source.id ? title ?? nextCopiedNodeTitle(source.title, nodes.map((item) => item.title)) : node.title, position: { x: node.position.x + 36, y: node.position.y + 36 }, parentId: node.parentId ? copiedMap.get(node.parentId) || node.parentId : undefined, metadata };
    });
    const copiedConnections = connections.filter((edge) => copiedIds.has(edge.fromNodeId) && copiedIds.has(edge.toNodeId)).map((edge) => ({ ...edge, id: connectionId(), fromNodeId: copiedMap.get(edge.fromNodeId)!, toNodeId: copiedMap.get(edge.toNodeId)! }));
    if (!isFrameNode(source)) {
        for (const edge of connections.filter((edge) => edge.toNodeId === source.id && !copiedIds.has(edge.fromNodeId))) copiedConnections.push({ ...edge, id: connectionId(), toNodeId: copiedMap.get(source.id)! });
    }
    return { nodes: copiedNodes, connections: copiedConnections };
}
