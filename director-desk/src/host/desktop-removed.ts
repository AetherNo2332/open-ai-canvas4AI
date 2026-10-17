/**
 * 被剥离的桌面能力的显式提示。
 *
 * 移植后的导演台不再具备 Electron 能力(按路径读写本地文件、自动更新、MCP 宿主、
 * 扩展插件宿主)。这些入口在云端必须给出明确说明,不允许静默失败或抛未捕获异常。
 */
export type RemovedCapability = "file-save" | "file-open" | "local-path" | "file-update" | "mcp" | "extension";

const MESSAGES: Record<RemovedCapability, string> = {
    "file-save": "保存到本地文件需要桌面版;云端请使用「导出到画布」。",
    "file-open": "从本地打开工程需要桌面版;云端请从画布素材或资源导入。",
    "local-path": "按路径读写需要桌面版;云端请使用导入 / 导出到画布。",
    "file-update": "软件更新由部署方管理,云端不提供更新入口。",
    "mcp": "MCP 宿主不进入云端产品边界;云端 AI 通过影策 Agent 接入。",
    "extension": "扩展插件需要桌面版,云端不可用。",
};

export function removedCapabilityMessage(capability: RemovedCapability): string {
    return MESSAGES[capability];
}

/** 在被剥离能力的容器里挂一条显式提示(带 data 标记,便于测试与样式定位)。 */
export function mountRemovedCapabilityNotice(container: HTMLElement, capability: RemovedCapability): HTMLElement {
    const notice = document.createElement("p");
    notice.className = "removed-capability-notice";
    notice.dataset.removedCapability = capability;
    notice.textContent = removedCapabilityMessage(capability);
    container.append(notice);
    return notice;
}
