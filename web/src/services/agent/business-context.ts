import { useEffect, useRef, useSyncExternalStore } from "react";
import { useLocation } from "react-router";
import type { AgentBusinessScope } from "@/sdk/agent";

export type AgentPageDeclaration = { enabled: false } | { enabled: true; kind: "platform" } | { enabled: true; kind: "business"; businessType: string; agentDefinitionId: string };

export type AgentPageTarget = {
    dramaEpisodeId?: string;
    displayLabel?: string;
    openedRunId?: string;
    openedConversationId?: string;
    episodeOptions?: Array<{ id: string; label: string }>;
    model?: {
        logicalModelId?: string;
        channelId?: string;
        modelKey?: string;
        label?: string;
    };
};

export type AgentPageContext = {
    owner: string;
    routeKey: string;
    declaration: AgentPageDeclaration;
    status: "loading" | "ready" | "error";
    scope?: AgentBusinessScope;
    agentDefinitionId?: string;
    displayName: string;
    target?: AgentPageTarget;
    error?: string;
};

const disabled: AgentPageContext = {
    owner: "",
    routeKey: "",
    declaration: { enabled: false },
    status: "error",
    displayName: "",
};
const listeners = new Set<() => void>();
let current: AgentPageContext = disabled;

function emit() {
    for (const listener of listeners) listener();
}

export function publishAgentPageContext(context: AgentPageContext) {
    current = context;
    emit();
}

export function clearAgentPageContext(owner: string) {
    if (current.owner !== owner) return;
    current = disabled;
    emit();
}

export function getAgentPageContext() {
    return current;
}

/** Entry points such as shortcuts and the command palette use the same declaration as the floating host. */
export function agentPageEntryAvailable() {
    return current.declaration.enabled;
}

export function subscribeAgentPageContext(listener: () => void) {
    listeners.add(listener);
    return () => listeners.delete(listener);
}

export function useAgentPageContext() {
    return useSyncExternalStore(subscribeAgentPageContext, getAgentPageContext, () => disabled);
}

let pageOwnerSequence = 0;

/**
 * A page publishes one route-scoped declaration. Cleanup is owner-checked so
 * a late unmount from the previous route cannot hide the new page's entry.
 */
export function useAgentPageDeclaration(input: Omit<AgentPageContext, "owner" | "routeKey"> & { routeKey?: string }) {
    const location = useLocation();
    const owner = useRef(`agent-page-${++pageOwnerSequence}`).current;
    const routeKey = input.routeKey || location.key || location.pathname;
    const signature = JSON.stringify({ input, routeKey, pathname: location.pathname });
    useEffect(() => {
        if (!input.declaration.enabled) {
            clearAgentPageContext(owner);
            return () => clearAgentPageContext(owner);
        }
        publishAgentPageContext({ ...input, owner, routeKey });
        return () => clearAgentPageContext(owner);
    }, [signature, owner, routeKey]);
}
