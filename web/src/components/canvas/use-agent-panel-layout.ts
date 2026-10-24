import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { AGENT_PANEL_MIN_WIDTH, AGENT_PANEL_WIDTH, agentPanelIsCompact, agentPanelWidth } from "@/lib/canvas/agent-panel-layout";

const viewport = () => ({ width: window.innerWidth, height: window.innerHeight });

const widthKey = "canvas-agent-panel-width";
const storedWidth = () => {
    try {
        const value = Number(window.localStorage.getItem(widthKey));
        return value > 0 && Number.isFinite(value) ? value : AGENT_PANEL_WIDTH;
    } catch {
        return AGENT_PANEL_WIDTH;
    }
};

/** 右侧面板可调宽，窄屏自动全宽；视口收窄不覆盖用户原来的宽度偏好。 */
export function useAgentPanelLayout() {
    const [viewportSize, setViewportSize] = useState(viewport);
    const [preferredWidth, setPreferredWidth] = useState(storedWidth);
    const [resizing, setResizing] = useState(false);
    const drag = useRef<{ pointerId: number; startX: number; width: number } | null>(null);

    useEffect(() => {
        const resize = () => setViewportSize(viewport());
        window.addEventListener("resize", resize);
        return () => window.removeEventListener("resize", resize);
    }, []);

    const compact = agentPanelIsCompact(viewportSize);
    useEffect(() => {
        if (!compact) return;
        drag.current = null;
        setResizing(false);
    }, [compact]);
    const width = agentPanelWidth(viewportSize, preferredWidth);
    const maxWidth = agentPanelWidth(viewportSize, Number.MAX_SAFE_INTEGER);
    const updateWidth = (value: number) => setPreferredWidth(agentPanelWidth(viewportSize, value));
    useEffect(() => {
        if (resizing) return;
        try {
            window.localStorage.setItem(widthKey, String(preferredWidth));
        } catch {
            /* 调宽在禁用存储时仍可使用。 */
        }
    }, [preferredWidth, resizing]);
    const finishResize = (event: PointerEvent<HTMLDivElement>) => {
        if (drag.current?.pointerId !== event.pointerId) return;
        drag.current = null;
        setResizing(false);
        if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    };

    return {
        compact,
        width,
        resizing,
        minWidth: AGENT_PANEL_MIN_WIDTH,
        maxWidth,
        resizeHandlers: {
            onPointerDown: (event: PointerEvent<HTMLDivElement>) => {
                if (compact || event.button !== 0) return;
                event.preventDefault();
                event.stopPropagation();
                drag.current = { pointerId: event.pointerId, startX: event.clientX, width };
                event.currentTarget.setPointerCapture(event.pointerId);
                setResizing(true);
            },
            onPointerMove: (event: PointerEvent<HTMLDivElement>) => {
                const start = drag.current;
                if (!start || start.pointerId !== event.pointerId || compact) return;
                updateWidth(start.width + start.startX - event.clientX);
            },
            onPointerUp: finishResize,
            onPointerCancel: finishResize,
            onLostPointerCapture: finishResize,
            onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => {
                if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
                event.preventDefault();
                event.stopPropagation();
                setResizing(true);
                updateWidth(event.key === "Home" ? AGENT_PANEL_MIN_WIDTH : event.key === "End" ? maxWidth : width + (event.key === "ArrowLeft" ? 20 : -20));
            },
            onKeyUp: () => setResizing(false),
            onBlur: () => {
                if (!drag.current) setResizing(false);
            },
            onDoubleClick: () => setPreferredWidth(AGENT_PANEL_WIDTH),
        },
        topInset: "var(--canvas-topbar-offset)",
        bottomInset: "var(--canvas-inset-y)",
        sideInset: "var(--canvas-inset-x)",
    };
}
