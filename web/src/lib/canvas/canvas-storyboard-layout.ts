import type { CanvasNodeData, StoryboardColumn } from "@/types/canvas";

export const STORYBOARD_ROW_HEIGHT = 48;
export const STORYBOARD_HEADER_HEIGHT = 124;

/** 全屏编辑器固定右侧操作列宽度。 */
export const STORYBOARD_EDITOR_ACTIONS_WIDTH = 148;

const STORYBOARD_ADD_ROW_HEIGHT = 36;
export const STORYBOARD_COMPOSER_MIN_HEIGHT = 104;
const STORYBOARD_COMPOSER_MAX_HEIGHT = 180;

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

/** Composer measurements are local UI state. Only a node below its minimum
 * needs a geometry change; measuring must not mark the node as manually sized. */
export function applyStoryboardComposerMeasurement<T extends Pick<CanvasNodeData, "height" | "metadata">>(node: T, composerHeight: number): T {
    const measured = normalizedComposerHeight(composerHeight);
    const height = node.metadata?.locked ? node.height : Math.max(node.height, storyboardMinNodeHeight(measured));
    if (node.height === height && node.metadata?.storyboardComposerHeight === measured) return node;
    return { ...node, height, metadata: { ...node.metadata, storyboardComposerHeight: measured } };
}

export function storyboardTableHeight(nodeHeight: number, composerHeight = STORYBOARD_COMPOSER_MIN_HEIGHT) {
    return Math.max(STORYBOARD_ROW_HEIGHT, nodeHeight - STORYBOARD_HEADER_HEIGHT - STORYBOARD_ADD_ROW_HEIGHT - normalizedComposerHeight(composerHeight));
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
    return STORYBOARD_CARD_COLUMN_WIDTHS[column] || DEFAULT_CARD_COLUMN_WIDTH;
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
    return STORYBOARD_EDITOR_COLUMN_WIDTHS[column] || 180;
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
