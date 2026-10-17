import { describe, expect, test } from "bun:test";

import { directorDocumentToPrevisScene, directorPreviewInput, previsSceneToDirectorDocument } from "@/integrations/director-desk/previs-adapter";
import { createDirectorRenderBridge } from "@/integrations/director-desk/render-bridge";
import { buildImageToDirectorAgentPrompt } from "@/lib/canvas/image-to-director-agent";
import type { PrevisScene } from "@/types/previs";

describe("DirectorDesk previs end-to-end contract", () => {
    test("keeps scene edits, preview input and image agent order on existing contracts", async () => {
        const scene: PrevisScene = {
            id: "scene-1",
            title: "镜头一",
            version: 1,
            background: "#202020",
            environmentIntensity: 1,
            gridVisible: true,
            updatedAt: "2026-10-12T00:00:00.000Z",
            createdAt: "2026-10-12T00:00:00.000Z",
            activeShotId: "shot-1",
            objects: [{ id: "actor-1", kind: "actor", name: "演员", transform: { position: [0, 0, 0], rotation: [0, 0, 0], scale: [1, 1, 1] }, color: "#ffffff", visible: true, castShadow: true, receiveShadow: true, keyframes: [] }],
            cameras: [{ id: "camera-1", name: "主机位", transform: { position: [0, 1, 4], rotation: [0, 0, 0], scale: [1, 1, 1] }, target: [0, 1, 0], focalLength: 50, fov: 45, aperture: 2.8, focusDistance: 4, near: 0.1, far: 1000, keyframes: [] }],
            lights: [],
            shots: [{ id: "shot-1", name: "近景", cameraId: "camera-1", duration: 2, fps: 24, shotSize: "close_up", cameraMove: "static", prompt: "" }],
        };
        const document = previsSceneToDirectorDocument(scene);
        const roundTrip = directorDocumentToPrevisScene(document);
        expect(roundTrip.id).toBe(scene.id);
        expect(roundTrip.objects?.[0]?.id).toBe("actor-1");
        expect(directorPreviewInput(roundTrip, "shot-1").shotId).toBe("shot-1");

        let submissions = 0;
        const bridge = createDirectorRenderBridge({
            submit: async () => {
                submissions += 1;
                return { taskId: "task-1" };
            },
        });
        await Promise.all([bridge.preview(roundTrip, { shotId: "shot-1", time: 0 }), bridge.preview(roundTrip, { shotId: "shot-1", time: 0 })]);
        expect(submissions).toBe(1);
        expect(buildImageToDirectorAgentPrompt({ kind: "image", nodeId: "image-1", mentionToken: "@图片1" })).toContain("canvas_inspect_image");
    });
});
