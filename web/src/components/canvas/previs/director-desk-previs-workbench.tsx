import { useCallback, useRef, type ComponentProps } from "react";
import { CanvasPrevisWorkbench } from "./canvas-previs-workbench";
import { useDirectorDeskPrevisRuntime } from "./use-director-desk-previs-runtime";
import { directorDeskRuntimePolicy } from "@/lib/canvas/director-desk-feature";
import { useUserStore } from "@/stores/use-user-store";
import { canvasCommandSession } from "@/stores/canvas/use-canvas-session-store";
import type { PrevisScene, PrevisShot } from "@/types/previs";
import { directorDocumentToPrevisScene } from "@/integrations/director-desk/previs-adapter";
import type { DirectorProject } from "@/integrations/director-desk/previs-runtime-types";
import { createDirectorDeskCommitBridge } from "./director-desk-commit-bridge";
import "./director-desk-previs-workbench.css";

export type DirectorDeskPrevisWorkbenchProps = ComponentProps<typeof CanvasPrevisWorkbench>;

/**
 * Single previs entry point backed by the DirectorDesk browser runtime.
 * The existing workbench remains the product shell while the runtime seam is
 * migrated incrementally; Agent/save/approval callbacks stay untouched.
 */
export function DirectorDeskPrevisWorkbench(props: DirectorDeskPrevisWorkbenchProps) {
    const mode = useUserStore((state) => state.features.directorDeskPrevisMode);
    const userId = useUserStore((state) => state.user?.id);
    const policy = directorDeskRuntimePolicy(mode, userId);
    const commitBridgeRef = useRef<ReturnType<typeof createDirectorDeskCommitBridge> | null>(null);
    if (!commitBridgeRef.current) commitBridgeRef.current = createDirectorDeskCommitBridge(props.onChange);
    const onCommitReady = useCallback((commit: (scene: PrevisScene) => void) => {
        commitBridgeRef.current?.register(commit);
    }, []);
    const onDocumentChange = useCallback((document: DirectorProject) => {
        try {
            commitBridgeRef.current?.commit(directorDocumentToPrevisScene(document));
        } catch {
            // Keep the current draft visible; the legacy workbench will surface
            // the next save/validation error through its existing coordinator.
        }
    }, [props.onChange]);
    const runtime = useDirectorDeskPrevisRuntime(props.open, props.scene, onDocumentChange);
    const submitPreview = useCallback(async (scene: PrevisScene, shot: PrevisShot) => {
        if (!props.canvasId) return false;
        const session = canvasCommandSession(props.canvasId);
        if (!session) return false;
        await session.submitPrevis(
            { sceneId: scene.id, shotId: shot.id, options: { duration: shot.duration, fps: shot.fps } },
            { operationId: `director-preview:${scene.id}:${shot.id}:${scene.updatedAt}` },
        );
        return true;
    }, [props.canvasId]);
    if (policy === "legacy") return <CanvasPrevisWorkbench {...props} />;
    return (
        <>
            <div className="director-desk-runtime-host" data-runtime-policy={policy} data-runtime-status={runtime.status}>
                <div ref={runtime.stageRef} className="director-desk-runtime-stage" data-director-runtime="stage" />
                <div ref={runtime.shotRef} className="director-desk-runtime-stage" data-director-runtime="shot" />
                <span className="director-desk-runtime-label">DirectorDesk {policy === "canary" ? "· 灰度同步" : "· 主运行时"} {runtime.status === "ready" ? "· 已连接" : runtime.status === "error" ? "· 回退" : "· 初始化"}</span>
            </div>
            <CanvasPrevisWorkbench {...props} onDraftChange={runtime.syncScene} onSubmitPreview={submitPreview} onCommitReady={onCommitReady} />
        </>
    );
}
