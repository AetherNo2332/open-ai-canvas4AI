import { useCallback, useEffect, useMemo, useRef, useState, type ComponentProps } from "react";
import { CanvasPrevisWorkbench } from "./canvas-previs-workbench";
import { useDirectorDeskPrevisRuntime } from "./use-director-desk-previs-runtime";
import { usePrevisSaveCoordinator } from "./use-previs-save-coordinator";
import { directorDeskRuntimePolicy } from "@/lib/canvas/director-desk-feature";
import { useUserStore } from "@/stores/use-user-store";
import { useActiveTheme } from "@/stores/canvas/use-canvas-theme-store";
import { canvasCommandSession } from "@/stores/canvas/use-canvas-session-store";
import { recordPrevisDiagnostic } from "@/lib/canvas/previs/previs-diagnostics-recorder";
import type { PrevisScene, PrevisShot } from "@/types/previs";
import { directorDocumentToPrevisScene, previsSceneToDirectorDocument } from "@/integrations/director-desk/previs-adapter";
import { createDirectorControllerTransport, createDirectorEditBridge, type DirectorEditBridge } from "@/integrations/director-desk/edit-bridge";
import type { DirectorProject } from "@/integrations/director-desk/previs-runtime-types";
import { DirectorDeskAppFrame } from "./director-desk-app-frame";
import { createDirectorDeskExportHandler } from "@/integrations/director-desk/export-handler";
import { createDirectorDeskExportTransport, ensureDirectorDeskCanvasSession } from "@/integrations/director-desk/canvas-export-transport";
import { queryGenerationTask, waitForGenerationTask } from "@/services/api/task-center";
import { readTaskMaterialization } from "@/services/api/canvas-commands";
import "./director-desk-previs-workbench.css";

