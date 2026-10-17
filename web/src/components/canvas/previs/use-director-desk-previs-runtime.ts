import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { createDirectorRuntime, disposeDirectorRuntime, DIRECTOR_DESK_VERSION, type DirectorRuntime } from "@/integrations/director-desk";
import { recordPrevisDiagnostic } from "@/lib/canvas/previs/previs-diagnostics-recorder";
import { previsSceneToDirectorDocument } from "@/integrations/director-desk/previs-adapter";
import { decideRuntimeSync, resolveRuntimeViewState, type RuntimeSyncKey } from "@/integrations/director-desk/scene-sync";
import type { DirectorProject } from "@/integrations/director-desk/previs-runtime-types";
import type { PrevisScene } from "@/types/previs";

export type DirectorDeskPrevisRuntimeState = {
    stageRef: RefObject<HTMLDivElement | null>;
    shotRef: RefObject<HTMLDivElement | null>;
    runtime: DirectorRuntime | null;
    status: "idle" | "ready" | "error";
    error: unknown;
    syncScene: (scene: PrevisScene | null, force?: boolean) => void;
};

/** runtime 事件回传（宿主 create 时固定，经 latest-ref 读取最新处理器）。 */
export type DirectorRuntimeEvents = {
    onTimeChange?: (time: number) => void;
    onPlayingChange?: (playing: boolean) => void;
    onSelectChange?: (entityId: string) => void;
    onError?: (error: unknown) => void;
};

/** Owns the DirectorDesk browser runtime for exactly one workbench session. */
export function useDirectorDeskPrevisRuntime(open: boolean, scene: PrevisScene | null, onDocumentChange?: (document: DirectorProject) => void, runtimeEvents?: DirectorRuntimeEvents): DirectorDeskPrevisRuntimeState {
    const stageRef = useRef<HTMLDivElement>(null);
    const shotRef = useRef<HTMLDivElement>(null);
    const runtimeRef = useRef<DirectorRuntime | null>(null);
    const [status, setStatus] = useState<DirectorDeskPrevisRuntimeState["status"]>("idle");
    const [error, setError] = useState<unknown>(null);

    // 提交回调身份随父级 store 更新变化（savePrevisScene 依赖 previsScenes）。
    // 引擎生命周期必须与回调身份无关，否则每次编辑回执都会重建 WebGL 引擎；
    // 用 latest-ref 解耦：创建 effect 只依赖 open/scene 存在性。
    const onDocumentChangeRef = useRef(onDocumentChange);
    useEffect(() => {
        onDocumentChangeRef.current = onDocumentChange;
    }, [onDocumentChange]);

    const runtimeEventsRef = useRef(runtimeEvents);
    useEffect(() => {
        runtimeEventsRef.current = runtimeEvents;
    }, [runtimeEvents]);

    // 按 sceneId/updatedAt 决策刷新：同场景同修订不重建（对象身份抖动免疫），
    // 同场景修订变化重建并保留/回退视图状态，场景 id 变化视为新会话。
    const lastSyncKeyRef = useRef<RuntimeSyncKey | null>(null);

    const syncScene = useCallback((nextScene: PrevisScene | null, force = false) => {
        const runtime = runtimeRef.current;
        if (!nextScene || !runtime) return;
        const nextKey: RuntimeSyncKey = { id: nextScene.id, updatedAt: nextScene.updatedAt };
        // force：提交被拒后把引擎强行拉回权威场景（store 未变 → 键相同 → 按 content 处理）。
        const decision = force && lastSyncKeyRef.current && lastSyncKeyRef.current.id === nextKey.id ? { kind: "content" as const } : decideRuntimeSync(lastSyncKeyRef.current, nextKey);
        if (decision.kind === "noop") return;
        let document: DirectorProject;
        try {
            document = previsSceneToDirectorDocument(nextScene);
        } catch {
            // 新场景无法转换时保留当前文档与修订记忆，不推进 lastSyncKeyRef；
            // 工作台的保存协调器会走原有的校验/诊断路径暴露问题。
            return;
        }
        lastSyncKeyRef.current = nextKey;
        runtime.scene.userData.directorDocument = document;
        if (decision.kind === "session") {
            // 先同步文档（更新 runtime 内部 currentDocument），再按新文档初始化视图状态。
            runtime.syncDocument(document);
            runtime.resetViewState();
        } else {
            // 外部/Agent 更新：选择、播放头、当前镜头仍存在则保留，否则确定性回退。
            runtime.syncDocument(document);
            runtime.applyViewState(resolveRuntimeViewState(document, runtime.viewState));
        }
        runtime.render();
    }, []);

    useEffect(() => {
        if (!open || !scene || !stageRef.current || !shotRef.current) return;
        if (runtimeRef.current) return;
        try {
            runtimeRef.current = createDirectorRuntime({
                stage: stageRef.current,
                shot: shotRef.current,
                onDocumentChange: (document) => onDocumentChangeRef.current?.(document),
                onTimeChange: (time) => runtimeEventsRef.current?.onTimeChange?.(time),
                onPlayingChange: (playing) => runtimeEventsRef.current?.onPlayingChange?.(playing),
                onError: (error) => runtimeEventsRef.current?.onError?.(error),
            });
            lastSyncKeyRef.current = null;
            syncScene(scene);
            setStatus("ready");
        } catch (cause) {
            setError(cause);
            setStatus("error");
            // 可观测性:运行时初始化失败必须留下带版本的结构化事件(P5 回退演练)。
            recordPrevisDiagnostic("PREVIS_RUNTIME_INIT_FAILED", { sceneId: scene?.id, runtimeVersion: DIRECTOR_DESK_VERSION });
        }
        return () => {
            if (!runtimeRef.current) return;
            disposeDirectorRuntime(runtimeRef.current);
            runtimeRef.current = null;
            setStatus("idle");
        };
    }, [open, Boolean(scene), syncScene]);

    useEffect(() => {
        if (!runtimeRef.current || !scene) return;
        syncScene(scene);
    }, [scene, syncScene]);

    return { stageRef, shotRef, runtime: runtimeRef.current, status, error, syncScene };
}
