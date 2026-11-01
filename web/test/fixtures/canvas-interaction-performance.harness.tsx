import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
const observers: TestResizeObserver[] = [];
class TestResizeObserver {
    targets = new Set<Element>();
    disconnected = false;
    constructor(readonly callback: () => void) {
        observers.push(this);
    }
    observe(target: Element) {
        this.targets.add(target);
    }
    unobserve(target: Element) {
        this.targets.delete(target);
    }
    disconnect() {
        this.disconnected = true;
        this.targets.clear();
    }
}
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    localStorage: dom.localStorage,
    Element: dom.Element,
    Node: dom.Node,
    HTMLElement: dom.HTMLElement,
    SVGElement: dom.SVGElement,
    ShadowRoot: dom.ShadowRoot,
    MutationObserver: dom.MutationObserver,
    ResizeObserver: TestResizeObserver,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
})) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act, createRef } = await import("react");
const { createRoot } = await import("react-dom/client");
const { useCanvasNodeToolbarPosition } = await import("../../src/components/canvas/use-canvas-node-toolbar-position");
const { CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, CANVAS_NODE_DRAG_PREVIEW_EVENT } = await import("../../src/lib/canvas/canvas-live-viewport");
const { CanvasNodeType } = await import("../../src/types/canvas");
import type { CanvasNodeData, ViewportTransform, StoryboardRow } from "../../src/types/canvas";

afterAll(() => {
    dom.happyDOM.abort();
    for (const [key, value] of originals) {
        if (value) Object.defineProperty(globalThis, key, value);
        else Reflect.deleteProperty(globalThis, key);
    }
});

test("node and viewport commits reuse measurements; resize, previews and unmount stay correct", async () => {
    const container = document.createElement("div");
    const toolbar = document.createElement("div");
    const dock = document.createElement("div");
    const host = document.createElement("div");
    toolbar.append(dock);
    container.append(toolbar, host);
    document.body.append(container);
    let reads = 0;
    let width = 1000;
    container.getBoundingClientRect = () => {
        reads++;
        return { left: 0, top: 0, width, height: 800, right: width, bottom: 800 } as DOMRect;
    };
    Object.defineProperty(toolbar, "offsetWidth", {
        get: () => {
            reads++;
            return 200;
        },
    });
    Object.defineProperty(toolbar, "offsetHeight", {
        get: () => {
            reads++;
            return 44;
        },
    });
    Object.defineProperty(dock, "scrollWidth", {
        get: () => {
            reads++;
            return 200;
        },
    });
    const containerRef = createRef<HTMLDivElement>();
    containerRef.current = container;
    const toolbarRef = createRef<HTMLDivElement>();
    toolbarRef.current = toolbar;
    const root = createRoot(host);
    let state: ReturnType<typeof useCanvasNodeToolbarPosition>;
    const node: CanvasNodeData = { id: "n", type: CanvasNodeType.Image, title: "image", position: { x: 100, y: 200 }, width: 200, height: 100 };
    function Harness({ node, viewport }: { node: CanvasNodeData; viewport: ViewportTransform }) {
        state = useCanvasNodeToolbarPosition(node, viewport, containerRef, toolbarRef, "professional");
        return null;
    }
    try {
        await act(async () => root.render(<Harness node={node} viewport={{ x: 0, y: 0, k: 1 }} />));
        const initialReads = reads;
        const initialObservers = observers.length;
        for (let i = 1; i <= 20; i++) {
            await act(async () => root.render(<Harness node={{ ...node, position: { x: 100 + i, y: 200 } }} viewport={{ x: i, y: 0, k: 1 }} />));
        }
        expect(reads).toBe(initialReads);
        expect(observers.length).toBe(initialObservers);
        expect(toolbar.style.transform).toBe("translate3d(240px, 170px, 0)");
        await act(async () => {
            container.dispatchEvent(new dom.CustomEvent(CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, { detail: { x: 50, y: 10, k: 2 } }) as unknown as Event);
            container.dispatchEvent(new dom.CustomEvent(CANVAS_NODE_DRAG_PREVIEW_EVENT, { detail: { nodeIds: new Set([node.id]), x: 10, y: 5 } }) as unknown as Event);
        });
        expect(reads).toBe(initialReads);
        expect(toolbar.style.transform).toBe("translate3d(510px, 390px, 0)");
        width = 500;
        await act(async () => {
            for (const observer of observers.filter((item) => !item.disconnected && item.targets.has(container))) observer.callback();
        });
        expect(state!.containerWidth).toBe(476);
        expect(toolbar.style.transform).toBe("translate3d(388px, 390px, 0)");
        const finalTransform = toolbar.style.transform;
        await act(async () => root.unmount());
        container.dispatchEvent(new dom.CustomEvent(CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, { detail: { x: 0, y: 0, k: 1 } }) as unknown as Event);
        await Promise.resolve();
        expect(toolbar.style.transform).toBe(finalTransform);
        expect(observers.every((item) => item.disconnected)).toBe(true);
    } finally {
        container.remove();
    }
});

