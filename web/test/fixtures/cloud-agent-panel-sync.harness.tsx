import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";
import type { AgentConversationMessageInput, AgentEvent, AgentMessageReceipt, AgentQueuedMessage, AgentRun, AgentSendMode, CreateAgentRunInput } from "@/services/api/agent";
import type { CanvasCloudAgentPanelHandle } from "@/components/canvas/canvas-cloud-agent-panel";
import type { CloudAgentConversation, CloudAgentPendingSubmission } from "@/services/cloud-agent-conversations";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    localStorage: dom.localStorage,
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
}))
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const agent = await import("@/services/api/agent");
const getRunFromService = agent.getAgentRun;
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
let sendMessageOverride: ((parentId: string, request: CreateAgentRunInput) => AgentRun | Promise<AgentRun>) | undefined;
let createRunOverride: (() => Promise<AgentRun>) | undefined;
let interjectionOverride: (() => Promise<void>) | undefined;
const sentRequests: Array<{ parentId: string; request: CreateAgentRunInput }> = [];
const interjections: Array<{ runId: string; text: string }> = [];
const createdRequests: CreateAgentRunInput[] = [];
const conversationRequests: AgentConversationMessageInput[] = [];
const queueItems: AgentQueuedMessage[] = [];
const acceptedRuns = new Map<string, AgentRun>();
let serverSendMode: AgentSendMode = "interject";
let queueMessageOverride: ((input: AgentConversationMessageInput) => Promise<AgentMessageReceipt>) | undefined;
let awaitingQuestionId = "";
let questionBar: any;
let refreshOverride: ((options: any) => Promise<CloudAgentConversation[]>) | undefined;
const subscriptions: Array<{ id: string; after: number; closed: boolean; receive: (event: AgentEvent) => void }> = [];
const reads: Array<{ canvasId: string; scope: string; signal: AbortSignal }> = [];
const silent = () => {};
mock.module("file-saver", () => ({ saveAs: silent }));
const Box = ({ children }: any) => <div>{children}</div>;
mock.module("motion/react", () => ({ AnimatePresence: Box, motion: { aside: Box, div: Box }, useReducedMotion: () => true }));
mock.module("antd", () => ({
    Button: ({ children, icon, onClick }: any) => (
        <button onClick={onClick}>
            {icon}
            {children}
        </button>
    ),
    Dropdown: Box,
    Popover: Box,
    Input: Object.assign(Box, { TextArea: Box }),
}));
mock.module("@/services/api/agent", () => ({
    ...agent,
    getAgentUISettings: async () => ({ defaultSendMode: serverSendMode, revision: 1 }),
    updateAgentUISettings: async (input: any) => {
        serverSendMode = input.defaultSendMode;
        return { ...input, revision: input.revision + 1 };
    },
    getAgentMessageQueue: async (id: string) => ({ conversationId: id, canvasId: "canvas", revision: 1, paused: false, awaitingQuestionId, latestRunId: local[0]?.run?.id || "desktop", items: [...queueItems] }),
    getAgentRun: async (id: string, ...args: any[]) => (acceptedRuns.has(id) ? { run: acceptedRuns.get(id)! } : getRunFromService(id, ...args)),
    sendAgentConversationMessage: async (id: string, input: AgentConversationMessageInput) => {
        conversationRequests.push(structuredClone(input));
        if (queueMessageOverride) return queueMessageOverride(input);
        const messageId = `message-${input.request.idempotencyKey}`;
        if (input.mode === "queue") {
            queueItems.push({ id: messageId, conversationId: id, canvasId: "canvas", prompt: input.request.prompt, revision: 1, sequence: queueItems.length + 1, mode: "queue", status: "queued", createdAt: "", updatedAt: "" });
            return { messageId, conversationId: id, queueRevision: 1, delivery: "queued" };
        }
        if (local[0]?.run?.status === "running") {
            interjections.push({ runId: input.expectedRunId, text: input.request.prompt });
            await interjectionOverride?.();
            return { messageId, conversationId: id, queueRevision: 1, delivery: "interjected", runId: input.expectedRunId };
        }
        sentRequests.push({ parentId: input.expectedRunId, request: input.request });
        const next = sendMessageOverride ? await sendMessageOverride(input.expectedRunId, input.request) : { ...conversation("local-child").run!, status: "running" as const, parentId: input.expectedRunId, createdAt: "2026-10-23T00:01:00Z" };
        acceptedRuns.set(next.id, next);
        return { messageId, conversationId: id, queueRevision: 1, delivery: "started", runId: next.id };
    },
    sendAgentMessage: async (parentId: string, request: CreateAgentRunInput) => {
        sentRequests.push({ parentId, request });
        return { run: sendMessageOverride ? await sendMessageOverride(parentId, request) : { ...conversation("local-child").run!, parentId, status: "running", createdAt: "2026-10-23T00:01:00Z" } };
    },
    createAgentRun: async (request: CreateAgentRunInput) => {
        createdRequests.push(request);
        return { run: createRunOverride ? await createRunOverride() : { ...conversation("first-run").run!, status: "running" } };
    },
    sendAgentInterjection: async (runId: string, request: { text: string }) => {
        interjections.push({ runId, text: request.text });
        await interjectionOverride?.();
    },
    getAgentCapabilities: async () => ({ permissionModes: ["auto"], skills: true, availability: { configured: true } }),
    listAgentSkillDefaults: async () => ({ count: 0, skills: [] }),
    subscribeAgentEvents: (id: string, receive: (event: AgentEvent) => void, options: any) => {
        const item = { id, receive, after: options.after, closed: false };
        subscriptions.push(item);
        options.onConnectionChange("connected");
        return () => {
            item.closed = true;
        };
    },
}));
mock.module("@/services/cloud-agent-conversations", () => ({
    ...cache,
    saveCloudAgentPendingSubmission: async (_canvas: string, _id: string, value: CloudAgentPendingSubmission) => {
        pending = value;
    },
    clearCloudAgentPendingSubmission: async () => {
        pending = null;
    },
    loadCloudAgentConversations: async (_canvas: string, scope: string) => ({ version: 1, activeId: scope === "sync-user" ? local[0]?.id : null, conversations: scope === "sync-user" ? local : [] }),
    saveCloudAgentConversations: async (_canvas: string, _id: string, items: CloudAgentConversation[], scope: string) => {
        if (scope === "sync-user") local = items;
    },
    loadCloudAgentPendingSubmission: async (_canvas: string, _id: string, scope: string) => (scope === "sync-user" ? pending : null),
}));
mock.module("@/services/cloud-agent-history", () => ({
    ...history,
    refreshCloudAgentConversations: async (options: any) => {
        reads.push(options);
        return refreshOverride ? refreshOverride(options) : options.scope === "sync-user" ? remote : [];
    },
}));
mock.module("@/services/api/skills", () => ({
    listSkillPresets: async () => ({ presets: [] }),
    listSkills: async () => ({ skills: [] }),
    listAddedSkills: async () => ({ skills: [] }),
    addSkill: silent,
}));
mock.module("@/services/user-data-sync", () => ({ saveRemoteUserDataNow: async () => {}, applyAgentCanvasPatches: async () => {}, refreshCanvasAfterAgent: async () => {} }));
mock.module("@/components/canvas/canvas-cloud-agent-chat-ui", () => ({
    ...chatUI,
    AGENT_SCENE_DEFS: [],
    AgentChatComposer: (props: any) => {
        composer = props;
        return <textarea value={props.prompt} readOnly />;
    },
    AgentChatMessage: ({ item }: any) => <div>{item.text}</div>,
    AgentOperationFeed: () => null,
    AgentReasoningFeed: () => null,
    AgentWorkingMessage: () => null,
    AgentPlanBar: () => null,
    AgentQuestionBar: (props: any) => {
        questionBar = props;
        return null;
    },
    AgentSceneCapsules: () => null,
}));
mock.module("@/components/canvas/canvas-agent-skill-library-modal", () => ({ CanvasAgentSkillLibraryModal: () => null }));
mock.module("@/components/canvas/canvas-cloud-agent-settings", () => ({
    CanvasCloudAgentSettings: () => null,
    agentPermissionLabel: () => "自动",
    agentPermissionMenuItems: () => [],
    agentPermissionVisual: () => ({ icon: () => null }),
}));
mock.module("@/components/canvas/use-agent-panel-layout", () => ({ useAgentPanelLayout: () => ({ style: {}, pointerHandlers: {}, reset: silent, onResizeKeyDown: silent }) }));
mock.module("@/components/canvas/live2d-avatar", () => ({ Live2DAvatar: () => null }));
mock.module("@/components/ui/fluid-orb", () => ({ FluidOrb: () => null }));
const { CanvasCloudAgentPanel } = await import("@/components/canvas/canvas-cloud-agent-panel");

