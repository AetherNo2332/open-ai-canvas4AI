import { dramaChoiceKey, dramaChoiceKeys } from "@/lib/drama/model-choice";
import type { DramaModelChoice } from "@/services/api/drama";
import { logicalModelIDForConfig, modelOptionName, normalizeModelOptionValue, resolveModelRequestConfig, type AiConfig } from "@/stores/use-config-store";

export function agentPanelDefaultModel(config: AiConfig, eligible: string[], target?: DramaModelChoice) {
    const key = dramaChoiceKey(target);
    const projectModel = key && eligible.find((model) => dramaChoiceKeys(config, model).includes(key));
    if (projectModel) return projectModel;
    for (const saved of [config.textModel, config.model]) {
        const normalized = normalizeModelOptionValue(saved, config.channels);
        if (eligible.includes(normalized)) return normalized;
    }
    return eligible[0] || "";
}

export function agentPanelModelRoute(config: AiConfig, model: string, target?: DramaModelChoice) {
    const key = dramaChoiceKey(target);
    if (!model) return { logicalModelId: "", channelId: "", channelModelKey: "" };
    if (key && dramaChoiceKeys(config, model).includes(key)) {
        return { logicalModelId: target?.logicalModelId || "", channelId: target?.channelId || "", channelModelKey: target?.modelKey || "" };
    }
    const resolved = resolveModelRequestConfig(config, model);
    return { logicalModelId: logicalModelIDForConfig({ ...config, model }), channelId: resolved.channelId || "", channelModelKey: modelOptionName(model) };
}
