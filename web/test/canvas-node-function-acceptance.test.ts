import { expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { CanvasNodeType } from "../src/types/canvas";

test("all registered node factories and feature producers produce host commands without discarded edits", () => {
    const directory = mkdtempSync(join(tmpdir(), "canvas-node-functions-"));
    try {
        const output = join(directory, "operations.json");
        const child = Bun.spawnSync([process.execPath, "scripts/audit-canvas-node-save.tsx", "test/fixtures/canvas-editor-capabilities.json", output], { cwd: resolve(import.meta.dir, ".."), timeout: 60000 });
        const report = JSON.parse(readFileSync(output, "utf8"));
        const failed = report.results.filter((item: any) => item.status === "error" || item.status === "producer-error");
        expect(failed.map((item: any) => ({ name: item.name, error: item.error }))).toEqual([]);
        expect(child.exitCode).toBe(0);
        for (const type of Object.values(CanvasNodeType)) {
            for (const action of ["create", "rename-move-resize-lock", "clipboard-paste", "delete"]) {
                expect(report.results.find((item: any) => item.name === `${type}:${action}`)?.status).toBe("converted");
            }
        }
        for (const name of [
            "storyboard:create-images",
            "storyboard:create-videos",
            "storyboard:action-board-draft",
            "storyboard:create-generate-videos-direct-draft",
            "storyboard:generate-videos-keyframe-draft",
            "previs:create-shot",
            "group:create-reference",
            "group:create-storyboard",
            "style:apply-preset",
            "batch:generate-draft",
            "media-conversion:edit-content-config",
        ]) {
            expect(report.results.find((item: any) => item.name === name)?.status).toBe("converted");
        }
        expect(report.observations).toEqual([{ name: "shot-number-display", expected: "镜头 1", contextLabel: "镜头 1", searchDescription: "镜头 1", wrongNumberMatches: 0 }]);
    } finally {
        rmSync(directory, { recursive: true, force: true });
    }
}, 65000);
