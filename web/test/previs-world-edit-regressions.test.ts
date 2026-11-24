import { describe, expect, test } from "bun:test";

import { resolvePrevisObjectTransformEdit } from "@/lib/canvas/previs/previs-animation-semantics";
import { evaluatePrevisActorPose } from "@/lib/canvas/previs/previs-motion";
import { applyPrevisMotionPath, resolvePrevisMotionPathEdit } from "@/lib/canvas/previs/previs-motion-path";
import { createPrevisActor, createPrevisObject, interpolatePrevisTransform } from "@/lib/canvas/previs/previs-scene";
import { applyPrevisActorTravel } from "@/lib/canvas/previs/previs-travel";
import { PREVIS_DEFAULT_WORLD, resolvePrevisWorldLighting } from "@/lib/canvas/previs/previs-world";
import type { PrevisMotionPath, PrevisTravelStyle } from "@/types/previs";

const path: PrevisMotionPath = {
    points: [
        [0, 0, 0],
        [4, 0, 0],
    ],
    speed: "walk",
    metersPerSecond: 1,
    startDelay: 0,
    orientToPath: true,
};

describe("world edits preserve established paths", () => {
    test("moving the whole actor animation survives a later speed edit without mutating its original path", () => {
        const actor = applyPrevisMotionPath(createPrevisActor("actor"), path, 12, true);
        const rendered = interpolatePrevisTransform(actor.transform, actor.keyframes, 2);
        const edit = resolvePrevisObjectTransformEdit({
            base: actor.transform,
            keyframes: actor.keyframes,
            motionPath: actor.motionPath,
            locked: true,
            rendered,
            edited: { ...rendered, position: [12, 0, -3] },
            autoKey: false,
            time: 2,
        });
        const moved = { ...actor, ...edit };
        const retimed = applyPrevisMotionPath(moved, { ...moved.motionPath!, metersPerSecond: 2 }, 12, true);

        expect(moved.motionPath?.points).toEqual([
            [10, 0, -3],
            [14, 0, -3],
        ]);
        expect(interpolatePrevisTransform(retimed.transform, retimed.keyframes, 0).position).toEqual([10, 0, -3]);
        expect(interpolatePrevisTransform(retimed.transform, retimed.keyframes, 2).position).toEqual([14, 0, -3]);
        expect(actor.motionPath?.points).toEqual([
            [0, 0, 0],
            [4, 0, 0],
        ]);
    });

    test("raising an actor moves its supporting surface once and preserves path lift", () => {
        const actor = applyPrevisMotionPath(
            createPrevisActor("actor", [0, 0.8, 0]),
            {
                ...path,
                points: [
                    [0, 2, 0],
                    [4, 2, 0],
                ],
            },
            12,
            true,
        );
        const rendered = interpolatePrevisTransform(actor.transform, actor.keyframes, 2);
        const edit = resolvePrevisObjectTransformEdit({
            base: actor.transform,
            keyframes: actor.keyframes,
            motionPath: actor.motionPath,
            locked: true,
            rendered,
            edited: { ...rendered, position: [2, 1.8, 0] },
            autoKey: false,
            time: 2,
        });
        const moved = { ...actor, ...edit };
        const retimed = applyPrevisMotionPath(moved, { ...moved.motionPath!, easing: "smooth" }, 12, true);

        expect(moved.motionPath?.points).toEqual([
            [0, 2, 0],
            [4, 2, 0],
        ]);
        expect(interpolatePrevisTransform(retimed.transform, retimed.keyframes, 0).position[1]).toBeCloseTo(1.8, 6);
        expect(evaluatePrevisActorPose(retimed, 1).worldLift).toBe(2);
    });

    test("a prop path keeps its translated absolute height after changing a hold", () => {
        const prop = applyPrevisMotionPath(
            createPrevisObject("box"),
            {
                ...path,
                points: [
                    [0, 2, 0],
                    [4, 2, 0],
                ],
                orientToPath: false,
            },
            12,
            false,
        );
        const rendered = interpolatePrevisTransform(prop.transform, prop.keyframes, 2);
        const edit = resolvePrevisObjectTransformEdit({
            base: prop.transform,
            keyframes: prop.keyframes,
            motionPath: prop.motionPath,
            locked: false,
            rendered,
            edited: { ...rendered, position: [5, 5, 1] },
            autoKey: false,
            time: 2,
        });
        const moved = { ...prop, ...edit };
        const retimed = applyPrevisMotionPath(moved, { ...moved.motionPath!, holds: [1, 0] }, 12, false);

        expect(interpolatePrevisTransform(retimed.transform, retimed.keyframes, 0).position).toEqual([3, 5, 1]);
        expect(interpolatePrevisTransform(retimed.transform, retimed.keyframes, 5).position).toEqual([7, 5, 1]);
    });

    test("AutoKey keeps the edited animation as manual keys instead of retaining a stale path generator", () => {
        const actor = applyPrevisMotionPath(createPrevisActor("actor"), path, 12, true);
        const rendered = interpolatePrevisTransform(actor.transform, actor.keyframes, 2);
        const edit = resolvePrevisObjectTransformEdit({
            base: actor.transform,
            keyframes: actor.keyframes,
            motionPath: actor.motionPath,
            locked: true,
            rendered,
            edited: { ...rendered, position: [2, 0, 3] },
            autoKey: true,
            time: 2,
        });
        const moved = { ...actor, ...edit };

        expect(moved.motionPath).toBeUndefined();
        expect(interpolatePrevisTransform(moved.transform, moved.keyframes, 2).position).toEqual([2, 0, 3]);
        expect(moved.keyframes[0]).toEqual(actor.keyframes[0]);
        expect(moved.keyframes.at(-1)).toEqual(actor.keyframes.at(-1));
    });
});

