import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";
import type { CloudAgentConversation } from "@/services/cloud-agent-conversations";
import type { AgentEvent, AgentQueuedMessage } from "@/services/api/agent";
import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";

const dom = new Window({ url: "http://localhost/", width: 1440, height: 900 });
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    localStorage: dom.localStorage,
    HTMLElement: dom.HTMLElement,
    HTMLInputElement: dom.HTMLInputElement,
    HTMLTextAreaElement: dom.HTMLTextAreaElement,
    SVGElement: dom.SVGElement,
    Element: dom.Element,
    Node: dom.Node,
    Event: dom.Event,
    CustomEvent: dom.CustomEvent,
    ShadowRoot: dom.ShadowRoot,
    MutationObserver: dom.MutationObserver,
    ResizeObserver: dom.ResizeObserver,
    requestAnimationFrame: dom.requestAnimationFrame.bind(dom),
    cancelAnimationFrame: dom.cancelAnimationFrame.bind(dom),
    getComputedStyle: dom.getComputedStyle.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
}))
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const agent = await import("@/services/api/agent");
const cache = await import("@/services/cloud-agent-conversations");
const history = await import("@/services/cloud-agent-history");
const subagents = await import("@/services/api/agent-subagent");
const { setActiveUserScope } = await import("@/lib/user-scope");
const { useUserStore } = await import("@/stores/use-user-store");
const { createModelChannel, defaultConfig, normalizeConfigSnapshot, useConfigStore } = await import("@/stores/use-config-store");
const { useGlobalAgentStore } = await import("@/stores/use-global-agent-store");
let reducedMotion = false;
let queueItems: AgentQueuedMessage[] = [];
let receiveEvent: ((event: AgentEvent) => void) | undefined;
const silent = () => {};
// Retain DOM props, children and refs; expose the exact animation contract emitted by production.
const motionTags = new Map<string, any>();
mock.module("motion/react", () => ({
    AnimatePresence: ({ children }: any) => <>{children}</>,
    useReducedMotion: () => reducedMotion,
    motion: new Proxy(
        {},
        {
            get: (_target, tag: string) => {
                if (!motionTags.has(tag))
                    motionTags.set(tag, ({ initial, animate, exit, transition, whileHover, whileTap, layout, ...props }: any) =>
                        React.createElement(tag, { ...props, "data-motion-contract": JSON.stringify({ initial, animate, exit, transition, layout }) }),
                    );
                return motionTags.get(tag);
            },
        },
    ),
}));
mock.module("file-saver", () => ({ saveAs: silent }));
mock.module("@/components/ai/ai-message-markdown", () => ({ AIMessageMarkdown: ({ children, content }: any) => <div>{children || content}</div> }));
mock.module("@/components/canvas/live2d-avatar", () => ({ Live2DAvatar: () => null }));
mock.module("@/components/ui/fluid-orb", () => ({ FluidOrb: () => null }));
mock.module("@/services/api/agent", () => ({
    ...agent,
    getAgentUISettings: async () => ({ defaultSendMode: "queue", revision: 1 }),
    getAgentCapabilities: async () => ({ permissionModes: ["auto"], skills: true, availability: { configured: true } }),
    listAgentSkillDefaults: async () => ({ count: 0, skills: [] }),
    getAgentMessageQueue: async () => ({ conversationId: "test", canvasId: "canvas", revision: 1, paused: false, latestRunId: currentConversation.run?.id || "run-approval", items: queueItems }),
    subscribeAgentEvents: (_id: string, receive: (event: AgentEvent) => void, options: any) => {
        receiveEvent = receive;
        options.onConnectionChange("connected");
        return silent;
    },
    getAgentRun: async () => ({ run: currentConversation.run }),
}));
mock.module("@/services/api/agent-subagent", () => ({ ...subagents, listAgentSubagents: async () => [] }));
const savedConversation: CloudAgentConversation = {
    id: "conversation-one",
    title: "Test conversation",
    messages: [
        { id: "user-one", role: "user", text: "A user message" },
        { id: "reasoning-one", role: "assistant", text: "A retained reasoning detail", reasoning: true },
        { id: "answer-one", role: "assistant", text: "An assistant message" },
    ],
    run: null,
    draft: "A retained unsent draft",
    permissionMode: "auto",
    createdAt: "2026-10-25T00:00:00Z",
    updatedAt: "2026-10-25T00:00:00Z",
};
let currentConversation = savedConversation;
mock.module("@/services/cloud-agent-conversations", () => ({
    ...cache,
    loadCloudAgentConversations: async () => ({ version: 1, activeId: currentConversation.id, conversations: [currentConversation] }),
    saveCloudAgentConversations: async () => {},
    loadCloudAgentPendingSubmission: async () => null,
}));
mock.module("@/services/cloud-agent-history", () => ({ ...history, refreshCloudAgentConversations: async () => (currentConversation.run ? [currentConversation] : []) }));
mock.module("@/services/api/skills", () => ({ listSkillPresets: async () => ({ presets: [] }), listSkills: async () => ({ skills: [] }), listAddedSkills: async () => ({ skills: [] }), addSkill: silent }));
mock.module("@/services/user-data-sync", () => ({ saveRemoteUserDataNow: async () => {}, applyAgentCanvasPatches: async () => {}, refreshCanvasAfterAgent: async () => {} }));
const { CanvasCloudAgentPanel } = await import("@/components/canvas/canvas-cloud-agent-panel");
const styles = document.createElement("style");
styles.textContent = await Bun.file(new URL("../../src/components/canvas/canvas-cloud-agent.css", import.meta.url)).text();
// Happy DOM cannot parse the app's Tailwind nesting. Apply the real toolbar CSS rules through PostCSS.
const { parse } = await import("postcss");
parse(await Bun.file(new URL("../../src/styles/globals.css", import.meta.url)).text()).walkRules((rule) => {
    if (rule.selector.includes(".canvas-editor-shell:has(")) styles.textContent += rule.toString();
});
document.head.append(styles);
const settle = async () => {
    for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 5));
};
function readOlderMessages(conversation: HTMLElement) {
    Object.defineProperties(conversation, { scrollHeight: { configurable: true, value: 1000 }, clientHeight: { configurable: true, value: 200 } });
    conversation.scrollTop = 175;
    conversation.dispatchEvent(new Event("scroll", { bubbles: true }));
}
function toolbarVisibility(host: HTMLElement) {
    // Happy DOM retains stale :has() matches after descendant attributes change.
    const snapshot = host.cloneNode(true) as HTMLElement;
    document.body.append(snapshot);
    const visibility = getComputedStyle(snapshot.querySelector<HTMLElement>(".canvas-main-toolbar")!).visibility;
    snapshot.remove();
    return visibility;
}
async function mount({ compact = false, docked = true, reduced = false, approval = false, queued = false, streaming = false, references = [] as CanvasResourceReference[], draft = savedConversation.draft } = {}) {
    reducedMotion = reduced;
    receiveEvent = undefined;
    currentConversation = approval
        ? {
              ...savedConversation,
              run: {
                  id: "run-approval",
                  canvasId: "canvas",
                  conversationId: "test",
                  status: "waiting_approval",
                  permissionMode: "auto",
                  createdAt: savedConversation.createdAt,
                  updatedAt: savedConversation.updatedAt,
                  approval: {
                      approvalId: "image-approval",
                      reason: "Keep approval reason",
                      call: { id: "image-call", function: { name: "generate_media", arguments: JSON.stringify({ mode: "image", size: "1:1", prompt: "An image", referenceNodeIds: [] }) } },
                  },
              },
          }
        : savedConversation;
    if (queued)
        currentConversation = { ...savedConversation, run: { id: "run-queue", conversationId: "test", canvasId: "canvas", status: "completed", permissionMode: "auto", createdAt: savedConversation.createdAt, updatedAt: savedConversation.updatedAt } };
    if (streaming)
        currentConversation = { ...savedConversation, run: { id: "run-stream", conversationId: "test", canvasId: "canvas", status: "running", permissionMode: "auto", createdAt: savedConversation.createdAt, updatedAt: savedConversation.updatedAt } };
    currentConversation = { ...currentConversation, draft };
    queueItems = queued
        ? [1, 2].map((sequence) => ({
              id: `queued-${sequence}`,
              conversationId: "test",
              canvasId: "canvas",
              sequence,
              revision: 1,
              mode: "queue",
              status: "queued",
              prompt: `Queued message ${sequence}`,
              createdAt: savedConversation.createdAt,
              updatedAt: savedConversation.updatedAt,
          }))
        : [];
    dom.happyDOM.setWindowSize({ width: compact ? 600 : 1440, height: 900 });
    window.localStorage.clear();
    // 面板改为读全局 store：画布上下文与开合状态都由 store 提供。
    useGlobalAgentStore.setState({ open: true, prefillRequest: null, dock: { docked, width: 440 }, canvas: { canvasId: "canvas", nodeCount: 1, selectedNodeIds: [], references } });
    setActiveUserScope("visibility-user");
    useUserStore.setState({ hydrated: true, user: { id: "visibility-user", username: "test", displayName: "test", role: "user", status: "active" }, features: { agentSubagentsEnabled: false, customChannelsEnabled: true } as any });
    const channel = createModelChannel({
        id: "test",
        scope: "user",
        apiKey: "test",
        models: ["deepseek-r1"],
        modelCosts: [
            {
                model: "deepseek-r1",
                capability: "text",
                billingMode: "token",
                unitPriceMicrocredits: 0,
                capabilityConfig: { version: 1, text: { thinking: true, references: { promptMaxChars: 1000, maxImages: 0, maxImageBytes: 0, maxVideos: 0, maxVideoBytes: 0 } } },
            },
        ],
    });
    useConfigStore.setState({ config: normalizeConfigSnapshot({ config: { ...defaultConfig, channels: [channel], model: "test::deepseek-r1", textModel: "test::deepseek-r1" } }).config });
    const host = document.createElement("div");
    host.className = "canvas-editor-shell";
    document.body.append(host);
    const root = createRoot(host);
    const render = async (open: boolean) =>
        act(async () => {
            useGlobalAgentStore.setState({ open });
            root.render(
                <>
                    <div className="canvas-main-toolbar">Toolbar</div>
                    <CanvasCloudAgentPanel launcher={false} />
                </>,
            );
            await settle();
        });
    await render(true);
    return {
        host,
        render,
        aside: () => host.querySelector<HTMLElement>("aside.canvas-agent-panel")!,
        close: async () => {
            await act(async () => root.unmount());
            host.remove();
        },
    };
}
afterAll(() => dom.happyDOM.abort());

