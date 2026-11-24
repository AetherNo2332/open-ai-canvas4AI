import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    HTMLElement: dom.HTMLElement,
    SVGElement: dom.SVGElement,
    Element: dom.Element,
    Node: dom.Node,
    ResizeObserver: class {
        observe() {}
        unobserve() {}
        disconnect() {}
    },
    getComputedStyle: dom.getComputedStyle.bind(dom),
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
const { AgentPanel } = await import("@/components/agent/agent-panel");
const { resolveAgentPanelConfig } = await import("@/services/agent/panel-config");
await import("@/services/agent/configs");

const capabilities = {
    version: 3,
    permissionModes: ["read_only", "request_approval", "auto"] as const,
    contextScopes: ["canvas"],
    skills: true,
    allowUserSkills: true,
    writeTools: true,
    defaultSkills: { count: 0, skills: [] },
    modelRequirements: { text: true, toolCalling: true, vision: true, streamOutput: true },
    availability: { configured: true, readyWorkers: 1, queueReason: "", reason: "" },
};

function client() {
    return {
        async listConversations() {
            return { items: [] };
        },
        async listConversationRuns() {
            return { items: [] };
        },
        async getConversationQueue() {
            return { conversationId: "", latestRunId: "", revision: 0, paused: false, items: [] };
        },
        async getRun() {
            throw new Error("unused");
        },
        async readEvents() {},
    } as never;
}

test("all three business configs render the same complete panel controls", async () => {
    for (const id of ["canvas", "drama", "platform"] as const) {
        const config = resolveAgentPanelConfig(id)!;
        const host = document.createElement("div");
        document.body.append(host);
        const root = createRoot(host);
        await act(async () => {
            root.render(
                <AgentPanel
                    context={{
                        owner: `test-${id}`,
                        routeKey: `/test/${id}`,
                        declaration: id === "platform" ? { enabled: true, kind: "platform" } : { enabled: true, kind: "business", businessType: id, agentDefinitionId: config.agentDefinitionId },
                        status: "ready",
                        scope: id === "platform" ? { type: "platform", id: "self" } : id === "drama" ? { type: "drama", id: "project" } : { type: "canvas", id: "canvas" },
                        agentDefinitionId: config.agentDefinitionId,
                        displayName: config.title,
                    }}
                    config={config}
                    adapter={{
                        configId: config.id,
                        owner: `test-${id}`,
                        buildRequest: ({ prompt, idempotencyKey }) => ({
                            scope: id === "platform" ? { type: "platform", id: "self" } : id === "drama" ? { type: "drama", id: "project" } : { type: "canvas", id: "canvas" },
                            canvasId: id === "canvas" ? "canvas" : "",
                            prompt,
                            permissionMode: "read_only",
                            idempotencyKey,
                        }),
                    }}
                    client={client()}
                    capabilities={{ ...capabilities, permissionModes: [...capabilities.permissionModes] }}
                    open
                    onOpen={() => {}}
                    onClose={() => {}}
                />,
            );
        });
        expect(host.querySelector("[data-agent-panel]")).not.toBeNull();
        expect(host.querySelector('[aria-label="Agent 输入"]')).not.toBeNull();
        expect(host.querySelector("[data-agent-context-ring]")).not.toBeNull();
        expect(host.querySelector('[aria-label="历史对话"]')).not.toBeNull();
        expect(host.querySelector('[aria-label="新建对话"]')).not.toBeNull();
        expect(host.querySelector('[aria-label^="打开 Skills 技能库"]')).not.toBeNull();
        expect(host.querySelector('[aria-label^="执行权限"]')).not.toBeNull();
        expect(host.querySelector('[aria-label="更多 Agent 操作"]')).not.toBeNull();
        expect(host.querySelector(".agent-composer-selection")).not.toBeNull();
        expect(host.querySelector("[data-agent-launcher]")).toBeNull();
        expect(host.querySelector('[aria-label="Agent 输入"]')?.getAttribute("placeholder")).toBe(config.placeholder);
        await act(async () => root.unmount());
        host.remove();
    }
});
