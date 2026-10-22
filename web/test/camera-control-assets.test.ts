import { expect, test } from "bun:test";
import { readdirSync } from "node:fs";

import { CAMERA_PROFILES, LENS_PROFILES } from "@/lib/canvas/camera-prompt-library";

const publicRoot = new URL("../public/", import.meta.url);
const fileFor = (publicPath: string) => new URL(publicPath.replace(/^\//, ""), publicRoot);
const directoryFor = (category: string) => new URL(`camera-controls/${category}/`, publicRoot);

test("every camera and lens profile ships a unique product image", async () => {
    const profiles = [...CAMERA_PROFILES, ...LENS_PROFILES];
    expect(new Set(profiles.map((profile) => profile.id)).size).toBe(profiles.length);

    for (const profile of profiles) {
        expect(profile.image).toMatch(/^\/camera-controls\/(cameras|lenses)\/[a-z0-9-]+\.png$/);
        const file = Bun.file(fileFor(profile.image));
        expect(await file.exists()).toBe(true);
        expect((await file.stat()).size).toBeGreaterThan(0);
    }
});

test("camera control public images are all referenced by a profile", () => {
    for (const [category, profiles] of [
        ["cameras", CAMERA_PROFILES],
        ["lenses", LENS_PROFILES],
    ] as const) {
        const files = readdirSync(directoryFor(category)).filter((name) => name.endsWith(".png"));
        const expected = profiles.map((profile) => profile.image.split("/").pop() ?? "").sort();
        expect(files.sort()).toEqual(expected);
    }
});
