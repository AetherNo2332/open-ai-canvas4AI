import { appQueryClient } from "@/lib/query-client";
import { characterCoverResourceId } from "@/lib/canvas/canvas-character-reference";
import { getActiveUserScope } from "@/lib/user-scope";
import { getCharacter } from "@/services/api/projects";
import { resourceFileUrl, resourceStorageKey } from "@/services/api/resources";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

export function canvasAssetPreviewFallback(node: CanvasNodeData | null): string {
    if (!node) return "";
    if (node.metadata?.workflowKind === "character") return node.metadata.characterCoverUrl || "";
    if (node.type === CanvasNodeType.Drawing) return node.metadata?.drawingPreviewUrl || node.metadata?.content || "";
    return node.metadata?.content || "";
}

/** Character cards store asset/version identities; a cover URL is only a display cache. */
export async function resolveCanvasAssetPreviewInput(node: CanvasNodeData): Promise<{ storageKey?: string; fallback: string }> {
    const metadata = node.metadata;
    const fallback = canvasAssetPreviewFallback(node);
    if (metadata?.workflowKind !== "character") return { storageKey: metadata?.storageKey, fallback };
    if (fallback) {
        const resourceId = characterCoverResourceId(fallback);
        return { storageKey: resourceId ? resourceStorageKey(resourceId) : undefined, fallback };
    }
    if (!metadata.characterAssetId) return { fallback: "" };
    const scope = getActiveUserScope();
    const detail = await appQueryClient.fetchQuery({
        queryKey: ["canvas-character-preview", scope, metadata.characterAssetId, metadata.characterVersionPolicy, metadata.characterVersionId],
        queryFn: () => getCharacter(metadata.characterAssetId!),
    });
    if (getActiveUserScope() !== scope) throw new Error("账号已切换，请重新打开角色预览");
    if (metadata.characterVersionPolicy === "pinned" && metadata.characterVersionId !== detail.character.versionId) {
        throw new Error("当前角色已更新，无法预览此固定版本的形象");
    }
    const images = detail.character.representations.filter((item) => item.resourceId && (item.mediaType === "image" || item.mediaType.startsWith("image/")));
    const cover = ["turnaround_sheet", "primary", "front"].map((role) => images.find((item) => item.role === role)).find(Boolean);
    return cover ? { storageKey: resourceStorageKey(cover.resourceId), fallback: resourceFileUrl(cover.resourceId) } : { fallback: "" };
}
