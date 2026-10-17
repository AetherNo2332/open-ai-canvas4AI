import { resolvePrevisCameraMoveKeyframes } from "./previs-animation-semantics";
import { resolvePrevisViewFraming } from "./previs-view-modes";
import { PREVIS_DEFAULT_ACTOR_URL } from "./previs-scene";
import type { PrevisCameraMove, PrevisScene, PrevisTransform, PrevisVec3 } from "@/types/previs";

export function cameraMoveTransform(transform: PrevisTransform, move: PrevisCameraMove): PrevisTransform {
    const offsets: Record<PrevisCameraMove, PrevisVec3> = { static: [0, 0, 0], push_in: [0, 0, -2], pull_out: [0, 0, 2], pan_left: [-2, 0, 0], pan_right: [2, 0, 0], tilt_up: [0, 1.5, 0], tilt_down: [0, -1.2, 0], orbit_left: [-2.5, 0, -1.5], orbit_right: [2.5, 0, -1.5], handheld: [0.18, 0.08, -0.15] };
    const offset = offsets[move];
    if (!offset) throw new Error("invalid_camera_move");
    return { ...transform, position: transform.position.map((value, index) => value + offset[index]) as PrevisVec3 };
}

export function preparePrevisRenderScene(source: PrevisScene, shotId: string, duration: number): PrevisScene {
    const scene = structuredClone(source);
    const shot = scene.shots.find((item) => item.id === shotId);
    const camera = scene.cameras.find((item) => item.id === shot?.cameraId);
    if (!shot || !camera || !Number.isFinite(duration) || duration < 0.1 || duration > 60) throw new Error("invalid_scene");
    scene.activeShotId = shotId;
    scene.gridVisible = false;
    shot.duration = duration;
    if (shot.cameraMove !== "static" && !camera.keyframes.length && !camera.motionPath) {
        camera.keyframes = resolvePrevisCameraMoveKeyframes([], camera.transform, cameraMoveTransform(camera.transform, shot.cameraMove), duration);
    }
    if (!resolvePrevisViewFraming({ scene, mode: "camera", playhead: 0, cameraId: camera.id })) throw new Error("invalid_camera");
    return scene;
}

export type PrevisRenderState = { ready: boolean; error: string; pending: string[] };
export function previsRenderReadiness(scene: PrevisScene, contextReady: boolean, signals: Record<string, string>): PrevisRenderState {
    const required = scene.objects.filter((item) => item.visible && !(item.kind === "actor" && item.url === PREVIS_DEFAULT_ACTOR_URL && !item.assetId) && (item.kind === "model" || item.kind === "actor" || item.primitive === "character" || item.kind === "billboard")).map((item) => item.id);
    if (scene.environment?.mode === "panorama") required.push("environment");
    const failed = required.some((id) => signals[id] === "error");
    const pending = required.filter((id) => signals[id] !== "ready");
    return { ready: contextReady && !failed && pending.length === 0, error: failed ? "asset_load_failed" : "", pending };
}
