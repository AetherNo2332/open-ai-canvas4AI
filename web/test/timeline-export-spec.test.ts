import { describe, expect, test } from "bun:test";

import { resolveTimelineExportFps, TIMELINE_DEFAULT_EXPORT_FPS } from "../src/lib/timeline/timeline-export-spec";
import type { CanvasNodeMetadata } from "../src/types/canvas";
import type { TimelineProject } from "../src/types/timeline";

function timeline(...nodeIds: string[]): TimelineProject {
    return {
        version: 2,
        tracks: [],
        durationMs: nodeIds.length * 1000,
        clips: nodeIds.map((nodeId, index) => ({ id: `clip-${index}`, kind: "video", nodeId, trackId: "video", startMs: index * 1000, durationMs: 1000 })),
    };
}

const scenes = [
    { id: "other", shots: [{ id: "other-shot", fps: 24 }] },
    {
        id: "scene",
        shots: [
            { id: "first", fps: 24 },
            { id: "second", fps: 25 },
        ],
    },
] as const;
const nodes = [
    { id: "other-video", metadata: { previsSceneId: "other", previsShotId: "other-shot" } },
    { id: "first-video", metadata: { previsSceneId: "scene", previsShotId: "first" } },
    { id: "second-video", metadata: { previsSceneId: "scene", previsShotId: "second", previsClayVideoNodeId: "clay", previsDepthVideoNodeId: "depth" } },
];

describe("timeline export frame rate", () => {
    test("takes the actual shot associated with the clip", () => {
        expect(resolveTimelineExportFps(timeline("second-video"), nodes, scenes)).toBe(25);
        expect(resolveTimelineExportFps(timeline("clay"), nodes, scenes)).toBe(25);
        expect(resolveTimelineExportFps(timeline("depth"), nodes, scenes)).toBe(25);
    });

    test("keeps each recorded video's FPS when composition output rebinds the workstation to another shot", () => {
        const reboundNodes: { id: string; metadata: CanvasNodeMetadata }[] = [
            { id: "shot", metadata: { previsSceneId: "scene", previsShotId: "third", previsClayVideoNodeId: "clay", previsDepthVideoNodeId: "depth" } },
            { id: "clay", metadata: { previsCaptureFps: 24 } },
            { id: "depth", metadata: { previsCaptureFps: 25 } },
        ];
        const updatedScenes = [{ id: "scene", shots: [...scenes[1].shots, { id: "third", fps: 30 }] }];

        expect(resolveTimelineExportFps(timeline("clay"), reboundNodes, updatedScenes)).toBe(24);
        expect(resolveTimelineExportFps(timeline("depth"), reboundNodes, updatedScenes)).toBe(25);
        expect(resolveTimelineExportFps(timeline("shot"), reboundNodes, updatedScenes)).toBe(30);
        expect(resolveTimelineExportFps(timeline("clay"), reboundNodes.slice(1), [])).toBe(24);
    });

    test("mixed timelines follow the first linked clip in playback order", () => {
        expect(resolveTimelineExportFps(timeline("ordinary", "clay", "first-video"), nodes, scenes)).toBe(25);
        expect(resolveTimelineExportFps(timeline("first-video", "second-video"), nodes, scenes)).toBe(24);
    });

    test("leaves unlinked and unsupported clips at the ordinary default", () => {
        expect(resolveTimelineExportFps(timeline("ordinary"), nodes, scenes)).toBe(TIMELINE_DEFAULT_EXPORT_FPS);
        expect(resolveTimelineExportFps(timeline("second-video"), nodes, [])).toBe(TIMELINE_DEFAULT_EXPORT_FPS);
        expect(resolveTimelineExportFps(timeline("second-video"), nodes, [{ id: "scene", shots: [{ id: "second", fps: 60 }] }])).toBe(TIMELINE_DEFAULT_EXPORT_FPS);
    });
});
