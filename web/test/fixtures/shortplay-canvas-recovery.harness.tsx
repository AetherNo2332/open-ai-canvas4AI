import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/shortplays/project" });
for (const key of ["window", "document", "navigator", "HTMLElement", "HTMLBodyElement", "HTMLHtmlElement", "Element", "Node", "SVGElement", "ShadowRoot", "Event", "MouseEvent", "MutationObserver", "ResizeObserver"] as const) {
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value: key === "window" ? dom : key === "document" ? dom.document : key === "navigator" ? dom.navigator : dom[key] });
}
Object.defineProperty(globalThis, "getComputedStyle", { configurable: true, value: dom.getComputedStyle.bind(dom) });
Object.defineProperty(globalThis, "IS_REACT_ACT_ENVIRONMENT", { configurable: true, value: true });
mock.module("../../src/pages/shortplays/assets-panel", () => ({ ShortplayAssetsPanel: () => null }));
mock.module("../../src/pages/shortplays/shortplay-models", () => ({ ShortplayModelPickers: () => null }));
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { MemoryRouter, useLocation } = await import("react-router");
const { QueryClient, QueryClientProvider, onlineManager } = await import("@tanstack/react-query");
const { App } = await import("antd");
const { apiClient } = await import("../../src/services/api/request");
const { ensureShortplayEpisodeCanvas } = await import("../../src/services/api/shortplay");
const { default: Detail } = await import("../../src/pages/shortplays/detail");
const { Routes, Route } = await import("react-router");
const originalAdapter = apiClient.defaults.adapter;
afterAll(() => {
    apiClient.defaults.adapter = originalAdapter;
    onlineManager.setOnline(true);
    dom.happyDOM.abort();
});

function Location() {
    return <output>{useLocation().pathname}</output>;
}

test("offline click settles with an error; retry online navigates without refreshing", async () => {
    let calls = 0;
    apiClient.defaults.adapter = async (config) => {
        if (config.method === "post") {
            calls++;
            if (calls === 1) throw { isAxiosError: true, code: "ERR_NETWORK", message: "测试网络不可用" };
            return { config, status: 200, statusText: "OK", headers: {}, data: { code: 0, data: { canvasId: "same-canvas", created: false } } };
        }
        return { config, status: 200, statusText: "OK", headers: {}, data: { code: 0, data: { name: "恢复测试", aspectRatio: "9:16", jobs: [], episodes: [{ id: "episode", number: 1, title: "第一集", wordCount: 1, assetIds: [] }] } } };
    };
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const flush = async () =>
        act(async () => {
            await new Promise((resolve) => setTimeout(resolve, 40));
        });
    try {
        await act(async () =>
            root.render(
                <QueryClientProvider client={client}>
                    <App>
                        <MemoryRouter initialEntries={["/shortplays/project"]}>
                            <Routes>
                                <Route path="/shortplays/:projectId" element={<Detail />} />
                                <Route path="/canvas/:id" element={<div>画布已打开</div>} />
                            </Routes>
                            <Location />
                        </MemoryRouter>
                    </App>
                </QueryClientProvider>,
            ),
        );
        await flush();
        const button = [...host.querySelectorAll("button")].find((item) => item.textContent?.includes("进精修画布"))!;
        expect(button).toBeDefined();
        onlineManager.setOnline(false);
        await act(async () => button.click());
        await flush();
        expect(calls).toBe(1);
        expect(document.body.textContent).toContain("测试网络不可用");
        // Ant Design keeps the loading class until its exit animation completes.
        for (let attempt = 0; button.className.includes("ant-btn-loading") && attempt < 20; attempt++) await flush();
        expect(button.className).not.toContain("ant-btn-loading");
        expect(host.querySelector("output")?.textContent).toBe("/shortplays/project");
        onlineManager.setOnline(true);
        await flush();
        expect(calls).toBe(1);
        expect(host.querySelector("output")?.textContent).toBe("/shortplays/project");
        await act(async () => button.click());
        await flush();
        expect(calls).toBe(2);
        expect(host.querySelector("output")?.textContent).toBe("/canvas/same-canvas");
    } finally {
        onlineManager.setOnline(true);
        await act(async () => root.unmount());
        client.clear();
        host.remove();
    }
});

test("canvas ensure request has a finite transport timeout", async () => {
    let timeout = 0;
    apiClient.defaults.adapter = async (config) => {
        timeout = config.timeout ?? 0;
        return { config, status: 200, statusText: "OK", headers: {}, data: { code: 0, data: { canvasId: "same-canvas", created: false } } };
    };
    await ensureShortplayEpisodeCanvas("project", "episode");
    expect(timeout).toBeGreaterThan(0);
    expect(timeout).toBeLessThanOrEqual(15000);
});
