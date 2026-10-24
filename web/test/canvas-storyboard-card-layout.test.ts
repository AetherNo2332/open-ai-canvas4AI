import { describe, expect, test } from "bun:test";

import { DEFAULT_STORYBOARD_COLUMNS, STORYBOARD_COLUMN_OPTIONS } from "@/lib/canvas/canvas-storyboard-columns";
import {
    STORYBOARD_EDITOR_ACTIONS_WIDTH,
    STORYBOARD_ROW_HEIGHT,
    applyStoryboardComposerMeasurement,
    storyboardCardGridTemplate,
    storyboardCardMinWidth,
    storyboardCardRowWindow,
    storyboardEditorColumnsTotal,
    storyboardEditorColumnWidth,
    storyboardEditorColumnWidths,
    storyboardEditorScrollWidth,
} from "@/lib/canvas/canvas-storyboard-layout";

describe("storyboard card column layout", () => {
    test("measuring a composer preserves manual size intent, avoids locked geometry changes and stabilizes", () => {
        const node = { height: 480, metadata: { manualSize: true, composerContent: "script" } };
        const measured = applyStoryboardComposerMeasurement(node, 152);
        expect(measured).toEqual({ height: 480, metadata: { manualSize: true, composerContent: "script", storyboardComposerHeight: 152 } });
        expect(applyStoryboardComposerMeasurement(measured, 152)).toBe(measured);
        const locked = applyStoryboardComposerMeasurement({ height: 240, metadata: { locked: true } }, 1000);
        expect(locked).toEqual({ height: 240, metadata: { locked: true, storyboardComposerHeight: 180 } });
        expect(applyStoryboardComposerMeasurement(locked, 1000)).toBe(locked);
    });

    test("default columns render fixed index/duration columns plus flexible content columns", () => {
        expect(DEFAULT_STORYBOARD_COLUMNS).toEqual(["shotNumber", "durationSeconds", "plotDescription", "shotSize", "motion", "assets"]);
        expect(storyboardCardGridTemplate(DEFAULT_STORYBOARD_COLUMNS)).toBe("64px 84px minmax(200px, 1.4fr) minmax(112px, 0.8fr) minmax(150px, 1.1fr) minmax(168px, 0.9fr)");
        expect(storyboardCardMinWidth(DEFAULT_STORYBOARD_COLUMNS)).toBe(64 + 84 + 200 + 112 + 150 + 168);
    });

    test("every selectable column has a positive card width and editor width", () => {
        for (const option of STORYBOARD_COLUMN_OPTIONS) {
            const template = storyboardCardGridTemplate([option.value]);
            expect(template.length).toBeGreaterThan(0);
            expect(template).not.toContain("NaN");
            expect(storyboardCardMinWidth([option.value])).toBeGreaterThanOrEqual(64);
            expect(storyboardEditorColumnWidth(option.value)).toBeGreaterThanOrEqual(72);
        }
    });

    test("card min width grows with more selected columns", () => {
        const twoColumns = storyboardCardMinWidth(["shotNumber", "plotDescription"]);
        const fiveColumns = storyboardCardMinWidth(["shotNumber", "plotDescription", "dialogue", "camera", "assets"]);
        expect(fiveColumns).toBeGreaterThan(twoColumns);
    });

    test("editor scroll width includes the first column and fixed action column", () => {
        const width = storyboardEditorScrollWidth(DEFAULT_STORYBOARD_COLUMNS);
        expect(width).toBe(72 + 104 + 320 + 150 + 200 + 240 + STORYBOARD_EDITOR_ACTIONS_WIDTH);
        expect(storyboardEditorScrollWidth([])).toBeGreaterThanOrEqual(960);
        expect(storyboardEditorScrollWidth(["shotNumber", "plotDescription"])).toBe(960);
    });

    test("wide editor windows grow text columns to fill the table area", () => {
        const narrow = storyboardEditorColumnWidths(DEFAULT_STORYBOARD_COLUMNS, 600);
        expect(narrow).toEqual(DEFAULT_STORYBOARD_COLUMNS.map((column) => storyboardEditorColumnWidth(column)));
        const wide = storyboardEditorColumnWidths(DEFAULT_STORYBOARD_COLUMNS, 1874);
        const total = storyboardEditorColumnsTotal(wide);
        expect(total).toBeLessThanOrEqual(1874);
        expect(total).toBeGreaterThan(1874 - 8);
        // 序号、时长、关联资产保持固定宽度；画面描述、景别、运镜分摊剩余空间。
        expect(wide[0]).toBe(72);
        expect(wide[1]).toBe(104);
        expect(wide[5]).toBe(240);
        expect(wide[2]).toBeGreaterThan(320);
        expect(wide[3]).toBeGreaterThan(150);
        expect(wide[4]).toBeGreaterThan(200);
    });
});

describe("storyboard card row window", () => {
    test("mounts only the scrolled rows plus overscan for a long storyboard", () => {
        // 73 shots in a node showing about four rows: the full list used to mount every control.
        expect(storyboardCardRowWindow(73, 0, 192)).toEqual({ start: 0, end: 8 });
        expect(storyboardCardRowWindow(73, 30 * STORYBOARD_ROW_HEIGHT, 192)).toEqual({ start: 26, end: 38 });
        expect(storyboardCardRowWindow(73, 73 * STORYBOARD_ROW_HEIGHT, 192)).toEqual({ start: 69, end: 73 });
    });

    test("short lists, empty lists and odd inputs stay within bounds", () => {
        expect(storyboardCardRowWindow(3, 0, 960)).toEqual({ start: 0, end: 3 });
        expect(storyboardCardRowWindow(0, 0, 192)).toEqual({ start: 0, end: 0 });
        expect(storyboardCardRowWindow(10, -40, 0)).toEqual({ start: 0, end: 5 });
        expect(storyboardCardRowWindow(10, 10_000, 192)).toEqual({ start: 10, end: 10 });
    });
});
