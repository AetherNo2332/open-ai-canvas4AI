import type { AgentEvent } from "@/services/api/agent";
import type { AgentCanvasPatch } from "@/lib/canvas/agent-canvas-patch";
import { directorToolAction } from "@/lib/canvas/agent-director-launch";

type Options = {
    canvasId: string;
    applyPatches: (patches: AgentCanvasPatch[]) => Promise<unknown>;
    refresh: () => Promise<unknown>;
    /** While true, committed Agent writes arrive as realtime canvas_changed and are merged incrementally. */
    realtimeConnected?: () => boolean;
    onError: (error: unknown) => void;
    batchMs?: number;
    refreshIntervalMs?: number;
};

/** director 写入导演场景（previsScenes 与工作站节点）；read 不改画布。 */
function directorWrite(event: AgentEvent) {
    return event.type === "tool_completed" && event.payload.toolName === "director" && directorToolAction(event.payload) !== "read";
}

/**
 * Batch SSE bursts. Committed Agent writes are announced over realtime
 * (canvas_changed -> applyAgentCanvasChange); a full snapshot is only the
 * fallback when realtime is down or a patch cannot be applied in order.
 */
export function createAgentCanvasSync(options: Options) {
    let patches: AgentCanvasPatch[] = [];
    let needsRefresh = false;
    let supportsPatches = false;
    let disposed = false;
    let running = false;
    let lastRefresh = -Infinity;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const schedule = () => {
        if (disposed || running || timer || (!patches.length && !needsRefresh)) return;
        const wait = patches.length ? options.batchMs ?? 40 : Math.max(options.batchMs ?? 40, (options.refreshIntervalMs ?? 1000) - (Date.now() - lastRefresh));
        timer = setTimeout(() => { timer = undefined; void flush(); }, wait);
    };
    const flush = async () => {
        if (disposed || running) return;
        running = true;
        const batch = patches;
        patches = [];
        try {
            if (batch.length) {
                try { await options.applyPatches(batch); }
                catch { needsRefresh = true; }
            }
            if (!disposed && needsRefresh && Date.now() - lastRefresh >= (options.refreshIntervalMs ?? 1000)) {
                needsRefresh = false;
                lastRefresh = Date.now();
                await options.refresh();
            }
        } catch (error) {
            if (!disposed) options.onError(error);
        } finally {
            running = false;
            schedule();
        }
    };
    return {
        receive(event: AgentEvent) {
            if (disposed || (event.payload.canvasId && event.payload.canvasId !== options.canvasId)) return;
            const patch = event.payload.canvasPatch as AgentCanvasPatch | undefined;
            if (patch && patch.canvasId === options.canvasId) {
                supportsPatches = true;
                patches.push(patch);
            } else if (options.realtimeConnected?.()) {
                // 实时通道在线：该写入提交后会收到带 revision 的 canvas_changed，
                // 由增量合并补齐（包括被瘦身的增量），不再拉整份画布。
            } else if (event.type === "canvas_updated" && event.payload.canvasPatchSlimmed) {
                // 服务端为了控制运行态体积丢掉了这条画布增量：增量链不再完整，
                // 必须拉全量画布，否则本地会静默漏掉这一步的变更。
                needsRefresh = true;
            } else if (directorWrite(event) || event.type === "director_open" || event.type === "canvas_updated" || event.type === "canvas_undone" || (!supportsPatches && (event.type === "generation_task_created" || event.type === "tool_failed" || event.type === "run_failed" || (event.type === "tool_completed" && ["canvas_apply_ops", "generate_media"].includes(String(event.payload.toolName)))))) {
                needsRefresh = true;
            }
            schedule();
        },
        reconcile() { if (!disposed) { needsRefresh = true; schedule(); } },
        dispose() { disposed = true; if (timer) clearTimeout(timer); patches = []; },
    };
}
