import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { APERTURE_IMAGES, CAMERA_BODY_IMAGES, CAMERA_CONTROL_BACKGROUND, LENS_IMAGES } from "../src/lib/canvas/camera-control-assets";
import { APERTURES, CAMERA_PROFILES, DEFAULT_CAMERA_CONTROL, LENS_PROFILES, buildCameraControlPrompt } from "../src/lib/canvas/camera-prompt-library";

describe("user supplied camera artwork", () => {
    test("each camera and lens image maps to its registered model", () => {
        expect(Object.keys(CAMERA_BODY_IMAGES)).toHaveLength(9);
        expect(Object.keys(LENS_IMAGES)).toHaveLength(10);
        for (const id of Object.keys(CAMERA_BODY_IMAGES)) expect(CAMERA_PROFILES.some(profile => profile.id === id)).toBe(true);
        for (const id of Object.keys(LENS_IMAGES)) expect(LENS_PROFILES.some(profile => profile.id === id)).toBe(true);
        for (const aperture of Object.keys(APERTURE_IMAGES)) expect(APERTURES.includes(Number(aperture) as typeof APERTURES[number])).toBe(true);
    });

    test("all 23 assets are local PNGs with real image dimensions", () => {
        const paths = [CAMERA_CONTROL_BACKGROUND, ...Object.values(CAMERA_BODY_IMAGES), ...Object.values(LENS_IMAGES), ...Object.values(APERTURE_IMAGES)];
        expect(new Set(paths).size).toBe(23);
        for (const path of paths) {
            expect(path.startsWith("/camera-controls/")).toBe(true);
            const file = join(import.meta.dir, "../public", path.slice(1));
            expect(existsSync(file)).toBe(true);
            const bytes = readFileSync(file);
            expect(bytes.subarray(1, 4).toString()).toBe("PNG");
            expect(bytes.readUInt32BE(16)).toBeGreaterThan(0);
            expect(bytes.readUInt32BE(20)).toBeGreaterThan(0);
        }
    });

    test("new HAR model choices compile their own names", () => {
        const prompt = buildCameraControlPrompt({ ...DEFAULT_CAMERA_CONTROL, enabled: true, camera: "arricam_lt", lens: "cooke_sf_1_8x" });
        expect(prompt).toContain("ARRICAM LT");
        expect(prompt).toContain("Cooke SF 1.8x");
        expect(prompt).toContain("oval");
    });
});
