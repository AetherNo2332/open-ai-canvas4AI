import type { StoryboardColumn } from "@/types/canvas";

export const STORYBOARD_ROW_HEIGHT = 48;
export const STORYBOARD_HEADER_HEIGHT = 124;

/** 全屏编辑器固定右侧操作列宽度。 */
export const STORYBOARD_EDITOR_ACTIONS_WIDTH = 148;

const STORYBOARD_ADD_ROW_HEIGHT = 36;
export const STORYBOARD_COMPOSER_MIN_HEIGHT = 104;
const STORYBOARD_COMPOSER_MAX_HEIGHT = 180;
/** 全屏编辑器表头与表格内边距占用的高度；表体滚动高度 = 容器高度 - 该值。 */
const STORYBOARD_EDITOR_TABLE_CHROME_HEIGHT = 64;
const STORYBOARD_ROW_OVERSCAN = 4;
/** 画布卡片列头（h-9）在滚动容器内 sticky，行从它下方开始。 */
export const STORYBOARD_CARD_COLUMN_HEADER_HEIGHT = 36;

// 布局计算属于画布领域逻辑，独立于 React 节点组件，避免纯函数测试加载完整 UI 依赖。
function normalizedComposerHeight(composerHeight: number) {
    return Math.min(STORYBOARD_COMPOSER_MAX_HEIGHT, Math.max(STORYBOARD_COMPOSER_MIN_HEIGHT, composerHeight));
}

export function storyboardNodeHeight(rowCount: number, composerHeight = STORYBOARD_COMPOSER_MIN_HEIGHT) {
    const visibleRows = Math.min(Math.max(rowCount, 1), 4);
    return STORYBOARD_HEADER_HEIGHT + visibleRows * STORYBOARD_ROW_HEIGHT + STORYBOARD_ADD_ROW_HEIGHT + normalizedComposerHeight(composerHeight);
}

export function storyboardMinNodeHeight(composerHeight = STORYBOARD_COMPOSER_MIN_HEIGHT) {
    return STORYBOARD_HEADER_HEIGHT + STORYBOARD_ROW_HEIGHT + STORYBOARD_ADD_ROW_HEIGHT + normalizedComposerHeight(composerHeight);
}

export function storyboardTableHeight(nodeHeight: number, composerHeight = STORYBOARD_COMPOSER_MIN_HEIGHT) {
    return Math.max(STORYBOARD_ROW_HEIGHT, nodeHeight - STORYBOARD_HEADER_HEIGHT - STORYBOARD_ADD_ROW_HEIGHT - normalizedComposerHeight(composerHeight));
}

export type StoryboardRowRange = { start: number; end: number };

/**
 * Rows the in-node card list keeps mounted: the scrolled window plus overscan
 * (`end` is exclusive). Rows have a fixed height, so the rest become spacers;
 * a long storyboard otherwise mounts thousands of controls nobody can see.
 */
export function storyboardVisibleRowWindow(scrollTop: number, viewportHeight: number, rowHeight: number, rowCount: number, overscan = STORYBOARD_ROW_OVERSCAN): StoryboardRowRange {
    const count = Math.max(0, Math.floor(rowCount));
    if (!(rowHeight > 0)) return { start: 0, end: count };
    const top = Math.max(0, scrollTop);
    const first = Math.floor(top / rowHeight) - overscan;
    const last = Math.ceil((top + Math.max(rowHeight, viewportHeight)) / rowHeight) + overscan;
    const start = Math.min(count, Math.max(0, first));
    return { start, end: Math.min(count, Math.max(start, last)) };
}

/**
 * The in-node card's row window. The sticky column header sits inside the same scroll
 * container, so rows begin STORYBOARD_CARD_COLUMN_HEADER_HEIGHT below scrollTop 0 and the
 * header covers that much of the viewport.
 */
export function storyboardCardRowWindow(scrollTop: number, viewportHeight: number, rowCount: number, overscan = STORYBOARD_ROW_OVERSCAN): StoryboardRowRange {
    const header = STORYBOARD_CARD_COLUMN_HEADER_HEIGHT;
    return storyboardVisibleRowWindow(scrollTop - header, viewportHeight - header, STORYBOARD_ROW_HEIGHT, rowCount, overscan);
}

/**
 * Ranges to mount, in document order: the visible window plus the row being edited.
 * The edited row stays mounted after it scrolls out so its input keeps focus and
 * IME state; rows between it and the window stay spacers.
 */
export function storyboardMountedRowRanges(window: StoryboardRowRange, rowCount: number, editingIndex: number): StoryboardRowRange[] {
    if (editingIndex < 0 || editingIndex >= rowCount || (editingIndex >= window.start && editingIndex < window.end)) return [window];
    if (editingIndex === window.end) return [{ start: window.start, end: window.end + 1 }];
    if (editingIndex === window.start - 1) return [{ start: editingIndex, end: window.end }];
    const edited = { start: editingIndex, end: editingIndex + 1 };
    return editingIndex < window.start ? [edited, window] : [window, edited];
}

/** 全屏编辑器表体的滚动高度完全跟随容器测量值，不设固定下限，避免全屏下方留白或溢出。 */
export function storyboardEditorTableScrollY(containerHeight: number) {
    return Math.max(1, containerHeight - STORYBOARD_EDITOR_TABLE_CHROME_HEIGHT);
}

type ResizeObserverLike = new (callback: () => void) => { observe: (target: never) => void; disconnect: () => void };

/**
 * 订阅元素尺寸：立即测量一次，之后每次 resize 再报告。Modal 的 portal 延迟挂载，
 * 调用方应在元素实际出现（callback ref）后再订阅，重开时重新绑定。
 */
