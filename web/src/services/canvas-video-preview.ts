import { captureVideoPoster } from "@/lib/video-poster";
import { resolveMediaUrl } from "@/services/file-storage";
import type { CanvasNodeData } from "@/types/canvas";

export type HydratedCanvasVideoPreview = {
    /** 仅用于当前页面显示；调用方负责在媒体切换或卸载时释放。 */
    localUrl: string;
};

export function hydrateCanvasVideoPreview(node: CanvasNodeData, signal?: AbortSignal) {
    const sourceKey = node.metadata?.storageKey || node.metadata?.content || "";
    if (!sourceKey) return Promise.resolve(null);
    return generateCanvasVideoPreview(node, signal).catch((error: unknown) => {
        if (error instanceof DOMException && error.name === "AbortError") throw error;
        return null;
    });
}

async function generateCanvasVideoPreview(node: CanvasNodeData, signal?: AbortSignal): Promise<HydratedCanvasVideoPreview | null> {
    await waitForBrowserIdle(signal);
    throwIfAborted(signal);
    const source = await resolveMediaUrl(node.metadata?.storageKey, node.metadata?.content || "");
    if (!source) return null;
    const captured = await captureVideoPoster(source, { signal, maxWidth: 400 });
    throwIfAborted(signal);
    if (!captured.poster) return null;
    const localUrl = URL.createObjectURL(captured.poster);
    return { localUrl };
}

function waitForBrowserIdle(signal?: AbortSignal) {
    return new Promise<void>((resolve, reject) => {
        if (signal?.aborted) {
            reject(abortError());
            return;
        }
        let idleId: number | undefined;
        let timerId: ReturnType<typeof globalThis.setTimeout> | undefined;
        const idleWindow = window as unknown as {
            requestIdleCallback?: Window["requestIdleCallback"];
            cancelIdleCallback?: Window["cancelIdleCallback"];
        };
        const cleanup = () => {
            signal?.removeEventListener("abort", handleAbort);
            if (idleId !== undefined) idleWindow.cancelIdleCallback?.(idleId);
            if (timerId !== undefined) globalThis.clearTimeout(timerId);
        };
        const finish = () => {
            cleanup();
            resolve();
        };
        const handleAbort = () => {
            cleanup();
            reject(abortError());
        };
        signal?.addEventListener("abort", handleAbort, { once: true });
        if (idleWindow.requestIdleCallback) idleId = idleWindow.requestIdleCallback(finish, { timeout: 1_000 });
        else timerId = globalThis.setTimeout(finish, 250);
    });
}

function throwIfAborted(signal?: AbortSignal) {
    if (signal?.aborted) throw abortError();
}

function abortError() {
    return new DOMException("Canvas video preview hydration aborted", "AbortError");
}
