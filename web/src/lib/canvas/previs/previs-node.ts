import type { CanvasNodeData } from "@/types/canvas";

/**
 * 可打开的预演台工作站判定：只有 workflowKind==="shot" 的节点才是工作站
 * （浏览器 createPrevisShot 与 Agent 工作站节点同构）。
 *
 * 服务端回写的产物节点（reference_video / reference_set）同样带 previsSceneId，
 * 但它们是普通媒体节点：按 previsSceneId 判定会把它们渲染成点不开的预演台卡片。
 */
export function isPrevisWorkstationNode(node: Pick<CanvasNodeData, "metadata"> | null | undefined) {
    return node?.metadata?.workflowKind === "shot";
}
