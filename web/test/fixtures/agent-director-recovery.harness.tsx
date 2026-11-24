import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";
import type { CanvasNodeData } from "@/types/canvas";
import type { PrevisScene } from "@/types/previs";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, sessionStorage: dom.sessionStorage, IS_REACT_ACT_ENVIRONMENT: true })) {
    Object.defineProperty(globalThis, key, { configurable: true, value });
}
afterAll(() => dom.happyDOM.abort());

const job = { runId: "run-1", requestId: "dpv_1", canvasId: "canvas-1", nodeId: "director-node", sceneId: "scene-1", deadline: new Date(Date.now() + 60_000).toISOString() };
const submit = mock(async () => {});
const message = { error: mock(() => {}), warning: mock(() => {}), success: mock(() => {}), info: mock(() => {}) };
mock.module("antd", () => ({ App: { useApp: () => ({ message }) } }));
const api = await import("@/services/api/agent");
mock.module("@/services/api/agent", () => ({
    ...api,
    getAgentRun: async () => ({ run: { id: job.runId, status: "running", waitKind: "director_preview", waitId: job.requestId, pendingDirectorPreview: job } }),
    submitDirectorPreviewResult: submit,
}));
mock.module("@/lib/canvas/agent-director-recovery", () => ({ findPendingCanvasDirectorPreview: async () => job }));
const { useAgentDirectorLaunch } = await import("@/pages/canvas/use-agent-director-launch");
const { directorPreviewHandledStore } = await import("@/lib/canvas/agent-director-launch");
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");

test("the original canvas resumes a still pending recording after reload with a handled marker and no uploaded result", async () => {
    directorPreviewHandledStore().markHandled(job.requestId);
    const host = document.createElement("div");
    const root = createRoot(host);
    const open = mock(() => {});
    const record = mock(async () => "resource:res-1");
    const nodes = [{ id: job.nodeId, metadata: { previsSceneId: job.sceneId } }] as CanvasNodeData[];
    const previsScenes = [{ id: job.sceneId }] as PrevisScene[];
    let controller!: ReturnType<typeof useAgentDirectorLaunch>;
    function Harness() {
        controller = useAgentDirectorLaunch({
            projectId: job.canvasId,
            previsNodeId: job.nodeId,
            nodes,
            previsScenes,
            openPrevisWorkbench: open,
        });
        return null;
    }
    try {
        await act(async () => root.render(<Harness />));
        expect(controller.directorPreview?.requestId).toBe(job.requestId);
        await act(async () => {
            controller.onDirectorPreview(controller.directorPreview!, record);
            await new Promise((resolve) => setTimeout(resolve, 0));
        });
        expect(record).toHaveBeenCalledTimes(1);
        expect(submit).toHaveBeenCalledWith(job.runId, job.requestId, { videoResourceId: "res-1" });
    } finally {
        await act(async () => root.unmount());
    }
});
