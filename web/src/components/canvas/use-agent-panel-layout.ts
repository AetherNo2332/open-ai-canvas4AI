import { useEffect, useState } from "react";
import { agentPanelIsCompact, agentPanelWidth } from "@/lib/canvas/agent-panel-layout";

const viewport = () => ({ width: window.innerWidth, height: window.innerHeight });

/**
 * Agent 面板已固定为画布右侧的平行窗口：不再支持拖拽移动、缩放或偏好恢复，
 * 宽度只随视口变化，画布与面板左右并行。
 */
export function useAgentPanelLayout() {
    const [viewportSize, setViewportSize] = useState(viewport);

    useEffect(() => {
        const resize = () => setViewportSize(viewport());
        window.addEventListener("resize", resize);
        return () => window.removeEventListener("resize", resize);
    }, []);

    const compact = agentPanelIsCompact(viewportSize);

    return {
        compact,
        width: agentPanelWidth(viewportSize),
        topInset: "var(--canvas-topbar-offset)",
        bottomInset: "var(--canvas-inset-y)",
        sideInset: "var(--canvas-inset-x)",
    };
}
