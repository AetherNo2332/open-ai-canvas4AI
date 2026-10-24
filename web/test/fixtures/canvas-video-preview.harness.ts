import { afterEach, expect, mock, test } from "bun:test";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

let uploads = 0;
let captures = 0;
let capture: () => Promise<{ poster: Blob }> = async () => ({ poster: new Blob(["poster"], { type: "image/jpeg" }) });
Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { requestIdleCallback: (callback: () => void) => (queueMicrotask(callback), 1), cancelIdleCallback: () => {} },
});
mock.module("@/services/file-storage", () => ({ resolveMediaUrl: async (_key: string, url: string) => url }));
mock.module("@/lib/video-poster", () => ({
    captureVideoPoster: async () => {
        captures += 1;
        return capture();
    },
}));
mock.module("@/services/image-storage", () => ({
    uploadImage: async () => {
        uploads += 1;
        return { url: "/poster", storageKey: "resource:poster" };
    },
}));
const { hydrateCanvasVideoPreview } = await import("@/services/canvas-video-preview");
const video: CanvasNodeData = { id: "video", type: CanvasNodeType.Video, title: "Video", position: { x: 0, y: 0 }, width: 320, height: 180, metadata: { content: "/video.mp4" } };
afterEach(() => {
    uploads = 0;
    captures = 0;
    capture = async () => ({ poster: new Blob(["poster"], { type: "image/jpeg" }) });
});

test("passive capture returns a local poster without creating a server resource", async () => {
    const before = structuredClone(video);
    const hydrated = await hydrateCanvasVideoPreview(video);
    expect(hydrated?.localUrl.startsWith("blob:")).toBe(true);
    expect(captures).toBe(1);
    expect(uploads).toBe(0);
    expect(video).toEqual(before);
    if (hydrated) URL.revokeObjectURL(hydrated.localUrl);
});

test("a replaced source cannot finish its cancelled poster capture or upload", async () => {
    let finish: (value: { poster: Blob }) => void = () => {};
    capture = () => new Promise((resolve) => (finish = resolve));
    const controller = new AbortController();
    const pending = hydrateCanvasVideoPreview(video, controller.signal);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(captures).toBe(1);
    controller.abort();
    finish({ poster: new Blob(["old poster"]) });
    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(uploads).toBe(0);
});
