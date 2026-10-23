import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";
import type { AgentEvent, AgentRun } from "@/services/api/agent";
import type { CloudAgentConversation, CloudAgentPendingSubmission } from "@/services/cloud-agent-conversations";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({
    window: dom, document: dom.document, navigator: dom.navigator, localStorage: dom.localStorage,
    HTMLElement: dom.HTMLElement, SVGElement: dom.SVGElement, Element: dom.Element, Node: dom.Node,
    Event: dom.Event, CustomEvent: dom.CustomEvent, ShadowRoot: dom.ShadowRoot,
    MutationObserver: dom.MutationObserver, ResizeObserver: dom.ResizeObserver,
    getComputedStyle: dom.getComputedStyle.bind(dom), IS_REACT_ACT_ENVIRONMENT: true,
})) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const agent = await import("@/services/api/agent");
const cache = await import("@/services/cloud-agent-conversations");
const history = await import("@/services/cloud-agent-history");
const chatUI = await import("@/components/canvas/canvas-cloud-agent-chat-ui");
const { setActiveUserScope } = await import("@/lib/user-scope");
const { useUserStore } = await import("@/stores/use-user-store");
const { createModelChannel, defaultConfig, normalizeConfigSnapshot, useConfigStore } = await import("@/stores/use-config-store");

let local: CloudAgentConversation[] = [];
let remote: CloudAgentConversation[] = [];
let pending: CloudAgentPendingSubmission | null = null;
let composer: any;
let refreshOverride: ((options: any) => Promise<CloudAgentConversation[]>) | undefined;
const subscriptions: Array<{ id: string; after: number; closed: boolean; receive: (event: AgentEvent) => void }> = [];
const reads: Array<{ canvasId: string; scope: string; signal: AbortSignal }> = [];
const silent = () => {};
mock.module("file-saver", () => ({ saveAs: silent }));
const Box = ({ children }: any) => <div>{children}</div>;
mock.module("motion/react", () => ({ AnimatePresence: Box, motion: { aside: Box, div: Box }, useReducedMotion: () => true }));
mock.module("antd", () => ({ Button: ({ children, icon, onClick }: any) => <button onClick={onClick}>{icon}{children}</button>, Dropdown: Box, Popover: Box, Input: Object.assign(Box, { TextArea: Box }) }));
mock.module("@/services/api/agent", () => ({
    ...agent,
    getAgentCapabilities: async () => ({ permissionModes: ["auto"], skills: true, availability: { configured: true } }),
    listAgentSkillDefaults: async () => ({ count: 0, skills: [] }),
    subscribeAgentEvents: (id: string, receive: (event: AgentEvent) => void, options: any) => {
        const item = { id, receive, after: options.after, closed: false };
        subscriptions.push(item);
        options.onConnectionChange("connected");
        return () => { item.closed = true; };
    },
}));
mock.module("@/services/cloud-agent-conversations", () => ({
    ...cache,
    loadCloudAgentConversations: async (_canvas: string, scope: string) => ({ version: 1, activeId: scope === "sync-user" ? local[0]?.id : null, conversations: scope === "sync-user" ? local : [] }),
    saveCloudAgentConversations: async (_canvas: string, _id: string, items: CloudAgentConversation[], scope: string) => { if (scope === "sync-user") local = items; },
    loadCloudAgentPendingSubmission: async (_canvas: string, _id: string, scope: string) => scope === "sync-user" ? pending : null,
}));
mock.module("@/services/cloud-agent-history", () => ({
    ...history,
    refreshCloudAgentConversations: async (options: any) => {
        reads.push(options);
        return refreshOverride ? refreshOverride(options) : options.scope === "sync-user" ? remote : [];
    },
}));
mock.module("@/services/api/skills", () => ({
    listSkillPresets: async () => ({ presets: [] }), listSkills: async () => ({ skills: [] }),
    listAddedSkills: async () => ({ skills: [] }), addSkill: silent,
}));
mock.module("@/services/user-data-sync", () => ({ saveRemoteUserDataNow: async () => {}, applyAgentCanvasPatches: async () => {}, refreshCanvasAfterAgent: async () => {} }));
mock.module("@/components/canvas/canvas-cloud-agent-chat-ui", () => ({
    ...chatUI, AGENT_SCENE_DEFS: [],
    AgentChatComposer: (props: any) => { composer = props; return <textarea value={props.prompt} readOnly />; },
    AgentChatMessage: ({ item }: any) => <div>{item.text}</div>,
    AgentOperationFeed: () => null, AgentReasoningFeed: () => null, AgentWorkingMessage: () => null,
    AgentPlanBar: () => null, AgentQuestionBar: () => null, AgentSceneCapsules: () => null,
}));
mock.module("@/components/canvas/canvas-agent-skill-library-modal", () => ({ CanvasAgentSkillLibraryModal: () => null }));
mock.module("@/components/canvas/canvas-cloud-agent-settings", () => ({
    CanvasCloudAgentSettings: () => null, agentPermissionLabel: () => "自动", agentPermissionMenuItems: () => [],
    agentPermissionVisual: () => ({ icon: () => null }),
}));
mock.module("@/components/canvas/use-agent-panel-layout", () => ({ useAgentPanelLayout: () => ({ style: {}, pointerHandlers: {}, reset: silent, onResizeKeyDown: silent }) }));
mock.module("@/components/canvas/live2d-avatar", () => ({ Live2DAvatar: () => null }));
mock.module("@/components/ui/fluid-orb", () => ({ FluidOrb: () => null }));
const { CanvasCloudAgentPanel } = await import("@/components/canvas/canvas-cloud-agent-panel");

