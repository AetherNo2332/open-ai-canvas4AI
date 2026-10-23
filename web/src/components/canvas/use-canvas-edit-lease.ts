import { useCallback, useEffect, useMemo, useSyncExternalStore } from "react";
import { canvasEditLeaseController, type CanvasEditState } from "@/services/canvas-edit-leases";
import { useCanvasSessionStore } from "@/stores/canvas/use-canvas-session-store";

const emptyState: CanvasEditState = { nodes: {} };
const subscribeEmpty = () => () => {};
const getEmpty = () => emptyState;
export function useCanvasEditLease(canvasId: string, nodeId: string, active = true, acquireOnOpen = false) {
    const scope = useCanvasSessionStore((state) => state.scope);
    const bootstrapState = useCanvasSessionStore((state) => state.bootstrapState);
    const generation = useCanvasSessionStore((state) => state.connectionGeneration);
    const controller = useMemo(() => (scope && canvasId ? canvasEditLeaseController(scope, canvasId) : null), [canvasId, scope, generation]);
    const state = useSyncExternalStore(controller?.subscribe || subscribeEmpty, controller?.getState || getEmpty, getEmpty);
    useEffect(() => {
        if (!controller || !active || !nodeId) return;
        const detach = controller.retain(nodeId);
        if (acquireOnOpen) void controller.begin(nodeId).catch(() => {});
        return detach;
    }, [controller, nodeId, active, acquireOnOpen]);
    const assertReady = useCallback(() => {
        if (!scope && bootstrapState !== "inactive") throw new Error("画布连接尚未就绪，编辑草稿已保留");
    }, [bootstrapState, scope]);
    const begin = useCallback(async () => {
        assertReady();
        await controller?.begin(nodeId);
    }, [assertReady, controller, nodeId]);
    const save = useCallback(
        async (persist: () => Promise<void>) => {
            assertReady();
            if (controller) await controller.save(nodeId, persist);
            else await persist();
        },
        [assertReady, controller, nodeId],
    );
    const node = state.nodes[nodeId];
    const markPending = useCallback(() => {
        controller?.markPending(nodeId);
    }, [controller, nodeId]);
    return {
        scope,
        begin,
        save,
        markPending,
        phase: node?.phase || "idle",
        message: node?.message || (scope ? "点击编辑后获取占用" : bootstrapState === "inactive" ? "本地编辑" : "正在连接画布…"),
        canEdit: !controller ? bootstrapState === "inactive" : node?.phase === "editing",
        isRemote: Boolean(controller),
        controller,
    };
}
export function useCanvasSyncActivity(canvasId: string) {
    const scope = useCanvasSessionStore((state) => state.scope);
    const generation = useCanvasSessionStore((state) => state.connectionGeneration);
    const controller = useMemo(() => (scope && canvasId ? canvasEditLeaseController(scope, canvasId) : null), [canvasId, scope, generation]);
    const state = useSyncExternalStore(controller?.subscribe || subscribeEmpty, controller?.getState || getEmpty, getEmpty);
    useEffect(() => controller?.watchStatus(), [controller]);
    return { activity: state.activity, error: state.activityError };
}
