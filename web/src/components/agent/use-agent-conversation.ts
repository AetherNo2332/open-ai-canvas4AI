import { useLayoutEffect, useMemo, useState, useSyncExternalStore } from "react";
import type { createAgentClient } from "@/sdk/agent/client";
import type { AgentBusinessScope } from "@/sdk/agent";
import { AgentConversationController } from "@/services/agent/conversation-controller";
import { emptyAgentContextUsage } from "@/lib/agent/context-usage";

type ScopedClient = ReturnType<typeof createAgentClient>;

export function useAgentConversation(options: {
    userScope: string;
    scope: AgentBusinessScope | null;
    agentDefinitionId: string;
    client: ScopedClient | null;
    prepare?: () => Promise<void>;
    initial?: {
        model?: string;
        reasoningMode?: "off" | "minimal" | "low" | "medium" | "high" | "max" | "auto" | "deep";
        skillIds?: string[];
        sendMode?: "queue" | "interject";
    };
}) {
    const identity = options.userScope && options.scope && options.client ? `${options.userScope}:${options.scope.type}:${options.scope.id}:${options.agentDefinitionId}` : "";
    const createController = useMemo(() => {
        if (!identity || !options.scope || !options.client) return null;
        const { scope, client } = options;
        return () =>
            new AgentConversationController({
                identity: { userScope: options.userScope, scope, agentDefinitionId: options.agentDefinitionId },
                client,
                prepare: options.prepare,
                initial: options.initial,
            });
    }, [
        identity,
        options.agentDefinitionId,
        options.userScope,
        options.scope?.type,
        options.scope?.id,
        options.client,
        options.prepare,
        options.initial?.model,
        options.initial?.reasoningMode,
        options.initial?.sendMode,
        options.initial?.skillIds?.join("\u0000"),
    ]);
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
            contextUsage: emptyAgentContextUsage(""),
            model: "",
            reasoningMode: "off" as const,
            skillIds: [],
            sendMode: "queue" as const,
        }),
        [],
    );
    const [mounted, setMounted] = useState<{ factory: typeof createController; controller: AgentConversationController }>();
    const controller = mounted?.factory === createController ? mounted?.controller || null : null;
    const state = useSyncExternalStore(controller?.subscribe || (() => () => undefined), controller?.getSnapshot || (() => fallback), () => fallback);
    useLayoutEffect(() => {
        if (!createController) return;
        const controller = createController();
        setMounted({ factory: createController, controller });
        void controller.hydrate();
        const timer = window.setInterval(() => void controller.refreshDirectory(), 4_000);
        return () => {
            window.clearInterval(timer);
            controller.dispose();
        };
    }, [createController]);
    return { controller, state };
}
