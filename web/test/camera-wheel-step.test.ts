import { expect, test } from "bun:test";

import { CAMERA_WHEEL_STEP_THRESHOLD, normalizeCameraWheelDelta, takeCameraWheelStep } from "@/lib/canvas/camera-wheel";

test("wheel delta normalizes line and page modes to pixel-like units", () => {
    expect(normalizeCameraWheelDelta(100)).toBe(100);
    expect(normalizeCameraWheelDelta(-100)).toBe(-100);
    expect(normalizeCameraWheelDelta(3, 1)).toBe(120);
    expect(normalizeCameraWheelDelta(1, 2)).toBe(100);
    expect(normalizeCameraWheelDelta(0)).toBe(0);
    expect(normalizeCameraWheelDelta(Number.NaN)).toBe(0);
});

test("one wheel notch steps once while trackpad deltas accumulate to the threshold", () => {
    expect(takeCameraWheelStep(0, 100)).toEqual({ step: 1, accumulator: 0 });
    expect(takeCameraWheelStep(0, -100)).toEqual({ step: -1, accumulator: 0 });

    let accumulator = 0;
    const steps: number[] = [];
    for (const delta of [20, 20, 20]) {
        const result = takeCameraWheelStep(accumulator, delta);
        accumulator = result.accumulator;
        if (result.step) steps.push(result.step);
    }
    expect(steps).toEqual([1]);
    expect(accumulator).toBe(0);
});

test("sub-threshold wheel deltas keep accumulating without switching", () => {
    const first = takeCameraWheelStep(0, 20);
    expect(first).toEqual({ step: 0, accumulator: 20 });
    const second = takeCameraWheelStep(first.accumulator, -10);
    expect(second).toEqual({ step: 0, accumulator: 10 });
    expect(takeCameraWheelStep(0, 0)).toEqual({ step: 0, accumulator: 0 });
});

test("fast wheel events never skip more than one option per event", () => {
    expect(takeCameraWheelStep(0, 900).step).toBe(1);
    expect(takeCameraWheelStep(0, -900).step).toBe(-1);
    expect(CAMERA_WHEEL_STEP_THRESHOLD).toBeGreaterThan(0);
});

test("camera panel binds a non-passive wheel listener to every parameter column", async () => {
    const source = await Bun.file(new URL("../src/components/canvas/canvas-node-camera-dialog.tsx", import.meta.url)).text();

    expect(source).toContain("takeCameraWheelStep");
    expect(source).toContain('addEventListener("wheel"');
    expect(source).toContain("passive: false");
    expect(source.match(/data-camera-column=/g)).toHaveLength(1);
    expect(source.match(/onWheelStep=\{\(direction\) =>/g)).toHaveLength(4);
});
