import { describe, expect, test } from "bun:test";
import { createPrevisScene } from "@/lib/canvas/previs/previs-scene";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";
import { previsSceneToDirectorDocument } from "@/integrations/director-desk/previs-adapter";
import { createDirectorDeskCanvasExporter, type DirectorDeskExportTransport, type PrevisExportTask } from "@/integrations/director-desk/export-to-canvas";

function projectFixture() {
    const scene = createPrevisScene("T7 导出");
    scene.id = "scene-1";
    scene.shots = [
        { ...scene.shots[0], id: "shot-a", name: "镜头一", duration: 4, prompt: "人物走入房间", cameraMove: "push_in" },
        { ...scene.shots[0], id: "shot-b", name: "镜头二", duration: 6, prompt: "人物停下回头", cameraMove: "static" },
    ];
    scene.activeShotId = "shot-a";
    return { scene, project: previsSceneToDirectorDocument(scene) };
}

function transportFixture(overrides: Partial<DirectorDeskExportTransport> = {}) {
    const calls = { submit: [] as unknown[], materialize: [] as string[], storyboard: [] as unknown[] };
    const tasks = new Map<string, PrevisExportTask>();
    const transport: DirectorDeskExportTransport = {
        submitPrevis: async (input) => {
            calls.submit.push(input);
            tasks.set("task-1", { id: "task-1", status: "queued" });
            return { taskId: "task-1" };
        },
        queryTask: async (taskId) => tasks.get(taskId) || { id: taskId, status: "queued" },
        readCanvas: async () => ({ nodes: [] }),
        waitForMaterialization: async (taskId) => {
            calls.materialize.push(taskId);
            return { state: "done", outcome: "applied" };
        },
        writeStoryboard: async (_canvasId, rows) => {
            calls.storyboard.push(rows);
        },
        ...overrides,
    };
    return { calls, tasks, transport };
}

