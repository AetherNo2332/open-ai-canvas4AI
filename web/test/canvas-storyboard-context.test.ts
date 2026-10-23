import { describe, expect, it } from "bun:test";

import { inspectStoryboardReadiness, resolveStoryboardGenerationContext } from "@/lib/canvas/canvas-storyboard-context";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

const node = (id: string, type: CanvasNodeType, metadata: CanvasNodeData["metadata"] = {}): CanvasNodeData => ({
    id,
    type,
    title: id,
    position: { x: 0, y: 0 },
    width: 320,
    height: 180,
    metadata,
});

const styleNode = node("style", CanvasNodeType.Text, {
    workflowKind: "styleboard",
    stylePresetId: "style-v1",
    content: "统一的项目画风",
});

describe("storyboard generation context", () => {
    it("allows planning without a style node or preset and requires an Agent confirmation", () => {
        const context = resolveStoryboardGenerationContext([]);
        expect(context.projectStyle).toBeUndefined();
        const readiness = inspectStoryboardReadiness([]);
        expect(readiness.canGenerateStoryboard).toBe(true);
        expect(readiness.blockingReason).toBeUndefined();
        expect(readiness.nextActions.join(" ")).toContain("确认");
        expect(readiness.nextActions.join(" ")).not.toContain("先 canvas_list_styles");
    });

    it("accepts a freeform project style without an effective preset", () => {
        const context = resolveStoryboardGenerationContext([node("free-style", CanvasNodeType.Text, { workflowKind: "styleboard", content: "低饱和手绘，缓慢叙事" })]);
        expect(context.projectStyle?.prompt).toBe("低饱和手绘，缓慢叙事");
        expect(context.projectStyle?.presetId).toBeUndefined();
        expect(inspectStoryboardReadiness([node("free-style", CanvasNodeType.Text, { workflowKind: "styleboard", content: "低饱和手绘" })]).canGenerateStoryboard).toBe(true);
    });
    it("ignores standalone character-design workflows without a linked asset", () => {
        const context = resolveStoryboardGenerationContext([
            styleNode,
            node("character-turnaround", CanvasNodeType.Image, {
                workflowKind: "character",
                composerContent: "生成角色三视图",
            }),
        ]);

        expect(context.characters).toEqual([]);
    });

    it("keeps the version check for linked character cards", () => {
        expect(() => resolveStoryboardGenerationContext([
            styleNode,
            node("character-card", CanvasNodeType.Text, {
                workflowKind: "character",
                characterAssetId: "asset-1",
                characterName: "李当歌",
            }),
        ])).toThrow("角色卡“李当歌”版本未同步");
    });

    it("passes linked character cards with a current version", () => {
        const context = resolveStoryboardGenerationContext([
            styleNode,
            node("character-card", CanvasNodeType.Text, {
                workflowKind: "character",
                characterAssetId: "asset-1",
                characterVersionId: "version-1",
                characterName: "李当歌",
                characterDefinition: { role: "镇国公世子" },
            }),
        ]);

        expect(context.characters).toEqual([{
            assetId: "asset-1",
            versionId: "version-1",
            name: "李当歌",
            definition: { role: "镇国公世子" },
        }]);
    });
});