const imageReference: CanvasResourceReference = { id: "image-one", nodeId: "image-one", kind: "image", label: "Image one", title: "Image one", active: true, previewUrl: "data:image/png;base64,iVBORw0KGgo=", mentionToken: "@[node:image-one]" };

test("closing removes the real mention body portal while its search field is focused", async () => {
    const mounted = await mount({ references: [imageReference] });
    try {
        const composer = mounted.aside().querySelector(".agent-composer-wrap");
        const editor = mounted.aside().querySelector<HTMLElement>('[aria-label="Agent 输入"]')!;
        await act(async () => {
            editor.focus();
            editor.textContent = "@";
            const range = document.createRange();
            range.selectNodeContents(editor);
            range.collapse(false);
            window.getSelection()!.removeAllRanges();
            window.getSelection()!.addRange(range);
            editor.dispatchEvent(new Event("input", { bubbles: true }));
            await settle();
        });
        const menu = document.body.querySelector<HTMLElement>("[data-canvas-resource-mention-menu]")!;
        expect(menu).not.toBeNull();
        expect(mounted.aside().contains(menu)).toBe(false);
        const search = menu.querySelector<HTMLInputElement>('[aria-label="搜索引用素材"]')!;
        await act(async () => search.focus());
        expect(document.activeElement === search).toBe(true);
        await mounted.render(false);
        expect({ portalConnected: menu.isConnected, portalFocused: document.activeElement === search }).toEqual({ portalConnected: false, portalFocused: false });
        await mounted.render(true);
        expect(document.body.querySelector("[data-canvas-resource-mention-menu]")).toBeNull();
        expect(mounted.aside().querySelector(".agent-composer-wrap") === composer).toBe(true);
        expect(mounted.aside().querySelector('[aria-label="Agent 输入"]') === editor).toBe(true);
        expect(editor.textContent).toBe("@");
    } finally {
        await mounted.close();
    }
});

