import type { ViewportTransform } from "@/types/canvas";

/** 空间网格的世界单元格边长（100% 缩放时的屏幕像素）。 */
export const CANVAS_GRID_CELL_PX = 48;

/** 网格层向视口外多画一圈，保证平移时不会露出边缘。 */
export const CANVAS_GRID_OVERSCAN_PX = 100;

/** 缩放低于该比例时隐藏空间网格，避免缩小后网格过密干扰内容。 */
export const CANVAS_GRID_MIN_VISIBLE_SCALE = 0.6;

export type CanvasGridGeometry = {
    /** 当前缩放下单元格的屏幕边长。 */
    cellPx: number;
    /** 层向视口外多画的尺寸，必须覆盖最大平移余量（即单元格屏幕边长）。 */
    overscanPx: number;
    /** 网格层相对视口的平移，让网格线落回世界坐标的整数格。 */
    offsetX: number;
    offsetY: number;
};

export type CanvasGridLayerStyle = {
    backgroundSize: string;
    transform: string;
    overscanPx: number;
    /** 视口缩放达到阈值时才显示网格。 */
    visible: boolean;
};

/**
 * 网格属于世界坐标：平移跟着视口走，缩放改变单元格屏幕间距。
 * 层本身向外多画一圈（inset = -overscan），所以相位要补上这段外扩；
 * 取模后只需很小的平移量，避免大数值坐标。
 */
export function canvasGridGeometry(viewport: ViewportTransform): CanvasGridGeometry {
    const cellPx = CANVAS_GRID_CELL_PX * viewport.k;
    const overscanPx = Math.max(CANVAS_GRID_OVERSCAN_PX, Math.ceil(cellPx));
    return {
        cellPx,
        overscanPx,
        offsetX: positiveModulo(viewport.x + overscanPx, cellPx),
        offsetY: positiveModulo(viewport.y + overscanPx, cellPx),
    };
}

/** 渲染层和逐帧更新共用同一份样式，避免两处取模结果不一致导致网格跳动。 */
export function canvasGridLayerStyle(viewport: ViewportTransform): CanvasGridLayerStyle {
    const { cellPx, overscanPx, offsetX, offsetY } = canvasGridGeometry(viewport);
    const cell = formatPx(cellPx);
    return {
        backgroundSize: `${cell}px ${cell}px`,
        transform: `translate3d(${formatPx(offsetX)}px, ${formatPx(offsetY)}px, 0)`,
        overscanPx,
        visible: viewport.k >= CANVAS_GRID_MIN_VISIBLE_SCALE,
    };
}

function positiveModulo(value: number, divisor: number) {
    if (!Number.isFinite(value) || !Number.isFinite(divisor) || divisor <= 0) return 0;
    const remainder = value % divisor;
    return remainder < 0 ? remainder + divisor : remainder;
}

function formatPx(value: number) {
    return String(Math.round(value * 100) / 100);
}
