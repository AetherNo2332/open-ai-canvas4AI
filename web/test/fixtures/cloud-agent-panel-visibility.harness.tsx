import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";
import type { CloudAgentConversation } from "@/services/cloud-agent-conversations";

const dom = new Window({ url: "http://localhost/", width: 1440, height: 900 });
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, localStorage: dom.localStorage, HTMLElement: dom.HTMLElement, SVGElement: dom.SVGElement, Element: dom.Element, Node: dom.Node, Event: dom.Event, CustomEvent: dom.CustomEvent, ShadowRoot: dom.ShadowRoot, MutationObserver: dom.MutationObserver, ResizeObserver: dom.ResizeObserver, getComputedStyle: dom.getComputedStyle.bind(dom), IS_REACT_ACT_ENVIRONMENT: true }))
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const agent = await import("@/services/api/agent");
const cache = await import("@/services/cloud-agent-conversations");
const history = await import("@/services/cloud-agent-history");
const { setActiveUserScope } = await import("@/lib/user-scope");
const { useUserStore } = await import("@/stores/use-user-store");
let reducedMotion = false;
const silent = () => {};
const Box = ({ children }: any) => <div>{children}</div>;
// Retain DOM props, children and refs; expose the exact animation contract emitted by production.
const motionTags = new Map<string, any>();
mock.module("motion/react", () => ({
    AnimatePresence: ({ children }: any) => <>{children}</>,
    useReducedMotion: () => reducedMotion,
    motion: new Proxy({}, { get: (_target, tag: string) => {
        if (!motionTags.has(tag)) motionTags.set(tag, ({ initial, animate, exit, transition, whileHover, whileTap, layout, ...props }: any) => React.createElement(tag, { ...props, "data-motion-contract": JSON.stringify({ initial, animate, exit, transition, layout }) }));
        return motionTags.get(tag);
    } }),
}));
mock.module("antd", () => ({ Button: ({ children, icon, onClick, ...props }: any) => <button onClick={onClick} aria-label={props["aria-label"]}>{icon}{children}</button>, Dropdown: Box, Popover: Box, Typography: { Text: Box }, Input: Object.assign(Box, { TextArea: Box }) }));
mock.module("file-saver", () => ({ saveAs: silent }));
mock.module("@/components/ai/ai-message-markdown", () => ({ AIMessageMarkdown: ({ children, content }: any) => <div>{children || content}</div> }));
mock.module("@/components/canvas/canvas-resource-mention-textarea", () => ({ CanvasResourceMentionTextarea: ({ value, onChange }: any) => <textarea value={value} onChange={onChange} /> }));
mock.module("@/components/model-picker", () => ({ ModelPicker: () => null }));
mock.module("@/components/canvas/live2d-avatar", () => ({ Live2DAvatar: () => null }));
mock.module("@/components/ui/fluid-orb", () => ({ FluidOrb: () => null }));
mock.module("@/components/canvas/canvas-agent-skill-library-modal", () => ({ CanvasAgentSkillLibraryModal: () => null }));
mock.module("@/services/api/agent", () => ({ ...agent, getAgentUISettings: async () => ({ defaultSendMode: "queue", revision: 1 }), getAgentCapabilities: async () => ({ permissionModes: ["auto"], skills: true, availability: { configured: true } }), listAgentSkillDefaults: async () => ({ count: 0, skills: [] }) }));
const savedConversation: CloudAgentConversation = { id: "conversation-one", title: "Test conversation", messages: [
    { id: "user-one", role: "user", text: "A user message" },
    { id: "reasoning-one", role: "assistant", text: "A retained reasoning detail", reasoning: true },
    { id: "answer-one", role: "assistant", text: "An assistant message" },
], run: null, draft: "", permissionMode: "auto", createdAt: "2026-10-25T00:00:00Z", updatedAt: "2026-10-25T00:00:00Z" };
mock.module("@/services/cloud-agent-conversations", () => ({ ...cache, loadCloudAgentConversations: async () => ({ version: 1, activeId: savedConversation.id, conversations: [savedConversation] }), saveCloudAgentConversations: async () => {}, loadCloudAgentPendingSubmission: async () => null }));
mock.module("@/services/cloud-agent-history", () => ({ ...history, refreshCloudAgentConversations: async () => [] }));
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
const settle = async () => { for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 5)); };
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
async function mount({ compact = false, floating = false, reduced = false } = {}) {
    reducedMotion = reduced;
    dom.happyDOM.setWindowSize({ width: compact ? 700 : 1440, height: 900 });
    window.localStorage.clear();
    setActiveUserScope("visibility-user");
    useUserStore.setState({ hydrated: true, user: { id: "visibility-user", username: "test", displayName: "test", role: "user", status: "active" }, features: { agentSubagentsEnabled: false } as any });
    const host = document.createElement("div");
    host.className = "canvas-editor-shell";
    document.body.append(host);
    const root = createRoot(host);
    const render = async (open: boolean) => act(async () => {
        root.render(<><div className="canvas-main-toolbar">Toolbar</div><CanvasCloudAgentPanel canvasId="canvas" nodeCount={1} selectedNodeIds={[]} references={[]} open={open} floating={floating} launcher={false} onOpen={silent} onCollapse={silent} /></>);
        await settle();
    });
    await render(true);
    return { host, render, aside: () => (floating ? document.body : host).querySelector<HTMLElement>("aside.canvas-agent-panel")!, close: async () => { await act(async () => root.unmount()); host.remove(); } };
}
afterAll(() => dom.happyDOM.abort());