test("closing removes the real inline image preview body portal without replacing the editor", async () => {
    const mounted = await mount({ references: [imageReference], draft: "Review @[node:image-one] " });
    try {
        const editor = mounted.aside().querySelector<HTMLElement>('[aria-label="Agent 输入"]')!;
        const chip = editor.querySelector<HTMLElement>('[data-mention-reference-id="image-one"]')!;
        expect(chip).not.toBeNull();
        await act(async () => {
            chip.dispatchEvent(new dom.MouseEvent("dblclick", { bubbles: true }));
            await settle();
        });
        const preview = document.body.querySelector<HTMLElement>('[role="dialog"][aria-label="预览Image one"]')!;
        expect(preview).not.toBeNull();
        expect(mounted.aside().contains(preview)).toBe(false);
        const close = preview.querySelector<HTMLButtonElement>('[aria-label="关闭图片预览"]')!;
        await act(async () => close.focus());
        await mounted.render(false);
        expect({ portalConnected: preview.isConnected, portalFocused: document.activeElement === close }).toEqual({ portalConnected: false, portalFocused: false });
        await mounted.render(true);
        expect(document.body.querySelector('[role="dialog"][aria-label="预览Image one"]')).toBeNull();
        expect(mounted.aside().querySelector('[aria-label="Agent 输入"]') === editor).toBe(true);
        expect(editor.querySelector('[data-mention-reference-id="image-one"]') === chip).toBe(true);
    } finally {
        await mounted.close();
    }
});

