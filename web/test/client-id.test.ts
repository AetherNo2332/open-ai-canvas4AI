import { afterEach, describe, expect, test } from "bun:test";

import { createClientId, createUuid } from "../src/lib/client-id";

const originalCrypto = globalThis.crypto;

afterEach(() => {
    Object.defineProperty(globalThis, "crypto", { value: originalCrypto, configurable: true, writable: true });
});

function stubCrypto(value: unknown) {
    Object.defineProperty(globalThis, "crypto", { value, configurable: true, writable: true });
}

describe("createClientId", () => {
    test("生成不依赖 randomUUID 的唯一客户端标识", () => {
        const ids = Array.from({ length: 100 }, () => createClientId());

        expect(new Set(ids).size).toBe(ids.length);
        ids.forEach((id) => expect(id).toMatch(/^[A-Za-z0-9_-]{21}$/));
    });
});

describe("createUuid", () => {
    const uuidV4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

    test("优先使用原生 randomUUID", () => {
        expect(createUuid()).toMatch(uuidV4);
    });

    // 内网 HTTP 部署属于非安全上下文：crypto.randomUUID 不存在，但 getRandomValues 仍可用。
    test("缺少 randomUUID 时用 getRandomValues 拼出 v4", () => {
        const source = originalCrypto;
        stubCrypto({ getRandomValues: (array: Uint8Array) => source.getRandomValues(array) });

        const ids = Array.from({ length: 50 }, () => createUuid());
        expect(new Set(ids).size).toBe(ids.length);
        ids.forEach((id) => expect(id).toMatch(uuidV4));
    });

    test("完全没有 Web Crypto 时退回 nanoid", () => {
        stubCrypto(undefined);

        expect(createUuid()).toMatch(/^[A-Za-z0-9_-]{21}$/);
    });
});
