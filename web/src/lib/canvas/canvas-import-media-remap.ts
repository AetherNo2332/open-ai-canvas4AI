import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

export type CanvasImportedMediaMapping = { storageKey: string; url: string };

/* Imported local blobs belong to the source browser tab and must not escape into the new canvas. */
export function remapImportedCanvasNodeMedia(node: CanvasNodeData, storageKeyMap: ReadonlyMap<string, CanvasImportedMediaMapping>, isDeadBlob: (value?: string) => boolean): CanvasNodeData {
    const oldKey = node.metadata?.storageKey;
    const mapped = oldKey ? storageKeyMap.get(oldKey) : undefined;
    const media = node.type === CanvasNodeType.Image || node.type === CanvasNodeType.Video || node.type === CanvasNodeType.Audio;
    const nextStorageKey = mapped ? mapped.storageKey : oldKey && !isDeadBlob(oldKey) ? oldKey : undefined;
    const content = media ? (mapped ? mapped.url : isDeadBlob(node.metadata?.content) ? "" : node.metadata?.content) : node.metadata?.content;
    const previewContent = media ? (mapped ? mapped.url : isDeadBlob(node.metadata?.previewContent) ? "" : node.metadata?.previewContent) : node.metadata?.previewContent;
    return {
        ...node,
        metadata: {
            ...node.metadata,
            storageKey: nextStorageKey,
            ...(content !== undefined ? { content } : {}),
            ...(previewContent !== undefined ? { previewContent } : {}),
        },
    };
}
