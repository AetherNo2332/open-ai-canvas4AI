import type { AgentPanelConfig } from "../panel-config";

export const platformAgentPanelConfig: AgentPanelConfig = {
    id: "platform",
    businessType: "platform",
    agentDefinitionId: "platform.default",
    title: "平台对话",
    placeholder: "说明你想完成的工作…",
    welcome: "这里提供当前账号已授权的平台对话能力。",
    modelSource: "user-text",
    controls: { model: true, reasoning: true, skills: true, contextUsage: true, sendMode: true },
    extensions: [],
    docking: "floating",
};
