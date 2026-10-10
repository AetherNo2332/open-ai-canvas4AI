import { describe, expect, test } from "bun:test";
import { createCameraWheelStepper } from "../src/lib/canvas/camera-control-wheel";

describe("camera parameter wheel steps", () => {
    test("one mouse notch advances one option and limits rapid repeated events", () => {
        const step = createCameraWheelStepper();
        expect(step({ deltaY: 120 }, 0)).toEqual({ consume: true, direction: 1 });
        expect(step({ deltaY: 120 }, 40)).toEqual({ consume: true, direction: 0 });
        expect(step({ deltaY: 120 }, 220)).toEqual({ consume: true, direction: 1 });
    });

    test("trackpad movement accumulates before changing an option", () => {
        const step = createCameraWheelStepper();
        expect(step({ deltaY: 4 }, 0).direction).toBe(0);
        expect(step({ deltaY: 8 }, 20).direction).toBe(0);
        expect(step({ deltaY: 12 }, 40).direction).toBe(1);
    });

    test("line and page wheel units are normalized", () => {
        expect(createCameraWheelStepper()({ deltaY: -3, deltaMode: 1 }, 0).direction).toBe(-1);
        expect(createCameraWheelStepper()({ deltaY: 1, deltaMode: 2 }, 0).direction).toBe(1);
    });

    test("changing direction responds immediately without old accumulated movement", () => {
        const step = createCameraWheelStepper();
        expect(step({ deltaY: 100 }, 0).direction).toBe(1);
        expect(step({ deltaY: -100 }, 30).direction).toBe(-1);
        expect(step({ deltaY: 8 }, 60).direction).toBe(0);
        expect(step({ deltaY: -16 }, 80).direction).toBe(0);
    });

    test("separate tiny gestures do not accumulate indefinitely", () => {
        const step = createCameraWheelStepper();
        expect(step({ deltaY: 16 }, 0).direction).toBe(0);
        expect(step({ deltaY: 16 }, 500).direction).toBe(0);
    });

    test("pinch zoom, horizontal movement, zero and invalid deltas are not consumed", () => {
        for (const event of [{ deltaY: 100, ctrlKey: true }, { deltaY: 20, deltaX: 100 }, { deltaY: 0 }, { deltaY: NaN }, { deltaY: Infinity }]) {
            expect(createCameraWheelStepper()(event, 0)).toEqual({ consume: false, direction: 0 });
        }
    });
});
