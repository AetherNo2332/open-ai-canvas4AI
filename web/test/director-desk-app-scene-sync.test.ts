import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { BridgeEndpoint } from "@desk/host/bridge";
import { createHostBridge } from "@desk/host/bridge";
import { createSceneSync } from "@desk/host/scene-sync";
import { readSceneDocument } from "@desk/scenes/sequence-project";
import { createDirectorDeskHostBridge } from "@/integrations/director-desk/app-bridge-client";
import { directorDocumentToPrevisScene, previsSceneToDirectorDocument } from "@/integrations/director-desk/previs-adapter";
import { createPrevisScene } from "@/lib/canvas/previs/previs-scene";

/**
 * T6 应用侧:导演台在 iframe 内启动后必须
 * 1) 发 desk.ready;2) 处理 host.scene.load 并用自己的 readSceneDocument 载入;
 * 3) 编辑后节流回传 desk.scene.changed(宿主下发的同一份文档不回声)。
 * 两端用真实 bridge 实现对接,只替换窗口端点。
 */

const ORIGIN = "http://localhost:3040";

type Listener = (event: { data: unknown; origin: string }) => void;
class FakeWindow {
    listeners = new Set<Listener>();
    peer: FakeWindow | null = null;
    endpoint(): BridgeEndpoint {
        return {
            postMessage: (message) => this.peer?.deliver(message),
            addEventListener: (_type, listener) => void this.listeners.add(listener as Listener),
            removeEventListener: (_type, listener) => void this.listeners.delete(listener as Listener),
        };
    }
    deliver(data: unknown) {
        for (const listener of [...this.listeners]) listener({ data: structuredClone(data), origin: ORIGIN });
    }
}

function pair() {
    const host = new FakeWindow();
    const app = new FakeWindow();
    host.peer = app;
    app.peer = host;
    return { host, app };
}

/** 手控定时器:节流回传可确定性地推进。 */
function manualSchedule() {
    const queue: (() => void)[] = [];
    return {
        schedule: (callback: () => void) => {
            queue.push(callback);
            return () => {
                const index = queue.indexOf(callback);
                if (index >= 0) queue.splice(index, 1);
            };
        },
        flush: () => {
            for (const callback of queue.splice(0)) callback();
        },
        get size() {
            return queue.length;
        },
    };
}

const tick = () => new Promise((done) => setTimeout(done, 0));

