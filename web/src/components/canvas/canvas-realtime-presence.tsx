import { useLayoutEffect, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { MousePointer2, Monitor } from "lucide-react";
import type { CanvasRealtimeState, RealtimePeer } from "../../services/canvas-realtime-client";
import type { ViewportTransform } from "../../types/canvas";
import { subscribeCanvasGraphicsViewportPreview } from "../../lib/canvas/canvas-live-viewport";

export function CanvasRealtimePresence({ peers, viewport }: { peers: readonly RealtimePeer[]; viewport: ViewportTransform }) {
    const position = (x: number, y: number) => ({ transform: `translate3d(${x * viewport.k + viewport.x}px, ${y * viewport.k + viewport.y}px, 0)` });
    return (
        <div aria-hidden="true" data-canvas-realtime-presence className="pointer-events-none absolute inset-0 overflow-hidden" style={{ zIndex: "var(--z-node-active)", color: "var(--site-text)" }}>
            {peers.map((peer) => (
                <div key={peer.sessionId}>
                    {peer.cursor ? (
                        <div className="absolute left-0 top-0 flex items-start gap-1" style={position(peer.cursor.x, peer.cursor.y)}>
                            <MousePointer2 className="size-4 shrink-0" />
                            <span className="border px-1.5 py-0.5 text-[10px] leading-4 tabular-nums" style={{ background: "var(--site-surface)", borderColor: "var(--site-border)", borderRadius: "var(--overlay-radius)" }}>
                                设备 {peer.deviceNumber}
                                {peer.deviceLabel ? ` · ${peer.deviceLabel}` : ""}
                            </span>
                        </div>
                    ) : null}
                    {peer.drag.map((item) => (
                        <div
                            key={item.nodeId}
                            data-presence-drag={item.nodeId}
                            className="absolute left-0 top-0 border border-dashed px-1.5 py-0.5 text-[10px] leading-4 tabular-nums"
                            style={{ ...position(item.x, item.y), borderColor: "var(--site-border)", background: "var(--site-surface)", borderRadius: "var(--overlay-radius)" }}
                        >
                            设备 {peer.deviceNumber} 正在移动
                        </div>
                    ))}
                </div>
            ))}
        </div>
    );
}
export function CanvasRealtimePresenceLayer({ peers, viewport, containerRef }: { peers: readonly RealtimePeer[]; viewport: ViewportTransform; containerRef: RefObject<HTMLDivElement | null> }) {
    const [container, setContainer] = useState<HTMLDivElement | null>(null);
    const [liveViewport, setLiveViewport] = useState(viewport);
    useLayoutEffect(() => {
        setLiveViewport(viewport);
    }, [viewport.x, viewport.y, viewport.k]);
    useLayoutEffect(() => {
        const element = containerRef.current;
        setContainer(element);
        if (element) return subscribeCanvasGraphicsViewportPreview(element, setLiveViewport);
    }, [containerRef]);
    return container ? createPortal(<CanvasRealtimePresence peers={peers} viewport={liveViewport} />, container) : null;
}
export function CanvasRealtimeStatus({ state }: { state: CanvasRealtimeState }) {
    const ready = state.status === "connected" && (state.coordination === "shared" || state.coordination === "local") && state.deviceNumber > 0;
    const label =
        state.status === "closed" || state.status === "offline"
            ? "实时连接离线"
            : state.status === "connecting"
              ? "实时连接中…"
              : state.coordination === "degraded"
                ? "协同降级"
                : !ready
                  ? "协同恢复中…"
                  : `${state.peers.length + 1} 台在线${state.coordination === "local" ? " · 本实例" : ""}`;
    return (
        <span
            role="status"
            aria-label={`画布实时状态：${label}`}
            title={ready ? `当前设备 ${state.deviceNumber}${state.coordination === "local" ? "，仅当前服务器实例可见" : ""}` : "画布继续通过轮询同步"}
            className="inline-flex min-w-0 items-center gap-1 px-1 text-xs text-muted-foreground tabular-nums"
        >
            <Monitor aria-hidden="true" className="size-3.5 shrink-0" />
            <span className="truncate">{label}</span>
        </span>
    );
}
