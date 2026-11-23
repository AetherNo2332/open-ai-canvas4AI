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
    const originalFetch = globalThis.fetch;
    globalThis.fetch = (async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes("/agent/director-previews/pending")) {
            return new Response(JSON.stringify({ code: 0, data: { items: [] } }), { headers: { "content-type": "application/json" } });
        }
        return new Response(
            JSON.stringify({
                code: 0,
                data: {
                    version: 3,
                    permissionModes: ["read_only"],
                    contextScopes: ["canvas"],
                    skills: true,
                    allowUserSkills: true,
                    writeTools: false,
                    defaultSkills: { count: 0, skills: [] },
                    modelRequirements: { text: true, toolCalling: true, vision: true, streamOutput: true },
                    availability: { configured: true, readyWorkers: 1, queueReason: "", reason: "" },
                },
            }),
            { headers: { "content-type": "application/json" } },
        );
    }) as typeof fetch;
    try {
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
        expect(view.host.textContent).toContain("正在加载 Agent 接入");
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

        const unregister = registerAgentPanelAdapter("canvas", {
            configId: "canvas",
            owner: "test-canvas",
            buildRequest: ({ prompt, idempotencyKey, selection }) => ({
                scope: { type: "canvas", id: "a" },
                canvasId: "a",
                prompt,
                permissionMode: selection.permissionMode,
                idempotencyKey,
            }),
        });
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
        await act(async () => {
            await new Promise((resolve) => setTimeout(resolve, 50));
        });
        expect(view.host.textContent).toContain("正在校验 Agent 能力");
        await view.unmount();
        unregister();
    } finally {
        globalThis.fetch = originalFetch;
    }
});
