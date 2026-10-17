/** Display range in camera-space metres, fixed throughout a scene/export. */
export interface DepthRange { near: number; far: number; invert: boolean; curve?: number }
export interface DepthVideo extends DepthRange { enabled: boolean }
export const DEFAULT_DEPTH_VIDEO: Readonly<DepthVideo> = Object.freeze({ enabled: false, near: .1, far: 30, invert: false });

export function assertDepthRange(value: unknown): asserts value is DepthRange {
    const v = value as DepthRange;
    if (!v || typeof v !== 'object' || !Number.isFinite(v.near) || !Number.isFinite(v.far)
        || v.near < 0 || v.far <= v.near || v.far > 2000 || typeof v.invert !== 'boolean')
        throw Error('深度范围无效：近端须 ≥ 0，远端须大于近端且 ≤ 2000 米');
    if (v.curve !== undefined && (!Number.isFinite(v.curve) || v.curve < .25 || v.curve > 4))
        throw Error('深度曲线须在 0.25—4 之间');
}
export function assertDepthVideo(value: unknown): asserts value is DepthVideo {
    assertDepthRange(value);
    if (typeof (value as DepthVideo).enabled !== 'boolean'
        || Object.keys(value).some(k => !['enabled', 'near', 'far', 'invert', 'curve'].includes(k))) throw Error('深度预览设置无效');
}

/** A one-shot suggestion; never normalize individual playback/export frames. */
export function suggestDepthRange(distances: number[]): Pick<DepthRange, 'near' | 'far'> {
    const sorted = distances.filter(v => Number.isFinite(v) && v > 0 && v < 2000).sort((a, b) => a - b);
    if (!sorted.length) throw Error('当前镜头中没有可取值的表面');
    const low = sorted[Math.floor((sorted.length - 1) * .02)], high = sorted[Math.ceil((sorted.length - 1) * .98)];
    const margin = Math.max(.1, (high - low) * .08);
    return { near: Math.max(0, Math.floor((low - margin) * 100) / 100), far: Math.min(2000, Math.ceil((high + margin) * 100) / 100) };
}