describe("DirectorDesk 导出到画布", () => {
    test("01 预演请求只提交 canvas/scene/shot/kind，不携带路径或 Blob", async () => {
        const { project } = projectFixture();
        const { calls, transport } = transportFixture();
        const exporter = createDirectorDeskCanvasExporter(transport);
        const result = await exporter.requestPrevisRender({
            canvasId: "canvas-1",
            sceneId: "scene-1",
            shotId: "shot-a",
            kind: "whitebox-video",
            render: { project, sourceSceneId: "scene-1", shotId: "shot-a", time: 0, duration: 4, fps: 24 },
        });
        expect(result).toEqual({ taskId: "task-1" });
        expect(calls.submit).toEqual([{ canvasId: "canvas-1", sceneId: "scene-1", shotId: "shot-a", kind: "whitebox-video", options: { duration: 4, fps: 24 } }]);
        expect(JSON.stringify(calls.submit)).not.toMatch(/(?:blob:|[A-Z]:\\|\/tmp\/|filePath|directory|filename)/u);
    });

    test("02 未完成、失败或取消的任务不会写回节点", async () => {
        const { project } = projectFixture();
        for (const status of ["queued", "running", "failed", "cancelled"] as const) {
            const { calls, transport } = transportFixture({ queryTask: async (id) => ({ id, status }) });
            const exporter = createDirectorDeskCanvasExporter(transport);
            const result = await exporter.writeBackPrevisArtifacts({ canvasId: "canvas-1", sceneId: "scene-1", taskId: "task-1", project });
            expect(result).toEqual({ nodeIds: [], storyboardUpdated: false });
            expect(calls.materialize).toHaveLength(0);
        }
    });

    test("03 成功任务写回白膜与构图节点且同 taskId 幂等", async () => {
        const { project } = projectFixture();
        const { calls, transport } = transportFixture({
            queryTask: async (id) => ({ id, status: "succeeded", resultJson: JSON.stringify({ resourceId: "res-video", previewResourceId: "res-image", width: 640, height: 360, durationMs: 4000 }) }),
        });
        let nodes: CanvasNodeData[] = [];
        transport.readCanvas = async () => ({ nodes });
        transport.waitForMaterialization = async (taskId) => {
            calls.materialize.push(taskId);
            if (!nodes.length)
                nodes = [CanvasNodeType.Video, CanvasNodeType.Image].map((type) => ({
                    id: type === CanvasNodeType.Video ? `previs-video-${taskId}` : `previs-preview-${taskId}`,
                    type,
                    title: "后台预演产物",
                    position: { x: 0, y: 0 },
                    width: 320,
                    height: 180,
                    metadata: { taskId, previsSceneId: "scene-1", previsShotId: "shot-a", storageKey: `resource:${type}`, assetId: `asset-${type}`, status: "success" },
                }));
            return { state: "done", outcome: "applied" };
        };
        const exporter = createDirectorDeskCanvasExporter(transport);
        const input = { canvasId: "canvas-1", sceneId: "scene-1", taskId: "task-1", project };
        const first = await exporter.writeBackPrevisArtifacts(input);
        const second = await exporter.writeBackPrevisArtifacts(input);
        expect(first.nodeIds).toEqual(["previs-video-task-1", "previs-preview-task-1"]);
        expect(second).toEqual({ nodeIds: first.nodeIds, storyboardUpdated: false });
        expect(nodes).toHaveLength(2);
        expect(nodes.every((node) => Boolean(node.metadata?.assetId && node.metadata.storageKey))).toBe(true);
    });

    test("05 后台拒绝已变化的源场景时前端不补建产物", async () => {
        const { project } = projectFixture();
        const { transport } = transportFixture({
            queryTask: async (id) => ({ id, status: "succeeded" }),
            waitForMaterialization: async () => ({ state: "done", outcome: "source_changed" }),
        });
        const exporter = createDirectorDeskCanvasExporter(transport);
        await expect(exporter.writeBackPrevisArtifacts({ canvasId: "canvas-1", sceneId: "scene-1", taskId: "task-1", project })).rejects.toThrow("source_changed");
    });

    test("06 磁盘路径、Blob 和渲染 scope 不一致在提交前拒绝", async () => {
        const { project } = projectFixture();
        const { calls, transport } = transportFixture();
        const exporter = createDirectorDeskCanvasExporter(transport);
        const input = { canvasId: "canvas-1", sceneId: "scene-1", shotId: "shot-a", kind: "composition-frame" as const, render: { project, sourceSceneId: "scene-1", shotId: "shot-a", time: 0 } };
        await expect(exporter.requestPrevisRender({ ...input, render: { ...input.render, project: { ...project, filename: "C:\\export.png" } } as any })).rejects.toThrow("forbidden");
        await expect(exporter.requestPrevisRender({ ...input, render: { ...input.render, project: new Blob() } as any })).rejects.toThrow("forbidden");
        await expect(exporter.requestPrevisRender({ ...input, render: { ...input.render, shotId: "shot-b" } })).rejects.toThrow("scope");
        expect(calls.submit).toHaveLength(0);
    });

    test("04 镜头表导出映射为 canvas_edit_storyboard 兼容行", async () => {
        const { project } = projectFixture();
        const { calls, transport } = transportFixture();
        const exporter = createDirectorDeskCanvasExporter(transport);
        const result = await exporter.exportShotTable(project, "canvas-1");
        expect(result).toEqual({ rows: 2 });
        const rows = calls.storyboard[0] as Array<Record<string, unknown>>;
        expect(rows).toHaveLength(2);
        expect(rows[0]).toMatchObject({ id: "shot-shot-a", shotNumber: 1, durationSeconds: 4, videoMotionPrompt: "人物走入房间", motion: "push_in", previsSceneId: "scene-1" });
        expect(rows.every((row) => typeof row.videoMotionPrompt === "string" && Number(row.durationSeconds) > 0)).toBe(true);
    });
});
