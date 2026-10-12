import { expect, test } from "bun:test";
import { createPrevisScene, interpolatePrevisTransform } from "@/lib/canvas/previs/previs-scene";
import { preparePrevisRenderScene, previsRenderReadiness } from "@/lib/canvas/previs/previs-render";

test("background render locks the requested shot without mutating the source", () => {
    const scene = createPrevisScene();
    const shot = { ...scene.shots[0], id: "second", cameraMove: "push_in" as const };
    scene.shots.push(shot);
    const rendered = preparePrevisRenderScene(scene, "second", 2);
    expect(rendered.activeShotId).toBe("second");
    expect(rendered.gridVisible).toBe(false);
    expect(scene.activeShotId).not.toBe("second");
    expect(scene.cameras[0].keyframes).toHaveLength(0);
    expect(interpolatePrevisTransform(rendered.cameras[0].transform, rendered.cameras[0].keyframes, 2).position).not.toEqual(rendered.cameras[0].transform.position);
});

test("explicit camera animation takes precedence over the text preset", () => {
    const scene = createPrevisScene();
    scene.shots[0].cameraMove = "pull_out";
    scene.cameras[0].keyframes = [{ id: "authored", time: 1, transform: scene.cameras[0].transform }];
    expect(preparePrevisRenderScene(scene, scene.shots[0].id, 2).cameras[0].keyframes).toEqual(scene.cameras[0].keyframes);
    expect(() => preparePrevisRenderScene(scene, "missing", 2)).toThrow();
    scene.cameras = [];
    expect(() => preparePrevisRenderScene(scene, scene.shots[0].id, 2)).toThrow();
});

test("render readiness requires actual asset readiness and usable WebGL", () => {
    const scene = createPrevisScene();
    const actorId = scene.objects[0].id;
    scene.objects[0].url = "/models/Xbot.glb";
    expect(previsRenderReadiness(scene, true, {})).toEqual({ ready: false, error: "", pending: [actorId] });
    expect(previsRenderReadiness(scene, true, { [actorId]: "ready" }).ready).toBe(true);
    expect(previsRenderReadiness(scene, true, { [actorId]: "error" }).error).toBe("asset_load_failed");
    expect(previsRenderReadiness(scene, false, { [actorId]: "ready" }).ready).toBe(false);
    scene.objects[0].visible = false;
    expect(previsRenderReadiness(scene, true, {}).ready).toBe(true);
    scene.environment = { mode: "panorama", url: "/asset/panorama" };
    expect(previsRenderReadiness(scene, true, {}).pending).toEqual(["environment"]);
});

test("the existing procedural clay actor needs no remote model load", () => {
    const scene = createPrevisScene();
    expect(previsRenderReadiness(scene, true, {}).ready).toBe(true);
});
