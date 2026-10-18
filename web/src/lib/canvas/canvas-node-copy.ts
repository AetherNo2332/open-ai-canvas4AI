import { createBatchRow } from "@/lib/canvas/canvas-batch-table";
import { createStoryboardRow } from "@/lib/canvas/canvas-project-domain";
import { resetGenerationTaskMetadata } from "@/lib/canvas/canvas-project-generation";
import type { ReferenceBinding } from "@/lib/canvas/generation-contract.generated";
import { CanvasNodeType, type CanvasBatchOperation, type CanvasBatchRow, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata, type CanvasNodeStatus, type StoryboardRow } from "@/types/canvas";
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

function remapReferenceBindings(bindings: ReferenceBinding[] | undefined, idMap: ReadonlyMap<string, string>) {
    return bindings?.map((binding) => (binding.nodeId ? { ...binding, nodeId: remapReferenceId(binding.nodeId, idMap)! } : { ...binding }));
}

// 托管边由服务端按权威字段重建，副本不能继承源节点的归属标记。
function isManagedReferenceConnection(edge: Pick<CanvasConnection, "managedBy">) {
    return edge.managedBy === "reference-fields-v1";
}

// 指向分镜行的副本连线必须改用副本行 id；行没被一起复制时该字段失效。
function copiedConnection(edge: CanvasConnection, fromNodeId: string, toNodeId: string, id: string, rowIds: ReadonlyMap<string, string>): CanvasConnection {
    const copied: CanvasConnection = { ...edge, id, fromNodeId, toNodeId };
    if (edge.storyboardRowId) {
        const rowId = rowIds.get(edge.storyboardRowId);
        if (rowId) copied.storyboardRowId = rowId;
        else delete copied.storyboardRowId;
    }
    return copied;
}

const ACTIVE_ROW_STATUSES = new Set<string>(["loading", "running", "pending", "queued"]);
const COPIED_ROW_TASK_KEYS = ["taskId", "taskClientOperationId", "taskStatus", "taskProgress", "taskStage", "errorDetails"];

// 副本没有自己的任务：源行还在跑时不能继承运行中状态，没有复制产物时回到未生成。
function copiedRowStatus(status: CanvasNodeStatus | undefined, hasCopiedOutput: boolean): CanvasNodeStatus {
    return hasCopiedOutput && status && !ACTIVE_ROW_STATUSES.has(status) ? status : "idle";
}

// 行 id 由副本节点 id 派生，前后端使用同一份确定性规则，副本之间不会串行。
function copiedRowId(copiedNodeId: string, suffix: "row" | "batch-row", index: number) {
    return `${copiedNodeId}-${suffix}-${index + 1}`;
}

function copyStoryboardRow(row: StoryboardRow, idMap: ReadonlyMap<string, string>, copiedNodeId: string, index: number, rowIds?: Map<string, string>): StoryboardRow {
    const imageNodeId = remapOwnedNodeId(row.imageNodeId, idMap);
    const videoNodeId = remapOwnedNodeId(row.videoNodeId, idMap);
    const copied: Record<string, unknown> = {
        ...row,
        characters: (row.characters || []).map((character) => ({
            ...character,
            characterImageNodeId: remapReferenceId(character.characterImageNodeId, idMap),
        })),
        assetBindings: (row.assetBindings || []).map((binding) => ({ ...binding, nodeId: remapReferenceId(binding.nodeId, idMap)! })),
        imageNodeId,
        videoNodeId,
        status: copiedRowStatus(row.status, Boolean(imageNodeId || videoNodeId)),
    };
    // 行身份属于源表：副本换新 rowId，并且不继承产生源结果的任务绑定。
    delete copied.id;
    for (const key of COPIED_ROW_TASK_KEYS) delete copied[key];
    const id = copiedRowId(copiedNodeId, "row", index);
    rowIds?.set(row.id, id);
    return { ...createStoryboardRow(row.shotNumber, copied as Partial<StoryboardRow>), id };
}

