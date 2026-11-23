import type { AgentPageContext } from "./business-context";

export function platformAgentPageContext(): Pick<AgentPageContext, "declaration" | "status" | "scope" | "agentDefinitionId" | "displayName"> {
    return {
        declaration: { enabled: true, kind: "platform" },
        status: "ready",
        scope: { type: "platform", id: "self" },
        agentDefinitionId: "platform.default",
        displayName: "平台对话",
    };
}