describe("actor paths replace prior travel", () => {
    test.each(["fly", "sword_fly", "float"] satisfies PrevisTravelStyle[])("a grounded path replaces %s lift and posture", (style) => {
        const original = applyPrevisActorTravel(createPrevisActor("actor"), { to: [4, 0, 0], start: 0, duration: 4, style });
        const before = structuredClone(original);
        expect(evaluatePrevisActorPose(original, 2).worldLift).toBeGreaterThan(0);

        const walking = applyPrevisMotionPath(original, path, 12, true);

        expect(walking.travels?.length ?? 0).toBe(0);
        expect(evaluatePrevisActorPose(walking, 2).worldLift).toBe(0);
        expect(evaluatePrevisActorPose(walking, 2).locomotion).toBe("walk");
        expect(original).toEqual(before);
    });

    test("a new elevated path replaces travel lift with the requested path lift", () => {
        const original = applyPrevisActorTravel(createPrevisActor("actor"), { to: [4, 0, 0], start: 0, duration: 4, style: "fly", height: 8 });
        const floating = applyPrevisMotionPath(
            original,
            {
                ...path,
                points: [
                    [0, 1, 0],
                    [4, 1, 0],
                ],
            },
            12,
            true,
        );

        expect(evaluatePrevisActorPose(floating, 2).worldLift).toBe(1);
        expect(evaluatePrevisActorPose(floating, 2).locomotion).toBe("still");
    });

    test("the inspector patch clears travel when merged and preserves other current object fields", () => {
        const original = applyPrevisActorTravel(createPrevisActor("actor"), { to: [4, 0, 0], start: 0, duration: 4, style: "sword_fly" });
        const patch = resolvePrevisMotionPathEdit(original, path, 12, true);
        const current = { ...original, name: "renamed actor", color: "#123456" };
        const updated = { ...current, ...patch };

        expect(updated.travels).toBeUndefined();
        expect(updated.name).toBe("renamed actor");
        expect(updated.color).toBe("#123456");
        expect(evaluatePrevisActorPose(updated, 2).worldLift).toBe(0);
    });
});

describe("panorama backgrounds coexist with world elements", () => {
    test("a usable panorama suppresses only the physical sky and retains world lighting and fog", () => {
        const world = { ...PREVIS_DEFAULT_WORLD };
        const original = resolvePrevisWorldLighting(world);
        const panorama = resolvePrevisWorldLighting(world, { mode: "panorama", url: "/panorama.png" });

        expect(original.physicalSky).toBe(true);
        expect(panorama.physicalSky).toBe(false);
        expect(panorama.sunDirection).toEqual(original.sunDirection);
        expect(panorama.sunIntensity).toBe(original.sunIntensity);
        expect(panorama.hemisphereIntensity).toBe(original.hemisphereIntensity);
        expect(panorama.fogDensity).toBe(original.fogDensity);
        expect(world).toEqual(PREVIS_DEFAULT_WORLD);
    });

    test("clearing a panorama or an empty panorama keeps the normal world sky", () => {
        expect(resolvePrevisWorldLighting(PREVIS_DEFAULT_WORLD, { mode: "color" }).physicalSky).toBe(true);
        expect(resolvePrevisWorldLighting(PREVIS_DEFAULT_WORLD, { mode: "panorama", url: "" }).physicalSky).toBe(true);
    });
});
