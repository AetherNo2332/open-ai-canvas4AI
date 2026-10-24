import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({
    window: dom, document: dom.document, navigator: dom.navigator, localStorage: dom.localStorage,
    HTMLElement: dom.HTMLElement, HTMLAnchorElement: dom.HTMLAnchorElement, SVGElement: dom.SVGElement, Element: dom.Element, Node: dom.Node,
    Event: dom.Event, CustomEvent: dom.CustomEvent, ShadowRoot: dom.ShadowRoot,
    MutationObserver: dom.MutationObserver, ResizeObserver: dom.ResizeObserver,
    getComputedStyle: dom.getComputedStyle.bind(dom), IS_REACT_ACT_ENVIRONMENT: true,
})) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });

const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const { App, ConfigProvider } = await import("antd");
const { CanvasSyncStatus } = await import("../../src/pages/canvas/canvas-sync-status");
const { useSyncProgressStore } = await import("../../src/stores/use-sync-progress-store");
const container = document.createElement("div");
document.body.append(container);
const root = createRoot(container);
const settle = () => new Promise((resolve) => setTimeout(resolve, 40));
const button = (text: string) => Array.from(document.querySelectorAll("button")).find((item) => item.textContent?.trim() === text && !item.closest(".ant-popover-hidden"))!;
async function click(text: string) {
    expect(button(text)).toBeDefined();
    await act(async () => { button(text).click(); await settle(); });
}

afterAll(async () => {
    await act(async () => { root.unmount(); });
    dom.happyDOM.abort();
    dom.close();
});

test("conflict guidance opens automatically, supports resync, and asks before replacing local edits", async () => {
    let loaded = 0;
    let retried = 0;
    useSyncProgressStore.getState().setProjectProgress("canvas", { phase: "conflict", message: "本地修改与云端不同" });
    await act(async () => {
        root.render(<ConfigProvider theme={{ token: { motion: false } }}><App><CanvasSyncStatus projectId="canvas" onRetrySync={async () => { retried++; }} onLoadLatest={async () => { loaded++; throw new Error("备份未能完成，修改已保留"); }} /></App></ConfigProvider>);
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
