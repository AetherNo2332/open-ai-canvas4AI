import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    HTMLElement: dom.HTMLElement,
    Element: dom.Element,
    Node: dom.Node,
    IS_REACT_ACT_ENVIRONMENT: true,
})) {
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
const { AgentPanelHost } = await import("@/components/agent/agent-panel-host");
const { publishAgentPageContext } = await import("@/services/agent/business-context");
const { registerAgentPanelAdapter } = await import("@/services/agent/panel-adapters");

async function renderHost() {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => root.render(<AgentPanelHost />));
    return {
        host,
        async unmount() {
            await act(async () => root.unmount());
            host.remove();
        },
    };
}

test("host hides an undeclared page, shows canvas loading/error, and resolves a registered adapter", async () => {
    publishAgentPageContext({
        owner: "disabled",
        routeKey: "/projects",
        declaration: { enabled: false },
        status: "error",
        displayName: "",
    });
    let view = await renderHost();
    expect(view.host.textContent).toBe("");
    await view.unmount();

    publishAgentPageContext({
        owner: "canvas-loading",
        routeKey: "/canvas/a",
        declaration: { enabled: true, kind: "business", businessType: "canvas", agentDefinitionId: "canvas.default" },
        status: "loading",
        scope: { type: "canvas", id: "a" },
        agentDefinitionId: "canvas.default",
        displayName: "画布 A",
    });
    view = await renderHost();
    expect(view.host.textContent).toContain("正在加载画布 Agent");
    await view.unmount();

    publishAgentPageContext({
        owner: "canvas-error",
        routeKey: "/canvas/a",
        declaration: { enabled: true, kind: "business", businessType: "canvas", agentDefinitionId: "canvas.default" },
        status: "error",
        scope: { type: "canvas", id: "a" },
        agentDefinitionId: "canvas.default",
        displayName: "画布 A",
        error: "无权访问",
    });
    view = await renderHost();
    expect(view.host.querySelector("button")?.disabled).toBe(true);
    expect(view.host.textContent).toContain("无权访问");
    await view.unmount();

    const unregister = registerAgentPanelAdapter("canvas", "test-canvas", () => <div>canvas adapter</div>);
    publishAgentPageContext({
        owner: "canvas-ready",
        routeKey: "/canvas/a",
        declaration: { enabled: true, kind: "business", businessType: "canvas", agentDefinitionId: "canvas.default" },
        status: "ready",
        scope: { type: "canvas", id: "a" },
        agentDefinitionId: "canvas.default",
        displayName: "画布 A",
    });
    view = await renderHost();
    expect(view.host.textContent).toContain("canvas adapter");
    await view.unmount();
    unregister();
});
