import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "https://localhost/" });
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    HTMLElement: dom.HTMLElement,
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
})) {
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { AgentCollapsedAttentionCard, AgentNotificationPermissionGuide } = await import("../../src/components/canvas/canvas-agent-attention");
const { canvasThemes } = await import("../../src/lib/canvas-theme");

afterAll(() => dom.happyDOM.abort());

function button(host: HTMLElement, label: string) {
    const match = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.includes(label));
    expect(match, `missing button: ${label}; found: ${host.textContent}`).toBeDefined();
    return match!;
}

test("授权指引只通过明确按钮请求权限", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    let requests = 0;
    try {
        await act(async () =>
            root.render(
                <AgentNotificationPermissionGuide
                    permission="default"
                    requesting={false}
                    onRequest={() => {
                        requests += 1;
                    }}
                />,
            ),
        );
        expect(host.textContent).toContain("仅在页面隐藏或 Agent 收起时提醒审批和提问");
        expect(requests).toBe(0);
        await act(async () => button(host, "允许通知").click());
        expect(requests).toBe(1);
    } finally {
        await act(async () => root.unmount());
        host.remove();
    }
});

test("收起态审批卡直接同意或拒绝当前审批", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const decisions: string[] = [];
    try {
        await act(async () =>
            root.render(
                <AgentCollapsedAttentionCard
                    theme={canvasThemes.light}
                    approval={{ approvalId: "approval-1", detail: { toolName: "arrange_nodes", arguments: { nodeIds: ["node-1"] } }, reason: "" }}
                    approvalSubmitting={false}
                    onApprovalReasonChange={() => undefined}
                    onApprove={() => decisions.push("approve")}
                    onReject={() => decisions.push("reject")}
                    onAnswer={() => undefined}
                />,
            ),
        );
        expect(host.getAttribute("role")).toBeNull();
        expect(host.querySelector('[role="dialog"]')?.getAttribute("aria-label")).toContain("审批");
        await act(async () => button(host, "同意执行").click());
        await act(async () => button(host, "暂不执行").click());
        expect(decisions).toEqual(["approve", "reject"]);
    } finally {
        await act(async () => root.unmount());
        host.remove();
    }
});

test("收起态表单提问保留现有校验和提交负载", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const answers: string[] = [];
    try {
        await act(async () =>
            root.render(
                <AgentCollapsedAttentionCard
                    theme={canvasThemes.dark}
                    question={{
                        kind: "form",
                        questionId: "question-1",
                        question: "选择画面风格",
                        fields: [
                            {
                                id: "style",
                                title: "风格",
                                type: "segmented",
                                required: true,
                                options: [
                                    { id: "real", label: "写实" },
                                    { id: "anime", label: "动画" },
                                ],
                            },
                        ],
                    }}
                    approvalSubmitting={false}
                    onApprovalReasonChange={() => undefined}
                    onApprove={() => undefined}
                    onReject={() => undefined}
                    onAnswer={(answer) => answers.push(answer)}
                />,
            ),
        );
        await act(async () => button(host, "动画").click());
        await act(async () => button(host, "确认并继续").click());
        expect(JSON.parse(answers[0]!)).toMatchObject({ questionId: "question-1", answers: { style: "anime" } });
    } finally {
        await act(async () => root.unmount());
        host.remove();
    }
});
