import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import type { BridgeEndpoint } from "@desk/host/bridge";
import { DirectorDeskAppFrame } from "../src/components/canvas/previs/director-desk-app-frame";
import { createDirectorDeskHostBridge } from "../src/integrations/director-desk/app-bridge-client";
import type { PrevisSaveController } from "../src/components/canvas/previs/use-previs-save-coordinator";
import { idlePrevisSaveProgress } from "../src/lib/canvas/previs/previs-save-wiring";
import type { PrevisScene } from "../src/types/previs";

// T5:把原版 DirectorDesk 应用以 iframe 挂进预演台,宿主能力只经 host bridge 交换。
const repoRoot = resolve(import.meta.dir, "../..");
const ORIGIN = "http://localhost:3040";

class FakeWin {
    private listeners = new Set<(event: { data: unknown; origin: string }) => void>();
    add(listener: (event: { data: unknown; origin: string }) => void) {
        this.listeners.add(listener);
    }
    remove(listener: (event: { data: unknown; origin: string }) => void) {
        this.listeners.delete(listener);
    }
    receive(data: unknown, origin = ORIGIN) {
        for (const listener of [...this.listeners]) listener({ data: structuredClone(data), origin });
    }
    listenerCount() {
        return this.listeners.size;
    }
}

function linked() {
    const host = new FakeWin();
    const app = new FakeWin();
    const received: unknown[] = [];
    app.add((event) => received.push(event.data));
    const endpoint: BridgeEndpoint = {
        postMessage: (message) => app.receive(message),
        addEventListener: (_type, listener) => host.add(listener as (event: { data: unknown; origin: string }) => void),
        removeEventListener: (_type, listener) => host.remove(listener as (event: { data: unknown; origin: string }) => void),
    };
    return { host, app, received, endpoint };
}

// 静态渲染只需要 progress 可读;其余方法给出 inert 实现。
const stubSaveController: PrevisSaveController = {
    progress: idlePrevisSaveProgress,
    currentSnapshot: () => idlePrevisSaveProgress,
    onSaveStatus: () => () => undefined,
    commitScene: () => true,
    retry: async () => true,
    restoreCandidate: () => null,
    restoreDraft: () => false,
    discardDraft: () => false,
    prepareClose: async () => "close",
    handlePageHide: async () => true,
};
const stubScene = { id: "scene-1", title: "测试场景" } as PrevisScene;

describe("DirectorDeskAppFrame(原版应用挂载)", () => {
    const html = renderToStaticMarkup(<DirectorDeskAppFrame sceneId="scene-1" scene={stubScene} saveController={stubSaveController} onSceneChange={() => undefined} onSaveRequest={async () => ({ ok: true })} />);

    test("宿主外壳提供关闭入口与保存状态", () => {
        expect(html).toContain('aria-label="关闭预演台"');
        expect(html).toContain("director-desk-app-frame-save-status");
        expect(html).toContain("已保存");
    });

    test("以 /desk/ 同源加载原版应用", () => {
        expect(html).toContain('src="/desk/"');
        expect(html).toContain('title="导演台"');
    });

    test("iframe 沙箱只放行应用所需能力", () => {
        const sandbox = html.match(/sandbox="([^"]*)"/)?.[1] ?? "";
        expect(sandbox).toContain("allow-scripts");
        expect(sandbox).toContain("allow-same-origin");
        expect(sandbox).not.toContain("allow-top-navigation");
    });

    test("加载中给出状态标记", () => {
        expect(html).toContain('data-desk-status="loading"');
    });

    test("挂载点与工作台来源标记齐全(便于排障)", () => {
        expect(html).toContain("director-desk-app-frame");
    });
});

describe("host bridge 客户端(宿主侧)", () => {
    test("应用就绪时下发当前场景文档", () => {
        const { host, app, received, endpoint } = linked();
        const bridge = createDirectorDeskHostBridge({
            endpoint,
            origin: ORIGIN,
            readDocument: () => ({ projectId: "p1", scenes: [] }),
            commitDocument: () => undefined,
            requestSave: async () => ({ ok: true }),
        });
        host.receive({ protocol: 1, type: "desk.ready" });
        const load = received.find((m) => (m as { type?: string }).type === "host.scene.load") as { payload?: { document?: unknown } } | undefined;
        expect(load?.payload?.document).toEqual({ projectId: "p1", scenes: [] });
        bridge.dispose();
    });

    test("应用文档变更经宿主提交(走同一条协调器路径)", () => {
        const { host, app, endpoint } = linked();
        const committed: unknown[] = [];
        const bridge = createDirectorDeskHostBridge({
            endpoint,
            origin: ORIGIN,
            readDocument: () => null,
            commitDocument: (document) => void committed.push(document),
            requestSave: async () => ({ ok: true }),
        });
        host.receive({ protocol: 1, type: "desk.scene.changed", payload: { projectId: "p1", revision: "r2" } });
        expect(committed).toEqual([{ projectId: "p1", revision: "r2" }]);
        bridge.dispose();
    });

    test("应用保存请求按映射应答", async () => {
        const { host, app, received, endpoint } = linked();
        let saves = 0;
        const bridge = createDirectorDeskHostBridge({
            endpoint,
            origin: ORIGIN,
            readDocument: () => null,
            commitDocument: () => undefined,
            requestSave: async () => {
                saves += 1;
                return { ok: true, message: "已保存" };
            },
        });
        host.receive({ protocol: 1, type: "desk.save.request", id: "req-1", payload: { saveAs: false } });
        await Bun.sleep(10);
        const result = received.find((m) => (m as { type?: string }).type === "host.save.result") as { id?: string; payload?: unknown } | undefined;
        expect(saves).toBe(1);
        expect(result?.id).toBe("req-1");
        expect(result?.payload).toEqual({ ok: true, message: "已保存" });
        bridge.dispose();
    });

    test("来源不符的消息不触发宿主行为", () => {
        const { host, app, endpoint } = linked();
        const committed: unknown[] = [];
        const bridge = createDirectorDeskHostBridge({
            endpoint,
            origin: ORIGIN,
            readDocument: () => null,
            commitDocument: (document) => void committed.push(document),
            requestSave: async () => ({ ok: true }),
        });
        host.receive({ protocol: 1, type: "desk.scene.changed", payload: { projectId: "evil" } }, "http://evil.example");
        expect(committed).toEqual([]);
        bridge.dispose();
    });

    test("释放后不再派发", () => {
        const { host, app, received, endpoint } = linked();
        const bridge = createDirectorDeskHostBridge({
            endpoint,
            origin: ORIGIN,
            readDocument: () => ({ projectId: "p1" }),
            commitDocument: () => undefined,
            requestSave: async () => ({ ok: true }),
        });
        bridge.dispose();
        expect(host.listenerCount()).toBe(0);
        const before = received.length;
        host.receive({ protocol: 1, type: "desk.ready" });
        expect(received.length).toBe(before);
    });
});
