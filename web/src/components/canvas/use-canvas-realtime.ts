import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import { createCanvasRealtimeClient, type CanvasRealtimeState, type RealtimePresence } from "../../services/canvas-realtime-client";
import { realtimeCursorWorldPoint, realtimeDragPositions } from "../../lib/canvas/canvas-realtime-presence";
import { subscribeCanvasGraphicsViewportPreview, subscribeCanvasNodeDragPreview } from "../../lib/canvas/canvas-live-viewport";
import type { ViewportTransform } from "../../types/canvas";

const inactive: CanvasRealtimeState = { status: "offline", coordination: "recovering", sessionId: "", deviceNumber: 0, peers: [] };
type Options = {
    scope: string | null;
    canvasId: string;
    apiBaseURL: string | null;
    enabled: boolean;
    containerRef: RefObject<HTMLDivElement | null>;
    viewport: ViewportTransform;
    nodesRef: RefObject<readonly { id: string; position: { x: number; y: number } }[]>;
    reconcile: () => Promise<void>;
};
function deviceLabel() {
    const agent = navigator.userAgent;
    return /Edg\//.test(agent) ? "Edge" : /Firefox\//.test(agent) ? "Firefox" : /Chrome\//.test(agent) ? "Chrome" : /Safari\//.test(agent) ? "Safari" : "Browser";
}

export function useCanvasRealtime({ scope, canvasId, apiBaseURL, enabled, containerRef, viewport, nodesRef, reconcile }: Options) {
    const viewportRef = useRef(viewport);
    useLayoutEffect(() => {
        viewportRef.current = viewport;
    }, [viewport.x, viewport.y, viewport.k]);
    const [snapshot, setSnapshot] = useState<{ scope: string; canvasId: string; apiBaseURL: string; state: CanvasRealtimeState } | null>(null);
    useLayoutEffect(() => {
        if (!enabled || !scope || !canvasId || !apiBaseURL || typeof window === "undefined") return;
        let client;
        try {
            client = createCanvasRealtimeClient({ scope, canvasId, apiBaseURL, pageURL: window.location.href, deviceLabel: deviceLabel(), reconcile });
        } catch {
            return;
        }
        const unsubscribe = client.subscribe(() => {
            setSnapshot({ scope, canvasId, apiBaseURL, state: client.state() });
        });
        client.start();
        const wake = () => client.wake();
        const visible = () => {
            if (document.visibilityState === "visible") wake();
        };
        window.addEventListener("focus", wake);
        window.addEventListener("online", wake);
        document.addEventListener("visibilitychange", visible);
        const element = containerRef.current;
        let local: RealtimePresence = { cursor: null, drag: [] };
        const publish = (patch: Partial<RealtimePresence>) => {
            local = { ...local, ...patch };
            client.presence(local);
        };
        // pointermove fires every frame while dragging. Reading geometry there
        // flushes any pending style work synchronously, so the container rect is
        // measured once and only re-read after a resize or a real scroll.
        let bounds: DOMRect | null = null;
        const invalidateBounds = () => {
            bounds = null;
        };
        // The canvas dispatches synthetic scroll notifications while panning;
        // they never move the container itself.
        const invalidateBoundsOnScroll = (event: Event) => {
            if (event.isTrusted) bounds = null;
        };
        const resizeObserver = element && typeof ResizeObserver !== "undefined" ? new ResizeObserver(invalidateBounds) : undefined;
        const move = (event: PointerEvent) => {
            if (!element) return;
            const target = event.target instanceof Element ? event.target : null;
            const ignored = target?.closest("[data-canvas-no-zoom],[data-connection-create-menu],input,textarea,select,button,[contenteditable='true'],.ant-modal,.ant-popover,.ant-dropdown,.ant-select-dropdown,.ant-picker-dropdown");
            if (ignored) {
                publish({ cursor: null });
                return;
            }
            bounds ??= element.getBoundingClientRect();
            publish({ cursor: realtimeCursorWorldPoint({ x: event.clientX, y: event.clientY }, bounds, viewportRef.current) });
        };
        const leave = () => publish({ cursor: null });
        const clear = () => publish({ cursor: null, drag: [] });
        const hidden = () => {
            if (document.visibilityState !== "visible") clear();
        };
        const offViewport = element
            ? subscribeCanvasGraphicsViewportPreview(element, (next) => {
                  viewportRef.current = next;
              })
            : undefined;
        const offDrag = element
            ? subscribeCanvasNodeDragPreview(element, (preview) => {
                  publish({ drag: realtimeDragPositions(nodesRef.current, preview) });
              })
            : undefined;
        if (element) resizeObserver?.observe(element);
        window.addEventListener("resize", invalidateBounds);
        window.addEventListener("scroll", invalidateBoundsOnScroll, true);
        element?.addEventListener("pointermove", move, true);
        element?.addEventListener("pointerleave", leave);
        element?.addEventListener("pointercancel", clear);
        window.addEventListener("blur", clear);
        document.addEventListener("visibilitychange", hidden);
        return () => {
            offViewport?.();
            offDrag?.();
            resizeObserver?.disconnect();
            window.removeEventListener("resize", invalidateBounds);
            window.removeEventListener("scroll", invalidateBoundsOnScroll, true);
            element?.removeEventListener("pointermove", move, true);
            element?.removeEventListener("pointerleave", leave);
            element?.removeEventListener("pointercancel", clear);
            window.removeEventListener("blur", clear);
            document.removeEventListener("visibilitychange", hidden);
            window.removeEventListener("focus", wake);
            window.removeEventListener("online", wake);
            document.removeEventListener("visibilitychange", visible);
            unsubscribe();
            client.close();
        };
    }, [scope, canvasId, apiBaseURL, enabled, reconcile, containerRef, nodesRef]);
    return enabled && snapshot?.scope === scope && snapshot.canvasId === canvasId && snapshot.apiBaseURL === apiBaseURL ? snapshot.state : inactive;
}
