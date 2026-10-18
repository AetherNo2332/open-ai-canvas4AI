import type { PrevisScene } from "@/types/previs";

/**
 * 时间轴纯逻辑（DirectorDesk 导演编辑器 scope，P1）：
 * 整场时间轴 = 各镜头时长的顺序累计（与 previs-adapter 的整片时长一致）。
 */

export function previsTimelineTotalDuration(scene: PrevisScene): number {
    return scene.shots.reduce((total, shot) => total + Math.max(0, shot.duration), 0);
}

/** scrub 比例（0..1）→ 整场时间；越界收敛，空场景恒为 0。 */
export function resolveTimelineScrub(scene: PrevisScene, ratio: number): number {
    const total = previsTimelineTotalDuration(scene);
    if (total <= 0) return 0;
    const clamped = Number.isFinite(ratio) ? Math.min(1, Math.max(0, ratio)) : 0;
    return clamped * total;
}

/** 时间 → 所属镜头 id（镜头区间左闭右开；结束时刻/末尾归属最后一个镜头）。 */
export function findShotAtTime(scene: PrevisScene, time: number): string | null {
    if (!scene.shots.length) return null;
    const total = previsTimelineTotalDuration(scene);
    const t = Number.isFinite(time) ? Math.min(Math.max(0, time), total) : 0;
    let clock = 0;
    for (let i = 0; i < scene.shots.length; i += 1) {
        const shot = scene.shots[i];
        const end = clock + Math.max(0, shot.duration);
        if (t < end || i === scene.shots.length - 1) return shot.id;
        clock = end;
    }
    return scene.shots[scene.shots.length - 1].id;
}

/** playhead 推进：正向越过末尾环绕回 0；负向在起点停住；空场景恒 0。 */
export function advancePlayhead(current: number, delta: number, total: number): number {
    if (total <= 0) return 0;
    const base = Number.isFinite(current) ? Math.min(Math.max(0, current), total) : 0;
    const value = base + (Number.isFinite(delta) ? delta : 0);
    if (value >= total) return value - total;
    if (value < 0) return 0;
    return value;
}
