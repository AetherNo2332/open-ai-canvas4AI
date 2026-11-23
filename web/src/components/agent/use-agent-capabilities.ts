import { useEffect, useState } from "react";
import type { AgentBusinessScope, AgentCapabilities } from "@/sdk/agent";
import type { createAgentClient } from "@/sdk/agent/client";

type Client = ReturnType<typeof createAgentClient>;

export function useAgentCapabilities({
    active,
    client,
    scope,
    agentDefinitionId,
}: {
    active: boolean;
    client: Client;
    scope: AgentBusinessScope | null;
    agentDefinitionId: string;
}) {
    const [state, setState] = useState<{
        status: "idle" | "loading" | "ready" | "error";
        capabilities: AgentCapabilities | null;
        error: string;
    }>({ status: "idle", capabilities: null, error: "" });

    useEffect(() => {
        if (!active || !scope) {
            setState({ status: "idle", capabilities: null, error: "" });
            return;
        }
        const controller = new AbortController();
        setState({ status: "loading", capabilities: null, error: "" });
        void client
            .getCapabilities({ scope, agentDefinitionId }, controller.signal)
            .then((capabilities) => {
                if (!controller.signal.aborted) setState({ status: "ready", capabilities, error: "" });
            })
            .catch((cause) => {
                if (!controller.signal.aborted) {
                    setState({ status: "error", capabilities: null, error: cause instanceof Error ? cause.message : "Agent 能力读取失败" });
                }
            });
        return () => controller.abort();
    }, [active, agentDefinitionId, client, scope?.type, scope?.id]);

    return state;
}
