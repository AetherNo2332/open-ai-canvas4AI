import { describe, expect, test } from "bun:test";

import { buildStoryboardAssetCatalog, storyboardAssetRoleForNode } from "@/lib/canvas/canvas-storyboard-assets";
import { CanvasNodeType } from "@/types/canvas";

describe("storyboard character card assets", () => {
    test("includes a character card even when its version has not been synchronized", () => {
        const node = {
            id: "character-card-1",
            type: CanvasNodeType.Text,
            title: "林默",
            position: { x: 0, y: 0 },
            width: 320,
            height: 260,
            metadata: { workflowKind: "character", characterAssetId: "character-asset-1" },
        };

        expect(buildStoryboardAssetCatalog([node])).toEqual([expect.objectContaining({ id: node.id, type: "character" })]);
        expect(storyboardAssetRoleForNode(node)).toBe("character");
    });
});