function conversation(runId: string, id = "server", draft = ""): CloudAgentConversation {
    const run: AgentRun = { id: runId, conversationId: "shared", canvasId: "canvas", status: "completed", permissionMode: "auto", revision: 1, createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z" };
    return {
        id,
        serverConversationId: "shared",
        run,
        runs: [run],
        lastEventSeq: 4,
        draft,
        title: "对话",
        messages: [{ id: `answer-${runId}`, role: "assistant", text: `结果-${runId}` }],
        permissionMode: "auto",
        createdAt: run.createdAt,
        updatedAt: run.updatedAt,
    };
}
const settle = async () => {
    for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 10));
};
async function focus() {
    await act(async () => {
        window.dispatchEvent(new Event("focus"));
        await settle();
    });
}
async function mount(initial: CloudAgentConversation[] = [], initialPending: CloudAgentPendingSubmission | null = null, loader?: typeof refreshOverride, mode: AgentSendMode = "interject", questionId = "") {
    local = initial;
    remote = [conversation("desktop")];
    pending = initialPending;
    refreshOverride = loader;
    sendMessageOverride = undefined;
    createRunOverride = undefined;
    interjectionOverride = undefined;
    subscriptions.length = 0;
    reads.length = 0;
    sentRequests.length = 0;
    interjections.length = 0;
    createdRequests.length = 0;
    serverSendMode = mode;
    queueItems.length = 0;
    conversationRequests.length = 0;
    acceptedRuns.clear();
    queueMessageOverride = undefined;
    awaitingQuestionId = questionId;
    questionBar = undefined;
    setActiveUserScope("sync-user");
    useUserStore.setState({ hydrated: true, user: { id: "sync-user", username: "test", displayName: "test", role: "user", status: "active" }, features: { agentSubagentsEnabled: false } as any });
    const channel = createModelChannel({ id: "test", scope: "system", models: ["text"] });
    useConfigStore.setState({ config: normalizeConfigSnapshot({ config: { ...defaultConfig, channels: [channel], model: "test::text", textModel: "test::text" } }).config });
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const handle = React.createRef<CanvasCloudAgentPanelHandle>();
    await act(async () => {
        root.render(<CanvasCloudAgentPanel ref={handle} canvasId="canvas" nodeCount={1} selectedNodeIds={[]} references={[]} open onOpen={silent} onCollapse={silent} />);
        await settle();
    });
    return {
        host,
        handle,
        close: async () => {
            await act(async () => root.unmount());
            host.remove();
        },
    };
}
afterAll(() => dom.happyDOM.abort());