test("closing cancels the real composer height drag while preserving height and draft", async () => {
    const mounted = await mount();
    try {
        const composer = mounted.aside().querySelector<HTMLElement>(".agent-composer-wrap")!;
        const handle = composer.querySelector<HTMLButtonElement>('[aria-label="调整提示词面板高度"]')!;
        const captures = new Set<number>();
        Object.assign(handle, {
            setPointerCapture: (id: number) => captures.add(id),
            hasPointerCapture: (id: number) => captures.has(id),
            releasePointerCapture: (id: number) => {
                captures.delete(id);
                handle.dispatchEvent(new dom.PointerEvent("lostpointercapture", { bubbles: true, pointerId: id }));
            },
        });
        await act(async () => handle.dispatchEvent(new dom.PointerEvent("pointerdown", { bubbles: true, pointerId: 19, button: 0, clientY: 600 })));
        await act(async () => handle.dispatchEvent(new dom.PointerEvent("pointermove", { bubbles: true, pointerId: 19, clientY: 540 })));
        expect(handle.getAttribute("aria-valuenow")).toBe("120");
        await mounted.render(false);
        expect(captures.has(19)).toBe(false);
        await mounted.render(true);
        expect(mounted.aside().querySelector(".agent-composer-wrap") === composer).toBe(true);
        await act(async () => handle.dispatchEvent(new dom.PointerEvent("pointermove", { bubbles: true, pointerId: 19, clientY: 500 })));
        expect(handle.getAttribute("aria-valuenow")).toBe("120");
        expect(composer.querySelector<HTMLTextAreaElement>('[aria-label="Agent 输入"]')!.value).toBe("A retained unsent draft");
    } finally {
        await mounted.close();
    }
});

for (const following of [false, true])
    test(`hidden stream additions preserve ${following ? "bottom following" : "older-message reading"} through reopen`, async () => {
        const mounted = await mount({ streaming: true });
        try {
            const conversation = mounted.aside().querySelector<HTMLElement>("[data-agent-conversation]")!;
            let height = 1000;
            let top = following ? 800 : 175;
            // Happy DOM has no layout; model the browser's clamped scroll geometry at this boundary.
            Object.defineProperties(conversation, {
                scrollHeight: { configurable: true, get: () => height },
                clientHeight: { configurable: true, value: 200 },
                scrollTop: {
                    configurable: true,
                    get: () => top,
                    set: (value: number) => {
                        top = Math.max(0, Math.min(value, height - 200));
                    },
                },
            });
            conversation.dispatchEvent(new Event("scroll", { bubbles: true }));
            await mounted.render(false);
            for (const seq of [1, 2]) {
                height += 200;
                await act(async () => {
                    receiveEvent!({ eventId: `stream-${seq}`, runId: "run-stream", seq, type: "assistant_delta", payload: { messageId: "answer-one", text: ` hidden-${seq}` }, createdAt: savedConversation.createdAt });
                    await settle();
                });
                for (let i = 0; i < 40 && !conversation.textContent?.includes(`hidden-${seq}`); i++) {
                    await act(async () => {
                        await new Promise((resolve) => setTimeout(resolve, 50));
                    });
                }
                expect(conversation.textContent).toContain(`hidden-${seq}`);
                expect(conversation.scrollTop).toBe(following ? height - 200 : 175);
            }
            await mounted.render(true);
            expect(mounted.aside().querySelector("[data-agent-conversation]") === conversation).toBe(true);
            expect(conversation.scrollTop).toBe(following ? 1200 : 175);
        } finally {
            await mounted.close();
        }
    });

