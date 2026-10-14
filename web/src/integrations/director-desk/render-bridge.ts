import { directorPreviewInput } from "./previs-adapter";
import type { PrevisScene } from "@/types/previs";
import { DIRECTOR_CANVAS_EXTENSION_KEY, type DirectorPreviewInput } from "./previs-runtime-types";
import type { PrevisExportKind } from "./export-to-canvas";

export type DirectorRenderResult = { taskId: string; status: "queued" | "running" | "succeeded" };
export type DirectorRenderSubmitOptions = { requestId: string; signal: AbortSignal; kind?: PrevisExportKind };
export type DirectorRenderBridge = ReturnType<typeof createDirectorRenderBridge>;

type RenderOptions = { shotId?: string; time?: number; requestId?: string; signal?: AbortSignal };

function keyFor(input: DirectorPreviewInput) {
    return `${input.sourceSceneId}:${input.shotId}:${input.time}:${input.project.extensions[DIRECTOR_CANVAS_EXTENSION_KEY].scene.updatedAt}`;
}

export function createDirectorRenderBridge(options: { submit: (input: DirectorPreviewInput, options: DirectorRenderSubmitOptions) => Promise<DirectorRenderResult> }) {
    const pending = new Map<string, { promise: Promise<DirectorRenderResult>; controller: AbortController }>();
    const render = (scene: PrevisScene, renderOptions: RenderOptions = {}, kind?: PrevisExportKind) => {
        const input = directorPreviewInput(scene, renderOptions.shotId || scene.activeShotId, renderOptions.time || 0);
        const key = `${keyFor(input)}:${kind || "preview"}`;
        if (renderOptions.signal?.aborted) return Promise.reject(new DOMException("The operation was aborted", "AbortError"));
        const existing = pending.get(key);
        if (existing) return existing.promise;
        const controller = new AbortController();
        renderOptions.signal?.addEventListener("abort", () => controller.abort(), { once: true });
        const promise = options.submit(input, { requestId: renderOptions.requestId || `director-preview:${crypto.randomUUID()}`, signal: controller.signal, kind }).finally(() => pending.delete(key));
        pending.set(key, { promise, controller });
        return promise;
    };
    const cancel = (sceneId: string, shotId: string, time: number) => {
        for (const [key, entry] of pending) {
            if (key.startsWith(`${sceneId}:${shotId}:${time}:`)) entry.controller.abort();
        }
    };
    return { preview: (scene: PrevisScene, renderOptions: RenderOptions = {}) => render(scene, renderOptions), exportToCanvas: (scene: PrevisScene, kind: PrevisExportKind, renderOptions: RenderOptions = {}) => render(scene, renderOptions, kind), cancel, pendingCount: () => pending.size };
}