test("fullscreen mounts a window of a 500-shot storyboard and creates text editors only on demand", async () => {
    const { App, ConfigProvider } = await import("antd");
    const { CanvasScriptEditor } = await import("../../src/components/canvas/canvas-script-node");
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const dimensionDescriptors = new Map<string, PropertyDescriptor | undefined>();
    for (const [key, size] of [
        ["clientWidth", 1000],
        ["clientHeight", 600],
    ] as const) {
        dimensionDescriptors.set(key, Object.getOwnPropertyDescriptor(dom.HTMLElement.prototype, key));
        Object.defineProperty(dom.HTMLElement.prototype, key, {
            configurable: true,
            get() {
                return this.className === "h-full min-h-0 pt-2" ? size : 0;
            },
        });
    }
    const rows = Array.from({ length: 500 }, (_, i): StoryboardRow => ({ id: `s-${i}`, shotNumber: i + 1, durationSeconds: 5, plotDescription: `剧情${i}`, dialogue: `台词${i}` }));
    let node: CanvasNodeData = { id: "script", title: "分镜", type: CanvasNodeType.Script, position: { x: 0, y: 0 }, width: 800, height: 600, metadata: { storyboard: { rows, visibleColumns: ["shotNumber", "plotDescription", "dialogue"] } } };
    let saved = "";
    const render = async (open = true) =>
        act(async () =>
            root.render(
                <ConfigProvider theme={{ token: { motion: false } }}>
                    <App>
                        <CanvasScriptEditor
                            node={node}
                            nodes={[node]}
                            open={open}
                            onClose={() => {}}
                            onUpdateRows={(next) => {
                                saved = next[0].plotDescription || "";
                                node = { ...node, metadata: { ...node.metadata, storyboard: { ...node.metadata!.storyboard!, rows: next } } };
                            }}
                            onEditAssets={() => {}}
                            onVisibleColumnsChange={() => {}}
                            onGenerateImages={() => {}}
                            onGenerateVideos={() => {}}
                            onVideoInputModeChange={() => {}}
                        />
                    </App>
                </ConfigProvider>,
            ),
        );
    try {
        await render();
        const dialog = document.querySelector('[role="dialog"]')!;
        expect(dialog).not.toBeNull();
        // 列标签以共享列定义为准；先用实际内容定位首格。
        const first = [...dialog.querySelectorAll("button")].find((item) => item.textContent === "剧情0")!;
        expect(first).toBeDefined();
        const mountedRows = dialog.querySelectorAll(".ant-table-row").length;
        expect(mountedRows).toBeGreaterThan(5);
        expect(mountedRows).toBeLessThan(60);
        expect(dialog.querySelectorAll("textarea")).toHaveLength(0);
        await act(async () => first.click());
        const editor = dialog.querySelector("textarea:not([aria-hidden])") as HTMLTextAreaElement;
        expect(editor).not.toBeNull();
        await act(async () => {
            Object.getOwnPropertyDescriptor(dom.HTMLTextAreaElement.prototype, "value")!.set!.call(editor, "改写剧情");
            editor.dispatchEvent(new dom.Event("input", { bubbles: true }) as unknown as Event);
        });
        expect(saved).toBe("改写剧情");
        await render();
        expect((dialog.querySelector("textarea") as HTMLTextAreaElement).value).toBe("改写剧情");
        await act(async () => editor.dispatchEvent(new dom.FocusEvent("focusout", { bubbles: true }) as unknown as Event));
        expect(dialog.querySelectorAll("textarea:not([aria-hidden])")).toHaveLength(0);
        expect(dialog.textContent).toContain("改写剧情");
        await render(false);
        await render();
        const reopened = document.querySelector('[role="dialog"]')!;
        expect(reopened.textContent).toContain("改写剧情");
        const holder = reopened.querySelector<HTMLElement>('[class*="virtual-holder"]')!;
        expect(holder).not.toBeNull();
        await act(async () => {
            holder.scrollTop = 100000;
            holder.dispatchEvent(new dom.Event("scroll", { bubbles: true }) as unknown as Event);
        });
        expect(reopened.textContent).toContain("剧情499");
        expect(reopened.querySelectorAll("textarea:not([aria-hidden])")).toHaveLength(0);
    } finally {
        await act(async () => root.unmount());
        host.remove();
        for (const [key, descriptor] of dimensionDescriptors) {
            if (descriptor) Object.defineProperty(dom.HTMLElement.prototype, key, descriptor);
            else Reflect.deleteProperty(dom.HTMLElement.prototype, key);
        }
    }
}, 30000);

test("task clocks live in visible labels and clean up on unmount", async () => {
    const { CanvasActiveTaskPanel } = await import("../../src/components/canvas/canvas-active-task-panel");
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const callbacks = new Map<number, () => void>();
    const originalSetInterval = window.setInterval;
    const originalClearInterval = window.clearInterval;
    const originalNow = Date.now;
    let now = 100000;
    let nextTimer = 0;
    Date.now = () => now;
    window.setInterval = ((callback: () => void) => {
        callbacks.set(++nextTimer, callback);
        return nextTimer;
    }) as typeof window.setInterval;
    window.clearInterval = ((id: number) => {
        callbacks.delete(id);
    }) as typeof window.clearInterval;
    const tasks = ["one", "two"].map((id) => ({ id, type: "canvas_image", mode: "image", status: "running", operation: "image", createdAt: new Date(now).toISOString() }));
    try {
        await act(async () => root.render(<CanvasActiveTaskPanel tasks={tasks as never} />));
        expect(callbacks.size).toBe(0);
        const toggle = host.querySelector<HTMLButtonElement>('[aria-controls="canvas-active-task-list"]')!;
        await act(async () => toggle.click());
        expect(callbacks.size).toBe(2);
        await act(async () => {
            now += 2000;
            for (const callback of callbacks.values()) callback();
        });
        expect(host.textContent).toContain("已运行 00:02");
        await act(async () => toggle.click());
        await act(async () => root.render(<CanvasActiveTaskPanel tasks={[]} />));
    } finally {
        await act(async () => root.unmount());
        expect(callbacks.size).toBe(0);
        window.setInterval = originalSetInterval;
        window.clearInterval = originalClearInterval;
        Date.now = originalNow;
        host.remove();
    }
});