test("closing preserves the real composer and queue expansion with an unsaved edit draft", async () => {
    const mounted = await mount({ queued: true });
    try {
        const composer = mounted.aside().querySelector(".agent-composer-wrap")!;
        const queue = composer.querySelector<HTMLElement>('.agent-message-queue[aria-label="待发消息"]')!;
        expect(queue).not.toBeNull();
        await act(async () => queue.querySelector<HTMLButtonElement>(".agent-message-queue-expand")!.click());
        expect(queue.querySelector(".agent-message-queue-expand")!.getAttribute("aria-expanded")).toBe("true");
        expect(queue.querySelectorAll("li")).toHaveLength(2);
        await act(async () => queue.querySelectorAll<HTMLButtonElement>('[aria-label="编辑待发消息"]')[1].click());
        const editor = queue.querySelector<HTMLTextAreaElement>('textarea[aria-label="编辑待发消息"]')!;
        await act(async () => {
            Object.getOwnPropertyDescriptor(dom.HTMLTextAreaElement.prototype, "value")!.set!.call(editor, "An unsaved queue edit draft");
            editor.dispatchEvent(new Event("input", { bubbles: true }));
        });
        await mounted.render(true);
        expect(editor.value).toBe("An unsaved queue edit draft");
        await mounted.render(false);
        await mounted.render(true);
        const reopenedComposer = mounted.aside().querySelector(".agent-composer-wrap")!;
        const reopenedQueue = reopenedComposer.querySelector<HTMLElement>('.agent-message-queue[aria-label="待发消息"]')!;
        const reopenedEditor = reopenedQueue.querySelector<HTMLTextAreaElement>('textarea[aria-label="编辑待发消息"]');
        expect({
            composerIdentity: reopenedComposer === composer,
            queueIdentity: reopenedQueue === queue,
            expanded: reopenedQueue.querySelector(".agent-message-queue-expand")!.getAttribute("aria-expanded"),
            visibleItems: reopenedQueue.querySelectorAll("li").length,
            editorIdentity: reopenedEditor === editor,
            editDraft: reopenedEditor?.value,
        }).toEqual({ composerIdentity: true, queueIdentity: true, expanded: "true", visibleItems: 2, editorIdentity: true, editDraft: "An unsaved queue edit draft" });
    } finally {
        await mounted.close();
    }
});

test("programmatic panel close dismisses the real context body portal and its focus", async () => {
    const mounted = await mount();
    try {
        const conversation = mounted.aside().querySelector("[data-agent-conversation]");
        await act(async () => {
            mounted.aside().querySelector<HTMLButtonElement>(".agent-context-ring")!.click();
            await settle();
        });
        const portal = document.body.querySelector<HTMLElement>(".agent-context-popover")!;
        expect(portal).not.toBeNull();
        expect(mounted.aside().contains(portal)).toBe(false);
        const focusTarget = portal.querySelector<HTMLElement>('[role="progressbar"]')!;
        focusTarget.tabIndex = 0;
        await act(async () => focusTarget.focus());
        expect(document.activeElement === focusTarget).toBe(true);
        await mounted.render(false);
        expect(portal.isConnected && getComputedStyle(portal).display !== "none").toBe(false);
        expect(document.activeElement === focusTarget).toBe(false);
        expect(mounted.aside().querySelector("[data-agent-conversation]") === conversation).toBe(true);
        await mounted.render(true);
        expect(mounted.aside().querySelector(".agent-context-ring")!.getAttribute("aria-expanded")).toBe("false");
    } finally {
        await mounted.close();
    }
});

test("closing retains approval state but removes its real model-picker portal", async () => {
    const mounted = await mount({ approval: true });
    try {
        const conversation = mounted.aside().querySelector("[data-agent-conversation]");
        const card = mounted.aside().querySelector(".canvas-agent-approval-card");
        expect(card).not.toBeNull();
        await act(async () => {
            mounted.aside().querySelector<HTMLButtonElement>('[aria-label="选择生成模型"]')!.click();
            await settle();
        });
        const portal = document.body.querySelector<HTMLElement>(".canvas-model-picker-popover")!;
        expect(portal).not.toBeNull();
        const target = portal.querySelector<HTMLElement>('[role="listbox"]')!;
        target.tabIndex = 0;
        await act(async () => target.focus());
        await mounted.render(false);
        expect(portal.isConnected && getComputedStyle(portal).display !== "none").toBe(false);
        expect(document.activeElement === target).toBe(false);
        await mounted.render(true);
        expect(mounted.aside().querySelector("[data-agent-conversation]") === conversation).toBe(true);
        expect(mounted.aside().querySelector(".canvas-agent-approval-card") === card).toBe(true);
        expect(mounted.aside().querySelector<HTMLTextAreaElement>(".canvas-agent-approval-reason")!.value).toBe("Keep approval reason");
    } finally {
        await mounted.close();
    }
});

