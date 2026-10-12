import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { latestEditedCanvasId } from "../src/lib/canvas/canvas-navigation";

test("selects the most recently edited canvas deterministically", () => {
    expect(
        latestEditedCanvasId([
            { id: "older", updatedAt: "2026-10-05T10:00:00.000Z" },
            { id: "newer", updatedAt: "2026-10-06T10:00:00.000Z" },
        ]),
    ).toBe("newer");
});

test("returns no destination when the user has no canvases", () => {
    expect(latestEditedCanvasId([])).toBeNull();
});

test("homepage wires the latest canvas id into the canvas route", () => {
    const source = readFileSync(new URL("../src/pages/create/index.tsx", import.meta.url), "utf8");
    expect(source).toContain("latestEditedCanvasId(canvasProjects)");
    expect(source).toContain("`/canvas/${encodeURIComponent(lastEditedCanvasId)}`");
    expect(source).toContain("从上一次离开的地方继续");
});
