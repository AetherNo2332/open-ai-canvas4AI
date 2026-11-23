export type AgentPanelControl = "model" | "reasoning" | "skills" | "contextUsage" | "sendMode";

export type AgentPanelModelSource = "user-text" | "project-text" | "canvas";

export type AgentPanelConfig = {
    id: string;
    businessType: "canvas" | "drama" | "platform";
    agentDefinitionId: string;
    title: string;
    placeholder: string;
    welcome: string;
    modelSource: AgentPanelModelSource;
    controls: Record<AgentPanelControl, boolean>;
    extensions: string[];
    docking: "floating" | "floating-and-docked";
};

const configs = new Map<string, AgentPanelConfig>();

export function registerAgentPanelConfig(config: AgentPanelConfig) {
    configs.set(config.id, config);
}

export function resolveAgentPanelConfig(id: string) {
    return configs.get(id) || null;
}

export function agentPanelControlEnabled(config: AgentPanelConfig, control: AgentPanelControl) {
    return config.controls[control] !== false;
}
