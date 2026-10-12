import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { X } from "lucide-react";
import { DirectorShotList } from "./director-shot-list";
import { DirectorTimeline } from "./director-timeline";
import { DirectorInspector } from "./director-inspector";
import { addPrevisShot, updatePrevisShot, removePrevisShot, movePrevisShot } from "@/lib/canvas/previs/previs-shot-list";
import { updatePrevisObjectTransform, updatePrevisCameraParams } from "@/lib/canvas/previs/previs-inspector";
import { describePrevisSaveStatus, resolvePrevisCloseOutcome, shouldBlockPrevisUnload } from "@/lib/canvas/previs/previs-save-wiring";
import { recordPrevisDiagnostic } from "@/lib/canvas/previs/previs-diagnostics-recorder";
import { touchPrevisScene } from "@/lib/canvas/previs/previs-scene";
import type { PrevisScene, PrevisTransform, PrevisVec3 } from "@/types/previs";
import type { DirectorRuntime } from "@/integrations/director-desk";
import type { PrevisSaveController } from "./use-previs-save-coordinator";
import "./director-previs-editor.css";

export type DirectorPrevisEditorProps = {
    scene: PrevisScene;
    policy: "canary" | "authoritative";
    stageRef: RefObject<HTMLDivElement | null>;
    shotRef: RefObject<HTMLDivElement | null>;
    runtime: DirectorRuntime | null;
    runtimeStatus: "idle" | "ready" | "error";
    /** 播放头（秒）与播放状态由 wrapper 经 runtime 事件回传。 */
    time: number;
    playing: boolean;
    selectedEntityId: string;
    saveController: PrevisSaveController;
    /** commit 结果的 store 镜像（保存协调器之外唯一的外发路径）。 */
    onChange: (scene: PrevisScene) => void;
    onClose: () => void;
};

/**
 * DirectorDesk 原生导演编辑器（P1 画布核心 scope）：
 * 视口 + 时间轴 + 镜头列表 + 检查器，所有编辑走保存协调器单通道，
 * store 场景是唯一权威（无第二写者）；页面离开走 coordinator 的
 * pagehide/prepareClose 语义，与 legacy 工作台一致。
 */