const externalPrompt = { canvasId: "canvas", userScope: "sync-user", prompt: "为目标分镜表提出拆镜方案，先询问确认", focusNodeIds: ["storyboard-target"] };

test("account queue default applies to node and composer sends while preserving drafts", async () => {
    const current = conversation("desktop", "local", "未发送的草稿");
    current.run = { ...current.run!, status: "running" };
    const mounted = await mount([current], null, async () => [current], "queue");
    try {
        await act(async () => {
            expect(await mounted.handle.current!.sendPrompt(externalPrompt)).toBe("queued");
            await settle();
        });
        expect(interjections).toHaveLength(0);
        expect(createdRequests).toHaveLength(0);
        expect(conversationRequests[0]).toMatchObject({ mode: "queue", expectedRunId: "desktop", request: { prompt: externalPrompt.prompt } });
        expect(composer.prompt).toBe("未发送的草稿");
        await act(async () => {
            composer.onSubmit();
            await settle();
        });
        expect(conversationRequests[1]).toMatchObject({ mode: "queue", request: { prompt: "未发送的草稿" } });
        expect(composer.prompt).toBe("");
        expect(mounted.host.textContent).not.toContain("幂等");
    } finally {
        await mounted.close();
    }
});

test("unknown queued POST keeps its original mode after the account default changes", async () => {
    const current = conversation("desktop", "local", "保留草稿");
    current.run = { ...current.run!, status: "running" };
    const mounted = await mount([current], null, async () => [current], "queue");
    try {
        queueMessageOverride = async () => {
            throw new Error("response lost");
        };
        await act(async () => {
            expect(await mounted.handle.current!.sendPrompt(externalPrompt)).toBe(false);
            await settle();
        });
        const identity = pending!.key;
        expect(pending?.conversationMessage?.mode).toBe("queue");
        await act(async () => {
            await composer.onSendModeChange("interject");
            await settle();
        });
        queueMessageOverride = async () => ({ delivery: "queued", messageId: "accepted", conversationId: "shared", queueRevision: 2 });
        await act(async () => {
            expect(await mounted.handle.current!.sendPrompt(externalPrompt)).toBe("queued");
            await settle();
        });
        expect(conversationRequests[1]).toMatchObject({ mode: "queue", request: { idempotencyKey: identity } });
        expect(composer.prompt).toBe("保留草稿");
    } finally {
        await mounted.close();
    }
});

