import { createHostBridge, type BridgeEndpoint, type HostBridge } from "./bridge";

/**
 * 应用侧 host bridge 的运行时持有者:入口(main.ts)安装,其余模块按需取用。
 * 未安装时(纯浏览器直开)返回 undefined,调用方走"能力不可用"的显式分支。
 */
let installed: HostBridge | undefined;

export function installHostBridge(bridge: HostBridge): void {
    installed = bridge;
}

export function hostBridge(): HostBridge | undefined {
    return installed;
}

export function hostBridgeReady(): boolean {
    return Boolean(installed);
}

/**
 * 作为被宿主嵌入的应用(iframe)安装默认 bridge:向父窗口投递、监听本窗口消息,
 * 并校验来源与协议版本(见 contract.ts)。未嵌入(父窗口即自身)时消息回到本窗口,
 * 不影响独立运行。
 */
export function installEmbeddedHostBridge(): void {
    if (typeof window === "undefined") return;
    const origin = window.location.origin;
    const endpoint: BridgeEndpoint = {
        postMessage: (message, targetOrigin) => window.parent.postMessage(message, targetOrigin ?? origin),
        addEventListener: (type, listener) => window.addEventListener(type, listener as unknown as EventListener),
        removeEventListener: (type, listener) => window.removeEventListener(type, listener as unknown as EventListener),
    };
    installHostBridge(createHostBridge(endpoint, { origin, targetOrigin: origin }));
}
