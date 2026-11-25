import { describe, expect, test } from "bun:test";

import { SUBMAP_COLLAPSED_HEIGHT, SUBMAP_COLLAPSED_WIDTH, submapMinimapTransform } from "@/lib/canvas/canvas-submap-minimap";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

function node(id: string, x: number, y: number, width: number, height: number): CanvasNodeData {
    return { id, type: CanvasNodeType.Text, position: { x, y }, width, height, title: id, metadata: {} };
}

describe("submap 折叠小地图坐标变换", () => {
    test("固定小窗尺寸与画布小地图同量级", () => {
        expect(SUBMAP_COLLAPSED_WIDTH).toBe(240);
        expect(SUBMAP_COLLAPSED_HEIGHT).toBe(160);
    });

    test("等比缩放装下全部子节点并居中", () => {
        const transform = submapMinimapTransform([node("a", 0, 0, 100, 50), node("b", 1000, 600, 200, 200)], SUBMAP_COLLAPSED_WIDTH, SUBMAP_COLLAPSED_HEIGHT);
        const spanX = transform.worldBounds.w * transform.scale;
        const spanY = transform.worldBounds.h * transform.scale;
        expect(spanX).toBeLessThanOrEqual(SUBMAP_COLLAPSED_WIDTH + 0.001);
        expect(spanY).toBeLessThanOrEqual(SUBMAP_COLLAPSED_HEIGHT + 0.001);
        expect(Math.abs(spanX - SUBMAP_COLLAPSED_WIDTH) < 0.001 || Math.abs(spanY - SUBMAP_COLLAPSED_HEIGHT) < 0.001).toBe(true);
        expect(transform.offset.x).toBeGreaterThanOrEqual(0);
        expect(transform.offset.y).toBeGreaterThanOrEqual(0);
    });

    test("映射单调，空集合不除零", () => {
        const transform = submapMinimapTransform([node("a", 0, 0, 100, 50), node("b", 800, 400, 100, 50)], SUBMAP_COLLAPSED_WIDTH, SUBMAP_COLLAPSED_HEIGHT);
        expect(transform.toMinimap(900, 450).x).toBeGreaterThan(transform.toMinimap(0, 0).x);
        expect(transform.toMinimap(900, 450).y).toBeGreaterThan(transform.toMinimap(0, 0).y);
        const empty = submapMinimapTransform([], SUBMAP_COLLAPSED_WIDTH, SUBMAP_COLLAPSED_HEIGHT);
        expect(empty.scale).toBeGreaterThan(0);
        expect(Number.isFinite(empty.toMinimap(0, 0).x)).toBe(true);
    });
});