test("question answers bypass queue default and queued words do not hide the pending question", async () => {
    const current = conversation("desktop", "local", "确认");
    current.messages.push({ id: "question-desktop:4", role: "assistant", text: "", question: { question: "采用这个方案吗", options: [{ label: "确认" }, { label: "取消" }] } });
    const mounted = await mount([current], null, async () => [current], "queue", "desktop:4");
    try {
        await act(async () => {
            composer.onSubmit();
            await settle();
        });
        expect(conversationRequests[0].mode).toBe("queue");
        expect(conversationRequests[0].replyToQuestionId).toBeUndefined();
        expect(questionBar.question.question).toBe("采用这个方案吗");
        expect(questionBar.disabled).toBe(false);
        await act(async () => {
            questionBar.onAnswer("确认");
            await settle();
        });
        expect(conversationRequests[1]).toMatchObject({ mode: "interject", replyToQuestionId: "desktop:4", request: { prompt: "确认" } });
    } finally {
        await mounted.close();
    }
});

test("a node request is not silently attached as an answer to a pending question", async () => {
    const current = conversation("desktop", "local", "保留草稿");
    const mounted = await mount([current], null, async () => [current], "interject", "desktop:4");
    try {
        queueMessageOverride = async () => {
            throw { status: 409, reason: "agent_question_pending" };
        };
        await act(async () => {
            expect(await mounted.handle.current!.sendPrompt(externalPrompt)).toBe(false);
            await settle();
        });
        expect(conversationRequests[0].replyToQuestionId).toBeUndefined();
        expect(pending).toBeNull();
        expect(composer.prompt).toBe("保留草稿");
    } finally {
        await mounted.close();
    }
});

test("typing a new draft while a queued POST is pending survives its acknowledgement", async () => {
    const current = conversation("desktop", "local", "准备入队的消息");
    current.run = { ...current.run!, status: "running" };
    const mounted = await mount([current], null, async () => [current], "queue");
    try {
        let release!: (value: AgentMessageReceipt) => void;
        queueMessageOverride = () =>
            new Promise((resolve) => {
                release = resolve;
            });
        await act(async () => {
            composer.onSubmit();
            await settle();
        });
        await act(async () => {
            composer.onPromptChange("下一条草稿正在输入");
            await settle();
        });
        await act(async () => {
            release({ delivery: "queued", messageId: "queued", conversationId: "shared", queueRevision: 2 });
            await settle();
        });
        expect(composer.prompt).toBe("下一条草稿正在输入");
    } finally {
        await mounted.close();
    }
});

