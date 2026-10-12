import { expect, test } from "bun:test";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
import { createPrevisScene } from "../src/lib/canvas/previs/previs-scene";
const names = (value: string) => Object.fromEntries(value.split(" ").map((name) => [name, {}]));
const capabilities = {
    protocolVersion: 1,
    nodes: [],
    structuredFields: {},
    previsFields: {
        settings: names("title background environmentIntensity environment gridVisible activeShotId"),
        object: names(
            "name kind primitive transform color visible castShadow receiveShadow pose archetype actorProfile rig motionClips activeMotionClipId characterBinding boneOverrides boneTracks sourceNodeId assetId storageKey url mimeType keyframes motionPath",
        ),
        camera: names("name transform target focalLength fov aperture focusDistance near far keyframes motionPath"),
        light: names("name type transform color intensity angle penumbra castShadow"),
        shot: names("name cameraId duration fps aspectRatio shotSize cameraMove prompt previewNodeId clayVideoNodeId depthNodeId normalNodeId"),
    },
};
test("actual native scene becomes entity commands preserving editor IDs and rig data", () => {
    const before = { id: "canvas", nodes: [], connections: [] };
    const scene = createPrevisScene();
    const ops = canvasEditorCommands(before, { ...before, previsScenes: [scene] }, capabilities);
    expect(ops).toHaveLength(1);
    expect(ops[0]).toMatchObject({ type: "edit_previs_scene", id: scene.id, action: "initialize" });
    expect(ops[0].previsEntityOperations).toContainEqual({ type: "upsert_camera", id: scene.cameras[0].id, patch: expect.objectContaining({ aperture: 2.8 }) });
    expect(JSON.stringify(ops)).not.toContain("createdAt");
});
test("previs edits preserve unrelated fields and express removal/order without whole scene replacement", () => {
    const scene = createPrevisScene() as any;
    scene.extension = "retain";
    scene.environment.extension = true;
    const next = structuredClone(scene);
    next.cameras[0].aperture = 4;
    next.environment.exposure = 1;
    next.lights.reverse();
    next.objects = [];
    const ops = canvasEditorCommands({ id: "canvas", nodes: [], connections: [], previsScenes: [scene] }, { id: "canvas", nodes: [], connections: [], previsScenes: [next] }, capabilities);
    expect(ops[0].previsEntityOperations).toContainEqual({ type: "settings", patch: { environment: { exposure: 1 } } });
    expect(ops[0].previsEntityOperations).toContainEqual({ type: "delete_object", id: scene.objects[0].id });
    expect(ops[0].previsEntityOperations).toContainEqual({ type: "reorder_light", ids: next.lights.map((v: any) => v.id) });
    expect(JSON.stringify(ops)).not.toContain("extension");
    next.taskStatus = "done";
    expect(() => canvasEditorCommands({ id: "canvas", previsScenes: [scene] }, { id: "canvas", previsScenes: [next] }, capabilities)).toThrow("canvas_editor_unmapped:previs:");
});
test("absent and empty previs collection have no semantic difference", () => {
    expect(canvasEditorCommands({ id: "canvas" }, { id: "canvas", previsScenes: [] }, capabilities)).toEqual([]);
});
