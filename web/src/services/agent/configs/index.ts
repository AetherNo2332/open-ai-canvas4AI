import { registerAgentPanelConfig } from "../panel-config";
import { canvasAgentPanelConfig } from "./canvas";
import { dramaAgentPanelConfig } from "./drama";
import { platformAgentPanelConfig } from "./platform";

registerAgentPanelConfig(canvasAgentPanelConfig);
registerAgentPanelConfig(dramaAgentPanelConfig);
registerAgentPanelConfig(platformAgentPanelConfig);

export { canvasAgentPanelConfig, dramaAgentPanelConfig, platformAgentPanelConfig };