test("programmatic close removes the real Skills modal and clears its open state", async () => {
    const mounted = await mount();
    try {
        const conversation = mounted.aside().querySelector("[data-agent-conversation]");
        await act(async () => {
            mounted.aside().querySelector<HTMLButtonElement>('[aria-label="打开 Skills 技能库"]')!.click();
            await settle();
        });
        const dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')!;
        expect(dialog).not.toBeNull();
        const target = document.body.querySelector<HTMLButtonElement>('[aria-label="关闭技能商店"]')!;
        await act(async () => target.focus());
        await mounted.render(false);
        expect(dialog.isConnected).toBe(false);
        expect(document.activeElement === target).toBe(false);
        await mounted.render(true);
        expect(document.body.querySelector('[role="dialog"]')).toBeNull();
        expect(mounted.aside().querySelector("[data-agent-conversation]") === conversation).toBe(true);
        expect(mounted.aside().querySelector<HTMLTextAreaElement>("textarea")!.value).toBe("A retained unsent draft");
    } finally {
        await mounted.close();
    }
});

test("programmatic close removes the settings model-picker portal", async () => {
    const mounted = await mount();
    try {
        await act(async () => {
            mounted.aside().querySelector<HTMLButtonElement>('[aria-label="Agent 设置"]')!.click();
            await settle();
        });
        await act(async () => {
            mounted.aside().querySelector<HTMLButtonElement>('[aria-label="选择文本模型"]')!.click();
            await settle();
        });
        const portal = document.body.querySelector<HTMLElement>(".canvas-model-picker-popover")!;
        expect(portal).not.toBeNull();
        const target = portal.querySelector<HTMLElement>('[role="listbox"]')!;
        target.tabIndex = 0;
        await act(async () => target.focus());
        await mounted.render(false);
        expect(portal.isConnected && getComputedStyle(portal).display !== "none").toBe(false);
        expect(document.activeElement === target).toBe(false);
    } finally {
        await mounted.close();
    }
});

for (const popup of [
    { label: "model picker", trigger: '[aria-label="选择文本模型"]', portal: ".canvas-model-picker-popover", focus: '[role="listbox"]' },
    { label: "permission dropdown", trigger: '[aria-label^="执行权限："]', portal: ".ant-dropdown", focus: '[role="menuitem"]' },
    { label: "reasoning dropdown", trigger: '[aria-label="选择 Agent 推理模式"]', portal: ".ant-dropdown", focus: '[role="menuitem"]' },
])
    test(`programmatic panel close dismisses the real ${popup.label} portal`, async () => {
        const mounted = await mount();
        try {
            const trigger = mounted.aside().querySelector<HTMLButtonElement>(popup.trigger)!;
            expect(trigger).not.toBeNull();
            await act(async () => {
                trigger.click();
                await settle();
            });
            const portal = document.body.querySelector<HTMLElement>(popup.portal)!;
            expect(portal).not.toBeNull();
            const target = portal.querySelector<HTMLElement>(popup.focus)!;
            target.tabIndex = 0;
            await act(async () => target.focus());
            expect(document.activeElement === target).toBe(true);
            await mounted.render(false);
            expect(portal.isConnected && getComputedStyle(portal).display !== "none").toBe(false);
            expect(document.activeElement === target).toBe(false);
            await mounted.render(true);
            const reopened = document.body.querySelector<HTMLElement>(popup.portal);
            expect(Boolean(reopened && getComputedStyle(reopened).display !== "none")).toBe(false);
        } finally {
            await mounted.close();
        }
    });