test("restoring an unknown queue send retains the newer draft separately from its frozen body", async () => {
    const request = { canvasId: "canvas", prompt: "原待确认消息", idempotencyKey: "queue-pending-new-draft" };
    const mounted = await mount([conversation("desktop", "local", "后来写的新草稿")], {
        fingerprint: "original",
        key: request.idempotencyKey,
        request,
        parentRunId: "desktop",
        conversationMessage: { conversationId: "shared", mode: "queue", expectedRunId: "desktop" },
    });
    try {
        expect(composer.prompt).toBe("后来写的新草稿");
        expect(pending?.request?.prompt).toBe("原待确认消息");
        await act(async () => {
            [...mounted.host.querySelectorAll("button")].find((button) => button.textContent === "重试发送")!.click();
            await settle();
        });
        expect(conversationRequests[0]).toMatchObject({ mode: "queue", request: { prompt: "原待确认消息", idempotencyKey: request.idempotencyKey } });
        expect(composer.prompt).toBe("后来写的新草稿");
    } finally {
        await mounted.close();
    }
});

test("external storyboard request continues the current chat and preserves its composer draft", async () => {
    const mounted = await mount([conversation("desktop", "local", "原来的 Agent 草稿")]);
    try {
        let accepted: Awaited<ReturnType<CanvasCloudAgentPanelHandle["sendPrompt"]>> | undefined;
        await act(async () => {
            accepted = await mounted.handle.current!.sendPrompt(externalPrompt);
            await settle();
        });
        expect(accepted).toBe("sent");
        expect(sentRequests).toHaveLength(1);
        expect(sentRequests[0]).toMatchObject({ parentId: "desktop", request: { prompt: externalPrompt.prompt, focusNodeIds: ["storyboard-target"], permissionMode: "auto" } });
        expect(createdRequests).toHaveLength(0);
        expect(composer.prompt).toBe("原来的 Agent 草稿");
        expect(local.find((entry) => entry.id === "local")?.draft).toBe("原来的 Agent 草稿");
    } finally {
        await mounted.close();
    }
});

test("external request interjects into the running Agent without creating another run", async () => {
    const current = conversation("desktop", "local", "保留的草稿");
    current.run = { ...current.run!, status: "running" };
    const mounted = await mount([current], null, async () => [current]);
    try {
        let accepted: Awaited<ReturnType<CanvasCloudAgentPanelHandle["sendPrompt"]>> | undefined;
        await act(async () => {
            accepted = await mounted.handle.current!.sendPrompt(externalPrompt);
            await settle();
        });
        expect(accepted).toBe("sent");
        expect(interjections).toEqual([{ runId: "desktop", text: externalPrompt.prompt }]);
        expect(sentRequests).toHaveLength(0);
        expect(createdRequests).toHaveLength(0);
        expect(composer.prompt).toBe("保留的草稿");
    } finally {
        await mounted.close();
    }
});

test("external request creates only the first run when the current chat is empty", async () => {
    const mounted = await mount([], null, async () => []);
    try {
        await act(async () => {
            expect(await mounted.handle.current!.sendPrompt(externalPrompt)).toBe("sent");
            await settle();
        });
        expect(createdRequests).toHaveLength(1);
        expect(createdRequests[0].prompt).toBe(externalPrompt.prompt);
        expect(sentRequests).toHaveLength(0);
    } finally {
        await mounted.close();
    }
});

test("an unknown external POST keeps its original identity and composer draft for a safe retry", async () => {
    const mounted = await mount([conversation("desktop", "local", "独立的 Agent 草稿")]);
    try {
        sendMessageOverride = async () => {
            throw new Error("network response unknown");
        };
        await act(async () => {
            expect(await mounted.handle.current!.sendPrompt(externalPrompt)).toBe(false);
            await settle();
        });
        expect(pending?.composerDraft).toBe("独立的 Agent 草稿");
        const originalKey = pending!.key;
        expect(composer.prompt).toBe("独立的 Agent 草稿");
        await act(async () => {
            expect(await mounted.handle.current!.sendPrompt({ ...externalPrompt, prompt: "另一个要求" })).toBe(false);
            await settle();
        });
        expect(sentRequests).toHaveLength(1);
        sendMessageOverride = undefined;
        await act(async () => {
            expect(await mounted.handle.current!.sendPrompt(externalPrompt)).toBe("sent");
            await settle();
        });
        expect(sentRequests[1].request.idempotencyKey).toBe(originalKey);
        expect(composer.prompt).toBe("独立的 Agent 草稿");
    } finally {
        await mounted.close();
    }
});

