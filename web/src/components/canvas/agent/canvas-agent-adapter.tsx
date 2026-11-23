import { CanvasCloudAgentPanel } from "@/components/canvas/canvas-cloud-agent-panel";

/** Canvas-specific composition root. Shared shell code only resolves it through registration. */
export function CanvasAgentAdapter() {
    return <CanvasCloudAgentPanel />;
}
