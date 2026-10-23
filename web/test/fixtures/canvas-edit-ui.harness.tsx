import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";
const dom = new Window({ url: "http://localhost/" });
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
    DOMParser: dom.DOMParser,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    requestAnimationFrame: dom.requestAnimationFrame.bind(dom),
    cancelAnimationFrame: dom.cancelAnimationFrame.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
}))
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
const notices: string[] = [];
const notice = { success: (text: string) => notices.push(text), error: (text: string) => notices.push(text) };
const Box = ({ children }: any) => <div>{children}</div>;
mock.module("antd", () => ({
    App: { useApp: () => ({ message: notice, modal: { confirm: ({ onOk }: any) => onOk() } }) },
    Button: ({ children, onClick, disabled, loading, icon, ...props }: any) => (
        <button onClick={onClick} disabled={disabled || loading} aria-label={props["aria-label"]}>
            {icon}
            {children}
        </button>
    ),
    Input: ({ value, onChange, readOnly, onFocus, ...props }: any) => <input value={value} readOnly={readOnly} onInput={onChange} onFocus={onFocus} aria-label={props["aria-label"]} />,
    ColorPicker: Box,
    Dropdown: Box,
    Popover: Box,
    Segmented: Box,
    Table: Box,
    InputNumber: () => null,
    Image: Box,
    Modal: ({ open, children }: any) => (open ? <div>{children}</div> : null),
}));
mock.module("@/components/ui/product/app-modal", () => ({ AppModal: ({ children, open }: any) => (open ? <div>{children}</div> : null) }));
mock.module("@/components/ui/base/tooltip", () => ({ Tooltip: Box }));
const localforage = (await import("localforage")).default;
const storage = new Map<string, unknown>();
localforage.getItem = (async (key: string) => storage.get(key) ?? null) as typeof localforage.getItem;
localforage.setItem = (async (key: string, value: unknown) => {
    storage.set(key, value);
    return value;
}) as typeof localforage.setItem;
localforage.removeItem = async (key: string) => {
    storage.delete(key);
};
const { setActiveUserScope } = await import("@/lib/user-scope");
const { apiClient, ApiError } = await import("@/services/api/request");
const { useCanvasSessionStore } = await import("@/stores/canvas/use-canvas-session-store");
const { CanvasTextEditorModal } = await import("@/components/canvas/canvas-text-editor-modal");
const { createCanvasCommandTransport } = await import("@/services/api/canvas-commands");
const { CanvasCommandSession } = await import("@/services/canvas-command-session");
const { attachCanvasCommandProject } = await import("@/services/canvas-command-project-lifecycle");
const { cancelCanvasEditLeases } = await import("@/services/canvas-edit-leases");
const { flushCanvasAuthorDrafts, readCanvasAuthorDraft, saveCanvasAuthorDraft } = await import("@/services/canvas-author-drafts");
const node = { id: "n", type: "text", title: "Text", position: { x: 0, y: 0 }, width: 200, height: 100, metadata: { content: "before" } } as any;
const tick = () => new Promise((resolve) => setTimeout(resolve, 10));
function editScope(scope: string, generation = 1) {
    setActiveUserScope(scope);
    useCanvasSessionStore.setState({ scope, bootstrapState: "ready", states: { c: { acknowledged: { documentEpoch: "e", entityGenerations: { "node:n": generation, "node:prompt": generation, "node:script": generation } } } as any } });
}
afterAll(() => {
    cancelCanvasEditLeases();
    dom.happyDOM.abort();
});

