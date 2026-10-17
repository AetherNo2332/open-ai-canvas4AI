import type { PrevisCamera, PrevisObject, PrevisScene, PrevisTransform, PrevisVec3 } from "@/types/previs";
import { touchPrevisScene } from "@/lib/canvas/previs/previs-scene";

/**
 * 检查器纯逻辑（DirectorDesk 导演编辑器 scope，P1）：
 * 「当前要看谁」（选中的对象 + 活动镜头的机位）与对象/机位的受限更新。
 */

export type PrevisInspectorViewModel = {
    object: { id: string; name: string; position: PrevisVec3; rotation: PrevisVec3; scale: PrevisVec3 } | null;
    camera: { id: string; name: string; focalLength: number; target: PrevisVec3 } | null;
};

export function previsInspectorViewModel(scene: PrevisScene, selectedEntityId: string, activeShotId: string): PrevisInspectorViewModel {
    const object = scene.objects.find((candidate) => candidate.id === selectedEntityId) ?? null;
    const shot = scene.shots.find((candidate) => candidate.id === activeShotId);
    const camera = shot ? (scene.cameras.find((candidate) => candidate.id === shot.cameraId) ?? null) : null;
    return {
        object: object
            ? { id: object.id, name: object.name, position: object.transform.position, rotation: object.transform.rotation, scale: object.transform.scale }
            : null,
        camera: camera ? { id: camera.id, name: camera.name, focalLength: camera.focalLength, target: camera.target } : null,
    };
}

const clampVec3 = (value: PrevisVec3, min: number, max: number): PrevisVec3 =>
    value.map((axis) => (Number.isFinite(axis) ? Math.min(max, Math.max(min, axis)) : 0)) as PrevisVec3;

/** 缩放边界 [0.01, 100]：0 会破坏引擎的包围盒，过大没有意义。 */
export function updatePrevisObjectTransform(scene: PrevisScene, id: string, patch: Partial<PrevisTransform>): PrevisScene | null {
    const object = scene.objects.find((candidate) => candidate.id === id);
    if (!object) return null;
    const next = {
        ...scene,
        objects: scene.objects.map((candidate) => {
            if (candidate.id !== id) return candidate;
            const transform: PrevisTransform = { ...candidate.transform, ...(patch as PrevisTransform) };
            transform.position = clampVec3(transform.position, -1000, 1000);
            transform.rotation = clampVec3(transform.rotation, -Math.PI * 2, Math.PI * 2);
            transform.scale = clampVec3(transform.scale, 0.01, 100);
            return { ...candidate, transform } as PrevisObject;
        }),
    };
    return touchPrevisScene(next);
}

/** 焦段边界 [1, 300]mm；目标点收敛到 ±1000m。 */
export function updatePrevisCameraParams(scene: PrevisScene, id: string, patch: { focalLength?: number; target?: PrevisVec3 }): PrevisScene | null {
    const camera = scene.cameras.find((candidate) => candidate.id === id);
    if (!camera) return null;
    const next = {
        ...scene,
        cameras: scene.cameras.map((candidate) => {
            if (candidate.id !== id) return candidate;
            const updated: PrevisCamera = { ...candidate, ...patch };
            if (patch.focalLength !== undefined) updated.focalLength = Number.isFinite(patch.focalLength) ? Math.min(300, Math.max(1, patch.focalLength)) : camera.focalLength;
            if (patch.target !== undefined) updated.target = clampVec3(updated.target, -1000, 1000);
            return updated;
        }),
    };
    return touchPrevisScene(next);
}