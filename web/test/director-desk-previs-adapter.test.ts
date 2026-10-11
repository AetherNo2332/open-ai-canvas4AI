import { expect, test } from "bun:test";
import { createPrevisScene } from "@/lib/canvas/previs/previs-scene";
import { previsSceneToDirectorDocument, directorDocumentToPrevisScene } from "@/integrations/director-desk/previs-adapter";

test("maps actors, props, cameras, paths, cuts and character bindings with stable ids", () => {
    const scene = createPrevisScene("卧室镜头");
    scene.objects[0].characterBinding = { characterAssetId: "asset-1", characterVersionId: "v2", referenceNodeId: "node-1" };
    scene.objects[0].motionPath = { points: [[0, 0, 0], [1, 0, 2]], speed: "walk", startDelay: 0, orientToPath: true };
    const nextShot = { ...scene.shots[0], id: "shot-2", cameraMove: "push_in" as const };
    scene.shots.push(nextShot);
    const document = previsSceneToDirectorDocument(scene);
    expect(document.format).toBe("director-desk");
    expect(document.entities.map((entity) => entity.id)).toContain(scene.objects[0].id);
    expect(document.entities.find((entity) => entity.id === scene.objects[0].id)?.path?.points).toHaveLength(2);
    expect(document.entities.find((entity) => entity.id === scene.cameras[0].id)?.camera?.focal).toBe(scene.cameras[0].focalLength);
    expect(document.cuts).toHaveLength(1);
    expect(document.extensions?.infiniteCanvas?.shots).toHaveLength(2);

    const restored = directorDocumentToPrevisScene(document);
    expect(restored.id).toBe(scene.id);
    expect(restored.objects[0].id).toBe(scene.objects[0].id);
    expect(restored.objects[0].characterBinding).toEqual(scene.objects[0].characterBinding);
    expect(restored.objects[0].motionPath).toEqual(scene.objects[0].motionPath);
    expect(restored.shots.map((shot) => shot.id)).toEqual(scene.shots.map((shot) => shot.id));
});