test("close retains the real conversation, message DOM, reading position and disclosure state", async () => {
    const mounted = await mount();
    try {
        const aside = mounted.aside();
        const conversation = aside.querySelector<HTMLElement>("[data-agent-conversation]")!;
        const message = conversation.querySelector(".agent-message-body")!;
        const disclosure = conversation.querySelector<HTMLButtonElement>(".agent-reasoning > .agent-activity-row")!;
        expect(message).not.toBeNull();
        expect(conversation.textContent).toContain("A user message");
        await act(async () => disclosure.click());
        readOlderMessages(conversation);
        const focused = aside.querySelector<HTMLTextAreaElement>("textarea")!;
        focused.focus();
        expect(document.activeElement === focused).toBe(true);
        await mounted.render(false);
        expect(mounted.aside() === aside).toBe(true);
        expect(aside.getAttribute("aria-hidden")).toBe("true");
        expect(aside.hasAttribute("inert")).toBe(true);
        expect(getComputedStyle(aside).pointerEvents).toBe("none");
        expect(aside.contains(document.activeElement)).toBe(false);
        expect(aside.querySelector("[data-agent-conversation]")).toBe(conversation);
        expect(conversation.scrollTop).toBe(175);
        await mounted.render(true);
        expect(mounted.aside().querySelector("[data-agent-conversation]")).toBe(conversation);
        expect(conversation.querySelector(".agent-message-body") === message).toBe(true);
        expect(conversation.querySelector(".agent-reasoning > .agent-activity-row")).toBe(disclosure);
        expect(disclosure.getAttribute("aria-expanded")).toBe("true");
        expect(conversation.scrollTop).toBe(175);
        expect(aside.hasAttribute("inert")).toBe(false);
        expect(aside.getAttribute("aria-hidden")).toBe("false");
    } finally {
        await mounted.close();
    }
});

test("panel visibility animates only transform and opacity and the docked shell takes the dock width", async () => {
    const mounted = await mount();
    try {
        const aside = mounted.aside();
        const contract = () => JSON.parse(aside.getAttribute("data-motion-contract")!);
        for (const state of [contract().initial, contract().animate, contract().exit]) {
            if (!state || typeof state !== "object") continue;
            expect(Object.keys(state).every((key) => ["x", "y", "opacity", "transform"].includes(key))).toBe(true);
        }
        expect(aside.classList.contains("is-docked")).toBe(true);
        expect(aside.style.width).toBe("440px");
        expect(aside.getAttribute("data-agent-docked")).toBe("true");
        expect(aside.style.right).toBe("0px");
        expect(contract().animate.opacity).toBe(1);
        await mounted.render(false);
        expect(contract().animate.opacity).toBe(0);
        expect(contract().animate.x).not.toBe(0);
        expect(Object.keys(contract().animate).every((key) => ["x", "y", "opacity", "transform"].includes(key))).toBe(true);
        expect(aside.style.width).toBe("440px");
    } finally {
        await mounted.close();
    }
});

test("reopening preserves the reading position without restoring to the end", async () => {
    const mounted = await mount();
    try {
        const conversation = mounted.aside().querySelector<HTMLElement>("[data-agent-conversation]")!;
        readOlderMessages(conversation);
        await mounted.render(false);
        await mounted.render(true);
        expect(mounted.aside().querySelector<HTMLElement>("[data-agent-conversation]")!.scrollTop).toBe(175);
    } finally {
        await mounted.close();
    }
});

test("reopening preserves the real reasoning component's local disclosure state", async () => {
    const mounted = await mount();
    try {
        await act(async () => mounted.aside().querySelector<HTMLButtonElement>(".agent-reasoning > .agent-activity-row")!.click());
        await mounted.render(false);
        await mounted.render(true);
        expect(mounted.aside().querySelector(".agent-reasoning > .agent-activity-row")!.getAttribute("aria-expanded")).toBe("true");
    } finally {
        await mounted.close();
    }
});

test("manual drag changes the real dock width and closing releases an active pointer capture", async () => {
    const mounted = await mount();
    try {
        const aside = mounted.aside();
        const handle = aside.querySelector<HTMLElement>('[data-agent-resize="dock"]')!;
        expect(handle.getAttribute("role")).toBe("separator");
        const captures = new Set<number>();
        // 上游外壳在面板根元素上捕获指针。
        Object.assign(aside, {
            setPointerCapture: (id: number) => captures.add(id),
            hasPointerCapture: (id: number) => captures.has(id),
            releasePointerCapture: (id: number) => captures.delete(id),
        });
        await act(async () => {
            handle.dispatchEvent(new dom.PointerEvent("pointerdown", { bubbles: true, pointerId: 7, button: 0, isPrimary: true, clientX: 1000 }));
        });
        await act(async () => {
            handle.dispatchEvent(new dom.PointerEvent("pointermove", { bubbles: true, pointerId: 7, clientX: 940 }));
        });
        expect(useGlobalAgentStore.getState().dock.width).toBe(500);
        expect(aside.style.width).toBe("500px");
        await mounted.render(false);
        expect(captures.has(7)).toBe(false);
        await mounted.render(true);
        await act(async () => new Promise((resolve) => setTimeout(resolve, 200)));
        expect(JSON.parse(window.localStorage.getItem("canvas:agent-panel-dock:v1")!)).toEqual({ docked: true, width: 500 });
        expect(window.localStorage.getItem("canvas-agent-panel-width")).toBeNull();
    } finally {
        await mounted.close();
    }
});