test("external send rejects stale scopes and synchronously excludes duplicate clicks", async () => {
    const mounted = await mount([conversation("desktop", "local", "草稿")]);
    try {
        expect(await mounted.handle.current!.sendPrompt({ ...externalPrompt, canvasId: "old-canvas" })).toBe(false);
        expect(await mounted.handle.current!.sendPrompt({ ...externalPrompt, userScope: "old-user" })).toBe(false);
        let release!: (run: AgentRun) => void;
        sendMessageOverride = () =>
            new Promise((resolve) => {
                release = resolve;
            });
        let first!: ReturnType<CanvasCloudAgentPanelHandle["sendPrompt"]>;
        await act(async () => {
            first = mounted.handle.current!.sendPrompt(externalPrompt);
            expect(await mounted.handle.current!.sendPrompt(externalPrompt)).toBe(false);
            await settle();
        });
        expect(sentRequests).toHaveLength(1);
        await act(async () => {
            release({ ...conversation("local-child").run!, status: "running" });
            expect(await first).toBe("sent");
            await settle();
        });
        expect(composer.prompt).toBe("草稿");
    } finally {
        await mounted.close();
    }
});

test("restoring an unknown external submission keeps the original Agent draft", async () => {
    const request = { canvasId: "canvas", prompt: externalPrompt.prompt, idempotencyKey: "external-pending-key" };
    const mounted = await mount([conversation("desktop", "local", "原草稿")], { request, key: request.idempotencyKey, fingerprint: "saved", parentRunId: "desktop", composerDraft: "原草稿" });
    try {
        expect(composer.prompt).toBe("原草稿");
        expect(pending?.request?.idempotencyKey).toBe("external-pending-key");
    } finally {
        await mounted.close();
    }
});

test("switching chats during an interjection releases only the old send lock", async () => {
    const current = conversation("desktop", "local", "旧草稿");
    current.run = { ...current.run!, status: "running" };
    const mounted = await mount([current], null, async () => [current]);
    try {
        let releaseOld!: () => void, releaseNew!: (run: AgentRun) => void;
        interjectionOverride = () =>
            new Promise((resolve) => {
                releaseOld = resolve;
            });
        createRunOverride = () =>
            new Promise((resolve) => {
                releaseNew = resolve;
            });
        let oldSend!: ReturnType<CanvasCloudAgentPanelHandle["sendPrompt"]>, newSend!: ReturnType<CanvasCloudAgentPanelHandle["sendPrompt"]>;
        await act(async () => {
            oldSend = mounted.handle.current!.sendPrompt(externalPrompt);
            await settle();
        });
        await act(async () => {
            (mounted.host.querySelector("svg.lucide-message-square-plus")!.closest("button") as HTMLButtonElement).click();
            await settle();
        });
        await act(async () => {
            composer.onPromptChange("新会话草稿");
            await settle();
        });
        await act(async () => {
            newSend = mounted.handle.current!.sendPrompt(externalPrompt);
            await settle();
        });
        expect(createdRequests).toHaveLength(1);
        await act(async () => {
            releaseOld();
            expect(await oldSend).toBe(false);
            await settle();
        });
        await act(async () => {
            expect(await mounted.handle.current!.sendPrompt(externalPrompt)).toBe(false);
        });
        expect(createdRequests).toHaveLength(1);
        await act(async () => {
            releaseNew({ ...conversation("first-run").run!, status: "running" });
            expect(await newSend).toBe("sent");
            await settle();
        });
        expect(composer.prompt).toBe("新会话草稿");
    } finally {
        await mounted.close();
    }
});

test("empty cache discovers a cloud conversation and resumes SSE after restored events", async () => {
    const mounted = await mount();
    try {
        expect(mounted.host.textContent).toContain("结果-desktop");
        expect(subscriptions.at(-1)).toMatchObject({ id: "desktop", after: 4 });
        expect(reads[0]).toMatchObject({ canvasId: "canvas", scope: "sync-user" });
    } finally {
        await mounted.close();
    }
});

