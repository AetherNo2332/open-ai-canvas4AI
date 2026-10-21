import { describe, expect, test } from "bun:test";
import { createRef } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { InfiniteCanvas } from "../src/components/canvas/infinite-canvas";
import { canvasAppearanceForTheme } from "../src/lib/canvas/canvas-appearance";
import { CANVAS_GRID_CELL_PX, CANVAS_GRID_MIN_VISIBLE_SCALE, CANVAS_GRID_OVERSCAN_PX, canvasGridGeometry, canvasGridLayerStyle } from "../src/lib/canvas/canvas-grid-geometry";
import { applyCanvasLiveViewport, CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, CANVAS_VIEWPORT_PREVIEW_EVENT } from "../src/lib/canvas/canvas-live-viewport";
import type { CanvasBackgroundMode } from "../src/lib/canvas-theme";
import type { ViewportTransform } from "../src/types/canvas";

const viewports: ViewportTransform[] = [
    { x: 0, y: 0, k: 1 },
    { x: 389.5, y: -207.25, k: 0.05 },
    { x: -17.125, y: 20.875, k: 0.119 },
    { x: -17.125, y: 20.875, k: 0.12 },
    { x: 150.2, y: -40.1, k: 0.666 },
    { x: 150.2, y: -40.1, k: 0.667 },
    { x: -1800.5, y: 3100.75, k: 2 },
    { x: 10_000_000.5, y: -9_999_999.25, k: 1 },
];

function renderGrid(mode: CanvasBackgroundMode, viewport: ViewportTransform, theme: "light" | "dark" = "dark") {
    const markup = renderToStaticMarkup(
        <InfiniteCanvas containerRef={createRef<HTMLDivElement>()} viewport={viewport} onViewportChange={() => {}} backgroundMode={mode} appearance={canvasAppearanceForTheme(theme)}>
            <div data-test-node />
        </InfiniteCanvas>,
    );
    return markup.match(/<div data-canvas-grid-layer[^>]*>/)?.[0] ?? null;
}

