import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";
import type { AgentMessageQueue } from "@/services/api/agent";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    HTMLElement: dom.HTMLElement,
    SVGElement: dom.SVGElement,
    Element: dom.Element,
    Node: dom.Node,
    Event: dom.Event,
    MouseEvent: dom.MouseEvent,
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
const { AgentMessageQueueList, AgentSendModeButton } = await import("@/components/canvas/canvas-agent-message-queue");
afterAll(() => dom.happyDOM.abort());
const settle = () => new Promise((resolve) => setTimeout(resolve, 80));

test("queue editor preserves a separate composer and sends the original item revision", async () => {
    const item = { id: "item", conversationId: "chat", canvasId: "canvas", mode: "queue" as const, revision: 3, sequence: 1, prompt: "原待发内容", status: "queued" as const, createdAt: "", updatedAt: "" };
    const queue: AgentMessageQueue = { conversationId: "chat", canvasId: "canvas", revision: 4, latestRunId: "run", paused: true, pauseReason: "积分不足，请补充额度", items: [item] };
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    let edited: unknown,
        promoted: unknown,
        removed: unknown,
        resumed = 0;
    await act(async () => {
        root.render(
            <>
                <textarea aria-label="原会话草稿" value="继续保留草稿" readOnly />
                <AgentMessageQueueList
                    queue={queue}
                    onEdit={async (entry, value) => {
                        edited = { entry, value };
                        return true;
                    }}
                    onPromote={async (entry) => {
                        promoted = entry;
                        return true;
                    }}
                    onRemove={async (entry) => {
                        removed = entry;
                        return true;
                    }}
                    onResume={async () => {
                        resumed++;
                        return true;
                    }}
                    onRetry={async () => {}}
                />
            </>,
        );
    });
    const click = async (label: string) => {
        const button = [...host.querySelectorAll("button")].find((entry) => entry.getAttribute("aria-label") === label);
        expect(button).toBeDefined();
        await act(async () => {
            button!.click();
            await settle();
        });
    };
    try {
        expect(host.textContent).toContain("积分不足，请补充额度");
        expect(host.textContent).toContain("原待发内容");
        await click("编辑待发消息");
        const input = host.querySelector<HTMLTextAreaElement>('[aria-label="编辑待发消息"]')!;
        await act(async () => {
            Object.getOwnPropertyDescriptor(dom.HTMLTextAreaElement.prototype, "value")!.set!.call(input, "编辑后的待发内容");
            input.dispatchEvent(new Event("input", { bubbles: true }));
            await settle();
        });
        await click("保存待发消息");
        expect(edited).toEqual({ entry: item, value: "编辑后的待发内容" });
        expect(host.querySelector<HTMLTextAreaElement>('[aria-label="原会话草稿"]')!.value).toBe("继续保留草稿");
        await click("插队发送");
        expect(promoted).toBe(item);
        await click("移除待发消息");
        expect(removed).toBe(item);
        await click("继续发送");
        expect(resumed).toBe(1);
        expect(host.textContent).not.toMatch(/CAS|幂等|调度|事务|worker/);
    } finally {
        await act(async () => root.unmount());
        host.remove();
    }
});

test("send mode icon exposes the default and opens settings", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    let opened = 0;
    for (const mode of ["queue", "interject"] as const) {
        await act(async () => root.render(<AgentSendModeButton value={mode} onOpenSettings={() => opened++} />));
        const button = host.querySelector("button")!;
        expect(button.getAttribute("aria-label")).toContain(mode === "queue" ? "默认排队" : "默认插队");
        expect(button.getAttribute("aria-label")).toContain("打开发送设置");
        expect(host.textContent).toBe("");
        await act(async () => button.click());
    }
    expect(opened).toBe(2);
    await act(async () => root.unmount());
    host.remove();
});