test("compact toolbar hides only while the retained panel is open", async () => {
    const mounted = await mount({ compact: true });
    try {
        const aside = mounted.aside();
        expect(aside.hasAttribute("data-agent-compact")).toBe(true);
        expect(toolbarVisibility(mounted.host)).toBe("hidden");
        await mounted.render(false);
        expect(mounted.aside() === aside).toBe(true);
        expect(toolbarVisibility(mounted.host)).not.toBe("hidden");
        await mounted.render(true);
        expect(toolbarVisibility(mounted.host)).toBe("hidden");
    } finally {
        await mounted.close();
    }
});

test("new conversation still replaces the keyed conversation subtree", async () => {
    const mounted = await mount();
    try {
        const conversation = mounted.aside().querySelector("[data-agent-conversation]");
        const button = mounted.aside().querySelector<HTMLButtonElement>('[aria-label="新建对话"]')!;
        expect(button).not.toBeNull();
        await act(async () => {
            button.click();
            await settle();
        });
        expect(mounted.aside().querySelector("[data-agent-conversation]")).not.toBe(conversation);
        expect(mounted.aside().textContent).not.toContain("A user message");
    } finally {
        await mounted.close();
    }
});

test("floating reduced-motion panel retains content and uses the window layout instead of the dock", async () => {
    const mounted = await mount({ docked: false, reduced: true });
    try {
        const aside = mounted.aside();
        expect(aside.classList.contains("is-docked")).toBe(false);
        expect(aside.querySelector('[data-agent-resize="dock"]')).toBeNull();
        expect(aside.querySelector('[data-agent-resize="northwest"]')).not.toBeNull();
        expect(aside.classList.contains("fixed")).toBe(true);
        expect(aside.hasAttribute("data-agent-docked")).toBe(false);
        expect(aside.style.left).not.toBe("");
        expect(JSON.parse(aside.getAttribute("data-motion-contract")!).transition.duration).toBe(0);
        await mounted.render(false);
        expect(mounted.aside() === aside).toBe(true);
        expect(aside.hasAttribute("inert")).toBe(true);
    } finally {
        await mounted.close();
    }
});

test("viewport below 960px falls back to the floating window and hides the dock toggle", async () => {
    const mounted = await mount();
    try {
        await act(async () => {
            dom.happyDOM.setWindowSize({ width: 900, height: 900 });
            window.dispatchEvent(new Event("resize"));
            await settle();
        });
        const aside = mounted.aside();
        expect(aside.classList.contains("is-docked")).toBe(false);
        expect(aside.querySelector('[aria-label="停靠到画布右侧"]')).toBeNull();
        expect(aside.querySelector('[aria-label="改为浮动窗口"]')).toBeNull();
    } finally {
        await mounted.close();
    }
});

test("dock toggle uses aria-pressed and switches between docked and floating", async () => {
    const mounted = await mount();
    try {
        // 标签固定，状态只由 aria-pressed 表达。
        const toggle = () => mounted.aside().querySelector<HTMLButtonElement>('[aria-label="停靠到画布右侧"]')!;
        expect(mounted.aside().querySelector('[aria-label="改为浮动窗口"]')).toBeNull();
        expect(toggle().getAttribute("aria-pressed")).toBe("true");
        await act(async () => {
            toggle().click();
            await settle();
        });
        expect(mounted.aside().classList.contains("is-docked")).toBe(false);
        expect(toggle().getAttribute("aria-pressed")).toBe("false");
    } finally {
        await mounted.close();
    }
});

test("off canvas the panel keeps the conversation readable but disables the composer with the canvas hint", async () => {
    const mounted = await mount();
    try {
        await act(async () => {
            useGlobalAgentStore.getState().setCanvas(null);
            await settle();
        });
        const aside = mounted.aside();
        expect(aside.textContent).toContain("进入画布后使用 Agent");
        expect(aside.textContent).toContain("A user message");
        expect(aside.classList.contains("is-docked")).toBe(false);
        expect(aside.querySelector('[aria-label="停靠到画布右侧"]')).toBeNull();
    } finally {
        await mounted.close();
    }
});
