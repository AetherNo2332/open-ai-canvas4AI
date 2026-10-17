import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { createHostBridge } from "../../director-desk/src/host/bridge";
import { PROTOCOL_VERSION } from "../../director-desk/src/host/contract";

// 宿主 ⇄ 导演台 host bridge 契约 v1(T3)。
// 契约单源在 director-desk/src/host/contract.ts,web 侧镜像 bridge-contract.ts,
// 本文件既守两边的漂移,也守 bridge 的收发/超时/忽略/释放行为。
const repoRoot = resolve(import.meta.dir, "../..");
const appContract = readFileSync(resolve(repoRoot, "director-desk/src/host/contract.ts"), "utf8");
const webContract = readFileSync(resolve(repoRoot, "web/src/integrations/director-desk/bridge-contract.ts"), "utf8");

function declaredTypes(source: string): string[] {
    return [...source.matchAll(/"((?:desk|host)\.[a-z.]+)"/g)].map((match) => match[1]).sort();
}
function declaredVersion(source: string): string | undefined {
    return source.match(/PROTOCOL_VERSION\s*=\s*(\d+)/)?.[1];
}

class Endpoint {
    peer!: Endpoint;
    peerOrigin = "http://localhost:3040";
    origin = "http://localhost:3040";
    private listeners = new Set<(event: { data: unknown; origin: string }) => void>();
    postMessage(data: unknown) {
        for (const listener of [...this.peer.listeners]) listener({ data: structuredClone(data), origin: this.peerOrigin });
    }
    addEventListener(_type: "message", listener: (event: { data: unknown; origin: string }) => void) {
        this.listeners.add(listener);
    }
    removeEventListener(_type: "message", listener: (event: { data: unknown; origin: string }) => void) {
        this.listeners.delete(listener);
    }
    listenerCount() {
        return this.listeners.size;
    }
}

function linkedPair(origin = "http://localhost:3040") {
    const app = new Endpoint();
    const host = new Endpoint();
    app.peer = host;
    host.peer = app;
    app.origin = host.origin = origin;
    app.peerOrigin = host.peerOrigin = origin;
    return { app, host };
}

describe("host bridge 契约漂移守卫", () => {
    test("两边 PROTOCOL_VERSION 一致且等于 1", () => {
        expect(declaredVersion(appContract)).toBe("1");
        expect(declaredVersion(webContract)).toBe("1");
        expect(PROTOCOL_VERSION).toBe(1);
    });

    test("两边消息类型集合逐一相同", () => {
        const appTypes = declaredTypes(appContract);
        const webTypes = declaredTypes(webContract);
        expect(appTypes.length).toBeGreaterThan(8);
        expect(webTypes).toEqual(appTypes);
    });
});

describe("host bridge 行为", () => {
    test("app 发起请求并由 host 应答", async () => {
        const { app, host } = linkedPair();
        const appBridge = createHostBridge(app, { origin: "http://localhost:3040" });
        const hostBridge = createHostBridge(host, { origin: "http://localhost:3040" });
        hostBridge.on("desk.save.request", (payload) => ({ saved: true, echoed: payload }));
        const result = await appBridge.request("desk.save.request", { revision: "r1" });
        expect(result).toEqual({ saved: true, echoed: { revision: "r1" } });
        appBridge.dispose();
        hostBridge.dispose();
    });

    test("未知消息类型被忽略且不抛异常", async () => {
        const { app, host } = linkedPair();
        const appBridge = createHostBridge(app, { origin: "http://localhost:3040" });
        let handled = 0;
        appBridge.on("host.scene.load", () => { handled += 1; });
        host.postMessage({ protocol: 1, type: "host.unknown.thing", payload: {} });
        await Bun.sleep(5);
        expect(handled).toBe(0);
        appBridge.dispose();
    });

    test("协议版本不匹配的消息被忽略", async () => {
        const { app, host } = linkedPair();
        const appBridge = createHostBridge(app, { origin: "http://localhost:3040" });
        let handled = 0;
        appBridge.on("host.scene.load", () => { handled += 1; });
        host.postMessage({ protocol: 999, type: "host.scene.load", payload: {} });
        await Bun.sleep(5);
        expect(handled).toBe(0);
        appBridge.dispose();
    });

    test("来源不匹配的消息被忽略", async () => {
        const { app, host } = linkedPair("http://evil.example");
        const appBridge = createHostBridge(app, { origin: "http://localhost:3040" });
        let handled = 0;
        appBridge.on("host.scene.load", () => { handled += 1; });
        host.postMessage({ protocol: 1, type: "host.scene.load", payload: {} });
        await Bun.sleep(5);
        expect(handled).toBe(0);
        appBridge.dispose();
    });

    test("请求超时以错误拒绝", async () => {
        const { app } = linkedPair();
        const appBridge = createHostBridge(app, { origin: "http://localhost:3040" });
        await expect(appBridge.request("desk.asset.request", {}, 20)).rejects.toThrow();
        appBridge.dispose();
    });

    test("释放后不再派发且请求被拒", async () => {
        const { app, host } = linkedPair();
        const appBridge = createHostBridge(app, { origin: "http://localhost:3040" });
        let handled = 0;
        appBridge.on("host.scene.load", () => { handled += 1; });
        appBridge.dispose();
        expect(app.listenerCount()).toBe(0);
        host.postMessage({ protocol: 1, type: "host.scene.load", payload: {} });
        await expect(appBridge.request("desk.asset.request", {}, 20)).rejects.toThrow();
        await Bun.sleep(5);
        expect(handled).toBe(0);
    });
});
