// Capture the manual policy from the pre-change source into a cross-language fixture.
// Run from web/. Before running, capture the pre-feature revision's policy:
// git show <revision>:web/src/lib/canvas/canvas-connection-policy.ts > src/lib/canvas/connection-baseline.ts
// Remove that temporary file after capturing the fixture.
import { canvasConnectionError } from "../src/lib/canvas/connection-baseline";
import { normalizeConnection } from "../src/lib/canvas/canvas-project-domain";
import { defaultConfig } from "../src/stores/use-config-store";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";
const config = { ...defaultConfig, channels: [], models: [], imageModels: [], videoModels: [] };
const cases = Object.values(CanvasNodeType).flatMap((from) =>
    Object.values(CanvasNodeType).map((to) => {
        const nodes: CanvasNodeData[] = [
            { id: "from", type: from },
            { id: "to", type: to },
        ].map((node) => ({ ...node, title: node.id, position: { x: 0, y: 0 }, width: 200, height: 100 }));
        const candidate = normalizeConnection("from", "to", nodes, "source");
        return { from, to, allowed: Boolean(candidate && !canvasConnectionError(config, nodes, [], candidate)) };
    }),
);
await Bun.write("../backend/internal/canvas/connection/manual-baseline.json", JSON.stringify(cases, null, 2) + "\n");
console.log(`Captured ${cases.length} manual node combinations`);