describe("导演台应用侧场景同步", () => {
    test("01 启动即发 desk.ready,宿主下发的场景经 readSceneDocument 载入应用", async () => {
        const { host, app } = pair();
        const scene = createPrevisScene("往返");
        let ready = 0;
        const hostBridge = createDirectorDeskHostBridge({
            endpoint: host.endpoint(),
            origin: ORIGIN,
            readDocument: () => previsSceneToDirectorDocument(scene),
            commitDocument: () => undefined,
            requestSave: async () => ({ ok: true }),
            onReady: () => void ready++,
        });
        const applied: unknown[] = [];
        const timers = manualSchedule();
        const sync = createSceneSync({
            bridge: createHostBridge(app.endpoint(), { origin: ORIGIN, targetOrigin: ORIGIN }),
            readDocument: () => null,
            applyDocument: (document) => void applied.push(document),
            schedule: timers.schedule,
        });
        sync.start();
        await tick();
        expect(ready).toBe(1);
        expect(applied).toHaveLength(1);
        const loaded = applied[0] as ReturnType<typeof readSceneDocument>;
        expect(loaded.version).toBe(3);
        expect(directorDocumentToPrevisScene(loaded, scene)).toEqual(scene);
        sync.dispose();
        hostBridge.dispose();
    });

    test("02 编辑后节流回传 desk.scene.changed,多次编辑合并为一次", async () => {
        const { host, app } = pair();
        const committed: unknown[] = [];
        const hostBridge = createDirectorDeskHostBridge({
            endpoint: host.endpoint(),
            origin: ORIGIN,
            readDocument: () => previsSceneToDirectorDocument(createPrevisScene("节流")),
            commitDocument: (document) => void committed.push(document),
            requestSave: async () => ({ ok: true }),
        });
        const timers = manualSchedule();
        let current = { format: "director-desk", version: 3, name: "a" };
        const sync = createSceneSync({
            bridge: createHostBridge(app.endpoint(), { origin: ORIGIN, targetOrigin: ORIGIN }),
            readDocument: () => current,
            applyDocument: () => undefined,
            schedule: timers.schedule,
        });
        sync.start();
        await tick();
        current = { ...current, name: "a2" };
        sync.notifyChanged();
        current = { ...current, name: "b" };
        sync.notifyChanged();
        expect(timers.size).toBe(1);
        timers.flush();
        await tick();
        expect(committed).toEqual([{ format: "director-desk", version: 3, name: "b" }]);
        sync.dispose();
        hostBridge.dispose();
    });

    test("03 宿主下发引起的应用刷新不回声给宿主", async () => {
        const { host, app } = pair();
        const committed: unknown[] = [];
        const scene = createPrevisScene("回声");
        const hostBridge = createDirectorDeskHostBridge({
            endpoint: host.endpoint(),
            origin: ORIGIN,
            readDocument: () => previsSceneToDirectorDocument(scene),
            commitDocument: (document) => void committed.push(document),
            requestSave: async () => ({ ok: true }),
        });
        const timers = manualSchedule();
        let current: unknown = null;
        const sync = createSceneSync({
            bridge: createHostBridge(app.endpoint(), { origin: ORIGIN, targetOrigin: ORIGIN }),
            readDocument: () => current,
            applyDocument: (document) => {
                current = document;
                // 应用载入后照常走 changed() → notifyChanged。
                sync.notifyChanged();
            },
            schedule: timers.schedule,
        });
        sync.start();
        await tick();
        timers.flush();
        await tick();
        expect(committed).toEqual([]);
        sync.dispose();
        hostBridge.dispose();
    });

    test("04 编辑进行中(readDocument 返回 null)不回传,结束后再回传", async () => {
        const { host, app } = pair();
        const committed: unknown[] = [];
        const hostBridge = createDirectorDeskHostBridge({
            endpoint: host.endpoint(),
            origin: ORIGIN,
            readDocument: () => previsSceneToDirectorDocument(createPrevisScene("编辑中")),
            commitDocument: (document) => void committed.push(document),
            requestSave: async () => ({ ok: true }),
        });
        const timers = manualSchedule();
        let current: unknown = null;
        const sync = createSceneSync({
            bridge: createHostBridge(app.endpoint(), { origin: ORIGIN, targetOrigin: ORIGIN }),
            readDocument: () => current,
            applyDocument: () => undefined,
            schedule: timers.schedule,
        });
        sync.start();
        await tick();
        sync.notifyChanged();
        timers.flush();
        await tick();
        expect(committed).toEqual([]);
        current = { format: "director-desk", version: 3, name: "done" };
        sync.notifyChanged();
        timers.flush();
        await tick();
        expect(committed).toHaveLength(1);
        sync.dispose();
        hostBridge.dispose();
    });

    test("04b 宿主场景载入前不回传(不把应用示例场景写回宿主)", async () => {
        const timers = manualSchedule();
        const sent: string[] = [];
        const sync = createSceneSync({
            bridge: { notify: (type) => void sent.push(type), request: async () => undefined, on: () => () => undefined, dispose: () => undefined },
            readDocument: () => ({ format: "director-desk", version: 3, name: "示例" }),
            applyDocument: () => undefined,
            schedule: timers.schedule,
        });
        sync.start();
        sync.notifyChanged();
        timers.flush();
        expect(sent).toEqual(["desk.ready"]);
        sync.dispose();
    });

    test("05 无效的下发文档不抛出、不调用 applyDocument,并上报错误", async () => {
        const { host, app } = pair();
        const hostBridge = createDirectorDeskHostBridge({
            endpoint: host.endpoint(),
            origin: ORIGIN,
            readDocument: () => ({ format: "not-director" }),
            commitDocument: () => undefined,
            requestSave: async () => ({ ok: true }),
        });
        const applied: unknown[] = [];
        const errors: unknown[] = [];
        const sync = createSceneSync({
            bridge: createHostBridge(app.endpoint(), { origin: ORIGIN, targetOrigin: ORIGIN }),
            readDocument: () => null,
            applyDocument: (document) => void applied.push(document),
            onLoadError: (error) => void errors.push(error),
            schedule: manualSchedule().schedule,
        });
        sync.start();
        await tick();
        expect(applied).toEqual([]);
        expect(errors).toHaveLength(1);
        sync.dispose();
        hostBridge.dispose();
    });

    test("06 dispose 后取消待发回传", () => {
        const timers = manualSchedule();
        const sent: string[] = [];
        const sync = createSceneSync({
            bridge: { notify: (type) => void sent.push(type), request: async () => undefined, on: () => () => undefined, dispose: () => undefined },
            readDocument: () => ({ format: "director-desk", version: 3 }),
            applyDocument: () => undefined,
            schedule: timers.schedule,
        });
        sync.start();
        sync.notifyChanged();
        sync.dispose();
        timers.flush();
        expect(sent).toEqual(["desk.ready"]);
    });

    test("07 main.ts 接线:启动场景同步并在 changed() 中回传", () => {
        const main = readFileSync(resolve(import.meta.dir, "../../director-desk/src/main.ts"), "utf8");
        expect(main).toContain("createSceneSync(");
        expect(main).toContain("applyDocument(document, history.context");
        // 嵌入时宿主是唯一事实源:不从本地恢复会话覆盖宿主场景。
        expect(main).toContain("sceneSync ? Promise.resolve(undefined) : recoverSession()");
        expect(main).toMatch(/function changed\(rebuild = true\) \{[^\n]*sceneSync\?\.notifyChanged\(\)/u);
    });
});
