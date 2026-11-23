import { useSyncExternalStore, type ComponentType } from "react";

type Adapter = {
    owner: string;
    component: ComponentType;
};

const adapters = new Map<string, Adapter>();
const listeners = new Set<() => void>();

function emit() {
    for (const listener of listeners) listener();
}

export function registerAgentPanelAdapter(businessType: string, owner: string, component: ComponentType) {
    adapters.set(businessType, { owner, component });
    emit();
    return () => {
        if (adapters.get(businessType)?.owner === owner) {
            adapters.delete(businessType);
            emit();
        }
    };
}

export function getAgentPanelAdapter(businessType: string) {
    return adapters.get(businessType)?.component || null;
}

function subscribe(listener: () => void) {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

export function useAgentPanelAdapter(businessType: string) {
    return useSyncExternalStore(
        subscribe,
        () => getAgentPanelAdapter(businessType),
        () => null,
    );
}