function conversation(runId: string, id = "server", draft = ""): CloudAgentConversation {
    const run: AgentRun = { id: runId, conversationId: "shared", canvasId: "canvas", status: "completed", permissionMode: "auto", revision: 1, createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z" };
    return { id, serverConversationId: "shared", run, runs: [run], lastEventSeq: 4, draft, title: "对话", messages: [{ id: `answer-${runId}`, role: "assistant", text: `结果-${runId}` }], permissionMode: "auto", createdAt: run.createdAt, updatedAt: run.updatedAt };
}
const settle = async () => { for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 10)); };
async function focus() { await act(async () => { window.dispatchEvent(new Event("focus")); await settle(); }); }
async function mount(initial: CloudAgentConversation[] = [], initialPending: CloudAgentPendingSubmission | null = null) {
    local = initial; remote = [conversation("desktop")]; pending = initialPending; refreshOverride = undefined;
    subscriptions.length = 0; reads.length = 0;
    setActiveUserScope("sync-user");
    useUserStore.setState({ hydrated: true, user: { id: "sync-user", username: "test", displayName: "test", role: "user", status: "active" }, features: { agentSubagentsEnabled: false } as any });
    const channel = createModelChannel({ id: "test", scope: "system", models: ["text"] });
    useConfigStore.setState({ config: normalizeConfigSnapshot({ config: { ...defaultConfig, channels: [channel], model: "test::text", textModel: "test::text" } }).config });
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(<CanvasCloudAgentPanel canvasId="canvas" nodeCount={1} selectedNodeIds={[]} references={[]} open onOpen={silent} onCollapse={silent} />); await settle(); });
    return { host, close: async () => { await act(async () => root.unmount()); host.remove(); } };
}
afterAll(() => dom.happyDOM.abort());

test("empty cache discovers a cloud conversation and resumes SSE after restored events", async () => {
    const mounted = await mount();
    try {
        expect(mounted.host.textContent).toContain("结果-desktop");
        expect(subscriptions.at(-1)).toMatchObject({ id: "desktop", after: 4 });
        expect(reads[0]).toMatchObject({ canvasId: "canvas", scope: "sync-user" });
    } finally { await mounted.close(); }
});
test("focus discovers a new child and closes the previous run observation while retaining the draft", async () => {
    const mounted = await mount([conversation("desktop", "local", "未发送草稿")]);
    try {
        remote = [conversation("phone-child")];
        await focus();
        expect(mounted.host.textContent).toContain("结果-phone-child");
        expect(composer.prompt).toBe("未发送草稿");
        expect(subscriptions.find((item) => item.id === "desktop")?.closed).toBe(true);
        expect(subscriptions.at(-1)?.id).toBe("phone-child");
    } finally { await mounted.close(); }
});
test("an unknown POST preserves the original local parent and request draft during cloud refresh", async () => {
    const request = { canvasId: "canvas", prompt: "结果待确认", idempotencyKey: "pending-original-key" };
    const mounted = await mount([conversation("desktop", "local")], { key: request.idempotencyKey, fingerprint: "original", parentRunId: "desktop", request });
    try {
        remote = [conversation("phone-child")];
        await focus();
        expect(composer.prompt).toBe("结果待确认");
        expect(subscriptions.at(-1)?.id).toBe("desktop");
        expect(pending?.key).toBe("pending-original-key");
    } finally { await mounted.close(); }
});
test("switching accounts aborts the old directory request and prevents its late result from appearing", async () => {
    const mounted = await mount([conversation("desktop", "local")]);
    try {
        let finish!: (value: CloudAgentConversation[]) => void;
        refreshOverride = (options) => options.scope === "sync-user" ? new Promise((resolve) => { finish = resolve; }) : Promise.resolve([]);
        await focus();
        const request = reads.at(-1)!;
        await act(async () => { setActiveUserScope("other"); useUserStore.setState({ user: { ...useUserStore.getState().user!, id: "other" } }); await settle(); });
        expect(request.signal.aborted).toBe(true);
        await act(async () => { finish([conversation("late-old-account")]); await settle(); });
        expect(mounted.host.textContent).not.toContain("结果-late-old-account");
    } finally { await mounted.close(); }
});