test("an external POST response from the old account cannot replace the new account's draft or run", async () => {
    const mounted = await mount([conversation("desktop", "local", "旧账号草稿")]);
    try {
        let release!: (run: AgentRun) => void;
        sendMessageOverride = () =>
            new Promise((resolve) => {
                release = resolve;
            });
        let sending!: ReturnType<CanvasCloudAgentPanelHandle["sendPrompt"]>;
        await act(async () => {
            sending = mounted.handle.current!.sendPrompt(externalPrompt);
            await settle();
        });
        await act(async () => {
            setActiveUserScope("other");
            useUserStore.setState({ user: { ...useUserStore.getState().user!, id: "other" } });
            await settle();
        });
        await act(async () => {
            composer.onPromptChange("新账号草稿");
            await settle();
        });
        await act(async () => {
            release({ ...conversation("late-old-child").run!, status: "running" });
            expect(await sending).toBe(false);
            await settle();
        });
        expect(composer.prompt).toBe("新账号草稿");
        expect(subscriptions.some((item) => item.id === "late-old-child")).toBe(false);
    } finally {
        await mounted.close();
    }
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
    } finally {
        await mounted.close();
    }
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
    } finally {
        await mounted.close();
    }
});
test("switching accounts aborts the old directory request and prevents its late result from appearing", async () => {
    const mounted = await mount([conversation("desktop", "local")]);
    try {
        let finish!: (value: CloudAgentConversation[]) => void;
        refreshOverride = (options) =>
            options.scope === "sync-user"
                ? new Promise((resolve) => {
                      finish = resolve;
                  })
                : Promise.resolve([]);
        await focus();
        const request = reads.at(-1)!;
        await act(async () => {
            setActiveUserScope("other");
            useUserStore.setState({ user: { ...useUserStore.getState().user!, id: "other" } });
            await settle();
        });
        expect(request.signal.aborted).toBe(true);
        await act(async () => {
            finish([conversation("late-old-account")]);
            await settle();
        });
        expect(mounted.host.textContent).not.toContain("结果-late-old-account");
    } finally {
        await mounted.close();
    }
});

test("a delayed parent directory result cannot close the SSE of an accepted local child", async () => {
    const mounted = await mount([conversation("desktop", "local")]);
    try {
        let finish!: (value: CloudAgentConversation[]) => void;
        refreshOverride = () =>
            new Promise((resolve) => {
                finish = resolve;
            });
        await focus();
        await act(async () => {
            composer.onPromptChange("继续");
            await settle();
        });
        await act(async () => {
            composer.onSubmit();
            await settle();
        });
        expect(subscriptions.at(-1)?.id).toBe("local-child");
        expect(pending).toBeNull();
        await act(async () => {
            finish([conversation("desktop")]);
            await settle();
        });
        expect(subscriptions.at(-1)).toMatchObject({ id: "local-child", closed: false });
        expect(subscriptions.map((item) => item.id)).toEqual(["desktop", "local-child"]);
    } finally {
        await mounted.close();
    }
});

test("an old directory read stays obsolete across repeated accepted POSTs and SSE completions, then fresh reads resume", async () => {
    const mounted = await mount([conversation("desktop", "local")]);
    try {
        let finish!: (value: CloudAgentConversation[]) => void;
        refreshOverride = () =>
            new Promise((resolve) => {
                finish = resolve;
            });
        await focus();
        const ids = ["local-first", "local-second", "local-third"];
        const parents: string[] = [];
        sendMessageOverride = (parentId) => {
            parents.push(parentId);
            return { ...conversation(ids[parents.length - 1]).run!, parentId, status: "running", createdAt: `2026-10-23T00:0${parents.length}:00Z` };
        };
        for (const [index, id] of ids.entries()) {
            await act(async () => {
                composer.onPromptChange(`继续第 ${index + 1} 轮`);
                await settle();
            });
            await act(async () => {
                composer.onSubmit();
                await settle();
            });
            expect(subscriptions.at(-1)).toMatchObject({ id, after: 0, closed: false });
            expect(pending).toBeNull();
            if (index < ids.length - 1) {
                await act(async () => {
                    subscriptions.at(-1)!.receive({ runId: id, eventId: `${id}:1`, seq: 1, type: "run_status", payload: { status: "completed", revision: 2 }, createdAt: `2026-10-23T00:0${index + 1}:01Z` });
                    await settle();
                });
            }
        }
        expect(parents).toEqual(["desktop", "local-first", "local-second"]);
        await act(async () => {
            finish([conversation("desktop")]);
            await settle();
        });
        expect(subscriptions.at(-1)).toMatchObject({ id: "local-third", closed: false });
        expect(subscriptions.map((item) => item.id)).toEqual(["desktop", ...ids]);
        refreshOverride = undefined;
        remote = [conversation("fresh-phone-child")];
        await focus();
        expect(subscriptions.at(-1)?.id).toBe("fresh-phone-child");
        expect(mounted.host.textContent).toContain("结果-fresh-phone-child");
    } finally {
        await mounted.close();
    }
});

