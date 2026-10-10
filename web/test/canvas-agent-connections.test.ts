import { expect, test } from "bun:test";
import baseline from "../../backend/internal/canvas/connection/manual-baseline.json";
import { canvasConnectionError } from "../src/lib/canvas/canvas-connection-policy";
import { defaultConfig } from "../src/stores/use-config-store";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";
const config = { ...defaultConfig, channels: [], models: [], imageModels: [], videoModels: [] };
const node = (id: string, type: string, metadata = {}): CanvasNodeData => ({ id, type: type as CanvasNodeType, title: id, position: { x: 0, y: 0 }, width: 200, height: 100, metadata });
test("all built-in connections retain the pre-change manual policy", () => {
    expect(baseline.length).toBe(Object.values(CanvasNodeType).length ** 2);
    for (const item of baseline) expect(!canvasConnectionError(config, [node("from", item.from), node("to", item.to)], [], { fromNodeId: "from", toNodeId: "to" })).toBe(item.allowed);
});
test("batch and storyboard handles validate real columns and rows", () => {
    const nodes = [node("image", "image"), node("table", "batch-table"), node("script", "script", { storyboard: { rows: [{ id: "r1" }] } })];
    expect(canvasConnectionError(config, nodes, [], { fromNodeId: "image", toNodeId: "table", toHandleId: "batch-reference:reference-1" })).toBe("");
    expect(canvasConnectionError(config, nodes, [], { fromNodeId: "image", toNodeId: "table", toHandleId: "batch-reference:missing" })).not.toBe("");
    expect(canvasConnectionError(config, nodes, [], { fromNodeId: "image", toNodeId: "script", toHandleId: "row:r1" })).toBe("");
    expect(canvasConnectionError(config, nodes, [], { fromNodeId: "image", toNodeId: "script", toHandleId: "row:missing" })).not.toBe("");
});
