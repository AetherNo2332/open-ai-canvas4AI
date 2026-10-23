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
const refreshFromService = history.refreshCloudAgentConversations;
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
    sendAgentMessage: async () => ({ run: { ...conversation("local-child").run!, parentId: "desktop", status: "running", createdAt: "2026-10-23T00:01:00Z" } }),
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
    saveCloudAgentPendingSubmission: async (_canvas: string, _id: string, value: CloudAgentPendingSubmission) => { pending = value; },
    clearCloudAgentPendingSubmission: async () => { pending = null; },
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
async function mount(initial: CloudAgentConversation[] = [], initialPending: CloudAgentPendingSubmission | null = null, loader?: typeof refreshOverride) {
    local = initial; remote = [conversation("desktop")]; pending = initialPending; refreshOverride = loader;
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

test("a delayed parent directory result cannot close the SSE of an accepted local child", async () => {
    const mounted = await mount([conversation("desktop", "local")]);
    try {
        let finish!: (value: CloudAgentConversation[]) => void;
        refreshOverride = () => new Promise((resolve) => { finish = resolve; });
        await focus();
        await act(async () => { composer.onPromptChange("继续"); await settle(); });
        await act(async () => { composer.onSubmit(); await settle(); });
        expect(subscriptions.at(-1)?.id).toBe("local-child");
        expect(pending).toBeNull();
        await act(async () => { finish([conversation("desktop")]); await settle(); });
        expect(subscriptions.at(-1)).toMatchObject({ id: "local-child", closed: false });
        expect(subscriptions.map((item) => item.id)).toEqual(["desktop", "local-child"]);
    } finally { await mounted.close(); }
});

test("the real history service restores a legacy tail with a visible limitation while discovering healthy conversations", async () => {
    let restored: CloudAgentConversation[] = [];
    const mounted = await mount([], null, async (options) => {
        const snapshot = (id: string): AgentRun => ({ ...conversation(id).run!, id, conversationId: id, eventSeqBase: id === "legacy" ? 2 : 0, eventCount: id === "legacy" ? 3 : 0,
            events: id === "legacy" ? [{ eventId: "legacy:3", runId: id, seq: 3, type: "assistant_message", payload: { messageId: "tail", text: "可用旧记录" }, createdAt: "2026-10-23T00:00:00Z" }] : [] });
        restored = await refreshFromService({ ...options, client: {
            listConversations: async () => ({ items: ["legacy", "healthy"].map((id) => ({ id, canvasId: "canvas", title: id, latestRunId: id, status: "completed", revision: 1, createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z" })) }),
            listRuns: async (id: string) => ({ items: [snapshot(id)] }), getRun: async (id: string) => ({ run: snapshot(id) }),
        } });
        return restored;
    });
    try {
        expect(restored).toHaveLength(2);
        expect(mounted.host.textContent).toContain("可用旧记录");
        expect(mounted.host.textContent).toContain("早期记录");
        expect(subscriptions.at(-1)).toMatchObject({ id: "legacy", after: 3 });
        expect(composer.disabled).toBe(false);
    } finally { await mounted.close(); }
});

test("one unavailable conversation keeps its draft and blocks continuation until a successful history retry", async () => {
    let broken = true;
    const mounted = await mount([conversation("desktop", "local", "保留草稿")], null, (options) => refreshFromService({ ...options, client: {
        listConversations: async () => ({ items: [{ id: "shared", canvasId: "canvas", title: "对话", latestRunId: "desktop", status: "completed", revision: 2, createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z" }] }),
        listRuns: async () => ({ items: [{ ...conversation("desktop").run!, revision: 2 }] }),
        getRun: async () => ({ run: { ...conversation("desktop").run!, revision: 2, eventSeqBase: 0, eventCount: broken ? 2 : 0,
            events: broken ? [{ eventId: "desktop:2", runId: "desktop", seq: 2, type: "assistant_message", payload: { text: "坏页" }, createdAt: "2026-10-23T00:00:00Z" }] : [] } }),
    } }));
    try {
        expect(mounted.host.textContent).toContain("暂时无法恢复");
        expect(composer.prompt).toBe("保留草稿");
        expect(composer.disabled).toBe(true);
        await act(async () => { composer.onSubmit(); await settle(); });
        expect(subscriptions.at(-1)?.id).toBe("desktop");
        expect(pending).toBeNull();
        broken = false;
        await focus();
        expect(mounted.host.textContent).not.toContain("暂时无法恢复");
        expect(composer.prompt).toBe("保留草稿");
        expect(composer.disabled).toBe(false);
    } finally { await mounted.close(); }
});
