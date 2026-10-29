import { describe, expect, test } from "bun:test";

import { DEFAULT_STORYBOARD_COLUMNS, STORYBOARD_COLUMN_OPTIONS } from "@/lib/canvas/canvas-storyboard-columns";
import {
    STORYBOARD_EDITOR_ACTIONS_WIDTH,
    STORYBOARD_ROW_HEIGHT,
    observeStoryboardElementSize,
    storyboardCardGridTemplate,
    storyboardCardMinWidth,
    storyboardEditorColumnsTotal,
    storyboardEditorColumnWidth,
    storyboardEditorColumnWidths,
    storyboardEditorScrollWidth,
    storyboardEditorTableScrollY,
    storyboardMountedRowRanges,
    storyboardVisibleRowWindow,
} from "@/lib/canvas/canvas-storyboard-layout";

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

    test("duration column is wide enough for a two-digit value plus its unit", () => {
        // 两位数输入（w-9 = 36px）+ 间距 + “秒” + 两侧留白，不能挤出单元格。
        expect(storyboardCardMinWidth(["durationSeconds"])).toBeGreaterThanOrEqual(76);
        expect(storyboardEditorColumnWidth("durationSeconds")).toBeGreaterThanOrEqual(100);
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

describe("storyboard visible row window", () => {
    const rowHeight = STORYBOARD_ROW_HEIGHT;

    test("mounts only the scrolled rows plus overscan for a long storyboard", () => {
        // 73 shots in a node showing about four rows: the full list used to mount every control.
        expect(storyboardVisibleRowWindow(0, 192, rowHeight, 73)).toEqual({ start: 0, end: 8 });
        expect(storyboardVisibleRowWindow(30 * rowHeight, 192, rowHeight, 73)).toEqual({ start: 26, end: 38 });
        expect(storyboardVisibleRowWindow(73 * rowHeight, 192, rowHeight, 73)).toEqual({ start: 69, end: 73 });
    });

    test("200 rows with a 10-row viewport mount about 10 + overscan rows", () => {
        const window = storyboardVisibleRowWindow(0, 10 * rowHeight, rowHeight, 200);
        expect(window).toEqual({ start: 0, end: 14 });
        const middle = storyboardVisibleRowWindow(100 * rowHeight, 10 * rowHeight, rowHeight, 200);
        expect(middle).toEqual({ start: 96, end: 114 });
        expect(middle.end - middle.start).toBeLessThanOrEqual(10 + 2 * 4);
        expect(storyboardVisibleRowWindow(0, 10 * rowHeight, rowHeight, 200, 0)).toEqual({ start: 0, end: 10 });
    });

    test("short lists, empty lists and odd inputs stay within bounds", () => {
        expect(storyboardVisibleRowWindow(0, 960, rowHeight, 3)).toEqual({ start: 0, end: 3 });
        expect(storyboardVisibleRowWindow(0, 192, rowHeight, 0)).toEqual({ start: 0, end: 0 });
        expect(storyboardVisibleRowWindow(-40, 0, rowHeight, 10)).toEqual({ start: 0, end: 5 });
        expect(storyboardVisibleRowWindow(10_000, 192, rowHeight, 10)).toEqual({ start: 10, end: 10 });
        expect(storyboardVisibleRowWindow(0, 192, 0, 10)).toEqual({ start: 0, end: 10 });
    });

    test("the card window treats scrollTop and tableHeight as the rows' own visible span", () => {
        // The sticky header covers the first 36px of the scroll viewport and rows start 36px into the
        // content, so they cancel: at scrollTop s the first visible row pixel is s from row 0's top,
        // and tableHeight (STORYBOARD_HEADER_HEIGHT already includes the header) is the rows' height.
        expect(storyboardVisibleRowWindow(30 * rowHeight, 4 * rowHeight, rowHeight, 73, 0)).toEqual({ start: 30, end: 34 });
        expect(storyboardVisibleRowWindow(30 * rowHeight, 4 * rowHeight, rowHeight, 73)).toEqual({ start: 26, end: 38 });
        // Unscrolled, a 4-row table mounts exactly rows 0-3 without overscan.
        expect(storyboardVisibleRowWindow(0, 4 * rowHeight, rowHeight, 73, 0)).toEqual({ start: 0, end: 4 });
        // A half-scrolled row at the top keeps the next row partially visible at the bottom.
        expect(storyboardVisibleRowWindow(rowHeight / 2, 4 * rowHeight, rowHeight, 73, 0)).toEqual({ start: 0, end: 5 });
    });

    test("the row being edited stays mounted without mounting the rows in between", () => {
        const window = storyboardVisibleRowWindow(0, 10 * rowHeight, rowHeight, 200);
        const ranges = storyboardMountedRowRanges(window, 200, 150);
        expect(ranges).toEqual([
            { start: 0, end: 14 },
            { start: 150, end: 151 },
        ]);
        const mounted = ranges.reduce((sum, range) => sum + range.end - range.start, 0);
        expect(mounted).toBe(15);
        // Editing a row above the window keeps the ranges in document order.
        expect(storyboardMountedRowRanges({ start: 96, end: 114 }, 200, 3)).toEqual([
            { start: 3, end: 4 },
            { start: 96, end: 114 },
        ]);
        // Inside the window, or no edited row, there is a single range.
        expect(storyboardMountedRowRanges(window, 200, 5)).toEqual([window]);
        expect(storyboardMountedRowRanges(window, 200, -1)).toEqual([window]);
        expect(storyboardMountedRowRanges(window, 200, 250)).toEqual([window]);
        // Adjacent to the window merges into one range.
        expect(storyboardMountedRowRanges(window, 200, 14)).toEqual([{ start: 0, end: 15 }]);
    });
});

describe("storyboard full-screen editor height", () => {
    test("table body height follows the measured container without a fixed floor", () => {
        expect(storyboardEditorTableScrollY(900)).toBe(900 - 64);
        expect(storyboardEditorTableScrollY(300)).toBe(300 - 64);
        // A collapsed container must not force a 200px body that leaves blank space or overflows.
        expect(storyboardEditorTableScrollY(0)).toBe(1);
    });

    test("size observer reports the initial size and every resize, then disconnects", () => {
        const observers: Array<{ callback: () => void; targets: unknown[]; disconnected: boolean }> = [];
        class FakeResizeObserver {
            private readonly entry: { callback: () => void; targets: unknown[]; disconnected: boolean };
            constructor(callback: () => void) {
                this.entry = { callback, targets: [], disconnected: false };
                observers.push(this.entry);
            }
            observe(target: unknown) {
                this.entry.targets.push(target);
            }
            disconnect() {
                this.entry.disconnected = true;
            }
        }
        const element = { clientWidth: 1200, clientHeight: 640 };
        const sizes: Array<{ width: number; height: number }> = [];
        const stop = observeStoryboardElementSize(element, (size) => sizes.push(size), FakeResizeObserver);
        expect(sizes).toEqual([{ width: 1200, height: 640 }]);
        expect(observers[0].targets).toEqual([element]);
        element.clientHeight = 880;
        element.clientWidth = 1600;
        observers[0].callback();
        expect(sizes.at(-1)).toEqual({ width: 1600, height: 880 });
        stop();
        expect(observers[0].disconnected).toBe(true);
    });

    test("size observer still measures once when ResizeObserver is unavailable", () => {
        const sizes: Array<{ width: number; height: number }> = [];
        const stop = observeStoryboardElementSize({ clientWidth: 10, clientHeight: 20 }, (size) => sizes.push(size), undefined);
        expect(sizes).toEqual([{ width: 10, height: 20 }]);
        stop();
    });
});