test("actual modal + rich text editor + command binding wait for durable ACK before success/release", async () => {
    editScope("ui-owner");
    let cloud = { id: "c", nodes: [structuredClone(node)], connections: [] } as any;
    let releaseCount = 0,
        wire: any,
        proof = "",
        acknowledge!: () => void;
    const ack = new Promise<void>((resolve) => {
        acknowledge = resolve;
    });
    apiClient.defaults.adapter = async (config) => {
        let data: any;
        const body = config.data && JSON.parse(config.data);
        if (config.url?.endsWith("/snapshot")) data = { protocolVersion: 1, canvasId: "c", documentEpoch: "e", revision: 1, eventSeq: 0, document: cloud, entityVersions: { "node:n": 1 }, entityGenerations: { "node:n": 1 } };
        else if (config.url?.endsWith("/acquire") || config.url?.endsWith("/renew")) data = { canvasId: "c", ...body, expiresAt: new Date(Date.now() + 30000).toISOString() };
        else if (config.url?.endsWith("/release")) {
            releaseCount++;
            data = { released: true };
        } else if (config.url?.endsWith("/commands")) {
            wire = body;
            proof = String(config.headers.get("X-Canvas-Edit-Proofs"));
            await ack;
            const patch = wire.operations.find((op: any) => op.type === "update_node").patch;
            cloud.nodes[0] = { ...cloud.nodes[0], ...(patch.title ? { title: patch.title } : {}), metadata: { ...cloud.nodes[0].metadata, ...Object.fromEntries(Object.entries(patch).filter(([key]) => key !== "title")) } };
            data = {
                protocolVersion: 1,
                canvasId: "c",
                documentEpoch: "e",
                operationId: wire.operationId,
                clientId: wire.clientId,
                revision: 2,
                eventSeq: 1,
                changes: { entities: [{ key: "node:n", value: cloud.nodes[0] }] },
                entityVersions: { "node:n": 2 },
                entityGenerations: { "node:n": 1 },
            };
        } else if (config.url?.endsWith("/events")) data = [];
        else throw new Error(`unexpected ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    const session = new CanvasCommandSession(createCanvasCommandTransport(), "client");
    await session.open("c");
    let live = structuredClone(cloud),
        present: ((node: any) => void) | undefined;
    const refs = { nodes: { current: live.nodes }, connections: { current: [] } };
    const binding = await attachCanvasCommandProject({
        canvasId: "c",
        session,
        capabilities: { protocolVersion: 1, nodes: [{ type: "text", patchFields: { title: { path: "title" }, content: { path: "metadata.content" }, richText: { path: "metadata.richText" } } }], structuredFields: {} },
        refs,
        read: () => live,
        present: (doc) => {
            live = doc;
            present?.(live.nodes[0]);
        },
        view: { read: async () => null, write: async () => {} },
        readViewport: () => ({ x: 0, y: 0, k: 1 }),
        presentViewport: () => {},
    });
    function Harness() {
        const [current, setCurrent] = React.useState(live.nodes[0]);
        present = setCurrent;
        return (
            <CanvasTextEditorModal
                projectId="c"
                node={current}
                open
                onClose={() => {}}
                onSave={async (_id, title, content, richText) => {
                    live = { ...live, nodes: [{ ...live.nodes[0], title, metadata: { ...live.nodes[0].metadata, content, richText } }] };
                    refs.nodes.current = live.nodes;
                    setCurrent(live.nodes[0]);
                    if (!(await binding.flush())) throw new Error("保存未确认");
                }}
            />
        );
    }
    const element = document.createElement("div");
    document.body.append(element);
    const root = createRoot(element);
    await act(async () => {
        root.render(<Harness />);
        await tick();
    });
    const input = element.querySelector<HTMLInputElement>('[aria-label="文本节点标题"]')!;
    expect(input.readOnly).toBe(false);
    await act(async () => {
        input.value = "Edited title";
        input.dispatchEvent(new dom.Event("input", { bubbles: true }));
        await tick();
    });
    const save = [...element.querySelectorAll("button")].find((button) => button.textContent === "保存")!;
    await act(async () => {
        save.click();
        await tick();
    });
    expect(wire.operations.some((op: any) => op.patch?.title === "Edited title")).toBe(true);
    expect(JSON.parse(proof)[0]).toMatchObject({ canvasId: "c", nodeId: "n" });
    expect(releaseCount).toBe(0);
    expect(notices.some((text) => text.includes("已确认保存"))).toBe(false);
    await act(async () => {
        acknowledge();
        await tick();
    });
    expect(releaseCount).toBe(1);
    expect(notices.some((text) => text.includes("已确认保存到云端"))).toBe(true);
    expect(await readCanvasAuthorDraft("ui-owner", "c", "n", "text")).toBeNull();
    await act(async () => root.unmount());
    binding.dispose();
    session.close();
    cancelCanvasEditLeases();
    element.remove();
});

test("denied edit restores an account-scoped local draft and does not call save or release", async () => {
    notices.length = 0;
    editScope("ui-denied");
    const base = { title: node.title, content: "before", richText: null };
    await saveCanvasAuthorDraft("ui-denied", "c", "n", { kind: "text", identity: { documentEpoch: "e", nodeGeneration: 1 }, base, value: { ...base, title: "Kept draft" } });
    let saves = 0,
        releases = 0;
    apiClient.defaults.adapter = async (config) => {
        if (config.url?.endsWith("/release")) releases++;
        throw new ApiError("busy", { status: 409, reason: "node_locked" });
    };
    const element = document.createElement("div");
    document.body.append(element);
    const root = createRoot(element);
    await act(async () => {
        root.render(
            <CanvasTextEditorModal
                projectId="c"
                node={node}
                open
                onClose={() => {}}
                onSave={() => {
                    saves++;
                }}
            />,
        );
        await tick();
    });
    const input = element.querySelector<HTMLInputElement>('[aria-label="文本节点标题"]')!;
    expect(input.value).toBe("Kept draft");
    expect(input.readOnly).toBe(true);
    expect(element.textContent).toContain("其他窗口正在编辑");
    const save = [...element.querySelectorAll("button")].find((button) => button.textContent === "保存")!;
    await act(async () => {
        save.click();
        await tick();
    });
    expect(saves).toBe(0);
    expect(releases).toBe(0);
    await flushCanvasAuthorDrafts();
    expect((await readCanvasAuthorDraft("ui-denied", "c", "n", "text"))?.value.title).toBe("Kept draft");
    expect(await readCanvasAuthorDraft("ui-owner", "c", "n", "text")).toBeNull();
    await act(async () => root.unmount());
    cancelCanvasEditLeases();
    element.remove();
});

test("failed save keeps the typed draft across unmount and never claims ACK or releases", async () => {
    notices.length = 0;
    editScope("ui-offline");
    let releases = 0;
    apiClient.defaults.adapter = async (config) => {
        if (config.url?.endsWith("/release")) releases++;
        const body = JSON.parse(config.data);
        return { data: { code: 0, data: { canvasId: "c", ...body, expiresAt: new Date(Date.now() + 30000).toISOString() }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    const element = document.createElement("div");
    document.body.append(element);
    const root = createRoot(element);
    await act(async () => {
        root.render(
            <CanvasTextEditorModal
                projectId="c"
                node={node}
                open
                onClose={() => {}}
                onSave={async () => {
                    throw new ApiError("offline", { status: 503 });
                }}
            />,
        );
        await tick();
    });
    const input = element.querySelector<HTMLInputElement>('[aria-label="文本节点标题"]')!;
    await act(async () => {
        input.value = "Offline draft";
        input.dispatchEvent(new dom.Event("input", { bubbles: true }));
        await tick();
    });
    await act(async () => {
        [...element.querySelectorAll("button")].find((button) => button.textContent === "保存")!.click();
        await tick();
    });
    expect(releases).toBe(0);
    expect(notices.some((text) => text.includes("已确认保存"))).toBe(false);
    expect(element.textContent).toContain("草稿已保留");
    await act(async () => root.unmount());
    await flushCanvasAuthorDrafts();
    cancelCanvasEditLeases();
    element.remove();
    expect((await readCanvasAuthorDraft("ui-offline", "c", "n", "text"))?.value.title).toBe("Offline draft");
});

test.each(["old-generation", "legacy-no-identity"])("an %s author draft is displayed but cannot be rebased onto a recreated same-ID node", async (kind) => {
    notices.length = 0;
    editScope(`ui-${kind}`, 2);
    const base = { title: node.title, content: "before", richText: null };
    const identity = kind === "old-generation" ? { documentEpoch: "e", nodeGeneration: 1 } : undefined;
    await saveCanvasAuthorDraft(`ui-${kind}`, "c", "n", { kind: "text", identity, base, value: { ...base, title: "Old incarnation draft" } });
    let saves = 0;
    apiClient.defaults.adapter = async (config) => {
        const body = JSON.parse(config.data);
        return { data: { code: 0, data: { canvasId: "c", ...body, expiresAt: new Date(Date.now() + 30000).toISOString() }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    const element = document.createElement("div");
    document.body.append(element);
    const root = createRoot(element);
    await act(async () => {
        root.render(
            <CanvasTextEditorModal
                projectId="c"
                node={node}
                open
                onClose={() => {}}
                onSave={() => {
                    saves++;
                }}
            />,
        );
        await tick();
    });
    const input = element.querySelector<HTMLInputElement>('[aria-label="文本节点标题"]')!;
    expect(input.value).toBe("Old incarnation draft");
    expect(input.readOnly).toBe(true);
    expect(element.textContent).toContain("节点版本与旧草稿不同");
    const save = [...element.querySelectorAll("button")].find((button) => button.textContent === "保存")!;
    expect(save.disabled).toBe(true);
    expect(saves).toBe(0);
    await saveCanvasAuthorDraft(`ui-${kind}`, "c", "n", { kind: "text", identity: { documentEpoch: "e", nodeGeneration: 2 }, base, value: { ...base, title: "still old intent" } });
    expect((await readCanvasAuthorDraft(`ui-${kind}`, "c", "n", "text"))?.identity).toEqual(identity);
    await act(async () => root.unmount());
    cancelCanvasEditLeases();
    element.remove();
});

test("actual prompt panel focuses a lease, preserves prompt mirrors and starts generation only after save ACK", async () => {
    for (const [path, name] of [
        ["model-picker", "ModelPicker"],
        ["canvas/canvas-camera-control-popover", "CanvasCameraControlPopover"],
        ["canvas/canvas-image-settings-popover", "CanvasImageSettingsPopover"],
        ["canvas/canvas-audio-settings-popover", "CanvasAudioSettingsPopover"],
        ["canvas/canvas-video-settings-popover", "CanvasVideoSettingsPopover"],
        ["canvas/canvas-video-prompt-tools", "CanvasVideoPromptTools"],
        ["canvas/canvas-preset-picker", "CanvasPresetPicker"],
        ["canvas/canvas-nine-grid-picker", "CanvasNineGridPicker"],
        ["canvas/canvas-choose-image-style-picker", "CanvasChooseImageStylePicker"],
        ["canvas/canvas-choose-effect-picker", "CanvasChooseEffectPicker"],
        ["canvas/canvas-choose-motion-picker", "CanvasChooseMotionPicker"],
        ["canvas/canvas-portrait-texture-popover", "CanvasPortraitTexturePopover"],
        ["canvas/canvas-prompt-optimizer-drawer", "CanvasPromptOptimizerDrawer"],
    ])
        mock.module(`@/components/${path}`, () => ({
            [name]:
                name === "CanvasPromptOptimizerDrawer"
                    ? Box
                    : name === "CanvasChooseImageStylePicker"
                      ? ({ onSelect }: any) => (
                            <button data-test-style onClick={() => onSelect(1, "style")}>
                                style
                            </button>
                        )
                      : () => null,
        }));
    mock.module("@/lib/plugins/builtin/prompt-optimizer", () => ({ PROMPT_OPTIMIZER_PLUGIN_ID: "prompt-optimizer", promptOptimizerPlugin: {} }));
    const { CanvasNodePromptPanel } = await import("@/components/canvas/canvas-node-prompt-panel");
    const { writeCanvasNodePrompt } = await import("@/lib/canvas/canvas-node-prompt");
    const { useUserStore } = await import("@/stores/use-user-store");
    useUserStore.setState((state) => ({ features: { ...state.features, creditsEnabled: false } }));
    notices.length = 0;
    editScope("ui-prompt");
    let mirror = writeCanvasNodePrompt(
            { ...node, id: "prompt", type: "image", metadata: { content: "", prompt: "produced prompt", generationSpec: { version: 1, mode: "image", prompt: "before", options: {}, referenceBindings: [], textInputMode: "append-sources" } } },
            "before",
        ),
        generated = 0,
        releases = 0,
        writes = 0,
        acknowledge!: () => void;
    const ack = new Promise<void>((resolve) => {
        acknowledge = resolve;
    });
    apiClient.defaults.adapter = async (config) => {
        const body = config.data && JSON.parse(config.data);
        let data: unknown;
        if (config.url?.endsWith("/acquire") || config.url?.endsWith("/renew")) data = { canvasId: "c", ...body, expiresAt: new Date(Date.now() + 30000).toISOString() };
        else if (config.url?.endsWith("/release")) {
            releases++;
            data = { released: true };
        } else throw new Error(`unexpected ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    function PromptHarness() {
        const [current, setCurrent] = React.useState(mirror);
        return (
            <CanvasNodePromptPanel
                projectId="c"
                node={current}
                isRunning={false}
                onConfigChange={() => {}}
                onGenerate={() => {
                    generated++;
                }}
                onPromptChange={(_id, prompt) => {
                    mirror = writeCanvasNodePrompt(mirror, prompt);
                    setCurrent(mirror);
                }}
                onPersist={async () => {
                    writes++;
                    await ack;
                }}
            />
        );
    }
    const element = document.createElement("div");
    document.body.append(element);
    const root = createRoot(element);
    await act(async () => {
        root.render(<PromptHarness />);
        await tick();
    });
    const textarea = element.querySelector("textarea")!;
    expect(textarea.readOnly).toBe(true);
    await act(async () => {
        textarea.focus();
        await tick();
    });
    expect(textarea.readOnly).toBe(false);
    await act(async () => {
        textarea.value = "new prompt";
        const propKey = Object.keys(textarea).find((key) => key.startsWith("__reactProps"))!;
        (textarea as any)[propKey].onChange({ target: textarea, currentTarget: textarea });
        await tick();
    });
    expect(mirror.metadata.prompt).toBe("new prompt");
    expect(mirror.metadata.composerContent).toBe("new prompt");
    expect(mirror.metadata.generationSpec.prompt).toBe("new prompt");
    await act(async () => {
        textarea.value = "new prompt second edit";
        const propKey = Object.keys(textarea).find((key) => key.startsWith("__reactProps"))!;
        (textarea as any)[propKey].onChange({ target: textarea, currentTarget: textarea });
        await tick();
    });
    expect(mirror.metadata.generationSpec.prompt).toBe("new prompt second edit");
    expect(element.textContent).not.toContain("旧草稿已保留，请先复制提示词");
    const generate = element.querySelector<HTMLButtonElement>('button[aria-label="生成"]')!;
    await act(async () => {
        generate.click();
        await tick();
    });
    expect(writes).toBe(1);
    expect(generated).toBe(0);
    expect(releases).toBe(0);
    await act(async () => {
        acknowledge();
        await tick();
    });
    expect(generated).toBe(1);
    expect(releases).toBe(1);
    expect(await readCanvasAuthorDraft("ui-prompt", "c", "prompt", "prompt")).toBeNull();
    await act(async () => root.unmount());
    cancelCanvasEditLeases();
    element.remove();
});

test("storyboard card composer retains its constraints in simple mode and waits for ACK before sending to Agent", async () => {
    mock.module("@/components/ui/base/select", () => ({ Select: ({ options, value, onChange }: any) => <select value={value} onChange={(event: any) => onChange(event.target.value)}>{options.map((item: any) => <option key={item.value} value={item.value}>{item.label}</option>)}</select> }));
    mock.module("@/components/canvas/storyboard-assets-cell", () => ({ StoryboardAssetsCell: () => null, StoryboardSharedReferencesControl: () => null }));
    const { CanvasScriptNodeContent } = await import("@/components/canvas/canvas-script-node");
    const { deriveStoryboardPipelineProgress } = await import("@/lib/canvas/canvas-storyboard-progress");
    editScope("ui-storyboard");
    let releases = 0,
        writes = 0,
        generated = 0,
        acknowledge!: () => void;
    const ack = new Promise<void>((resolve) => {
        acknowledge = resolve;
    });
    apiClient.defaults.adapter = async (config) => {
        const body = JSON.parse(config.data);
        if (config.url?.endsWith("/release")) releases++;
        return { data: { code: 0, data: config.url?.endsWith("/release") ? { released: true } : { canvasId: "c", ...body, expiresAt: new Date(Date.now() + 30000).toISOString() }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    const callbacks = Object.fromEntries(
        [
            "onOpen",
            "onCreateImageNodes",
            "onCreateVideoNodes",
            "onGenerateImages",
            "onGenerateVideos",
            "onVideoInputModeChange",
            "onReferenceNodeIdsChange",
            "onMergeVideos",
            "onCreateActionBoards",
            "onRetryBatch",
            "onRetryBatchItem",
            "onStopBatch",
            "onAddRow",
            "onRemoveRow",
            "onUpdateRow",
            "onShotDurationChange",
            "onShotCountChange",
            "onComposerHeightChange",
            "onConnectStart",
            "onScrollTopChange",
        ].map((name) => [name, () => {}]),
    ) as any;
    const script = { ...node, id: "script", type: "script", metadata: { composerContent: "before", storyboard: { rows: [] } } };
    function StoryboardHarness() {
        const [current, setCurrent] = React.useState(script);
        return (
            <CanvasScriptNodeContent
                {...callbacks}
                projectId="c"
                node={current}
                nodes={[]}
                pipeline={deriveStoryboardPipelineProgress(current, [], [])}
                scale={1}
                workspaceMode="simple"
                mentionReferences={[]}
                onPromptChange={(value) => setCurrent((old) => ({ ...old, metadata: { ...old.metadata, composerContent: value } }))}
                onPersist={async () => {
                    writes++;
                    await ack;
                }}
                onRequestStoryboard={async () => {
                    generated++;
                    return true;
                }}
            />
        );
    }
    const element = document.createElement("div");
    document.body.append(element);
    const root = createRoot(element);
    await act(async () => {
        root.render(<StoryboardHarness />);
        await tick();
    });
    expect(element.querySelectorAll("select")).toHaveLength(2);
    expect(element.textContent).toContain("自动拆分");
    expect(element.textContent).toContain("每镜 10 秒");
    const textarea = element.querySelector("textarea")!;
    await act(async () => {
        textarea.focus();
        await tick();
    });
    expect(textarea.readOnly).toBe(false);
    for (const value of ["story draft", "story draft continued"])
        await act(async () => {
            textarea.value = value;
            const props = Object.keys(textarea).find((key) => key.startsWith("__reactProps"))!;
            (textarea as any)[props].onChange({ target: textarea, currentTarget: textarea });
            await tick();
        });
    expect(textarea.value).toBe("story draft continued");
    const generate = element.querySelector("svg.lucide-send")!.closest("button")!;
    await act(async () => {
        generate.click();
        await tick();
    });
    expect(writes).toBe(1);
    expect(generated).toBe(0);
    expect(releases).toBe(0);
    await act(async () => {
        acknowledge();
        await tick();
    });
    expect(generated).toBe(1);
    expect(releases).toBe(1);
    await act(async () => root.unmount());
    cancelCanvasEditLeases();
    element.remove();
});

test.each(["title", "body"])("inline %s ACK preserves unrelated restored author fields and their original base", async (surface) => {
    const actualStore = await import("@/stores/canvas/use-canvas-session-store");
    mock.module("@/stores/canvas/use-canvas-session-store", () => ({ ...actualStore, flushCanvasCommandWorkspace: async () => true }));
    const { useCanvasInlineAuthorEdit } = await import("@/components/canvas/use-canvas-inline-author-edit");
    const scope = `field-${surface}`;
    editScope(scope);
    const persisted = structuredClone(node);
    const base = { title: "Text", content: "before", richText: null };
    const richText = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "UNSAVED BODY", marks: [{ type: "bold" }] }] }] };
    const value = surface === "title" ? { ...base, content: "UNSAVED BODY", richText } : { ...base, title: "UNSAVED TITLE" };
    await saveCanvasAuthorDraft(scope, "c", "n", { kind: "text", identity: { documentEpoch: "e", nodeGeneration: 1 }, base, value });
    apiClient.defaults.adapter = async (config) => {
        const body = JSON.parse(config.data);
        return { data: { code: 0, data: config.url?.endsWith("/release") ? { released: true } : { canvasId: "c", ...body, expiresAt: new Date(Date.now() + 30000).toISOString() }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    let author: any;
    const element = document.createElement("div");
    document.body.append(element);
    let root = createRoot(element);
    function Inline() {
        author = useCanvasInlineAuthorEdit("c", persisted, true);
        return null;
    }
    try {
        await act(async () => {
            root.render(<Inline />);
            await tick();
        });
        await act(async () => {
            await author.enter(() => {});
        });
        const patch = surface === "title" ? { title: "Renamed" } : { content: "ACK BODY", richText: null };
        await act(async () => {
            author.remember(patch);
            await flushCanvasAuthorDrafts();
            expect(
                await author.commit(patch, () => {
                    if (surface === "title") persisted.title = "Renamed";
                    else persisted.metadata.content = "ACK BODY";
                }),
            ).toBe(true);
        });
        const remaining = await readCanvasAuthorDraft(scope, "c", "n", "text");
        expect(remaining?.identity).toEqual({ documentEpoch: "e", nodeGeneration: 1 });
        if (surface === "title") {
            expect(remaining?.value.content).toBe("UNSAVED BODY");
            expect(remaining?.value.richText).toEqual(richText);
            expect(remaining?.base.content).toBe("before");
        } else {
            expect(remaining?.value.title).toBe("UNSAVED TITLE");
            expect(remaining?.base.title).toBe("Text");
        }
        await act(async () => root.unmount());
        root = createRoot(element);
        await act(async () => {
            root.render(<CanvasTextEditorModal projectId="c" node={persisted} open onClose={() => {}} onSave={() => {}} />);
            await tick();
        });
        expect(element.querySelector<HTMLInputElement>('[aria-label="文本节点标题"]')?.value).toBe(surface === "title" ? "Renamed" : "UNSAVED TITLE");
        expect(element.querySelector(".ProseMirror")?.textContent).toBe(surface === "title" ? "UNSAVED BODY" : "ACK BODY");
        if (surface === "title") expect(element.querySelector(".ProseMirror strong")?.textContent).toBe("UNSAVED BODY");
    } finally {
        await act(async () => root.unmount());
        cancelCanvasEditLeases();
        element.remove();
    }
});

test("tool edits and generate cannot borrow a pending save ACK; a later tool intent gets a separate ACK", async () => {
    const { CanvasNodePromptPanel } = await import("@/components/canvas/canvas-node-prompt-panel");
    const { writeCanvasNodePrompt } = await import("@/lib/canvas/canvas-node-prompt");
    editScope("exact-prompt");
    let current = writeCanvasNodePrompt({ ...node, id: "prompt", type: "image", metadata: { content: "" } }, "before"),
        writes = 0,
        generated: string[] = [],
        ackA!: () => void,
        ackB!: () => void;
    const a = new Promise<void>((resolve) => {
            ackA = resolve;
        }),
        b = new Promise<void>((resolve) => {
            ackB = resolve;
        });
    apiClient.defaults.adapter = async (config) => {
        const body = JSON.parse(config.data);
        return { data: { code: 0, data: config.url?.endsWith("/release") ? { released: true } : { canvasId: "c", ...body, expiresAt: new Date(Date.now() + 30000).toISOString() }, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    function Prompt() {
        const [live, setLive] = React.useState(current);
        return (
            <CanvasNodePromptPanel
                projectId="c"
                node={live}
                isRunning={false}
                onConfigChange={() => {}}
                onPromptChange={(_id, value) => {
                    current = writeCanvasNodePrompt(current, value);
                    setLive(current);
                }}
                onPersist={async () => {
                    writes++;
                    await (writes === 1 ? a : b);
                }}
                onGenerate={(_id, _mode, value) => generated.push(value)}
            />
        );
    }
    const element = document.createElement("div");
    document.body.append(element);
    const root = createRoot(element);
    try {
        await act(async () => {
            root.render(<Prompt />);
            await tick();
        });
        const textarea = element.querySelector("textarea")!;
        await act(async () => {
            textarea.focus();
            await tick();
            textarea.value = "A";
            const props = Object.keys(textarea).find((key) => key.startsWith("__reactProps"))!;
            (textarea as any)[props].onChange({ target: textarea, currentTarget: textarea });
            await tick();
        });
        await act(async () => {
            [...element.querySelectorAll("button")].find((button) => button.textContent === "保存")!.click();
            await tick();
        });
        await act(async () => {
            element.querySelector<HTMLButtonElement>("[data-test-style]")!.click();
            await tick();
            element.querySelector<HTMLButtonElement>('button[aria-label="生成"]')!.click();
            await tick();
        });
        expect(current.metadata.composerContent).toBe("A");
        expect(writes).toBe(1);
        await act(async () => {
            ackA();
            await tick();
        });
        expect(generated).toEqual([]);
        await act(async () => {
            element.querySelector<HTMLButtonElement>("[data-test-style]")!.click();
            await tick();
            element.querySelector<HTMLButtonElement>('button[aria-label="生成"]')!.click();
            await tick();
        });
        expect(writes).toBe(2);
        expect(generated).toEqual([]);
        expect((await readCanvasAuthorDraft("exact-prompt", "c", "prompt", "prompt"))?.value.prompt).toContain("tool:style");
        await act(async () => {
            ackB();
            await tick();
        });
        expect(generated).toEqual([current.metadata.composerContent!]);
    } finally {
        ackA();
        ackB();
        await act(async () => root.unmount());
        cancelCanvasEditLeases();
        element.remove();
    }
});
