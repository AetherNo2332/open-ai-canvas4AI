import { afterEach, describe, expect, test } from "bun:test";

import { stableDigestHex, stableDigestHexSync } from "../src/lib/stable-digest";

const originalCrypto = globalThis.crypto;

afterEach(() => {
    Object.defineProperty(globalThis, "crypto", { value: originalCrypto, configurable: true, writable: true });
});

function stubCrypto(value: unknown) {
    Object.defineProperty(globalThis, "crypto", { value, configurable: true, writable: true });
}

describe("stableDigestHex", () => {
    test("有 Web Crypto 时与 SHA-256 一致", async () => {
        expect(await stableDigestHex("abc")).toBe("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    });

    // 内网 HTTP 部署（非安全上下文）下 subtle 不存在：兜底摘要必须确定且跨输入区分。
    test("缺少 subtle 时退回确定性摘要", async () => {
        stubCrypto({ getRandomValues: (array: Uint8Array) => originalCrypto.getRandomValues(array) });

        const first = await stableDigestHex("inline-media-a");
        const second = await stableDigestHex("inline-media-a");
        const other = await stableDigestHex("inline-media-b");

        expect(first).toMatch(/^[0-9a-f]{16}$/);
        expect(first).toBe(second);
        expect(first).not.toBe(other);
        expect(first).toBe(stableDigestHexSync("inline-media-a"));
    });

    test("二进制输入在两种路径下都可用", async () => {
        const bytes = new Uint8Array([1, 2, 3, 4, 5]);
        const secure = await stableDigestHex(bytes);
        stubCrypto({ getRandomValues: (array: Uint8Array) => originalCrypto.getRandomValues(array) });
        const fallback = await stableDigestHex(bytes);

        expect(secure).not.toBe(fallback);
        expect(fallback).toBe(stableDigestHexSync(bytes));
    });
});
