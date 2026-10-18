import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";
import { buildCanvasMediaDownloadFileName } from "./canvas-media-download";

export function isCanvasDownloadableMediaNode(node: CanvasNodeData) {
    const media = node.type === CanvasNodeType.Image || node.type === CanvasNodeType.Video || node.type === CanvasNodeType.Audio;
    return media && Boolean(node.metadata?.storageKey?.trim() || node.metadata?.content?.trim());
}

export function resolveCanvasMediaDownloadItems(nodes: CanvasNodeData[]) {
    const usedNames = new Set<string>();
    return nodes.filter(isCanvasDownloadableMediaNode).map((node) => ({ node, fileName: buildCanvasMediaDownloadFileName(node, usedNames) }));
}
