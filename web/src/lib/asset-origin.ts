type AssetOrigin = { source?: string; metadata?: Record<string, unknown> };

function text(value: unknown) {
    return typeof value === "string" ? value.trim() : "";
}

export function assetSourceLabel(asset: AssetOrigin) {
    const declared = text(asset.source);
    if (declared) return declared;
    switch (asset.metadata?.source) {
        case "generation-task":
            return "生成任务";
        case "canvas-generation":
            return "画布生成";
        case "canvas-upload":
            return "画布上传";
        case "canvas-manual":
            return "画布素材";
        default:
            return "未标注来源";
    }
}

export function assetProjectLabel(asset: AssetOrigin) {
    return text(asset.metadata?.projectName) || (Array.isArray(asset.metadata?.projectIds) && asset.metadata.projectIds.length ? "已关联业务项目" : "未关联业务项目");
}

export function assetOriginLabel(asset: AssetOrigin) {
    const canvasName = text(asset.metadata?.canvasName);
    const projectName = text(asset.metadata?.projectName);
    const project = projectName ? `项目：${projectName}` : Array.isArray(asset.metadata?.projectIds) && asset.metadata.projectIds.length ? assetProjectLabel(asset) : "";
    const canvas = canvasName ? `画布：${canvasName}` : text(asset.metadata?.canvasId) ? "来自画布" : "";
    return [project, canvas].filter(Boolean).join(" · ") || "个人素材";
}
