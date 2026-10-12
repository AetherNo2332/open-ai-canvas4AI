import { useCallback, useEffect, useRef, type ComponentProps } from "react";
import { CanvasPrevisWorkbench } from "./canvas-previs-workbench";
import { useDirectorDeskPrevisRuntime } from "./use-director-desk-previs-runtime";
import { usePrevisSaveCoordinator } from "./use-previs-save-coordinator";
import { directorDeskRuntimePolicy } from "@/lib/canvas/director-desk-feature";
import { useUserStore } from "@/stores/use-user-store";
import { canvasCommandSession } from "@/stores/canvas/use-canvas-session-store";
import { recordPrevisDiagnostic } from "@/lib/canvas/previs/previs-diagnostics-recorder";
import type { PrevisScene, PrevisShot } from "@/types/previs";
import { directorDocumentToPrevisScene } from "@/integrations/director-desk/previs-adapter";
import { createDirectorControllerTransport, createDirectorEditBridge, type DirectorEditBridge } from "@/integrations/director-desk/edit-bridge";
import type { DirectorProject } from "@/integrations/director-desk/previs-runtime-types";
import "./director-desk-previs-workbench.css";

export type DirectorDeskPrevisWorkbenchProps = ComponentProps<typeof CanvasPrevisWorkbench>;

/**
 * Single previs entry point backed by the DirectorDesk browser runtime.
 * In canary/authoritative mode the wrapper owns the save coordinator so the
 * runtime's document commits and the legacy workbench's draft commits share
 * one draft/flush/CAS path; legacy mode keeps the workbench's own coordinator.
 */
export function DirectorDeskPrevisWorkbench(props: DirectorDeskPrevisWorkbenchProps) {
    const mode = useUserStore((state) => state.features.directorDeskPrevisMode);
    const policy = directorDeskRuntimePolicy(mode);
    const scene = props.scene;

    const saveController = usePrevisSaveCoordinator({
        sceneId: policy === "legacy" ? null : (scene?.id ?? null),
        initialScene: scene,
        persistScene: (next) => props.onChange(next),
        flushPersistence: () => props.onFlush?.(),
    });
    const saveControllerRef = useRef(saveController);
    saveControllerRef.current = saveController;

    // One edit bridge per runtime session: runtime document commits are
    // converted to a PrevisScene and pushed through the coordinator above,
    // so they get the same local draft, debounce, CAS flush and close path
    // as legacy edits instead of a direct store mirror.
    const editBridgeRef = useRef<DirectorEditBridge | null>(null);
    useEffect(() => {
        if (policy === "legacy" || !scene) {
            editBridgeRef.current?.dispose();
            editBridgeRef.current = null;
            return;
        }
        if (!editBridgeRef.current) {
            editBridgeRef.current = createDirectorEditBridge(createDirectorControllerTransport(saveControllerRef.current), {
                expectedSceneId: scene.id,
                // 诊断白名单只允许 sceneId/revision（reason 会被投影丢弃）；
                // save-failed 另有协调器自身的 PREVIS_SAVE_FLUSH_FAILED（error 级）记录。
                onCommitConflict: (record) => {
                    recordPrevisDiagnostic("PREVIS_DIRECTOR_COMMIT_CONFLICT", { sceneId: scene.id, revision: record.revision });
                },
            });
        }
        return () => {
            if (editBridgeRef.current) {
                editBridgeRef.current.dispose();
                editBridgeRef.current = null;
            }
        };
        // Bridge lifetime follows the session (mode + scene id), not every
        // scene object update.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [policy, scene?.id]);

    const onDocumentChange = useCallback((document: DirectorProject) => {
        const bridge = editBridgeRef.current;
        if (!bridge) {
            // Legacy mode or a not-yet-ready session: keep the previous
            // mirror behavior so no edit is dropped.
            try {
                props.onChange(directorDocumentToPrevisScene(document));
            } catch {
                // Keep the current draft visible; the workbench surfaces the
                // next save/validation error through its existing coordinator.
            }
            return;
        }
        bridge.commitDocument(document);
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
            <CanvasPrevisWorkbench {...props} saveController={saveController} onDraftChange={runtime.syncScene} onSubmitPreview={submitPreview} />
        </>
    );
}