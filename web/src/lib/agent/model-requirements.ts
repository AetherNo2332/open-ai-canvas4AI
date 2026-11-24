import type { AgentModelRequirements } from "@/sdk/agent";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { logicalModelIDForConfig, type AiConfig } from "@/stores/use-config-store";

/**
 * Capabilities are the source of truth for which models can run this business.
 * Unknown local metadata is treated as compatible so a missing client-side
 * profile never hides an otherwise server-authorized model.
 */
export function agentModelSupportsRequirements(config: AiConfig, model: string, requirements?: AgentModelRequirements) {
    if (!requirements) return true;
    let text: ReturnType<typeof modelCapabilityConfigFor>["text"];
    try {
        text = modelCapabilityConfigFor(config, model).text;
    } catch {
        return true;
    }
    if (!text) return !requirements.text;
    if (requirements.toolCalling) {
        if (text.toolCallingSupported === false) return false;
        const logicalId = logicalModelIDForConfig({ ...config, model });
        if (logicalId && config.channels.some((channel) => channel.modelCosts?.some((route) => route.logicalModelId === logicalId && route.available !== false && route.capabilityConfig?.text?.toolCallingSupported === false))) return false;
    }
    if (requirements.vision && text.visionSupported === false) return false;
    if (requirements.streamOutput && text.streaming === false) return false;
    return true;
}

export function agentModelFilter(config: AiConfig, requirements?: AgentModelRequirements) {
    if (!requirements) return undefined;
    return (model: string) => agentModelSupportsRequirements(config, model, requirements);
}
