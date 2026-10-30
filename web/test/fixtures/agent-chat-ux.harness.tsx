import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

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
    matchMedia: dom.matchMedia.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
}))
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
afterAll(() => dom.happyDOM.abort());

const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const { AgentChatMessage, AgentMessageList, agentChatRenderProbe } = await import("@/components/canvas/canvas-cloud-agent-chat-ui");
const { sendAgentReplyOption } = await import("@/components/canvas/canvas-agent-reply-options");
const { requestAgentPrefill, takeAgentPrefill, settleAgentPrefill, useGlobalAgentStore } = await import("@/stores/use-global-agent-store");
const { canvasThemes } = await import("@/lib/canvas-theme");
type Message = import("@/components/canvas/canvas-cloud-agent-chat-ui").CloudAgentChatMessage;

const settle = () => new Promise((resolve) => setTimeout(resolve, 30));
const reference = { id: "image-1790-a", nodeId: "image-1790-a", kind: "image" as const, label: "封面", title: "封面", sourceType: "image" as const };

function mount() {
    const host = document.createElement("div");
    document.body.append(host);
    return { host, root: createRoot(host) };
}

test("clicking a node chip focuses that node through the store callback", async () => {
    const { host, root } = mount();
    const focused: string[] = [];
    await act(async () => {
        root.render(<AgentChatMessage item={{ id: "a", role: "assistant", text: "已生成 image-1790-a" }} theme={canvasThemes.light} references={[reference]} onFocusNode={(id) => focused.push(id)} />);
    });
    await settle();
    const chip = host.querySelector<HTMLAnchorElement>("a.agent-message-node-link");
    expect(chip).not.toBeNull();
    const event = new dom.MouseEvent("click", { bubbles: true, cancelable: true });
    await act(async () => {
        chip!.dispatchEvent(event as unknown as Event);
    });
    expect(focused).toEqual(["image-1790-a"]);
    expect(event.defaultPrevented).toBe(true);
    await act(async () => root.unmount());
    host.remove();
});

test("an option card submits through the global prefill path and is disabled while pending", async () => {
    useGlobalAgentStore.setState({ prefillRequest: null, panelUnavailable: false });
    const outcome = sendAgentReplyOption({ canvasId: "canvas-1", userScope: "user-1", text: "先出分镜" });
    const request = useGlobalAgentStore.getState().prefillRequest;
    expect(request).toMatchObject({ canvasId: "canvas-1", userScope: "user-1", text: "先出分镜", autoSubmit: true });
    // 已有待消费请求时，第二次点击不会重复投递。
    expect(await sendAgentReplyOption({ canvasId: "canvas-1", userScope: "user-1", text: "直接生成视频" })).toBe(false);
    expect(takeAgentPrefill(request!.id)?.text).toBe("先出分镜");
    settleAgentPrefill(request!.id, "sent");
    expect(await outcome).toBe("sent");
    // 没有画布时不投递。
    expect(await sendAgentReplyOption({ canvasId: "", userScope: "user-1", text: "x" })).toBe(false);
    expect(useGlobalAgentStore.getState().prefillRequest).toBeNull();
    // requestAgentPrefill 与选项卡共用同一条路径。
    expect(typeof requestAgentPrefill).toBe("function");

    const { host, root } = mount();
    const picked: string[] = [];
    const item: Message = { id: "a", role: "assistant", text: "你想先做哪个？\n1. 先出分镜\n2. 直接生成视频" };
    await act(async () => {
        root.render(<AgentMessageList messages={[item]} theme={canvasThemes.light} references={[]} busy={false} onReplyOption={(text) => picked.push(text)} />);
    });
    await settle();
    const buttons = [...host.querySelectorAll<HTMLButtonElement>("button.agent-reply-option")];
    expect(buttons).toHaveLength(2);
    await act(async () => {
        buttons[1].click();
    });
    expect(picked).toEqual(["直接生成视频"]);
    await act(async () => {
        root.render(<AgentMessageList messages={[item]} theme={canvasThemes.light} references={[]} busy={false} onReplyOption={(text) => picked.push(text)} replyOptionsDisabled />);
    });
    expect([...host.querySelectorAll<HTMLButtonElement>("button.agent-reply-option")].every((button) => button.disabled)).toBe(true);
    await act(async () => root.unmount());
    host.remove();
});

test("the error diagnostic disclosure opens and closes", async () => {
    const { host, root } = mount();
    await act(async () => {
        root.render(<AgentChatMessage item={{ id: "e", role: "error", title: "Agent 执行失败", text: "中断；如反复出现，请把诊断号 ag-1 反馈给管理员", runId: "ag-1" }} theme={canvasThemes.dark} />);
    });
    const details = host.querySelector<HTMLDetailsElement>("details.agent-error-diagnostics");
    expect(details).not.toBeNull();
    expect(details!.open).toBe(false);
    await act(async () => {
        details!.querySelector("summary")!.dispatchEvent(new dom.MouseEvent("click", { bubbles: true, cancelable: true }) as unknown as Event);
    });
    expect(details!.open).toBe(true);
    expect(details!.textContent).toContain("ag-1");
    await act(async () => root.unmount());
    host.remove();
});

test("streaming one message re-renders only that message, even with fresh callbacks", async () => {
    const { host, root } = mount();
    const base: Message[] = [
        { id: "u1", role: "user", text: "第一句" },
        { id: "a1", role: "assistant", text: "第一段回复" },
        { id: "u2", role: "user", text: "第二句" },
        { id: "a2", role: "assistant", text: "正", streaming: true },
    ];
    const render = (messages: Message[]) =>
        root.render(
            <AgentMessageList
                messages={messages}
                theme={canvasThemes.light}
                // 每次都是新数组 / 新回调：模拟面板父组件的普通重渲染。
                references={[reference]}
                busy
                onFocusNode={() => {}}
                onReplyOption={() => {}}
            />,
        );
    await act(async () => render(base));
    await settle();
    agentChatRenderProbe.counts.clear();
    agentChatRenderProbe.enabled = true;
    let messages = base;
    for (const chunk of ["在", "写", "第二段"]) {
        const last = messages[messages.length - 1];
        messages = [...messages.slice(0, -1), { ...last, text: last.text + chunk }];
        await act(async () => render(messages));
    }
    await settle();
    agentChatRenderProbe.enabled = false;
    expect(agentChatRenderProbe.counts.get("u1") ?? 0).toBe(0);
    expect(agentChatRenderProbe.counts.get("a1") ?? 0).toBe(0);
    expect(agentChatRenderProbe.counts.get("u2") ?? 0).toBe(0);
    expect(agentChatRenderProbe.counts.get("a2") ?? 0).toBeGreaterThanOrEqual(3);
    await act(async () => root.unmount());
    host.remove();
});
