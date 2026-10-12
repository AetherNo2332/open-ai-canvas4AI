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
        rebuild(project: unknown): void;
        render(force?: boolean): void;
        dispose(): void;
    }

    export function configureCamera(camera: unknown, focal: number, aspect: number): void;
}
