import { expect, test } from "bun:test";
import { Window } from "happy-dom";
import type { createAgentClient } from "@/sdk/agent/client";

test("StrictMode can hydrate the shared controller and changing the mounted identity cancels its old requests", async () => {
    const dom = new Window({ url: "http://localhost/" });
    const globals = { window: dom, document: dom.document, navigator: dom.navigator, IS_REACT_ACT_ENVIRONMENT: true };
    const originals = new Map(Object.keys(globals).map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
    for (const [key, value] of Object.entries(globals)) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
    // The controller's storage adapter treats an environment without a browser storage driver as empty.
    Object.defineProperty(dom, "localStorage", { value: undefined });
    Object.defineProperty(dom, "indexedDB", { value: undefined });
    const { act, StrictMode } = await import("react");
    const { createRoot } = await import("react-dom/client");
    const { useAgentConversation } = await import("@/components/agent/use-agent-conversation");
    const root = createRoot(document.body);
    const requests: AbortSignal[] = [];
    const client = {
        listConversations(_page: unknown, currentSignal: AbortSignal) {
            requests.push(currentSignal);
            return new Promise((resolve) => currentSignal?.addEventListener("abort", () => resolve({ items: [] }), { once: true }));
        },
    } as unknown as ReturnType<typeof createAgentClient>;
    function Probe({ userScope }: { userScope: string }) {
        const { controller, state } = useAgentConversation({ userScope, scope: { type: "platform", id: "self" }, agentDefinitionId: "platform.default", client });
        return (
            <p>
                {state.status} {controller?.identityKey}
            </p>
        );
    }
    try {
        await act(async () =>
            root.render(
                <StrictMode>
                    <Probe userScope="account-a" />
                </StrictMode>,
            ),
        );
        expect(document.body.textContent).toBe("ready account-a:platform:self:platform.default");
        expect(requests.map((signal) => signal.aborted)).toEqual([false]);
        await act(async () =>
            root.render(
                <StrictMode>
                    <Probe userScope="account-b" />
                </StrictMode>,
            ),
        );
        expect(document.body.textContent).toBe("ready account-b:platform:self:platform.default");
        expect(requests.map((signal) => signal.aborted)).toEqual([true, false]);
    } finally {
        await act(async () => root.unmount());
        for (const [key, descriptor] of originals) {
            if (descriptor) Object.defineProperty(globalThis, key, descriptor);
            else Reflect.deleteProperty(globalThis, key);
        }
        dom.happyDOM.abort();
    }
    expect(requests.map((signal) => signal.aborted)).toEqual([true, true]);
});
