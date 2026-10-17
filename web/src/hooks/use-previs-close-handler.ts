/**
 * 共享的预演台关闭与离页逻辑：
 * - 关闭决策（直接退出 / 二次确认 / 留在现场）
 * - beforeunload/pagehide 处理
 * - 保存状态提示与重试
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { resolvePrevisCloseOutcome, shouldBlockPrevisUnload } from "@/lib/canvas/previs/previs-save-wiring";
import { recordPrevisDiagnostic } from "@/lib/canvas/previs/previs-diagnostics-recorder";
import type { PrevisScene } from "@/types/previs";
import type { PrevisSaveController } from "@/components/canvas/previs/use-previs-save-coordinator";

export type PrevisCloseOutcome = {
    /** 关闭状态：直接离开、二次确认、留在现场 */
    kind: "close" | "blocked" | "confirm";
    /** 用户提示信息 */
    message: string;
};

export type UsePrevisCloseHandlerOptions = {
    saveController: PrevisSaveController;
    scene: PrevisScene;
    onClose: () => void;
};

export type UsePrevisCloseHandlerResult = {
    /** 当前关闭状态：是否正在处理关闭请求 */
    closing: boolean;
    /** 用户提示信息（错误或状态） */
    notice: string;
    /** 二次确认信息（有值时应显示确认对话框） */
    exitConfirm: string;
    /** 触发关闭流程 */
    closeHandler: () => void;
    /** 清除提示 */
    clearNotice: () => void;
    /** 确认离开（仍未保存状态下的二次确认） */
    confirmExit: () => void;
    /** 取消离开 */
    cancelExit: () => void;
};

export function usePrevisCloseHandler({
    saveController,
    scene,
    onClose,
}: UsePrevisCloseHandlerOptions): UsePrevisCloseHandlerResult {
    const [closing, setClosing] = useState(false);
    const [notice, setNotice] = useState("");
    const [exitConfirm, setExitConfirm] = useState("");

    const sceneRef = useRef(scene);
    sceneRef.current = scene;
    const saveControllerRef = useRef(saveController);
    saveControllerRef.current = saveController;
    const onCloseRef = useRef(onClose);
    onCloseRef.current = onClose;

    const clearNotice = useCallback(() => {
        setNotice("");
    }, []);

    const closeHandler = useCallback(() => {
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
                onCloseRef.current();
                return;
            }
            if (outcome.kind === "blocked") {
                recordPrevisDiagnostic("PREVIS_CLOSE_BLOCKED", {
                    sceneId: sceneRef.current.id,
                    saveOutcome: "stay",
                    revision: saveControllerRef.current.progress.revision,
                    draftStored: saveControllerRef.current.progress.draftStored,
                });
                setNotice(outcome.message);
                setClosing(false);
                return;
            }
            setExitConfirm(outcome.message);
            setClosing(false);
        })();
    }, [closing]);

    const confirmExit = useCallback(() => {
        setExitConfirm("");
        onCloseRef.current();
    }, []);

    const cancelExit = useCallback(() => {
        setExitConfirm("");
    }, []);

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

    return {
        closing,
        notice,
        exitConfirm,
        closeHandler,
        clearNotice,
        confirmExit,
        cancelExit,
    };
}
