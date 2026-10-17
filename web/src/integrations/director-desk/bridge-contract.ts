/**
 * host bridge 契约 v1 —— web 侧镜像。
 *
 * 契约单源在 `director-desk/src/host/contract.ts`；本文件是它的镜像，供工作台侧
 * (bridge client / 宿主实现)引用。两边的 `PROTOCOL_VERSION` 与消息类型集合
 * 必须一致，由 `web/test/director-desk-bridge-contract.test.ts` 的漂移守卫检查。
 */
export const PROTOCOL_VERSION = 1;

/** 导演台 → 宿主 */
export const APP_TO_HOST_TYPES = [
    "desk.ready",
    "desk.scene.changed",
    "desk.save.request",
    "desk.asset.request",
    "desk.export.request",
    "desk.ai.send",
    "desk.ai.cancel",
    "desk.ai.approval",
] as const;

/** 宿主 → 导演台 */
export const HOST_TO_APP_TYPES = [
    "host.appearance.changed",
    "host.scene.load",
    "host.save.result",
    "host.asset.result",
    "host.export.result",
    "host.ai.event",
    "host.ai.state",
] as const;

/** 请求类型 → 应答类型(带 id 的请求必须收到同名 id 的应答) */
export const REQUEST_RESPONSE: Record<string, string> = {
    "desk.save.request": "host.save.result",
    "desk.asset.request": "host.asset.result",
    "desk.export.request": "host.export.result",
};

export const REQUEST_TIMEOUT_MS = 15000;

export type AppToHostType = (typeof APP_TO_HOST_TYPES)[number];
export type HostToAppType = (typeof HOST_TO_APP_TYPES)[number];
export type BridgeMessageType = AppToHostType | HostToAppType;

export interface BridgeMessage {
    protocol: number;
    type: string;
    id?: string;
    payload?: unknown;
    error?: string;
}

/** 只接受本协议版本、且带非空 type 的消息；其余一律忽略。 */
export function isBridgeMessage(value: unknown): value is BridgeMessage {
    if (!value || typeof value !== "object") return false;
    const message = value as Partial<BridgeMessage>;
    return message.protocol === PROTOCOL_VERSION && typeof message.type === "string" && message.type.length > 0;
}
