export {};

declare module "./director-desk-engine.js" {
    export type DirectorEngineCallbacks = {
        select?: (id: string) => void;
        point?: (index: number) => void;
        ground?: (point: [number, number, number]) => void;
        stroke?: (points: [number, number, number][]) => void;
        joint?: (joint: string) => void;
        transformStart?: () => void;
        transform?: (position: [number, number, number], rotation: [number, number, number], scale: [number, number, number]) => void;
        transformEnd?: (cancel?: boolean) => void;
    };

    export class Engine {
        constructor(project: unknown, stage: HTMLElement, shot: HTMLElement, callbacks: DirectorEngineCallbacks);
        /** 当前播放头（秒）。UI 直接读写后调用 sample() 生效。 */
        time: number;
        /** 预览机位：'program' 表示按 cuts 自动切镜的成片视角。 */
        previewId: string;
        rebuild(project: unknown): void;
        render(force?: boolean): void;
        /** 采样给定时间的全部实体/机位状态（scrub 核心）。 */
        sample(time: number): void;
        /** 选中实体（空串清除选择）；不存在或锁定实体时不挂 gizmo。 */
        select(id: string, point?: number): void;
        dispose(): void;
    }

    export function configureCamera(camera: unknown, focal: number, aspect: number): void;
}
