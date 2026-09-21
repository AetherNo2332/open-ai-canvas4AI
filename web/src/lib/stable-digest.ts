// Web Crypto 的 subtle 接口只在安全上下文（HTTPS 或 localhost）可用。内网 HTTP 部署
// （例如 http://192.168.x.x:3001）下 crypto.subtle 为 undefined，直接调用会抛
// "Cannot read properties of undefined"。
//
// 这里给"本地缓存键 / 幂等键 / 客户端操作 id"这类**不参与服务端校验**的用途提供兜底摘要：
// 有 Web Crypto 时仍是 SHA-256（与既有行为一致），没有时退回确定性的 64 位非加密摘要。
// 兜底值必须跨设备稳定，否则同一个输入会在不同机器上得到不同的 id。

function fallbackDigestHex(bytes: Uint8Array): string {
    // 两个 32 位累加器拼成 64 位：只用整数运算，避免大文件下 BigInt 逐字节的开销。
    let first = 0x811c9dc5;
    let second = 0x01000193;
    for (let index = 0; index < bytes.length; index += 1) {
        const byte = bytes[index];
        first = Math.imul(first ^ byte, 16777619) >>> 0;
        second = Math.imul(second + byte + index, 2246822519) >>> 0;
    }
    return first.toString(16).padStart(8, "0") + second.toString(16).padStart(8, "0");
}

export function stableDigestHexSync(input: string | ArrayBuffer | ArrayBufferView): string {
    const bytes = typeof input === "string" ? new TextEncoder().encode(input) : input instanceof ArrayBuffer ? new Uint8Array(input) : new Uint8Array(input.buffer, input.byteOffset, input.byteLength);
    return fallbackDigestHex(bytes);
}

export async function stableDigestHex(input: string | ArrayBuffer | ArrayBufferView): Promise<string> {
    const subtle = globalThis.crypto?.subtle;
    if (!subtle) return stableDigestHexSync(input);
    const data = typeof input === "string" ? new TextEncoder().encode(input) : input;
    const digest = await subtle.digest("SHA-256", data as BufferSource);
    return Array.from(new Uint8Array(digest), (value) => value.toString(16).padStart(2, "0")).join("");
}
