import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

// 短剧资产卡节点（`shortplay-asset`）：集画布上的只读投影。
//
// 渲染零表依赖：封面、形象状态、分类、音色全部读节点快照（后端注入时冗余写入），
// 所以短剧服务端不在场时这张卡照样能渲染、能连线、能被引用。权威是资产表，
// 节点只是投影；刷新按 shortplayAssetRevision 判断表变没变。

/** 资产分类：与短剧域一致的三种。 */
export type ShortplayAssetCategory = "character" | "prop" | "scene";

/** 参考图 / 音色样本生命周期；空串 = 从未生成。 */
export type ShortplayAssetMediaStatus = "" | "queued" | "running" | "ready" | "failed";

/** 资产卡节点的 metadata 形状；节点 metadata 是 JSON，读时按本类型取用。 */
export type ShortplayAssetNodeMetadata = {
    content?: string;
    /** 资产表行 ID（作品内整数序号）；空 = 局部卡，不参与刷新与回写。 */
    shortplayAssetId?: number;
    shortplayAssetAppearance?: string;
    shortplayAssetAliases?: string[];
    shortplayAssetVoice?: string;
    shortplayAssetLocked?: boolean;
    shortplayAssetCategory?: ShortplayAssetCategory;
    /** 快照对应的表行 revision：回写 CAS 与「表变了没有」都看它。 */
    shortplayAssetRevision?: number;
    shortplayAssetImageResourceId?: string;
    shortplayAssetImageVersion?: number;
    shortplayAssetCoverUrl?: string;
    shortplayAssetImageStatus?: ShortplayAssetMediaStatus;
    shortplayAssetDerivedFrom?: number;
    shortplayAssetVariantName?: string;
    shortplayAssetRenderRule?: string;
    shortplayAssetBaseImageStale?: boolean;
    shortplayAssetAudioResourceId?: string;
    shortplayAssetAudioUrl?: string;
    shortplayAssetAudioStatus?: ShortplayAssetMediaStatus;
    shortplayAssetAudioVersion?: number;
    shortplayAssetBaseAudioStale?: boolean;
};

export function isShortplayAssetNode(node: CanvasNodeData): boolean {
    return node.type === CanvasNodeType.ShortplayAsset;
}

export function shortplayAssetMetadata(node: CanvasNodeData): ShortplayAssetNodeMetadata | null {
    return isShortplayAssetNode(node) ? (node.metadata as unknown as ShortplayAssetNodeMetadata) : null;
}

/**
 * 归属是双判：节点带 shortplayAssetId **且** 当前画布是集画布。
 * 只看字段会在「复制残留」的自由画布上把局部卡显示成项目资产。
 */
export function shortplayAssetPlacement(node: CanvasNodeData, canvasLinked: boolean): "domain" | "local" {
    return canvasLinked && Boolean(shortplayAssetMetadata(node)?.shortplayAssetId) ? "domain" : "local";
}

/** 卡片头像回落字：取名字第一个字符。 */
export function shortplayAssetInitial(name: string): string {
    return Array.from(name.trim())[0] || "资";
}

/**
 * 自由画布上的短剧入口（局部卡与「收录到短剧」）只认独立的 `shortplayEnabled`，
 * 与 drama 的 `shortDramaEnabled` 无关；一旦画布已是关联画布，资产走作品投影，不再出现入口。
 */
export function shortplayCanvasEntriesEnabled(input: { shortplayEnabled: boolean; isProjectLinked: boolean }): boolean {
    return input.shortplayEnabled && !input.isProjectLinked;
}

export const SHORTPLAY_ASSET_CATEGORY_LABELS: Record<ShortplayAssetCategory, string> = {
    character: "角色",
    prop: "道具",
    scene: "场景",
};
