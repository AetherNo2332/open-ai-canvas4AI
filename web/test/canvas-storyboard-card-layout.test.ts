import { describe, expect, test } from "bun:test";

import { DEFAULT_STORYBOARD_COLUMNS, STORYBOARD_COLUMN_OPTIONS } from "@/lib/canvas/canvas-storyboard-columns";
import { STORYBOARD_EDITOR_ACTIONS_WIDTH, storyboardCardGridTemplate, storyboardCardMinWidth, storyboardEditorColumnsTotal, storyboardEditorColumnWidth, storyboardEditorColumnWidths, storyboardEditorScrollWidth } from "@/lib/canvas/canvas-storyboard-layout";

describe("storyboard card column layout", () => {
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
