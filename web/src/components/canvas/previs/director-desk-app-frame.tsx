import { useEffect, useRef, useState } from "react";
import { X } from "lucide-react";
import type { BridgeEndpoint } from "@desk/host/bridge";
import { createDirectorDeskHostBridge, type DirectorDeskSaveResult } from "@/integrations/director-desk/app-bridge-client";
import { usePrevisCloseHandler } from "@/hooks/use-previs-close-handler";
import { describePrevisSaveStatus } from "@/lib/canvas/previs/previs-save-wiring";
import type { PrevisScene } from "@/types/previs";
import type { PrevisSaveController } from "./use-previs-save-coordinator";
import "./director-desk-app-frame.css";

/** 原版导演台应用的静态挂载点(由 nginx 同源托管,见仓库根 nginx.conf)。 */
export const DIRECTOR_DESK_APP_URL = "/desk/";

/**
 * iframe 沙箱:原版应用是独立文档(CSS/DOM 与工作台真隔离),只放行它需要的脚本、
 * 同源访问(Cookie 与 postMessage)、表单、下载与弹窗;不放行顶层导航。
 */
export const DIRECTOR_DESK_APP_SANDBOX = "allow-scripts allow-same-origin allow-forms allow-downloads allow-modals";

/** 应用在此时限内没有发出 `desk.ready` 就判定为加载失败,给出重试而不是一直转圈。 */
const READY_TIMEOUT_MS = 20000;

export type DirectorDeskAppFrameProps = {
    /** 场景标识:变化时重挂载 bridge(新场景 = 新会话)。 */
    sceneId: string;
    /** 当前场景(用于关闭逻辑)。 */
    scene: PrevisScene;
    /** 保存协调器(用于关闭/保存状态)。 */
    saveController: PrevisSaveController;
    /** 读取当前应下发的场景文档;未提供时应用自行从宿主拉取(本阶段为挂载骨架)。 */
    readDocument?: () => unknown;
    onSceneChange: (document: unknown) => void;
    onSaveRequest: () => Promise<DirectorDeskSaveResult>;
    onReady?: () => void;
    /** 关闭回调(触发回退到旧工作台)。 */
    onClose?: () => void;
    /** 应用加载失败时回退到旧工作台(注入同一 saveController)。 */
    onFallback?: () => void;
};

export function DirectorDeskAppFrame({
    sceneId,
    scene,
    saveController,
    readDocument,
    onSceneChange,
    onSaveRequest,
    onReady,
    onClose,
    onFallback,
}: DirectorDeskAppFrameProps) {
    const frameRef = useRef<HTMLIFrameElement>(null);
    const [status, setStatus] = useState<"loading" | "ready" | "error">("loading");
    const [attempt, setAttempt] = useState(0);

    // 关闭逻辑:复用 director-previs-editor 的处理流程。
    const { closing, notice, exitConfirm, closeHandler, clearNotice, confirmExit, cancelExit } = usePrevisCloseHandler({
        saveController,
        scene,
        onClose: () => {
            onClose?.();
        },
    });

    const indicator = describePrevisSaveStatus(saveController.progress);

    // 回调放 ref:bridge 生命周期只跟随 sceneId/attempt,不随每次渲染重建。
    const readDocumentRef = useRef(readDocument);
    readDocumentRef.current = readDocument;
    const onSceneChangeRef = useRef(onSceneChange);
    onSceneChangeRef.current = onSceneChange;
    const onSaveRequestRef = useRef(onSaveRequest);
    onSaveRequestRef.current = onSaveRequest;
    const onReadyRef = useRef(onReady);
    onReadyRef.current = onReady;

    useEffect(() => {
        setStatus("loading");
        const origin = typeof window === "undefined" ? "" : window.location.origin;
        const endpoint: BridgeEndpoint = {
            postMessage: (message, targetOrigin) => frameRef.current?.contentWindow?.postMessage(message, targetOrigin ?? origin),
            addEventListener: (type, listener) => window.addEventListener(type, listener as unknown as EventListener),
            removeEventListener: (type, listener) => window.removeEventListener(type, listener as unknown as EventListener),
        };
        const bridge = createDirectorDeskHostBridge({
            endpoint,
            origin,
            readDocument: () => readDocumentRef.current?.() ?? null,
            commitDocument: (document) => onSceneChangeRef.current(document),
            requestSave: () => onSaveRequestRef.current(),
            onReady: () => {
                setStatus("ready");
                onReadyRef.current?.();
            },
        });
        const timer = window.setTimeout(() => {
            // 已就绪则忽略;超时未就绪 = 应用没能起来(资源缺失/脚本错误)。
            setStatus((current) => (current === "loading" ? "error" : current));
        }, READY_TIMEOUT_MS);
        return () => {
            window.clearTimeout(timer);
            bridge.dispose();
        };
    }, [sceneId, attempt]);

    // 应用加载失败时回退到旧工作台。
    if (status === "error") {
        onFallback?.();
        return null;
    }

    return (
        <div className="director-desk-app-frame" data-desk-status={status} data-desk-scene={sceneId}>
            {/* 顶部标题栏:关闭、保存状态、标题 */}
            <header className="director-desk-app-frame-header">
                <h2 className="director-desk-app-frame-title">导演台</h2>
                <div className="director-desk-app-frame-save-status" data-tone={indicator.tone} role="status">
                    <span className="director-desk-app-frame-save-label">{indicator.label}</span>
                    {indicator.retryable ? (
                        <button
                            type="button"
                            className="director-desk-app-frame-save-retry"
                            onClick={() => {
                                void saveController.retry();
                            }}
                        >
                            重试
                        </button>
                    ) : null}
                </div>
                <button
                    type="button"
                    className="director-desk-app-frame-close"
                    aria-label="关闭预演台"
                    title="关闭"
                    disabled={closing}
                    onClick={closeHandler}
                >
                    <X className="size-4" aria-hidden />
                </button>
            </header>

            {/* 提示信息 */}
            {notice ? (
                <div className="director-desk-app-frame-notice" role="alert">
                    {notice}
                </div>
            ) : null}

            {/* iframe 容器 */}
            <div className="director-desk-app-frame-body">
                <iframe
                    ref={frameRef}
                    key={attempt}
                    className="director-desk-app-frame-view"
                    src={DIRECTOR_DESK_APP_URL}
                    title="导演台编辑器"
                    sandbox={DIRECTOR_DESK_APP_SANDBOX}
                    allow="fullscreen; clipboard-write"
                    onError={() => setStatus("error")}
                />
                {status === "loading" ? (
                    <div className="director-desk-app-frame-overlay" role="status" aria-live="polite">
                        正在打开导演台…
                    </div>
                ) : null}
            </div>

            {/* 二次确认对话框:未保存状态下的离开 */}
            {exitConfirm ? (
                <div className="director-desk-app-frame-confirm" role="alertdialog" aria-label="确认离开">
                    <p className="director-desk-app-frame-confirm-msg">{exitConfirm}</p>
                    <div className="director-desk-app-frame-confirm-actions">
                        <button type="button" className="director-desk-app-frame-confirm-ok" onClick={confirmExit}>
                            仍然离开
                        </button>
                        <button type="button" className="director-desk-app-frame-confirm-cancel" onClick={cancelExit}>
                            留在预演台
                        </button>
                    </div>
                </div>
            ) : null}
        </div>
    );
}
