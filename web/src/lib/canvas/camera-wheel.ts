/**
 * 摄像机参数卡的滚轮切档：把一次滚轮事件换算成「前进/后退一档」，
 * 并累计触控板的小增量，避免一次高速滚动跳过多个选项。
 */
export const CAMERA_WHEEL_STEP_THRESHOLD = 50;
export const CAMERA_WHEEL_IDLE_RESET_MS = 180;

/** 不同 deltaMode 的滚轮事件统一换算为近似像素：行模式按 40、页模式按 100 计。 */
export function normalizeCameraWheelDelta(deltaY: number, deltaMode = 0) {
    if (!Number.isFinite(deltaY) || deltaY === 0) return 0;
    const scale = deltaMode === 1 ? 40 : deltaMode === 2 ? 100 : 1;
    return deltaY * scale;
}

export type CameraWheelStep = { step: -1 | 0 | 1; accumulator: number };

/** 累计滚轮增量；达到阈值时返回一次切换方向并清空累计，一次事件最多切换一档。 */
export function takeCameraWheelStep(accumulator: number, deltaY: number, threshold = CAMERA_WHEEL_STEP_THRESHOLD): CameraWheelStep {
    if (!Number.isFinite(deltaY) || deltaY === 0) return { step: 0, accumulator };
    const limit = threshold > 0 ? threshold : 1;
    const next = accumulator + deltaY;
    if (Math.abs(next) < limit) return { step: 0, accumulator: next };
    return { step: next > 0 ? 1 : -1, accumulator: 0 };
}
