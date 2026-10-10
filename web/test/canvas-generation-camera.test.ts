import { describe, expect, test } from "bun:test";

import { buildCameraControlPrompt, type CameraControlOptions } from "../src/lib/canvas/camera-prompt-library";
import { DEFAULT_PORTRAIT_TEXTURE_SETTINGS } from "../src/lib/canvas/canvas-portrait-texture";
import { buildCanvasVisualGenerationPrompt, canvasCameraControlSnapshot } from "../src/pages/canvas/canvas-generation-camera";

const cameraControl: CameraControlOptions = {
    enabled: true,
    camera: "arri_alexa_mini_lf",
    lens: "cooke_s7i",
    focalLength: 50,
    aperture: 2.8,
};

describe("canvas camera generation wiring", () => {
    test.each(["image", "video"])("%s submission compiles one camera fragment while keeping the editable prompt separate", (mode) => {
        const composerContent = "人物走过雨中的街道 @图片1";
        const metadata = { composerContent, cameraControl };
        const effectivePrompt = buildCanvasVisualGenerationPrompt(composerContent, mode, metadata);
        const fragment = buildCameraControlPrompt(cameraControl);

        expect(effectivePrompt.startsWith(composerContent)).toBe(true);
        expect(effectivePrompt.split(fragment)).toHaveLength(2);
        expect(effectivePrompt).toContain("Cooke");
        expect(metadata.composerContent).toBe(composerContent);
    });

    test("disabled camera and nonvisual generation leave the prompt unchanged", () => {
        expect(buildCanvasVisualGenerationPrompt("原始提示词", "image", { cameraControl: { ...cameraControl, enabled: false } })).toBe("原始提示词");
        for (const mode of ["text", "audio"]) {
            expect(buildCanvasVisualGenerationPrompt("原始提示词", mode, { cameraControl })).toBe("原始提示词");
        }
        expect(() => buildCanvasVisualGenerationPrompt("原始提示词", "video", { cameraControl: { ...cameraControl, lens: "invalid-lens" } })).toThrow();
    });

    test("image portrait treatment and camera direction compose without changing video content", () => {
        const metadata = { cameraControl, portraitTexture: DEFAULT_PORTRAIT_TEXTURE_SETTINGS };
        const image = buildCanvasVisualGenerationPrompt("街头人像", "image", metadata);
        const video = buildCanvasVisualGenerationPrompt("街头人像", "video", metadata);
        expect(image).toContain(buildCameraControlPrompt(cameraControl));
        expect(image.length).toBeGreaterThan(video.length);
        expect(video).toBe(buildCanvasVisualGenerationPrompt("街头人像", "video", { cameraControl }));
    });

    test("output and task metadata keep an independent copy, including disabled settings", () => {
        const source = { cameraControl: { ...cameraControl, enabled: false } };
        const snapshot = canvasCameraControlSnapshot(source);
        const restored = JSON.parse(JSON.stringify(snapshot));
        source.cameraControl.focalLength = 85;
        expect(snapshot.cameraControl?.focalLength).toBe(50);
        expect(restored.cameraControl).toEqual(snapshot.cameraControl);
        expect(restored.cameraControl.enabled).toBe(false);
        expect(canvasCameraControlSnapshot()).toEqual({});
    });
});
