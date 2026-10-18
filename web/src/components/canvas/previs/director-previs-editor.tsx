import { useCallback, useRef, type RefObject } from "react";
import { X } from "lucide-react";
import { DirectorShotList } from "./director-shot-list";
import { DirectorTimeline } from "./director-timeline";
import { DirectorInspector } from "./director-inspector";
import { addPrevisShot, updatePrevisShot, removePrevisShot, movePrevisShot } from "@/lib/canvas/previs/previs-shot-list";
import { updatePrevisObjectTransform, updatePrevisCameraParams } from "@/lib/canvas/previs/previs-inspector";
import { describePrevisSaveStatus } from "@/lib/canvas/previs/previs-save-wiring";
import { usePrevisCloseHandler } from "@/hooks/use-previs-close-handler";
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
export function DirectorPrevisEditor({ scene, policy, stageRef, shotRef, runtime, runtimeStatus, time, playing, selectedEntityId, saveController, onChange, onClose }: DirectorPrevisEditorProps) {
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

    const selectShot = useCallback(
        (shotId: string) => {
            commit((current) => (current.activeShotId === shotId ? null : { ...current, activeShotId: shotId }));
            resetPlayhead();
        },
        [commit, resetPlayhead],
    );

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

    const updateShot = useCallback(
        (id: string, patch: Parameters<typeof updatePrevisShot>[2]) => {
            commit((current) => updatePrevisShot(current, id, patch));
        },
        [commit],
    );

    const removeShot = useCallback(
        (id: string) => {
            let wasActive = false;
            commit((current) => {
                wasActive = current.activeShotId === id;
                return removePrevisShot(current, id);
            });
            if (wasActive) resetPlayhead();
        },
        [commit, resetPlayhead],
    );

    const moveShot = useCallback(
        (id: string, delta: -1 | 1) => {
            commit((current) => movePrevisShot(current, id, delta));
        },
        [commit],
    );

    const updateObject = useCallback(
        (id: string, patch: Partial<PrevisTransform>) => {
            commit((current) => updatePrevisObjectTransform(current, id, patch));
        },
        [commit],
    );

    const updateCamera = useCallback(
        (id: string, patch: { focalLength?: number; target?: PrevisVec3 }) => {
            commit((current) => updatePrevisCameraParams(current, id, patch));
        },
        [commit],
    );

    // 关闭决策与离页语义(prepareClose / pagehide / beforeunload)由共享 hook 统一实现。
    const { closing, notice, exitConfirm, closeHandler: closeEditor, confirmExit, cancelExit } = usePrevisCloseHandler({ saveController, scene, onClose });

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
                        <button
                            type="button"
                            className="dpe-save__retry"
                            onClick={() => {
                                void saveController.retry();
                            }}
                        >
                            重试
                        </button>
                    ) : null}
                </div>
                <button type="button" className="dpe-close" aria-label="关闭预演台" title="关闭" disabled={closing} onClick={closeEditor}>
                    <X className="size-4" aria-hidden />
                </button>
            </header>
            {notice ? (
                <div className="dpe-notice" role="alert">
                    {notice}
                </div>
            ) : null}
            {exitConfirm ? (
                <div className="dpe-confirm" role="alertdialog" aria-label="远端保存失败">
                    <span className="dpe-confirm__msg">{exitConfirm}</span>
                    <div className="dpe-confirm__actions">
                        <button type="button" className="dpe-confirm__ok" onClick={confirmExit}>
                            仍然离开
                        </button>
                        <button type="button" className="dpe-confirm__cancel" onClick={cancelExit}>
                            留在预演台
                        </button>
                    </div>
                </div>
            ) : null}
            <div className="dpe-body">
                <div className="dpe-stage-column">
                    <div className="dpe-stage-host" data-director-runtime-host="true">
                        <div ref={stageRef} className="dpe-stage" data-director-runtime="stage" />
                        <div ref={shotRef} className="dpe-stage" data-director-runtime="shot" />
                        <span className="dpe-stage-label">视口 {runtimeStatus === "ready" ? "· 已连接" : runtimeStatus === "error" ? "· 回退" : "· 初始化"}</span>
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
                        <DirectorShotList scene={scene} onSelectShot={selectShot} onAddShot={addShot} onUpdateShot={updateShot} onRemoveShot={removeShot} onMoveShot={moveShot} />
                    </div>
                    <div className="dpe-panels__inspector">
                        <DirectorInspector scene={scene} selectedEntityId={selectedEntityId} activeShotId={scene.activeShotId} onUpdateObject={updateObject} onUpdateCamera={updateCamera} />
                    </div>
                </aside>
            </div>
        </div>
    );
}
