import { describe, expect, test } from "bun:test";

import {
    APERTURES,
    CAMERA_PROFILES,
    DEFAULT_CAMERA_CONTROL,
    FOCAL_LENGTHS,
    LENS_PROFILES,
    appendCameraControlPrompt,
    buildCameraControlPrompt,
    buildCameraPrompt,
} from "../src/lib/canvas/camera-prompt-library";

describe("camera prompt contract", () => {
    test("builds a deterministic prompt from registered camera parameters", () => {
        const prompt = buildCameraPrompt({
            cameraId: CAMERA_PROFILES[0].id,
            lensId: LENS_PROFILES[0].id,
            focalLengthMm: FOCAL_LENGTHS[0],
            apertureF: APERTURES[0],
        });

        expect(prompt).toContain(CAMERA_PROFILES[0].shortTag);
        expect(prompt).toContain(LENS_PROFILES[0].shortTag);
    });

    test("rejects unknown persisted parameters instead of silently changing generation semantics", () => {
        const valid = {
            cameraId: CAMERA_PROFILES[0].id,
            lensId: LENS_PROFILES[0].id,
            focalLengthMm: FOCAL_LENGTHS[0],
            apertureF: APERTURES[0],
        };

        expect(() => buildCameraPrompt({ ...valid, cameraId: "unknown-camera" })).toThrow("不支持的相机配置");
        expect(() => buildCameraPrompt({ ...valid, lensId: "unknown-lens" })).toThrow("不支持的镜头配置");
        expect(() => buildCameraPrompt({ ...valid, focalLengthMm: 47 })).toThrow("不支持的焦距");
        expect(() => buildCameraPrompt({ ...valid, apertureF: 3.2 })).toThrow("不支持的光圈");
    });

    test("turns Alexa 35 and Cooke selections into visible color and focus direction", () => {
        const prompt = buildCameraControlPrompt({ ...DEFAULT_CAMERA_CONTROL, enabled: true });

        expect(prompt).toContain("ARRI Alexa 35");
        expect(prompt).toContain("Cooke S7/i");
        expect(prompt).toContain("warm");
        expect(prompt).toContain("focus transition");
        expect(prompt).toContain("35mm");
        expect(prompt).toContain("f/2.8");
        expect(prompt).toContain("visual direction");
    });

    test("keeps Cooke vintage and anamorphic looks distinct without imposing an aspect ratio", () => {
        const vintage = buildCameraControlPrompt({ ...DEFAULT_CAMERA_CONTROL, enabled: true, lens: "cooke_panchro_i_classic" });
        const anamorphic = buildCameraControlPrompt({ ...DEFAULT_CAMERA_CONTROL, enabled: true, lens: "anamorphic_cooke" });

        expect(vintage).toContain("Cooke Panchro/i Classic");
        expect(vintage).toContain("vintage");
        expect(anamorphic).toContain("Cooke Anamorphic /i");
        expect(anamorphic).toContain("oval");
        expect(anamorphic).not.toContain("2.39:1");
        expect(anamorphic).not.toContain("do not add any physical camera");
    });

    test("disabled or missing controls preserve the user's exact prompt", () => {
        const original = "  A camera on a desk.\nKeep the handwritten note.\n";
        const disabled = { ...DEFAULT_CAMERA_CONTROL, camera: "unknown-camera", lens: "unknown-lens" };

        expect(buildCameraControlPrompt()).toBe("");
        expect(buildCameraControlPrompt(DEFAULT_CAMERA_CONTROL)).toBe("");
        expect(buildCameraControlPrompt(disabled)).toBe("");
        expect(appendCameraControlPrompt(original, undefined, "image")).toBe(original);
        expect(appendCameraControlPrompt(original, disabled, "video")).toBe(original);
    });

    test.each(["image", "video"])("appends direction to %s without mutating the selection or original prompt", (mode) => {
        const original = "A camera operator crosses the room.\n  Track left.";
        const options = Object.freeze({ enabled: true, camera: "arri_alexa_35", lens: "cooke_s7i", focalLength: 85, aperture: 1.4 });
        const prompt = appendCameraControlPrompt(original, options, mode);

        expect(prompt.startsWith(`${original}\n\n`)).toBe(true);
        expect(prompt).toContain("85mm");
        expect(prompt).toContain("f/1.4");
        expect(prompt).toContain("shallow depth of field");
        expect(options).toEqual({ enabled: true, camera: "arri_alexa_35", lens: "cooke_s7i", focalLength: 85, aperture: 1.4 });
    });

    test("does not inject camera direction into text or audio generation", () => {
        const options = { ...DEFAULT_CAMERA_CONTROL, enabled: true, camera: "unknown-camera" };

        expect(appendCameraControlPrompt("Keep this text", options, "text")).toBe("Keep this text");
        expect(appendCameraControlPrompt("Keep this sound", options, "audio")).toBe("Keep this sound");
    });

    test("enabled generation rejects invalid controls through the append entry point", () => {
        const options = { ...DEFAULT_CAMERA_CONTROL, enabled: true };

        expect(() => appendCameraControlPrompt("scene", { ...options, camera: "unknown-camera" }, "image")).toThrow("不支持的相机配置");
        expect(() => appendCameraControlPrompt("scene", { ...options, lens: "unknown-lens" }, "video")).toThrow("不支持的镜头配置");
        expect(() => appendCameraControlPrompt("scene", { ...options, focalLength: NaN }, "video")).toThrow("不支持的焦距");
        expect(() => appendCameraControlPrompt("scene", { ...options, aperture: 0 }, "image")).toThrow("不支持的光圈");
    });
});
