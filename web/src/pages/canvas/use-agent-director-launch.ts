import { useCallback, useEffect, useRef, useState } from "react";
import { App } from "antd";

import {
    directorOpenTargetReady,
    directorPreviewHandledStore,
    directorPreviewJobPending,
    directorPreviewOutcomeNotice,
    directorPreviewVideoResourceId,
    handleDirectorOpen,
    runDirectorPreview,
    subscribeDirectorOpen,
    withDirectorPreviewLock,
    type DirectorOpenRunEvent,
    type DirectorPreviewJob,
} from "@/lib/canvas/agent-director-launch";
import { getAgentRun, submitDirectorPreviewResult } from "@/services/api/agent";
import type { CanvasNodeData } from "@/types/canvas";
import type { PrevisScene } from "@/types/previs";

type Options = {
    projectId: string;
    previsNodeId: string | null;
    nodes: CanvasNodeData[];
    previsScenes: PrevisScene[] | undefined;
    openPrevisWorkbench: (nodeId: string) => void;
};

/**
 * Agent 调用 director 工具时自动拉起导演台（审批模式由后端决定事件何时发出），
 * 并在 action=preview 时把浏览器录制结果回传给挂起的工具调用。
 */
export function useAgentDirectorLaunch({ projectId, previsNodeId, nodes, previsScenes, openPrevisWorkbench }: Options) {
    const { message } = App.useApp();
    const [pendingOpen, setPendingOpen] = useState<DirectorOpenRunEvent | null>(null);
    const [directorPreview, setDirectorPreview] = useState<DirectorPreviewJob | null>(null);
    const previsNodeIdRef = useRef(previsNodeId);
    const handledStoreRef = useRef(directorPreviewHandledStore());
    previsNodeIdRef.current = previsNodeId;

    const onDirectorOpen = useCallback(
        (event: DirectorOpenRunEvent) => {
            if (event.canvasId !== projectId || event.action === "read") return;
            if (event.action === "preview" && event.requestId) setDirectorPreview({ runId: event.runId, requestId: event.requestId, sceneId: event.sceneId, shotId: event.shotId, openSeq: event.seq });
            setPendingOpen(event);
        },
        [projectId],
    );

    // 全局面板经 window 事件转交 director_open；只订阅当前画布的事件。
    useEffect(() => subscribeDirectorOpen(projectId, onDirectorOpen), [onDirectorOpen, projectId]);

    // 写入后的实时合并可能晚于事件到达：节点与场景都同步到本地后再打开。
    useEffect(() => {
        if (!pendingOpen || !directorOpenTargetReady(pendingOpen, nodes, previsScenes || [])) return;
        setPendingOpen(null);
        handleDirectorOpen(pendingOpen, { currentCanvasId: projectId, openNodeId: previsNodeIdRef.current, openPrevisWorkbench });
    }, [nodes, openPrevisWorkbench, pendingOpen, previsScenes, projectId]);

    // 导演台关闭后未开始的预演不再执行，由后端超时回执给 Agent。
    useEffect(() => {
        if (!previsNodeId) setDirectorPreview((current) => (current && !pendingOpen ? null : current));
    }, [pendingOpen, previsNodeId]);

    useEffect(() => {
        setPendingOpen(null);
        setDirectorPreview(null);
    }, [projectId]);

    const onDirectorPreview = useCallback(
        (job: DirectorPreviewJob, record: () => Promise<string | undefined>) => {
            setDirectorPreview((current) => (current?.requestId === job.requestId ? null : current));
            void runDirectorPreview({
                runId: job.runId,
                requestId: job.requestId,
                record: async () => directorPreviewVideoResourceId(await record()),
                submit: submitDirectorPreviewResult,
                // 后台标签页录制会因不可见中止；只让可见标签页录制，且同一请求跨标签页只执行一次。
                isHidden: () => typeof document !== "undefined" && document.visibilityState === "hidden",
                isPending: () => directorPreviewJobPending(job, (runId, options) => getAgentRun(runId, AbortSignal.timeout(10_000), options)),
                handled: handledStoreRef.current,
                withLock: withDirectorPreviewLock,
            })
                .then((outcome) => {
                    const notice = directorPreviewOutcomeNotice(outcome);
                    if (notice) message[notice.level](notice.text);
                })
                .catch((cause) => message.error(cause instanceof Error ? `预演结果回传失败：${cause.message}` : "预演结果回传失败"));
        },
        [message],
    );

    return { directorPreview, onDirectorPreview };
}
