import { useCanvasSessionStore, openCanvasCommandSession, commitCanvasCommandEditor } from "../stores/canvas/use-canvas-session-store";
import { createCanvasGenerationContinuation } from "./canvas-generation-continuation";
import { captureHTTP } from "./api/request";
import { waitForGenerationTask, type CreateTaskInput, type GenerationTask } from "./api/task-center";
import { waitCanvasHostMaterialization } from "./canvas-host-materialization";
import type { MaterializationStatus } from "../lib/canvas/command-contract.generated";
export async function consumeCanvasGeneration(projectId: string, taskId: string, signal?: AbortSignal): Promise<MaterializationStatus | undefined> {
    const scope = useCanvasSessionStore.getState().scope;
    if (!scope) return undefined;
    const api = captureHTTP(),
        session = await openCanvasCommandSession(projectId);
    const check = () => {
        signal?.throwIfAborted();
        if (useCanvasSessionStore.getState().scope !== scope) throw new DOMException("画布主机已切换", "AbortError");
    };
    check();
    return waitCanvasHostMaterialization(taskId, {
        signal,
        read: () => {
            check();
            return api.get<MaterializationStatus>("/canvas-runtime/tasks/" + encodeURIComponent(taskId) + "/materialization", { signal });
        },
        synchronize: async () => {
            check();
            await session.reconnect();
            check();
        },
    });
}
/** No legacy fallback for a canvas task once command mode is active. */
export async function submitCanvasGeneration(request: CreateTaskInput, signal?: AbortSignal): Promise<GenerationTask | undefined> {
    const scope = useCanvasSessionStore.getState().scope;
    if (!scope || !request.projectId) return undefined;
    const metadata = request.input?.metadata as Record<string, unknown> | undefined;
    if (typeof metadata?.nodeId !== "string" || !metadata.nodeId) throw new Error("canvas_generation_binding_missing");
    const api = captureHTTP();
    const session = await openCanvasCommandSession(request.projectId);
    if (useCanvasSessionStore.getState().scope !== scope) throw new DOMException("画布主机已切换", "AbortError");
    const continuation = createCanvasGenerationContinuation({
        session,
        commitEditor: () => commitCanvasCommandEditor(request.projectId!),
        queryTask: (id, options) => api.get<GenerationTask>("/tasks/" + encodeURIComponent(id), { signal: options?.signal }),
        waitTask: waitForGenerationTask,
    });
    return continuation.submitForNode(metadata.nodeId, request, signal);
}
