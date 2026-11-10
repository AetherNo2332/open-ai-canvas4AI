import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";
import type { CanvasNodeData } from "@/types/canvas";
import type { UploadedImage } from "@/services/image-storage";

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
const { App } = await import("antd");
const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
const { setActiveUserScope } = await import("@/lib/user-scope");
const { CanvasNodeType } = await import("@/types/canvas");
const fileUpload = await import("@/lib/canvas/canvas-file-upload");
const storage = await import("@/services/image-storage");
const assetSync = await import("@/services/project-asset-sync");
let upload: (...args: unknown[]) => Promise<UploadedImage>;
mock.module("@/services/image-storage", () => ({ ...storage, uploadImage: (...args: unknown[]) => upload(...args) }));
mock.module("@/services/project-asset-sync", () => ({ ...assetSync, ensureCanvasNodeAsset: async () => ({ assetId: "asset", created: true, syncPending: false }) }));
mock.module("@/lib/canvas/canvas-file-upload", () => ({
    ...fileUpload,
    createFileUploadPlaceholder: async (id: string, file: File, position: { x: number; y: number }) => fileUpload.createImageUploadPlaceholder(id, file, position, { width: 100, height: 100 }),
}));
const { useCanvasUpload } = await import("@/pages/canvas/use-canvas-upload");
const image: UploadedImage = { storageKey: "resource:server-image", url: "https://example.test/image.png", width: 100, height: 100, bytes: 5, mimeType: "image/png" };
const file = new File(["image"], "image.png", { type: "image/png" });
const noop = () => {};
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
afterAll(() => dom.happyDOM.abort());

async function mount(prepare = async () => {}) {
    setActiveUserScope("upload-owner");
    const nodesRef = { current: [] as CanvasNodeData[] };
    let api!: ReturnType<typeof useCanvasUpload>;
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const client = new QueryClient();
    function Harness({ canvasId }: { canvasId: string }) {
        api = useCanvasUpload({
            canvasId,
            nodesRef,
            selectedNodeIdsRef: { current: new Set() },
            getCanvasCenter: () => ({ x: 0, y: 0 }),
            screenToCanvas: () => ({ x: 0, y: 0 }),
            setNodes: (update) => {
                nodesRef.current = typeof update === "function" ? update(nodesRef.current) : update;
            },
            setSelectedNodeIds: noop,
            setSelectedConnectionId: noop,
            setContextMenu: noop,
            setDialogNodeId: noop,
            prepareCanvasForServerAction: prepare,
        });
        return null;
    }
    const render = async (canvasId: string) =>
        act(async () =>
            root.render(
                <QueryClientProvider client={client}>
                    <App>
                        <Harness canvasId={canvasId} />
                    </App>
                </QueryClientProvider>,
            ),
        );
    await render("canvas-one");
    return {
        nodesRef,
        api: () => api,
        render,
        close: async () => {
            await act(async () => root.unmount());
            client.clear();
            host.remove();
        },
    };
}

test("same-canvas cancellation immediately makes the upload placeholder terminal", async () => {
    let finish!: (value: UploadedImage) => void;
    upload = () =>
        new Promise((resolve) => {
            finish = resolve;
        });
    const mounted = await mount();
    const controller = new AbortController();
    let result!: Promise<unknown>;
    try {
        await act(async () => {
            result = mounted
                .api()
                .createFileNode(file, { x: 0, y: 0 }, undefined, { quiet: { nodeId: "stable-image", signal: controller.signal } })
                .catch((error) => error);
            await settle();
        });
        expect(mounted.nodesRef.current[0].metadata?.fileUpload).toBe("uploading");
        await act(async () => controller.abort());
        expect(mounted.nodesRef.current[0].metadata?.fileUpload).toBe("error");
        expect(mounted.nodesRef.current[0].metadata?.errorDetails).toContain("中断");
        await act(async () => {
            finish(image);
            await result;
        });
        expect(mounted.nodesRef.current[0].metadata?.fileUpload).toBe("error");
    } finally {
        await mounted.close();
    }
});

test("late cancellation and upload settlement do not mutate a different canvas", async () => {
    let finish!: (value: UploadedImage) => void;
    upload = () =>
        new Promise((resolve) => {
            finish = resolve;
        });
    const mounted = await mount();
    const controller = new AbortController();
    let result!: Promise<unknown>;
    try {
        await act(async () => {
            result = mounted
                .api()
                .createFileNode(file, { x: 0, y: 0 }, undefined, { quiet: { nodeId: "stable-image", signal: controller.signal } })
                .catch((error) => error);
            await settle();
        });
        await mounted.render("canvas-two");
        const other: CanvasNodeData = { id: "stable-image", type: CanvasNodeType.Image, position: { x: 2, y: 3 }, metadata: { content: "other-canvas", fileUpload: "uploading" } };
        mounted.nodesRef.current = [other];
        await act(async () => {
            controller.abort();
            finish(image);
            await result;
        });
        expect(mounted.nodesRef.current).toEqual([other]);
        expect(mounted.nodesRef.current[0]).toBe(other);
    } finally {
        await mounted.close();
    }
});

test("Agent retries reuse the placeholder and upload identity, then retry only saving durable media", async () => {
    const identities: unknown[] = [];
    let uploads = 0;
    let saves = 0;
    upload = async (...args) => {
        identities.push(args[3]);
        if (++uploads === 1) throw new Error("upload failed");
        return image;
    };
    const mounted = await mount(async () => {
        if (++saves === 1) throw new Error("canvas save failed");
    });
    try {
        expect(typeof mounted.api().insertAgentFiles).toBe("function");
        await act(async () => {
            await expect(mounted.api().insertAgentFiles([file], undefined, ["stable-image"])).rejects.toThrow("upload failed");
        });
        expect(mounted.nodesRef.current).toHaveLength(1);
        expect(mounted.nodesRef.current[0].metadata?.fileUpload).toBe("error");
        await act(async () => {
            await expect(mounted.api().insertAgentFiles([file], undefined, ["stable-image"])).rejects.toThrow("canvas save failed");
        });
        expect(mounted.nodesRef.current).toHaveLength(1);
        expect(mounted.nodesRef.current[0].metadata?.storageKey).toBe(image.storageKey);
        await act(async () => {
            expect(await mounted.api().insertAgentFiles([file], undefined, ["stable-image"])).toEqual(["stable-image"]);
        });
        expect(uploads).toBe(2);
        expect(saves).toBe(2);
        expect(identities[0]).toEqual(identities[1]);
        expect(identities[0]).toEqual(expect.objectContaining({ idempotencyKey: expect.stringContaining("stable-image") }));
    } finally {
        await mounted.close();
    }
});
