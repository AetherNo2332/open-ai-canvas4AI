import type { PrevisScene, PrevisShot } from "@/types/previs";
import { touchPrevisScene } from "@/lib/canvas/previs/previs-scene";

/**
 * 镜头列表的纯逻辑：DirectorDesk 原生镜头列表（导演编辑器）与
 * 保存协调器/画布场景之间的唯一数据通道。所有变更都刷新 updatedAt，
 * 使场景同步决策（sceneId/updatedAt）能感知每一次 UI 编辑。
 */

export type PrevisShotListItem = {
    shotId: string;
    shot: PrevisShot;
    index: number;
    /** 在整场时间轴上的起点（秒，累计）。 */
    startAt: number;
    /** 在整场时间轴上的终点（秒，累计）。 */
    endAt: number;
    isActive: boolean;
};

export function buildPrevisShotListItems(scene: PrevisScene): PrevisShotListItem[] {
    let clock = 0;
    return scene.shots.map((shot, index) => {
        const startAt = clock;
        clock += Math.max(0, shot.duration);
        return { shotId: shot.id, shot, index, startAt, endAt: clock, isActive: shot.id === scene.activeShotId };
    });
}

/** 时长边界：收敛到 (0, 120] 秒。 */
const clampShotDuration = (value: number): number => {
    if (!Number.isFinite(value)) return 5;
    return Math.min(120, Math.max(0.5, value));
};

const newShotId = (): string => (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function" ? crypto.randomUUID() : `shot-${Date.now()}-${Math.random().toString(16).slice(2)}`);

export function addPrevisShot(scene: PrevisScene, cameraId: string): { scene: PrevisScene; shot: PrevisShot } | null {
    if (!scene.cameras.some((camera) => camera.id === cameraId)) return null;
    const shot: PrevisShot = {
        id: newShotId(),
        name: `镜头 ${scene.shots.length + 1}`,
        cameraId,
        duration: 5,
        fps: 24,
        shotSize: "medium",
        cameraMove: "static",
        prompt: "",
    };
    return { scene: touchPrevisScene({ ...scene, shots: [...scene.shots, shot], activeShotId: shot.id }), shot };
}

export function updatePrevisShot(scene: PrevisScene, id: string, patch: Partial<PrevisShot>): PrevisScene | null {
    if (!scene.shots.some((shot) => shot.id === id)) return null;
    const next = {
        ...scene,
        shots: scene.shots.map((shot) => {
            if (shot.id !== id) return shot;
            const merged = { ...shot, ...patch };
            if (patch.duration !== undefined) merged.duration = clampShotDuration(patch.duration);
            if (patch.fps !== undefined && ![24, 25, 30].includes(patch.fps)) merged.fps = shot.fps;
            return merged;
        }),
    };
    return touchPrevisScene(next);
}

/** 场景至少保留一个镜头；删除活动镜头时激活其后继（末位则回退第一个）。 */
export function removePrevisShot(scene: PrevisScene, id: string): PrevisScene | null {
    const index = scene.shots.findIndex((shot) => shot.id === id);
    if (index < 0 || scene.shots.length <= 1) return null;
    const shots = scene.shots.filter((shot) => shot.id !== id);
    let activeShotId = scene.activeShotId;
    if (scene.activeShotId === id) {
        const successor = shots[Math.min(index, shots.length - 1)];
        activeShotId = successor.id;
    }
    return touchPrevisScene({ ...scene, shots, activeShotId });
}

export function movePrevisShot(scene: PrevisScene, id: string, delta: -1 | 1): PrevisScene | null {
    const index = scene.shots.findIndex((shot) => shot.id === id);
    const target = index + delta;
    if (index < 0 || target < 0 || target >= scene.shots.length) return null;
    const shots = [...scene.shots];
    [shots[index], shots[target]] = [shots[target], shots[index]];
    return touchPrevisScene({ ...scene, shots });
}