export function observeStoryboardElementSize<T extends { clientWidth: number; clientHeight: number }>(
    element: T,
    onSize: (size: { width: number; height: number }) => void,
    Observer: ResizeObserverLike | undefined = typeof ResizeObserver === "undefined" ? undefined : ResizeObserver,
) {
    const measure = () => onSize({ width: element.clientWidth, height: element.clientHeight });
    measure();
    const observer = Observer ? new Observer(measure) : null;
    observer?.observe(element as never);
    return () => observer?.disconnect();
}

/**
 * 画布节点卡片按勾选的列渲染：序号、时长、关联资产保持固定宽度，
 * 文本列用 minmax(最小宽度, 权重) 在节点变宽时分配剩余空间，放不下时由卡片横向滚动兜底。
 */
const STORYBOARD_CARD_COLUMN_WIDTHS: Partial<Record<StoryboardColumn, { min: number; grow: number }>> = {
    shotNumber: { min: 64, grow: 0 },
    durationSeconds: { min: 84, grow: 0 },
    assets: { min: 168, grow: 0.9 },
    plotDescription: { min: 200, grow: 1.4 },
    dialogue: { min: 180, grow: 1.2 },
    videoMotionPrompt: { min: 200, grow: 1.4 },
    imageGenerationPrompt: { min: 200, grow: 1.4 },
    timeBeats: { min: 150, grow: 1.1 },
    performanceBlocking: { min: 150, grow: 1.1 },
    narrativeIntent: { min: 140, grow: 1 },
    viewerPOV: { min: 140, grow: 1 },
    camera: { min: 140, grow: 1 },
    motion: { min: 150, grow: 1.1 },
    lightingAndAtmosphere: { min: 150, grow: 1 },
    continuityOut: { min: 150, grow: 1 },
    negativePrompt: { min: 150, grow: 1 },
    shotSize: { min: 112, grow: 0.8 },
    emotion: { min: 120, grow: 0.9 },
    audioEffects: { min: 130, grow: 0.9 },
};

const DEFAULT_CARD_COLUMN_WIDTH = { min: 120, grow: 1 };

function storyboardCardColumnWidth(column: StoryboardColumn) {
    return STORYBOARD_CARD_COLUMN_WIDTHS[column] ?? DEFAULT_CARD_COLUMN_WIDTH;
}

export function storyboardCardGridTemplate(columns: readonly StoryboardColumn[]) {
    return columns
        .map((column) => {
            const width = storyboardCardColumnWidth(column);
            return width.grow > 0 ? `minmax(${width.min}px, ${width.grow}fr)` : `${width.min}px`;
        })
        .join(" ");
}

export function storyboardCardMinWidth(columns: readonly StoryboardColumn[]) {
    return columns.reduce((total, column) => total + storyboardCardColumnWidth(column).min, 0);
}

/** 全屏编辑器列宽按内容类型给不同基准，避免长文本列和短枚举列等宽。 */
const STORYBOARD_EDITOR_COLUMN_WIDTHS: Partial<Record<StoryboardColumn, number>> = {
    shotNumber: 72,
    durationSeconds: 104,
    assets: 240,
    plotDescription: 320,
    dialogue: 280,
    videoMotionPrompt: 300,
    imageGenerationPrompt: 300,
    timeBeats: 200,
    performanceBlocking: 200,
    narrativeIntent: 180,
    viewerPOV: 180,
    shotSize: 150,
    emotion: 150,
    lightingAndAtmosphere: 180,
    audioEffects: 170,
    camera: 200,
    motion: 200,
    continuityOut: 200,
    negativePrompt: 220,
};

export function storyboardEditorColumnWidth(column: StoryboardColumn) {
    return STORYBOARD_EDITOR_COLUMN_WIDTHS[column] ?? 180;
}

export function storyboardEditorScrollWidth(columns: readonly StoryboardColumn[]) {
    return Math.max(960, columns.reduce((total, column) => total + storyboardEditorColumnWidth(column), 0) + STORYBOARD_EDITOR_ACTIONS_WIDTH);
}

// 固定宽度列不参与“填满窗口”的剩余空间分配，避免序号/时长被拉得很宽。
const STORYBOARD_EDITOR_FIXED_COLUMNS = new Set<StoryboardColumn>(["shotNumber", "durationSeconds", "assets"]);

/**
 * 全屏编辑器的实际列宽：窗口足够宽时把剩余空间按比例分给文本列，填满表格区；
 * 窗口不够宽时保留基准宽度，让 Table 出现横向滚动而不是压缩到不可读。
 */
export function storyboardEditorColumnWidths(columns: readonly StoryboardColumn[], availableWidth = 0) {
    const base = columns.map((column) => storyboardEditorColumnWidth(column));
    const total = base.reduce((sum, width) => sum + width, 0) + STORYBOARD_EDITOR_ACTIONS_WIDTH;
    if (availableWidth <= total) return base;
    const extra = availableWidth - total;
    const flexible = columns.map((column, index) => (STORYBOARD_EDITOR_FIXED_COLUMNS.has(column) ? -1 : index)).filter((index) => index >= 0);
    if (!flexible.length) return base;
    const weight = flexible.reduce((sum, index) => sum + base[index], 0);
    const widths = base.slice();
    for (const index of flexible) widths[index] = base[index] + Math.floor((extra * base[index]) / weight);
    return widths;
}

export function storyboardEditorColumnsTotal(widths: readonly number[]) {
    return widths.reduce((sum, width) => sum + width, 0) + STORYBOARD_EDITOR_ACTIONS_WIDTH;
}
