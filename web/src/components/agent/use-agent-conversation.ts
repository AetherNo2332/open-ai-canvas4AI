import { useEffect, useMemo, useSyncExternalStore } from "react";
import type { createAgentClient } from "@/sdk/agent/client";
import type { AgentBusinessScope } from "@/sdk/agent";
import { AgentConversationController } from "@/services/agent/conversation-controller";

type ScopedClient = ReturnType<typeof createAgentClient>;

export function useAgentConversation(options: {
    userScope: string;
    scope: AgentBusinessScope | null;
    agentDefinitionId: string;
    client: ScopedClient | null;
    prepare?: () => Promise<void>;
}) {
    const identity = options.userScope && options.scope && options.client ? `${options.userScope}:${options.scope.type}:${options.scope.id}:${options.agentDefinitionId}` : "";
    const controller = useMemo(() => {
        if (!identity || !options.scope || !options.client) return null;
        return new AgentConversationController({
            identity: { userScope: options.userScope, scope: options.scope, agentDefinitionId: options.agentDefinitionId },
            client: options.client,
            prepare: options.prepare,
        });
    }, [identity, options.agentDefinitionId, options.userScope, options.scope?.type, options.scope?.id, options.client, options.prepare]);
    const fallback = useMemo(
        () => ({
            status: "loading" as const,
            error: "",
            busy: false,
            connection: "connecting" as const,
            conversations: [],
            activeConversationId: "",
            messages: [],
            run: null,
            approval: null,
            draft: "",
        }),
        [],
    );
    const state = useSyncExternalStore(controller?.subscribe || (() => () => undefined), controller?.getSnapshot || (() => fallback), () => fallback);
    useEffect(() => {
        if (!controller) return;
        void controller.hydrate();
        const timer = window.setInterval(() => void controller.refreshDirectory(), 4_000);
        return () => {
            window.clearInterval(timer);
            controller.dispose();
        };
    }, [controller]);
    return { controller, state };
}
