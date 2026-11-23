import { useSyncExternalStore } from "react";
import { canvasThemes, type CanvasColorTheme } from "@/lib/agent/theme";

const listeners = new Set<() => void>();
let observer: MutationObserver | null = null;

function subscribe(listener: () => void) {
    listeners.add(listener);
    if (!observer && typeof MutationObserver !== "undefined" && typeof document !== "undefined") {
        observer = new MutationObserver(() => {
            for (const item of listeners) item();
        });
        observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    }
    return () => {
        listeners.delete(listener);
        if (!listeners.size) {
            observer?.disconnect();
            observer = null;
        }
    };
}

function snapshot(): CanvasColorTheme {
    return typeof document !== "undefined" && document.documentElement.classList.contains("dark") ? "dark" : "light";
}

export function useAgentTheme() {
    const mode = useSyncExternalStore(subscribe, snapshot, () => "dark" as const);
    return canvasThemes[mode];
}
