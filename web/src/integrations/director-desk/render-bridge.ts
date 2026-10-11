import { directorPreviewInput } from "./previs-adapter";
import type { PrevisScene } from "@/types/previs";
import type { DirectorPreviewInput } from "./previs-runtime-types";

export type DirectorRenderResult = { taskId: string; status: "queued" | "running" | "succeeded" };
export type DirectorRenderSubmitOptions = { requestId: string; signal: AbortSignal };
export type DirectorRenderBridge = ReturnType<typeof createDirectorRenderBridge>;

type RenderOptions = { shotId?: string; time?: number; requestId?: string; signal?: AbortSignal };

function keyFor(input: DirectorPreviewInput) {
    return `${input.sourceSceneId}:${input.shotId}:${input.time}:${input.project.extensions.infiniteCanvas.scene.updatedAt}`;
}

export function createDirectorRenderBridge(options: { submit: (input: DirectorPreviewInput, options: DirectorRenderSubmitOptions) => Promise<DirectorRenderResult> }) {
    const pending = new Map<string, { promise: Promise<DirectorRenderResult>; controller: AbortController }>();
    const preview = (scene: PrevisScene, renderOptions: RenderOptions = {}) => {
        const input = directorPreviewInput(scene, renderOptions.shotId || scene.activeShotId, renderOptions.time || 0);
        const key = keyFor(input);
        const existing = pending.get(key);
        if (existing) return existing.promise;
        const controller = new AbortController();
        renderOptions.signal?.addEventListener("abort", () => controller.abort(), { once: true });
        const promise = options.submit(input, { requestId: renderOptions.requestId || `director-preview:${key}`, signal: controller.signal }).finally(() => pending.delete(key));
        pending.set(key, { promise, controller });
        return promise;
    };
    const cancel = (sceneId: string, shotId: string, time: number) => {
        for (const [key, entry] of pending) {
            if (key.startsWith(`${sceneId}:${shotId}:${time}:`)) entry.controller.abort();
        }
    };
    return { preview, cancel, pendingCount: () => pending.size };
}