describe("canvas world-space background", () => {
    test("grid cell follows zoom and stays anchored to the world origin", () => {
        for (const viewport of viewports) {
            const geometry = canvasGridGeometry(viewport);
            expect(geometry.cellPx).toBeCloseTo(CANVAS_GRID_CELL_PX * viewport.k, 6);
            for (const [screen, offset] of [
                [viewport.x, geometry.offsetX],
                [viewport.y, geometry.offsetY],
            ] as const) {
                expect(offset).toBeGreaterThanOrEqual(0);
                expect(offset).toBeLessThan(geometry.cellPx);
                // 层原点在 -overscan，相位必须补上这段外扩，网格线才落在世界坐标的整数格上。
                const cells = (screen + geometry.overscanPx - offset) / geometry.cellPx;
                expect(Math.abs(cells - Math.round(cells))).toBeLessThan(1e-6);
            }
        }
        // 异常存档的大倍率下，外扩必须覆盖单元格边长，避免平移时露出视口底色。
        expect(canvasGridLayerStyle({ x: 0, y: 0, k: 4 }).overscanPx).toBe(192);
    });

    test("rendered grid tracks pan and zoom instead of staying screen-fixed", () => {
        const initial = renderGrid("lines", { x: 0, y: 0, k: 1 });
        expect(initial).not.toBeNull();
        expect(initial).toContain(`inset:-${CANVAS_GRID_OVERSCAN_PX}px`);
        expect(initial).toContain("background-size:48px 48px");
        expect(initial).toContain("transform:translate3d(4px, 4px, 0)");
        expect(initial).not.toContain("overscan");
        expect(initial).not.toContain("visibility:hidden");

        expect(renderGrid("lines", { x: 120, y: -36, k: 1 })).toContain("transform:translate3d(28px, 16px, 0)");
        const zoomedOut = renderGrid("lines", { x: 0, y: 0, k: 0.5 });
        expect(zoomedOut).toContain("background-size:24px 24px");
        expect(zoomedOut).toContain("visibility:hidden");
        expect(renderGrid("lines", { x: 0, y: 0, k: 2 })).toContain("background-size:96px 96px");

        const oversized = renderGrid("lines", { x: 0, y: 0, k: 4 });
        expect(oversized).toContain("inset:-192px");
        expect(oversized).toContain("background-size:192px 192px");

        for (const viewport of viewports.slice(1)) {
            expect(renderGrid("lines", viewport)).not.toBe(initial);
        }
    });

    test("grid hides when zoom drops below 60%", () => {
        expect(canvasGridLayerStyle({ x: 0, y: 0, k: CANVAS_GRID_MIN_VISIBLE_SCALE }).visible).toBe(true);
        expect(canvasGridLayerStyle({ x: 0, y: 0, k: 0.599 }).visible).toBe(false);
        for (const [scale, hidden] of [
            [0.05, true],
            [0.599, true],
            [0.6, false],
            [1, false],
            [2, false],
        ] as const) {
            for (const mode of ["lines", "dots"] as const) {
                const grid = renderGrid(mode, { x: 12.5, y: -8, k: scale });
                expect(grid?.includes("visibility:hidden") ?? false).toBe(hidden);
            }
        }
        // 空白模式仍由 display:none 负责，缩放阈值不会把它重新显示出来。
        expect(renderGrid("blank", { x: 0, y: 0, k: 0.5 })).toContain("display:none");
    });

    test("dots reuse the same world geometry and keep their radius", () => {
        for (const viewport of viewports) {
            const lines = renderGrid("lines", viewport);
            const dots = renderGrid("dots", viewport);
            const style = canvasGridLayerStyle(viewport);
            expect(dots).toContain("0.8px, transparent 1px");
            expect(dots).toContain(`background-size:${style.backgroundSize}`);
            expect(dots).toContain(`transform:${style.transform}`);
            expect(lines).toContain(`transform:${style.transform}`);
            expect(lines).toContain(`background-size:${style.backgroundSize}`);
        }
    });

    test("blank mode keeps the layer but hides drawing", () => {
        for (const viewport of viewports) {
            const blank = renderGrid("blank", viewport);
            expect(blank).not.toBeNull();
            expect(blank).toContain("display:none");
        }
    });

    test("live viewport updates move the grid with the world layer", () => {
        const properties = new Map<string, string>([["--canvas-committed-scale", "0.5"]]);
        const world = { isConnected: true, style: { transform: "", transformOrigin: "", willChange: "" } };
        const grid = { isConnected: true, style: { backgroundSize: "", transform: "", visibility: "" } };
        let gridLookups = 0;
        const container = Object.assign(new EventTarget(), {
            style: {
                getPropertyValue: (name: string) => properties.get(name) ?? "",
                setProperty: (name: string, value: string) => properties.set(name, value),
            },
            dataset: { canvasViewportInteracting: "true" },
            querySelector: (selector: string) => {
                if (selector === "[data-canvas-world-layer]") return world;
                if (selector === "[data-canvas-grid-layer]") {
                    gridLookups += 1;
                    return grid;
                }
                return null;
            },
        });
        const graphics: ViewportTransform[] = [];
        const previews: ViewportTransform[] = [];
        let scrolls = 0;
        container.addEventListener(CANVAS_GRAPHICS_VIEWPORT_PREVIEW_EVENT, (event) => graphics.push((event as CustomEvent<ViewportTransform>).detail));
        container.addEventListener(CANVAS_VIEWPORT_PREVIEW_EVENT, (event) => previews.push((event as CustomEvent<ViewportTransform>).detail));
        container.addEventListener("scroll", () => scrolls++);

        const gridTransforms = new Set<string>();
        for (const viewport of viewports) {
            applyCanvasLiveViewport(container as unknown as HTMLDivElement, viewport, false);
            const style = canvasGridLayerStyle(viewport);
            expect(world.style.transform).toBe(`translate3d(${viewport.x}px, ${viewport.y}px, 0) scale(${viewport.k / 0.5})`);
            expect(grid.style.backgroundSize).toBe(style.backgroundSize);
            expect(grid.style.transform).toBe(style.transform);
            expect(grid.style.visibility).toBe(style.visible ? "" : "hidden");
            // 交互期只写合成层，根变量留到提交帧统一校正，避免整棵节点树重算样式。
            expect(properties.get("--canvas-live-scale")).toBeUndefined();
            expect(properties.get("--canvas-live-inverse-scale")).toBeUndefined();
            gridTransforms.add(style.transform);
        }
        expect(gridTransforms.size).toBe(viewports.length);
        expect(gridLookups).toBe(1);
        expect(graphics).toEqual(viewports);
        expect(previews).toEqual([]);
        expect(scrolls).toBe(0);
        expect(world.style.willChange).toBe("transform");

        grid.isConnected = false;
        applyCanvasLiveViewport(container as unknown as HTMLDivElement, viewports[0], false);
        expect(gridLookups).toBe(2);

        container.dataset.canvasViewportInteracting = "false";
        applyCanvasLiveViewport(container as unknown as HTMLDivElement, viewports[0]);
        expect(properties.get("--canvas-live-scale")).toBe(String(viewports[0].k));
        expect(properties.get("--canvas-live-inverse-scale")).toBe(String(1 / Math.max(viewports[0].k, 0.05)));
        expect(world.style.willChange).toBe("");
        expect(previews).toEqual([viewports[0]]);
        expect(scrolls).toBe(1);
    });
});
