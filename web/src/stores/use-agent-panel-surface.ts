import { useSyncExternalStore } from "react";

let open = false;
const listeners = new Set<() => void>();

function emit() {
    for (const listener of listeners) listener();
}

export function openAgentPanelSurface() {
    if (open) return;
    open = true;
    emit();
}

export function isAgentPanelSurfaceOpen() {
    return open;
}

export function closeAgentPanelSurface() {
    if (!open) return;
    open = false;
    emit();
}

export function subscribeAgentPanelSurface(listener: () => void) {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

export function useAgentPanelSurfaceOpen() {
    return useSyncExternalStore(
        subscribeAgentPanelSurface,
        () => open,
        () => false,
    );
}
