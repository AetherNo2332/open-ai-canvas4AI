import { nanoid } from "nanoid";

export function createClientId() {
    // randomUUID 在 HTTP 内网地址下不可用；nanoid 依赖仍可用的 getRandomValues。
    return nanoid();
}

// createUuid 产出 UUID v4 形态的标识：优先用原生 randomUUID，其次用 getRandomValues 自行拼装。
// crypto.randomUUID 只在安全上下文存在，内网 HTTP 部署（如 http://192.168.x.x:3001）下为
// undefined；getRandomValues 在非安全上下文仍可用，所以不需要降级到弱随机。
export function createUuid(): string {
    const api = globalThis.crypto;
    if (typeof api?.randomUUID === "function") return api.randomUUID();
    if (typeof api?.getRandomValues === "function") {
        const bytes = api.getRandomValues(new Uint8Array(16));
        bytes[6] = (bytes[6] & 0x0f) | 0x40;
        bytes[8] = (bytes[8] & 0x3f) | 0x80;
        const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
        return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
    }
    return nanoid();
}
