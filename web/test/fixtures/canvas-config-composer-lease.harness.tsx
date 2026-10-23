import { afterAll, afterEach, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    localStorage: dom.localStorage,
    HTMLElement: dom.HTMLElement,
    Element: dom.Element,
    Node: dom.Node,
    Event: dom.Event,
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
mock.module("antd", () => ({
    App: { useApp: () => ({ message: { error: (text: string) => notices.push(text) } }) },
    Button: ({ children, onClick, disabled, loading, icon, ...props }: any) => (
        <button onClick={onClick} disabled={disabled || loading} aria-label={props["aria-label"]}>
            {icon}
            {children}
        </button>
    ),
    Image: () => null,
}));
mock.module("@/components/canvas/canvas-preset-picker", () => ({
    CanvasPresetPicker: ({ onSelect }: any) => (
        <button data-test-preset onClick={() => onSelect({ prompt: "preset B" })}>
            preset
        </button>
    ),
}));
mock.module("@/components/canvas/canvas-video-prompt-tools", () => ({
    CanvasVideoPromptTools: ({ onMetadataChange }: any) => (
        <button data-test-metadata onClick={() => onMetadataChange({ videoStartFrameNodeId: "frame" })}>
            frame
        </button>
    ),
}));
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
const { cancelCanvasEditLeases } = await import("@/services/canvas-edit-leases");
const { readCanvasAuthorDraft, saveCanvasAuthorDraft, flushCanvasAuthorDrafts } = await import("@/services/canvas-author-drafts");
const { CanvasConfigComposer, saveCanvasConfigComposerIntent } = await import("@/components/canvas/canvas-config-composer");
const tick = () => new Promise((resolve) => setTimeout(resolve, 10));
const mounted: Array<{ root: ReturnType<typeof createRoot>; element: HTMLDivElement }> = [];
function scope(name: string, generation = 1) {
    setActiveUserScope(name);
    useCanvasSessionStore.setState({ scope: name, bootstrapState: "ready", states: { c: { acknowledged: { documentEpoch: "epoch", entityGenerations: { "node:config": generation } } } as any } });
}
function mount(props: Record<string, unknown>, extra?: (ref: React.RefObject<any>) => React.ReactNode) {
    const element = document.createElement("div");
    document.body.append(element);
    const root = createRoot(element);
    const ref = React.createRef<any>();
    function Harness() {
        const [value, setValue] = React.useState("cloud prompt");
        return (
            <>
                <CanvasConfigComposer ref={ref} projectId="c" nodeId="config" inputs={[]} value={value} onChange={setValue} onPersist={async () => {}} onClose={() => {}} {...props} />
                {extra?.(ref)}
            </>
        );
    }
    mounted.push({ root, element });
    return { root, element, ref, render: () => root.render(<Harness />) };
}
afterEach(async () => {
    for (const { root, element } of mounted.splice(0)) {
        await act(async () => root.unmount());
        element.remove();
    }
    cancelCanvasEditLeases();
    await flushCanvasAuthorDrafts();
    storage.clear();
    notices.length = 0;
});
afterAll(() => dom.happyDOM.abort());

test("Config denial keeps recovered scoped draft; retry and close wait for its durable ACK", async () => {
    scope("config-denied");
    await saveCanvasAuthorDraft("config-denied", "c", "config", { kind: "prompt", identity: { documentEpoch: "epoch", nodeGeneration: 1 }, base: { prompt: "cloud prompt" }, value: { prompt: "recovered config" } });
    let denied = true,
        acquireCount = 0,
        persistCount = 0,
        closeCount = 0,
        releases = 0;
    const writes: string[] = [];
    let acknowledge!: () => void;
    const ack = new Promise<void>((resolve) => {
        acknowledge = resolve;
    });
    apiClient.defaults.adapter = async (config) => {
        if (config.url?.endsWith("/release")) {
            releases++;
            return { data: { code: 0, data: { released: true } }, status: 200, statusText: "OK", headers: {}, config };
        }
        acquireCount++;
        if (denied) throw new ApiError("busy", { status: 409, reason: "node_locked" });
        return { data: { code: 0, data: { canvasId: "c", ...JSON.parse(config.data), expiresAt: new Date(Date.now() + 30000).toISOString() } }, status: 200, statusText: "OK", headers: {}, config };
    };
    const ui = mount({
        onChange: (value: string) => writes.push(value),
        onPersist: async () => {
            persistCount++;
            await ack;
        },
        onClose: () => closeCount++,
    });
    await act(async () => {
        ui.render();
        await tick();
    });
    const editor = ui.element.querySelector<HTMLElement>(".canvas-config-composer-editor > [contenteditable]")!;
    expect(editor.textContent).toBe("recovered config");
    expect(editor.contentEditable).toBe("false");
    expect(ui.element.textContent).toContain("其他窗口正在编辑");
    expect(acquireCount).toBe(1);
    expect(writes).toEqual([]);
    expect((await readCanvasAuthorDraft("config-denied", "c", "config", "prompt"))?.value.prompt).toBe("recovered config");
    denied = false;
    await act(async () => {
        ui.element.querySelector<HTMLButtonElement>('[aria-label="重新获取编辑占用"]')!.click();
        await tick();
    });
    expect(editor.contentEditable).toBe("true");
    await act(async () => {
        ui.element.querySelector<HTMLButtonElement>('[aria-label="关闭组装提示词"]')!.click();
        await tick();
    });
    expect(writes).toEqual(["recovered config"]);
    expect(persistCount).toBe(1);
    expect(closeCount).toBe(0);
    expect(releases).toBe(0);
    expect(await readCanvasAuthorDraft("config-denied", "c", "config", "prompt")).not.toBeNull();
    await act(async () => {
        acknowledge();
        await tick();
    });
    expect(closeCount).toBe(1);
    expect(releases).toBe(1);
    expect(await readCanvasAuthorDraft("config-denied", "c", "config", "prompt")).toBeNull();
});

test("Config old-incarnation recovery remains readable and cannot be freshly rebased by save", async () => {
    scope("config-old", 2);
    await saveCanvasAuthorDraft("config-old", "c", "config", { kind: "prompt", identity: { documentEpoch: "epoch", nodeGeneration: 1 }, base: { prompt: "old base" }, value: { prompt: "old local intent" } });
    apiClient.defaults.adapter = async (config) => ({ data: { code: 0, data: { canvasId: "c", ...JSON.parse(config.data), expiresAt: new Date(Date.now() + 30000).toISOString() } }, status: 200, statusText: "OK", headers: {}, config });
    let writes = 0;
    const ui = mount({ onChange: () => writes++ });
    await act(async () => {
        ui.render();
        await tick();
    });
    expect(ui.element.querySelector<HTMLElement>(".canvas-config-composer-editor > [contenteditable]")!.textContent).toBe("old local intent");
    expect(ui.element.querySelector<HTMLElement>(".canvas-config-composer-editor > [contenteditable]")!.contentEditable).toBe("false");
    await expect(ui.ref.current.save()).rejects.toThrow();
    expect(writes).toBe(0);
    expect((await readCanvasAuthorDraft("config-old", "c", "config", "prompt"))?.identity).toEqual({ documentEpoch: "epoch", nodeGeneration: 1 });
});

test("Config generation saves an immutable intent and rejects all mutations during delayed ACK", async () => {
    scope("config-ack");
    apiClient.defaults.adapter = async (config) => ({
        data: { code: 0, data: config.url?.endsWith("/release") ? { released: true } : { canvasId: "c", ...JSON.parse(config.data), expiresAt: new Date(Date.now() + 30000).toISOString() } },
        status: 200,
        statusText: "OK",
        headers: {},
        config,
    });
    let acknowledge!: () => void;
    const ack = new Promise<void>((resolve) => {
        acknowledge = resolve;
    });
    const writes: string[] = [],
        generated: string[] = [];
    let persists = 0,
        metadataWrites = 0;
    const ui = mount(
        {
            generationMode: "video",
            onChange: (text: string) => writes.push(text),
            onMetadataChange: () => metadataWrites++,
            onPersist: async () => {
                persists++;
                await ack;
            },
        },
        (ref) => (
            <button
                data-test-generate
                onClick={() => {
                    void ref.current.save().then((text: string) => generated.push(text));
                }}
            >
                generate
            </button>
        ),
    );
    await act(async () => {
        ui.render();
        await tick();
    });
    const editor = ui.element.querySelector<HTMLElement>(".canvas-config-composer-editor > [contenteditable]")!;
    await act(async () => {
        editor.textContent = "intent A";
        editor.dispatchEvent(new dom.Event("input", { bubbles: true }));
        await tick();
    });
    await act(async () => {
        ui.element.querySelector<HTMLButtonElement>("[data-test-generate]")!.click();
        await tick();
    });
    expect(persists).toBe(1);
    expect(generated).toEqual([]);
    expect(editor.contentEditable).toBe("false");
    await act(async () => {
        ui.element.querySelector<HTMLButtonElement>("[data-test-preset]")!.click();
        ui.element.querySelector<HTMLButtonElement>("[data-test-metadata]")!.click();
        editor.textContent = "forged B";
        editor.dispatchEvent(new dom.Event("input", { bubbles: true }));
        await tick();
    });
    expect(writes.every((text) => text === "intent A")).toBe(true);
    expect(metadataWrites).toBe(0);
    await expect(ui.ref.current.save()).rejects.toThrow();
    expect(generated).toEqual([]);
    expect((await readCanvasAuthorDraft("config-ack", "c", "config", "prompt"))?.value.prompt).toBe("intent A");
    await act(async () => {
        acknowledge();
        await tick();
    });
    expect(generated).toEqual(["intent A"]);
    expect(persists).toBe(1);
    expect(await readCanvasAuthorDraft("config-ack", "c", "config", "prompt")).toBeNull();
});

test("closed Config generation retains offline intent and rejects changed cloud fields before retry", async () => {
    scope("config-closed");
    await saveCanvasAuthorDraft("config-closed", "c", "config", { kind: "prompt", identity: { documentEpoch: "epoch", nodeGeneration: 1 }, base: { prompt: "cloud prompt" }, value: { prompt: "closed local intent" } });
    const writes: string[] = [];
    let offline = true,
        persists = 0,
        acquired = 0;
    apiClient.defaults.adapter = async (config) => {
        if (offline) throw new Error("offline");
        acquired++;
        return { data: { code: 0, data: config.url?.endsWith("/release") ? { released: true } : { canvasId: "c", ...JSON.parse(config.data), expiresAt: new Date(Date.now() + 30000).toISOString() } }, status: 200, statusText: "OK", headers: {}, config };
    };
    const save = (value: string) =>
        saveCanvasConfigComposerIntent({
            projectId: "c",
            nodeId: "config",
            value,
            onChange: (text) => writes.push(text),
            onPersist: async () => {
                persists++;
            },
        });
    await expect(save("cloud prompt")).rejects.toThrow("offline");
    expect(writes).toEqual([]);
    expect(persists).toBe(0);
    expect((await readCanvasAuthorDraft("config-closed", "c", "config", "prompt"))?.value.prompt).toBe("closed local intent");
    offline = false;
    await expect(save("a different cloud edit")).rejects.toThrow("云端提示词已变化");
    expect(acquired).toBe(0);
    expect(await save("cloud prompt")).toBe("closed local intent");
    expect(writes).toEqual(["closed local intent"]);
    expect(persists).toBe(1);
    expect(await readCanvasAuthorDraft("config-closed", "c", "config", "prompt")).toBeNull();
});

test("Config delayed acquire rejects a newly reconciled same-generation field before author mutation", async () => {
    scope("config-acquire-race");
    await saveCanvasAuthorDraft("config-acquire-race", "c", "config", { kind: "prompt", identity: { documentEpoch: "epoch", nodeGeneration: 1 }, base: { prompt: "cloud A" }, value: { prompt: "local intent" } });
    let acquired!: () => void;
    const acquire = new Promise<void>((resolve) => {
        acquired = resolve;
    });
    apiClient.defaults.adapter = async (config) => {
        await acquire;
        return { data: { code: 0, data: { canvasId: "c", ...JSON.parse(config.data), expiresAt: new Date(Date.now() + 30000).toISOString() } }, status: 200, statusText: "OK", headers: {}, config };
    };
    let liveValue = "cloud A",
        writes = 0,
        persists = 0;
    const save = saveCanvasConfigComposerIntent({
        projectId: "c",
        nodeId: "config",
        value: liveValue,
        readValue: () => liveValue,
        onChange: () => writes++,
        onPersist: async () => {
            persists++;
        },
    });
    const outcome = save.then(
        () => null,
        (error: unknown) => error,
    );
    await tick();
    liveValue = "reconciled cloud B";
    acquired();
    expect(await outcome).toBeInstanceOf(Error);
    expect(writes).toBe(0);
    expect(persists).toBe(0);
    expect((await readCanvasAuthorDraft("config-acquire-race", "c", "config", "prompt"))?.value.prompt).toBe("local intent");
    expect((await readCanvasAuthorDraft("config-acquire-race", "c", "config", "prompt"))?.base.prompt).toBe("cloud A");
});