test("the real history service restores a legacy tail with a visible limitation while discovering healthy conversations", async () => {
    let restored: CloudAgentConversation[] = [];
    const mounted = await mount([], null, async (options) => {
        const snapshot = (id: string): AgentRun => ({
            ...conversation(id).run!,
            id,
            conversationId: id,
            eventSeqBase: id === "legacy" ? 2 : 0,
            eventCount: id === "legacy" ? 3 : 0,
            events: id === "legacy" ? [{ eventId: "legacy:3", runId: id, seq: 3, type: "assistant_message", payload: { messageId: "tail", text: "可用旧记录" }, createdAt: "2026-10-23T00:00:00Z" }] : [],
        });
        restored = await refreshFromService({
            ...options,
            client: {
                listConversations: async () => ({
                    items: ["legacy", "healthy"].map((id) => ({ id, canvasId: "canvas", title: id, latestRunId: id, status: "completed", revision: 1, createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z" })),
                }),
                listRuns: async (id: string) => ({ items: [snapshot(id)] }),
                getRun: async (id: string) => ({ run: snapshot(id) }),
            },
        });
        return restored;
    });
    try {
        expect(restored).toHaveLength(2);
        expect(mounted.host.textContent).toContain("可用旧记录");
        expect(mounted.host.textContent).toContain("早期记录");
        expect(subscriptions.at(-1)).toMatchObject({ id: "legacy", after: 3 });
        expect(composer.disabled).toBe(false);
    } finally {
        await mounted.close();
    }
});

test("one unavailable conversation keeps its draft and blocks continuation until a successful history retry", async () => {
    let broken = true;
    const mounted = await mount([conversation("desktop", "local", "保留草稿")], null, (options) =>
        refreshFromService({
            ...options,
            client: {
                listConversations: async () => ({ items: [{ id: "shared", canvasId: "canvas", title: "对话", latestRunId: "desktop", status: "completed", revision: 2, createdAt: "2026-10-23T00:00:00Z", updatedAt: "2026-10-23T00:00:00Z" }] }),
                listRuns: async () => ({ items: [{ ...conversation("desktop").run!, revision: 2 }] }),
                getRun: async () => ({
                    run: {
                        ...conversation("desktop").run!,
                        revision: 2,
                        eventSeqBase: 0,
                        eventCount: broken ? 2 : 0,
                        events: broken ? [{ eventId: "desktop:2", runId: "desktop", seq: 2, type: "assistant_message", payload: { text: "坏页" }, createdAt: "2026-10-23T00:00:00Z" }] : [],
                    },
                }),
            },
        }),
    );
    try {
        expect(mounted.host.textContent).toContain("暂时无法恢复");
        expect(composer.prompt).toBe("保留草稿");
        expect(composer.disabled).toBe(true);
        await act(async () => {
            composer.onSubmit();
            await settle();
        });
        expect(subscriptions.at(-1)?.id).toBe("desktop");
        expect(pending).toBeNull();
        broken = false;
        await focus();
        expect(mounted.host.textContent).not.toContain("暂时无法恢复");
        expect(composer.prompt).toBe("保留草稿");
        expect(composer.disabled).toBe(false);
    } finally {
        await mounted.close();
    }
});
