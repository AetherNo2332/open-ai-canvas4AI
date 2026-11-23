import type { AgentPanelConfig } from "../panel-config";

export const canvasAgentPanelConfig: AgentPanelConfig = {
    id: "canvas",
    businessType: "canvas",
    agentDefinitionId: "canvas.default",
    title: "画布助手",
    placeholder: "输入消息，用 @ 引用节点，用 / 引用技能",
    welcome: "从一个想法开始，和助手一起完成画布创作。",
    modelSource: "canvas",
    controls: { model: true, reasoning: true, skills: true, contextUsage: true, sendMode: true },
    extensions: ["canvas-context", "canvas-approval", "canvas-preview"],
    docking: "floating-and-docked",
};
