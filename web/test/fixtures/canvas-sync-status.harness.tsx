import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    localStorage: dom.localStorage,
    HTMLElement: dom.HTMLElement,
    HTMLAnchorElement: dom.HTMLAnchorElement,
    SVGElement: dom.SVGElement,
    Element: dom.Element,
    Node: dom.Node,
    Event: dom.Event,
    CustomEvent: dom.CustomEvent,
    ShadowRoot: dom.ShadowRoot,
    MutationObserver: dom.MutationObserver,
    ResizeObserver: dom.ResizeObserver,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
}))
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });

const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const { App, ConfigProvider } = await import("antd");
const { CanvasSyncStatus } = await import("../../src/pages/canvas/canvas-sync-status");
const { useSyncProgressStore } = await import("../../src/stores/use-sync-progress-store");
const { useCanvasSessionStore } = await import("../../src/stores/canvas/use-canvas-session-store");
const { apiClient } = await import("../../src/services/api/request");
const { setActiveUserScope } = await import("../../src/lib/user-scope");
const { canvasEditLeaseController, cancelCanvasEditLeases } = await import("../../src/services/canvas-edit-leases");
const previousAdapter = apiClient.defaults.adapter;
const container = document.createElement("div");
document.body.append(container);
const root = createRoot(container);
const settle = () => new Promise((resolve) => setTimeout(resolve, 40));
const button = (text: string) => Array.from(document.querySelectorAll("button")).find((item) => item.textContent?.trim() === text && !item.closest(".ant-popover-hidden"))!;
async function click(text: string) {
    expect(button(text)).toBeDefined();
    await act(async () => {
        button(text).click();
        await settle();
    });
}

afterAll(async () => {
    await act(async () => {
        root.unmount();
    });
    dom.happyDOM.abort();
    cancelCanvasEditLeases();
    apiClient.defaults.adapter = previousAdapter;
    dom.close();
});

test("lease activity shows readable names, window ownership and working node locations", async () => {
    let sessionId = "";
    const fullId = "image-reference-node-00000000000000000001";
    const located: string[] = [];
    setActiveUserScope("ui");
    useCanvasSessionStore.setState({ scope: "ui" });
    apiClient.defaults.adapter = async (config) => {
        let data: unknown;
        if (config.url?.endsWith("/acquire")) {
            const proof = JSON.parse(config.data);
            sessionId = proof.sessionId;
            data = { canvasId: "lease-canvas", ...proof, expiresAt: new Date(Date.now() + 30000).toISOString() };
        } else if (config.url?.endsWith("/sync-status")) {
            data = {
                revision: 1,
                documentEpoch: "epoch",
                eventSeq: 0,
                activeAgents: [],
                leases: [
                    { canvasId: "lease-canvas", nodeId: fullId, sessionId, nodeTitle: "镜头 A · 关键帧（已归档）", nodeType: "image", nodeTypeLabel: "图片", nodeExists: true, expiresAt: new Date(Date.now() + 30000).toISOString() },
                    { canvasId: "lease-canvas", nodeId: "hero", sessionId: "other-window", nodeTitle: "角色甲", nodeType: "character", nodeTypeLabel: "角色卡", nodeExists: true, expiresAt: new Date(Date.now() + 30000).toISOString() },
                    { canvasId: "lease-canvas", nodeId: "removed", sessionId: "other-window", nodeTitle: "节点已删除（removed）", nodeType: "", nodeTypeLabel: "节点", nodeExists: false, expiresAt: new Date(Date.now() + 30000).toISOString() },
                ],
            };
        } else throw new Error("unexpected lease request");
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    const controller = canvasEditLeaseController("ui", "lease-canvas");
    await controller.begin(fullId);
    await controller.refreshStatus();
    useSyncProgressStore.getState().setProjectProgress("lease-canvas", { phase: "error", reason: "invalid_reference" });
    await act(async () => {
        root.render(
            <ConfigProvider theme={{ token: { motion: false } }}>
                <App>
                    <CanvasSyncStatus projectId="lease-canvas" onLocateNode={(id) => located.push(id)} onLoadLatest={async () => {}} />
                </App>
            </ConfigProvider>,
        );
        await settle();
    });
    expect(document.body.textContent).toContain("镜头 A · 关键帧（已归档）");
    expect(document.body.textContent).toContain("角色卡");
    expect(document.body.textContent).toContain("当前窗口正在编辑");
    expect(document.body.textContent).toContain("其他窗口正在编辑");
    expect(document.body.textContent).toContain("编辑时自动续期");
    const locate = Array.from(document.querySelectorAll("button")).filter((node) => node.textContent?.trim() === "定位节点");
    expect(locate.length).toBe(2);
    await act(async () => {
        locate[0].click();
        await settle();
    });
    expect(located).toEqual([fullId]);
});

test("conflict guidance opens automatically, supports resync, and asks before replacing local edits", async () => {
    let loaded = 0;
    let retried = 0;
    useSyncProgressStore.getState().setProjectProgress("canvas", { phase: "conflict", message: "本地修改与云端不同" });
    await act(async () => {
        root.render(
            <ConfigProvider theme={{ token: { motion: false } }}>
                <App>
                    <CanvasSyncStatus
                        projectId="canvas"
                        onRetrySync={async () => {
                            retried++;
                        }}
                        onLoadLatest={async () => {
                            loaded++;
                            throw new Error("备份未能完成，修改已保留");
                        }}
                    />
                </App>
            </ConfigProvider>,
        );
        await settle();
    });
    expect(document.querySelector("ol")).not.toBeNull();
    expect(button("本地草稿")).toBeDefined();
    await click("重新同步");
    expect(retried).toBe(1);
    expect(loaded).toBe(0);
    await click("备份草稿并加载云端");
    expect(loaded).toBe(0);
    await click("暂不处理");
    expect(loaded).toBe(0);
    await click("备份草稿并加载云端");
    await click("备份并加载");
    expect(loaded).toBe(1);
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("备份未能完成");
    expect(useSyncProgressStore.getState().syncingProjects.canvas.phase).toBe("conflict");
});

test("a rejected save shows its reason and retries without asking to adopt the cloud", async () => {
    let retried = 0;
    let loaded = 0;
    useSyncProgressStore.getState().setProjectProgress("rejected", { phase: "error", reason: "invalid_reference", message: "此角色尚未配置参考形象" });
    await act(async () => {
        root.render(
            <ConfigProvider theme={{ token: { motion: false } }}>
                <App>
                    <CanvasSyncStatus
                        projectId="rejected"
                        onRetrySync={async () => {
                            retried++;
                        }}
                        onLoadLatest={async () => {
                            loaded++;
                        }}
                    />
                </App>
            </ConfigProvider>,
        );
        await settle();
    });
    expect(document.body.textContent).toContain("此角色尚未配置参考形象");
    expect(document.body.textContent).toContain("修改未保存");
    expect(document.querySelector("ol")).toBeNull();
    await click("重试保存");
    expect(retried).toBe(1);
    expect(loaded).toBe(0);
});