export type DirectorDeskPrevisWorkbenchProps = ComponentProps<typeof CanvasPrevisWorkbench> & {
    /** 打开影策 Agent(导演台顶栏入口);旧工作台不消费。 */
    onOpenAgent?: () => void;
    onCanvasRefresh?: () => void | Promise<void>;
    /** Optional Agent patch envelope; applying it is delegated to the iframe host bridge. */
    agentPatch?: ComponentProps<typeof DirectorDeskAppFrame>["agentPatch"];
};

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
    const theme = useActiveTheme();
    // canary/authoritative 走原版应用(iframe);legacy 仍走既有工作台。
    const usingApp = policy !== "legacy" && props.open && Boolean(scene);
    // 应用加载失败 → 本会话回退到旧工作台;换场景时重置,让新场景重新尝试应用壳。
    const [appFailed, setAppFailed] = useState(false);
    const [agentPatch, setAgentPatch] = useState<ComponentProps<typeof DirectorDeskAppFrame>["agentPatch"]>(null);
    useEffect(() => {
        setAppFailed(false);
    }, [scene?.id]);

    // Canvas Agent writes remain the canvas fact source. Once the projected
    // PrevisScene changes, feed the new document into the already-open iframe
    // while explicitly preserving its transient selection/playhead state.
    useEffect(() => {
        if (policy === "legacy" || !scene) {
            setAgentPatch(null);
            return;
        }
        setAgentPatch({ document: previsSceneToDirectorDocument(scene), preserveViewState: true, revision: scene.updatedAt });
    }, [policy, scene?.id, scene?.updatedAt]);

    const saveController = usePrevisSaveCoordinator({
        sceneId: policy === "legacy" ? null : (scene?.id ?? null),
        initialScene: scene,
        persistScene: (next) => props.onChange(next),
        flushPersistence: () => props.onFlush?.(),
    });
    const saveControllerRef = useRef(saveController);
    saveControllerRef.current = saveController;

    // 冲突回同步需要「当前 store 场景」：桥回调创建一次，不能用闭包里的旧 scene。
    const sceneRef = useRef(scene);
    sceneRef.current = scene;

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
                hostScene: () => sceneRef.current ?? null,
                // 诊断白名单只允许 sceneId/revision（reason 会被投影丢弃）；
                // save-failed 另有协调器自身的 PREVIS_SAVE_FLUSH_FAILED（error 级）记录。
                onCommitConflict: (record) => {
                    recordPrevisDiagnostic("PREVIS_DIRECTOR_COMMIT_CONFLICT", { sceneId: scene.id, revision: record.revision });
                    // rejected/translation：store 场景未变，引擎里的新内容没有落库——
                    // 把 runtime 强制拉回权威场景，避免界面与已保存状态漂移。
                    // save-failed 不需要：coordinator 草稿已是新场景，重试恢复后自然一致。
                    if (record.conflictReason === "rejected" || record.conflictReason === "translation") {
                        const authoritative = sceneRef.current;
                        if (authoritative && authoritative.id === scene.id) {
                            runtimeRef.current?.syncScene(authoritative, true);
                        }
                    }
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

    const onDocumentChange = useCallback(
        (document: DirectorProject) => {
            const bridge = editBridgeRef.current;
            if (!bridge) {
                // Legacy mode or a not-yet-ready session: keep the previous
                // mirror behavior so no edit is dropped.
                try {
                    props.onChange(directorDocumentToPrevisScene(document, sceneRef.current));
                } catch {
                    // Keep the current draft visible; the workbench surfaces the
                    // next save/validation error through its existing coordinator.
                }
                return;
            }
            bridge.commitDocument(document);
        },
        [props.onChange],
    );

    // runtime 事件回传 → 编辑器 UI 状态（播放头/播放/选择）。
    const [editorTime, setEditorTime] = useState(0);
    const [editorPlaying, setEditorPlaying] = useState(false);
    const [editorSelectedEntityId, setEditorSelectedEntityId] = useState("");
    const runtime = useDirectorDeskPrevisRuntime(props.open && !usingApp, props.scene, onDocumentChange, {
        onTimeChange: (time) => setEditorTime(time),
        onPlayingChange: (playing) => setEditorPlaying(playing),
        onSelectChange: (entityId) => setEditorSelectedEntityId(entityId),
    });
    const runtimeRef = useRef(runtime);
    runtimeRef.current = runtime;

    // 编辑快捷键（canary/authoritative 会话）：Ctrl/Cmd+Z 撤销、Shift 变体重做。
    // 输入态（输入框/富文本）不劫持；桥不存在（legacy 或无场景）时不处理。
    useEffect(() => {
        if (policy === "legacy") return;
        const onKeyDown = (event: KeyboardEvent) => {
            if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
            if (event.key.toLowerCase() !== "z") return;
            const bridge = editBridgeRef.current;
            if (!bridge) return;
            const target = event.target as HTMLElement | null;
            const inEditable = Boolean(target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable));
            if (inEditable) return;
            event.preventDefault();
            if (event.shiftKey) {
                bridge.redo();
            } else {
                bridge.undo();
            }
        };
        window.addEventListener("keydown", onKeyDown);
        return () => {
            window.removeEventListener("keydown", onKeyDown);
        };
    }, [policy]);
    const submitPreview = useCallback(
        async (scene: PrevisScene, shot: PrevisShot) => {
            if (!props.canvasId) return false;
            const session = canvasCommandSession(props.canvasId);
            if (!session) return false;
            await session.submitPrevis({ sceneId: scene.id, shotId: shot.id, options: { duration: shot.duration, fps: shot.fps } }, { operationId: `director-preview:${scene.id}:${shot.id}:${scene.updatedAt}` });
            return true;
        },
        [props.canvasId],
    );
    const refreshRef = useRef(props.onCanvasRefresh);
    refreshRef.current = props.onCanvasRefresh;
    const userScope = useUserStore((state) => state.user?.id) ?? "guest";
    const exportHandler = useMemo(
        () =>
            !props.canvasId || !scene?.id
                ? null
                : createDirectorDeskExportHandler({
                      canvasId: props.canvasId,
                      sceneId: scene.id,
                      scene: () => sceneRef.current,
                      saveDocument: async (document) => {
                          const record = editBridgeRef.current?.commitDocument(document as DirectorProject);
                          if (!record?.scene || record.status === "conflict" || !(await saveControllerRef.current.retry())) throw new Error("保存未完成，请先重试保存再导出");
                          return record.scene;
                      },
                      transport: createDirectorDeskExportTransport({ session: canvasCommandSession, queryTask: queryGenerationTask, readMaterialization: readTaskMaterialization }),
                      waitTask: (taskId) => waitForGenerationTask(taskId),
                      onRefresh: () => refreshRef.current?.(),
                  }),
        [props.canvasId, scene?.id],
    );
    // legacy 模式(或未打开/无场景)沿用工作台自己的协调器;应用壳只在 canary/authoritative 生效。
    if (!usingApp || !scene) return <CanvasPrevisWorkbench {...props} />;

    // app 加载失败回退到旧工作台,注入同一 saveController(运行时故障回退,数据不离手)。
    if (appFailed) {
        return <CanvasPrevisWorkbench {...props} saveController={saveController} onDraftChange={runtime.syncScene} onSubmitPreview={submitPreview} />;
    }

    return (
        <DirectorDeskAppFrame
            canvasId={props.canvasId}
            theme={theme}
            sceneId={scene.id}
            scene={scene}
            saveController={saveController}
            readDocument={() => {
                const current = sceneRef.current;
                return current ? previsSceneToDirectorDocument(current) : null;
            }}
            // bridge 载荷是不可信 unknown:形状校验由 commitDocument/适配器负责并在失败时保留草稿。
            onSceneChange={(document) => onDocumentChange(document as DirectorProject)}
            onSaveRequest={async () => {
                // 应用内「保存」= 把草稿真正落盘:复用协调器的既有 flush 语义。
                const ok = await saveController.retry();
                return { ok, message: ok ? undefined : "保存未完成" };
            }}
            onExportRequest={async (payload, publish) => {
                if (!exportHandler) throw new Error("导出画布未就绪");
                if (!props.canvasId) throw new Error("导出画布未就绪");
                await ensureDirectorDeskCanvasSession({ canvasId: props.canvasId, scope: userScope });
                return exportHandler(payload, (event) => {
                    if (event.status === "failed") recordPrevisDiagnostic("PREVIS_DIRECTOR_EXPORT_FAILED", { sceneId: scene.id, requestId: event.taskId });
                    publish(event);
                });
            }}
            agentPatch={props.agentPatch ?? agentPatch}
            onReady={() => {
                const current = sceneRef.current;
                if (current) {
                    saveControllerRef.current.commitScene(current);
                }
            }}
            onClose={props.onClose}
            onFallback={() => setAppFailed(true)}
            onOpenAgent={props.onOpenAgent}
        />
    );
}
