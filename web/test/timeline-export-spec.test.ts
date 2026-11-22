import { describe, expect, test } from "bun:test";

import { resolveTimelineExportFps, TIMELINE_DEFAULT_EXPORT_FPS } from "../src/lib/timeline/timeline-export-spec";

describe("timeline export frame rate", () => {
    test("keeps supported previs shot frame rates", () => {
        expect(resolveTimelineExportFps(24)).toBe(24);
        expect(resolveTimelineExportFps(25)).toBe(25);
        expect(resolveTimelineExportFps(30)).toBe(30);
    });

    test("uses the existing generic timeline default for missing or unsupported values", () => {
        expect(resolveTimelineExportFps()).toBe(TIMELINE_DEFAULT_EXPORT_FPS);
        expect(resolveTimelineExportFps(Number.NaN)).toBe(TIMELINE_DEFAULT_EXPORT_FPS);
        expect(resolveTimelineExportFps(60)).toBe(TIMELINE_DEFAULT_EXPORT_FPS);
    });
});
