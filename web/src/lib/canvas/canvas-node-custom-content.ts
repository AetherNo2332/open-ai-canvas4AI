import { isPrevisWorkstationNode } from "@/lib/canvas/previs/previs-node";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

/**
 * 画布节点是否走自定义面板（renderNodeContent），而不是内置媒体/文本渲染器。
 *
 * 预演台分支只看工作站身份（isPrevisWorkstationNode）：服务端回写的产物节点同样带
 * previsSceneId，但必须继续交给视频/图片渲染器，否则会变成点不开的"死卡片"。
 */
export function canvasNodeHasCustomContent(node: Pick<CanvasNodeData, "type" | "metadata">, isEditingContent: boolean) {
    return node.type === CanvasNodeType.Config ||
        node.type === CanvasNodeType.Script ||
        node.type === CanvasNodeType.BatchTable ||
        isPrevisWorkstationNode(node) ||
        (node.metadata?.workflowKind === "character" && Boolean(node.metadata.characterAssetId)) ||
        (node.metadata?.workflowKind === "story_input" && !isEditingContent) ||
        (node.metadata?.workflowKind === "styleboard" && !node.metadata.content);
}