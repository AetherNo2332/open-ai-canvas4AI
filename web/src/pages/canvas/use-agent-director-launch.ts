import { useCallback, useEffect, useRef, useState } from "react";
import { App } from "antd";

import { directorOpenTargetReady, directorPreviewVideoResourceId, handleDirectorOpen, runDirectorPreview, type DirectorOpenRunEvent, type DirectorPreviewJob } from "@/lib/canvas/agent-director-launch";
import { submitDirectorPreviewResult } from "@/services/api/agent";
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
    previsNodeIdRef.current = previsNodeId;

    const onDirectorOpen = useCallback(
        (event: DirectorOpenRunEvent) => {
            if (event.canvasId !== projectId) return;
            if (event.action === "preview" && event.requestId) setDirectorPreview({ runId: event.runId, requestId: event.requestId, sceneId: event.sceneId, shotId: event.shotId });
            setPendingOpen(event);
        },
        [projectId],
    );

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
            })
                .then((outcome) => {
                    if (outcome === "submitted") message.success("预演视频已回传给 Agent");
                    else if (outcome === "expired") message.warning("Agent 的预演请求已超时或已结束，视频已保留在画布");
                })
                .catch((cause) => message.error(cause instanceof Error ? `预演结果回传失败：${cause.message}` : "预演结果回传失败"));
        },
        [message],
    );

    return { onDirectorOpen, directorPreview, onDirectorPreview };
}