export function DirectorPrevisEditor({
    scene,
    policy,
    stageRef,
    shotRef,
    runtime,
    runtimeStatus,
    time,
    playing,
    selectedEntityId,
    saveController,
    onChange,
    onClose,
}: DirectorPrevisEditorProps) {
    const [closing, setClosing] = useState(false);
    const [notice, setNotice] = useState("");
    const [exitConfirm, setExitConfirm] = useState("");

    const sceneRef = useRef(scene);
    sceneRef.current = scene;
    const onChangeRef = useRef(onChange);
    onChangeRef.current = onChange;
    const saveControllerRef = useRef(saveController);
    saveControllerRef.current = saveController;

    const indicator = describePrevisSaveStatus(saveController.progress);

    /** 唯一提交入口：纯函数算出下一个场景 → 协调器 → store 镜像。 */
    const commit = useCallback((updater: (current: PrevisScene) => PrevisScene | null) => {
        const current = sceneRef.current;
        const next = updater(current);
        if (!next || next === current) return;
        const touched = touchPrevisScene(next);
        saveControllerRef.current.commitScene(touched);
        onChangeRef.current(touched);
    }, []);

    const resetPlayhead = useCallback(() => {
        runtime?.setPlayhead(0);
    }, [runtime]);

    const selectShot = useCallback((shotId: string) => {
        commit((current) => (current.activeShotId === shotId ? null : { ...current, activeShotId: shotId }));
        resetPlayhead();
    }, [commit, resetPlayhead]);

    const addShot = useCallback(() => {
        const current = sceneRef.current;
        const cameraId = current.shots.find((shot) => shot.id === current.activeShotId)?.cameraId ?? current.cameras[0]?.id;
        if (!cameraId) return;
        commit((candidate) => {
            const result = addPrevisShot(candidate, cameraId);
            return result ? result.scene : null;
        });
        resetPlayhead();
    }, [commit, resetPlayhead]);

    const updateShot = useCallback((id: string, patch: Parameters<typeof updatePrevisShot>[2]) => {
        commit((current) => updatePrevisShot(current, id, patch));
    }, [commit]);

    const removeShot = useCallback((id: string) => {
        let wasActive = false;
        commit((current) => {
            wasActive = current.activeShotId === id;
            return removePrevisShot(current, id);
        });
        if (wasActive) resetPlayhead();
    }, [commit, resetPlayhead]);

    const moveShot = useCallback((id: string, delta: -1 | 1) => {
        commit((current) => movePrevisShot(current, id, delta));
    }, [commit]);

    const updateObject = useCallback((id: string, patch: Partial<PrevisTransform>) => {
        commit((current) => updatePrevisObjectTransform(current, id, patch));
    }, [commit]);

    const updateCamera = useCallback((id: string, patch: { focalLength?: number; target?: PrevisVec3 }) => {
        commit((current) => updatePrevisCameraParams(current, id, patch));
    }, [commit]);

    /** 关闭统一入口：prepareClose 决策 → 直接退出 / 二次确认 / 留在现场。 */
    const closeEditor = useCallback(() => {
        if (closing) return;
        setClosing(true);
        void (async () => {
            let outcome;
            try {
                outcome = resolvePrevisCloseOutcome(await saveControllerRef.current.prepareClose());
            } catch {
                setNotice("关闭前的保存检查失败，已留在预演台");
                setClosing(false);
                return;
            }
            if (outcome.kind === "close") {
                onClose();
                return;
            }
            if (outcome.kind === "blocked") {
                recordPrevisDiagnostic("PREVIS_CLOSE_BLOCKED", { sceneId: sceneRef.current.id, saveOutcome: "stay", revision: saveControllerRef.current.progress.revision, draftStored: saveControllerRef.current.progress.draftStored });
                setNotice(outcome.message);
                setClosing(false);
                return;
            }
            // 二次确认用内联面板（不用静态 Modal.confirm）。
            setExitConfirm(outcome.message);
            setClosing(false);
        })();
    }, [closing, onClose]);

    // 离开页面：dirty 时 coordinator.handlePageHide 自己 persist + flush；
    // beforeunload 只同步声明「仍有未确认改动」。
    useEffect(() => {
        const onPageHide = () => {
            void saveControllerRef.current.handlePageHide();
        };
        const onBeforeUnload = (event: BeforeUnloadEvent) => {
            const controller = saveControllerRef.current;
            if (!controller || !shouldBlockPrevisUnload(controller.progress)) return;
            event.preventDefault();
            event.returnValue = "";
        };
        window.addEventListener("pagehide", onPageHide);
        window.addEventListener("beforeunload", onBeforeUnload);
        return () => {
            window.removeEventListener("pagehide", onPageHide);
            window.removeEventListener("beforeunload", onBeforeUnload);
        };
    }, []);

    return (
        <div className="dpe-root" data-runtime-policy={policy} data-runtime-status={runtimeStatus}>
            <header className="dpe-header">
                <div className="dpe-header__titles">
                    <span className="dpe-header__kicker">DirectorDesk · {policy === "canary" ? "灰度同步" : "主运行时"}</span>
                    <span className="dpe-header__scene">{scene.title}</span>
                </div>
                <div className="dpe-save" data-tone={indicator.tone} role="status">
                    <span className="dpe-save__label">{indicator.label}</span>
                    {indicator.retryable ? (
                        <button type="button" className="dpe-save__retry" onClick={() => { void saveController.retry(); }}>
                            重试
                        </button>
                    ) : null}
                </div>
                <button type="button" className="dpe-close" aria-label="关闭预演台" title="关闭" disabled={closing} onClick={closeEditor}>
                    <X className="size-4" aria-hidden />
                </button>
            </header>
            {notice ? (
                <div className="dpe-notice" role="alert">{notice}</div>
            ) : null}
            {exitConfirm ? (
                <div className="dpe-confirm" role="alertdialog" aria-label="远端保存失败">
                    <span className="dpe-confirm__msg">{exitConfirm}</span>
                    <div className="dpe-confirm__actions">
                        <button type="button" className="dpe-confirm__ok" onClick={() => { setExitConfirm(""); onClose(); }}>仍然离开</button>
                        <button type="button" className="dpe-confirm__cancel" onClick={() => setExitConfirm("")}>留在预演台</button>
                    </div>
                </div>
            ) : null}
            <div className="dpe-body">
                <div className="dpe-stage-column">
                    <div className="dpe-stage-host" data-director-runtime-host="true">
                        <div ref={stageRef} className="dpe-stage" data-director-runtime="stage" />
                        <div ref={shotRef} className="dpe-stage" data-director-runtime="shot" />
                        <span className="dpe-stage-label">
                            视口 {runtimeStatus === "ready" ? "· 已连接" : runtimeStatus === "error" ? "· 回退" : "· 初始化"}
                        </span>
                    </div>
                    <div className="dpe-timeline">
                        <DirectorTimeline
                            scene={scene}
                            time={time}
                            playing={playing}
                            onScrub={(value) => runtime?.setPlayhead(value)}
                            onTogglePlay={() => {
                                if (playing) runtime?.pause();
                                else runtime?.play();
                            }}
                            onSelectShot={selectShot}
                        />
                    </div>
                </div>
                <aside className="dpe-panels" aria-label="导演编辑面板">
                    <div className="dpe-panels__shot-list">
                        <DirectorShotList
                            scene={scene}
                            onSelectShot={selectShot}
                            onAddShot={addShot}
                            onUpdateShot={updateShot}
                            onRemoveShot={removeShot}
                            onMoveShot={moveShot}
                        />
                    </div>
                    <div className="dpe-panels__inspector">
                        <DirectorInspector
                            scene={scene}
                            selectedEntityId={selectedEntityId}
                            activeShotId={scene.activeShotId}
                            onUpdateObject={updateObject}
                            onUpdateCamera={updateCamera}
                        />
                    </div>
                </aside>
            </div>
        </div>
    );
}