function copyBatchTableRow(row: CanvasBatchRow, operation: CanvasBatchOperation, idMap: ReadonlyMap<string, string>, copiedNodeId: string, index: number, rowIds?: Map<string, string>): CanvasBatchRow {
    const id = copiedRowId(copiedNodeId, "batch-row", index);
    rowIds?.set(row.id, id);
    return {
        ...createBatchRow(operation, remapReferenceIds(row.inputNodeIds, idMap) || []),
        enabled: row.enabled,
        prompt: row.prompt,
        textNodeIds: remapReferenceIds(row.textNodeIds, idMap),
        cells: row.cells ? { ...row.cells } : undefined,
        // 行产物归属只在复制范围内跟随副本，范围外仍引用原产物。
        outputNodeId: remapOwnedNodeId(row.outputNodeId, idMap),
        id,
    };
}

// 副本只能继承内容和用户引用，运行中任务、批次及指向源生成结果的关系必须隔离。
// rowIds 收集源行 id → 副本行 id，供同批复制出来的连线改写 storyboardRowId。
export function isolateCopiedNodeMetadata(node: CanvasNodeData, idMap: ReadonlyMap<string, string>, rowIds?: Map<string, string>): CanvasNodeMetadata {
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
    const copiedNodeId = idMap.get(node.id) || node.id;
    metadata.storyboard = node.metadata?.storyboard ? {
        rows: node.metadata.storyboard.rows.map((row, index) => copyStoryboardRow(row, idMap, copiedNodeId, index, rowIds)),
        visibleColumns: [...node.metadata.storyboard.visibleColumns],
        referenceNodeIds: remapReferenceIds(node.metadata.storyboard.referenceNodeIds, idMap) || [],
    } : undefined;
    const batchTable = node.metadata?.batchTable;
    metadata.batchTable = batchTable ? {
        ...batchTable,
        rows: (batchTable.rows || []).map((row, index) => copyBatchTableRow(row, batchTable.operation, idMap, copiedNodeId, index, rowIds)),
    } : undefined;
    metadata.batchSourceNodeId = remapReferenceId(node.metadata?.batchSourceNodeId, idMap);
    // 引用权威字段必须随之重映射；复制范围之外的素材继续引用原节点。
    const generationSpec = node.metadata?.generationSpec;
    const referenceBindings = remapReferenceBindings(generationSpec?.referenceBindings, idMap);
    if (generationSpec && referenceBindings) metadata.generationSpec = { ...generationSpec, referenceBindings };
    const inputBindings = remapReferenceBindings(node.metadata?.inputBindings, idMap);
    if (inputBindings) metadata.inputBindings = inputBindings;
    return metadata;
}

export function copyCanvasNodeGraph(nodes: CanvasNodeData[], connections: CanvasConnection[], sourceId: string, idMap: ReadonlyMap<string, string>, connectionId: () => string, title?: string) {
    const source = nodes.find((node) => node.id === sourceId);
    if (!source) throw new Error("复制来源节点不存在");
    const sources = isFrameNode(source) ? [source, ...getFrameChildren(source.id, nodes)] : [source];
    const copiedIds = new Set(sources.map((node) => node.id));
    const copiedMap = new Map([...idMap].filter(([id]) => copiedIds.has(id)));
    const rowIds = new Map<string, string>();
    const copiedNodes = sources.map((node) => {
        const id = copiedMap.get(node.id);
        if (!id || nodes.some((item) => item.id === id)) throw new Error("复制节点 ID 缺失或重复");
        const metadata = isolateCopiedNodeMetadata(node, copiedMap, rowIds);
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
    const copiedConnections = connections.filter((edge) => copiedIds.has(edge.fromNodeId) && copiedIds.has(edge.toNodeId) && !isManagedReferenceConnection(edge)).map((edge) => copiedConnection(edge, copiedMap.get(edge.fromNodeId)!, copiedMap.get(edge.toNodeId)!, connectionId(), rowIds));
    if (!isFrameNode(source)) {
        // 来源在复制范围外的入边保留原节点，只把目标改挂到副本上。
        for (const edge of connections.filter((edge) => edge.toNodeId === source.id && !copiedIds.has(edge.fromNodeId) && !isManagedReferenceConnection(edge))) copiedConnections.push(copiedConnection(edge, edge.fromNodeId, copiedMap.get(source.id)!, connectionId(), rowIds));
    }
    return { nodes: copiedNodes, connections: copiedConnections };
}
