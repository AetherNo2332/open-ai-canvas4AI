import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
// 测试共用一个进程：用完后还原全局对象，避免影响其他测试文件。
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, HTMLElement: dom.HTMLElement, Element: dom.Element, Node: dom.Node, IS_REACT_ACT_ENVIRONMENT: true })) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
afterAll(() => {
    for (const [key, descriptor] of originals) {
        if (descriptor) Object.defineProperty(globalThis, key, descriptor);
        else delete (globalThis as Record<string, unknown>)[key];
    }
    dom.happyDOM.abort();
});

const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const { GlobalAgentPanelBoundary } = await import("@/layouts/global-agent-panel-boundary");
const { requestAgentPrefill, useGlobalAgentStore } = await import("@/stores/use-global-agent-store");

function FailedChunk(): never {
    throw new Error("Failed to fetch dynamically imported module");
}

test("a failed Agent chunk renders nothing next to the layout and settles pending prefills", async () => {
    useGlobalAgentStore.setState({ prefillRequest: null, panelUnavailable: false });
    const pending = requestAgentPrefill({ canvasId: "canvas", userScope: "user", text: "hi", autoSubmit: true });
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const error = console.error;
    console.error = () => {};
    try {
        await act(async () => {
            root.render(
                <>
                    <main>workspace</main>
                    <GlobalAgentPanelBoundary>
                        <FailedChunk />
                    </GlobalAgentPanelBoundary>
                </>,
            );
        });
    } finally {
        console.error = error;
    }
    expect(host.textContent).toBe("workspace");
    expect(await pending).toBe(false);
    expect(await requestAgentPrefill({ canvasId: "canvas", userScope: "user", text: "later" })).toBe(false);
    expect(useGlobalAgentStore.getState().prefillRequest).toBeNull();
    await act(async () => root.unmount());
});
