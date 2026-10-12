import { useCallback, type ComponentProps } from "react";
import { CanvasPrevisWorkbench } from "./canvas-previs-workbench";
import { useDirectorDeskPrevisRuntime } from "./use-director-desk-previs-runtime";
import { directorDeskRuntimePolicy } from "@/lib/canvas/director-desk-feature";
import { useUserStore } from "@/stores/use-user-store";
import { canvasCommandSession } from "@/stores/canvas/use-canvas-session-store";
import type { PrevisScene, PrevisShot } from "@/types/previs";
import "./director-desk-previs-workbench.css";

export type DirectorDeskPrevisWorkbenchProps = ComponentProps<typeof CanvasPrevisWorkbench>;

/**
 * Single previs entry point backed by the DirectorDesk browser runtime.
 * The existing workbench remains the product shell while the runtime seam is
 * migrated incrementally; Agent/save/approval callbacks stay untouched.
 */
export function DirectorDeskPrevisWorkbench(props: DirectorDeskPrevisWorkbenchProps) {
    const mode = useUserStore((state) => state.features.directorDeskPrevisMode);
    const policy = directorDeskRuntimePolicy(mode);
    const runtime = useDirectorDeskPrevisRuntime(props.open, props.scene);
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
            <CanvasPrevisWorkbench {...props} onDraftChange={runtime.syncScene} onSubmitPreview={submitPreview} />
        </>
    );
}
