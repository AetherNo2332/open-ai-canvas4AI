import { describe, expect, test } from "bun:test";

import {
    CAMERA_PROFILES,
    LEGACY_CAMERA_ID_ALIASES,
    LEGACY_LENS_ID_ALIASES,
    LENS_PROFILES,
    cameraProfileIndex,
    lensProfileIndex,
    resolveCameraId,
    resolveLensId,
} from "../src/lib/canvas/camera-prompt-library";
import { canvasGenerationInputPrompt } from "../src/lib/canvas/canvas-generation-input-prompt";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";

const cameraTable: Record<string, string> = {
    panavision_dxl2: "panavision_dxl2",
    arri_alexa_mini_lf: "arri_alexa_65",
    red_komodo_6k: "red_v_raptor",
    red_v_raptor_8k: "red_v_raptor",
    sony_venice_2: "sony_venice",
    sony_fx6: "sony_venice",
    blackmagic_ursa_12k: "arri_alexa_35",
    canon_c500_mk2: "arri_alexa_35",
};

const lensTable: Record<string, string> = {
    arri_signature_prime: "arri_signature_prime",
    cooke_s7i: "cooke_s4",
    zeiss_supreme_prime: "zeiss_ultra_prime",
    canon_sumire_prime: "canon_k35",
    anamorphic_cooke: "cooke_sf_18x",
    anamorphic_atlas: "hawk_class_x",
    vintage_leica_r: "cooke_speed_panchro",
    macro_100mm: "zeiss_ultra_prime",
};

describe("upstream camera/lens id aliases", () => {
    test("every upstream camera id resolves to an existing fork camera", () => {
        expect(resolveCameraId("arri_alexa_mini_lf")).toBe("arri_alexa_65");
        for (const [legacy, target] of Object.entries(cameraTable)) {
            expect(resolveCameraId(legacy)).toBe(target);
            expect(CAMERA_PROFILES.some((item) => item.id === target)).toBe(true);
        }
        for (const target of Object.values(LEGACY_CAMERA_ID_ALIASES)) expect(CAMERA_PROFILES.some((item) => item.id === target)).toBe(true);
    });

    test("every upstream lens id resolves to an existing fork lens", () => {
        for (const [legacy, target] of Object.entries(lensTable)) {
            expect(resolveLensId(legacy)).toBe(target);
            expect(LENS_PROFILES.some((item) => item.id === target)).toBe(true);
        }
        for (const target of Object.values(LEGACY_LENS_ID_ALIASES)) expect(LENS_PROFILES.some((item) => item.id === target)).toBe(true);
    });

    test("unknown and current ids pass through unchanged", () => {
        expect(resolveCameraId("unknown_x")).toBe("unknown_x");
        expect(resolveLensId("unknown_x")).toBe("unknown_x");
        expect(resolveCameraId("arri_alexa_35")).toBe("arri_alexa_35");
        expect(resolveCameraId(undefined)).toBeUndefined();
        expect(resolveLensId(undefined)).toBeUndefined();
    });

    test("generation prompt maps legacy ids instead of throwing", () => {
        const node: CanvasNodeData = {
            id: "n",
            type: CanvasNodeType.Image,
            title: "n",
            position: { x: 0, y: 0 },
            width: 200,
            height: 200,
            metadata: { cameraControl: { enabled: true, camera: "sony_fx6", lens: "cooke_s7i", focalLength: 50, aperture: 2.8 } },
        };
        const venice = CAMERA_PROFILES.find((item) => item.id === "sony_venice")!;
        const cooke = LENS_PROFILES.find((item) => item.id === "cooke_s4")!;
        const prompt = canvasGenerationInputPrompt(node, "image", "base");
        expect(prompt).toContain(venice.shortTag);
        expect(prompt).toContain(cooke.shortTag);
    });

    test("dialog unsupported predicate accepts legacy ids and rejects unknown ids", () => {
        expect(CAMERA_PROFILES[cameraProfileIndex("sony_fx6")]?.id).toBe("sony_venice");
        expect(LENS_PROFILES[lensProfileIndex("anamorphic_atlas")]?.id).toBe("hawk_class_x");
        expect(cameraProfileIndex("unknown_x")).toBe(-1);
        expect(lensProfileIndex("unknown_x")).toBe(-1);
    });
});