test("close retains the real conversation, message DOM, reading position and disclosure state", async () => {
    const mounted = await mount();
    try {
        const aside = mounted.aside();
        const conversation = aside.querySelector<HTMLElement>("[data-agent-conversation]")!;
        const message = conversation.querySelector(".agent-message-body")!;
        const disclosure = conversation.querySelector<HTMLButtonElement>(".agent-reasoning-summary")!;
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
        expect(conversation.querySelector(".agent-reasoning-summary")).toBe(disclosure);
        expect(disclosure.getAttribute("aria-expanded")).toBe("true");
        expect(conversation.scrollTop).toBe(175);
        expect(aside.hasAttribute("inert")).toBe(false);
        expect(aside.getAttribute("aria-hidden")).toBe("false");
    } finally { await mounted.close(); }
});

test("panel visibility animates only transform and opacity while dock width changes immediately", async () => {
    const mounted = await mount();
    try {
        const aside = mounted.aside();
        const contract = () => JSON.parse(aside.getAttribute("data-motion-contract")!);
        for (const state of [contract().initial, contract().animate, contract().exit]) {
            if (!state || typeof state !== "object") continue;
            expect(Object.keys(state).every((key) => ["x", "opacity", "transform"].includes(key))).toBe(true);
        }
        const slot = aside.parentElement!;
        expect(slot.style.width).toBe("440px");
        expect(slot.style.flexBasis).toBe("440px");
        expect(contract().animate.opacity).toBe(1);
        await mounted.render(false);
        expect(slot.style.width).toBe("0px");
        expect(slot.style.flexBasis).toBe("0px");
        expect(contract().animate.opacity).toBe(0);
        expect(contract().animate.x).not.toBe(0);
        expect(Object.keys(contract().animate).every((key) => ["x", "opacity", "transform"].includes(key))).toBe(true);
        expect(aside.style.width).toBe("440px");
    } finally { await mounted.close(); }
});

test("reopening preserves the reading position without restoring to the end", async () => {
    const mounted = await mount();
    try {
        const conversation = mounted.aside().querySelector<HTMLElement>("[data-agent-conversation]")!;
        readOlderMessages(conversation);
        await mounted.render(false);
        await mounted.render(true);
        expect(mounted.aside().querySelector<HTMLElement>("[data-agent-conversation]")!.scrollTop).toBe(175);
    } finally { await mounted.close(); }
});

test("reopening preserves the real reasoning component's local disclosure state", async () => {
    const mounted = await mount();
    try {
        await act(async () => mounted.aside().querySelector<HTMLButtonElement>(".agent-reasoning-summary")!.click());
        await mounted.render(false);
        await mounted.render(true);
        expect(mounted.aside().querySelector(".agent-reasoning-summary")!.getAttribute("aria-expanded")).toBe("true");
    } finally { await mounted.close(); }
});

test("manual drag changes real dock width and closing releases an active pointer capture", async () => {
    const mounted = await mount();
    try {
        const aside = mounted.aside();
        const handle = aside.querySelector<HTMLElement>('[role="separator"]')!;
        const captures = new Set<number>();
        Object.assign(handle, {
            setPointerCapture: (id: number) => captures.add(id),
            hasPointerCapture: (id: number) => captures.has(id),
            releasePointerCapture: (id: number) => { captures.delete(id); handle.dispatchEvent(new dom.PointerEvent("lostpointercapture", { bubbles: true, pointerId: id })); },
        });
        await act(async () => {
            handle.dispatchEvent(new dom.PointerEvent("pointerdown", { bubbles: true, pointerId: 7, button: 0, clientX: 1000 }));
        });
        await act(async () => {
            handle.dispatchEvent(new dom.PointerEvent("pointermove", { bubbles: true, pointerId: 7, clientX: 940 }));
        });
        expect(aside.parentElement!.style.width).toBe("500px");
        expect(aside.style.width).toBe("500px");
        await mounted.render(false);
        expect(captures.has(7)).toBe(false);
        await mounted.render(true);
        const contract = JSON.parse(mounted.aside().getAttribute("data-motion-contract")!);
        expect(contract.transition.duration).toBeGreaterThan(0);
        expect(window.localStorage.getItem("canvas-agent-panel-width")).toBe("500");
    } finally { await mounted.close(); }
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
    } finally { await mounted.close(); }
});

test("new conversation still replaces the keyed conversation subtree", async () => {
    const mounted = await mount();
    try {
        const conversation = mounted.aside().querySelector("[data-agent-conversation]");
        const button = mounted.aside().querySelector<HTMLButtonElement>('[aria-label="新建对话"]')!;
        expect(button).not.toBeNull();
        await act(async () => { button.click(); await settle(); });
        expect(mounted.aside().querySelector("[data-agent-conversation]")).not.toBe(conversation);
        expect(mounted.aside().textContent).not.toContain("A user message");
    } finally { await mounted.close(); }
});

test("floating reduced-motion panel retains content and takes no dock space", async () => {
    const mounted = await mount({ floating: true, reduced: true });
    try {
        const aside = mounted.aside();
        expect(aside.hasAttribute("data-agent-floating")).toBe(true);
        expect(getComputedStyle(aside).position).toBe("fixed");
        expect(aside.parentElement!.style.width).toBe("0px");
        expect(JSON.parse(aside.getAttribute("data-motion-contract")!).transition.duration).toBe(0);
        await mounted.render(false);
        expect(mounted.aside() === aside).toBe(true);
        expect(aside.hasAttribute("inert")).toBe(true);
    } finally { await mounted.close(); }
});
