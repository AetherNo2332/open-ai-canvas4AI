import { describe, expect, test } from "bun:test";

import { exportSubmapProject } from "@/lib/canvas/canvas-submap-export";
import type { CanvasProject } from "@/stores/canvas/use-canvas-store";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

function node(id: string, type: CanvasNodeType, metadata: CanvasNodeData["metadata"] = {}, scopeId?: string): CanvasNodeData {
    return { id, type, title: id, position: { x: 0, y: 0 }, width: 320, height: 180, scopeId, metadata };
}

function project(): CanvasProject {
    return {
        id: "canvas-1",
        projectId: "project-1",
        title: "画布",
        createdAt: "2026-11-15",
        updatedAt: "2026-11-15",
        backgroundMode: "lines",
        showImageInfo: false,
        viewport: { x: 0, y: 0, k: 1 },
        previsScenes: [{ id: "scene-1" }] as unknown as CanvasProject["previsScenes"],
        chatSessions: [{ id: "chat-1" }] as unknown as CanvasProject["chatSessions"],
        activeChatId: "chat-1",
        timeline: { clips: [] } as unknown as CanvasProject["timeline"],
        idCounters: { "submap-1__image": 1, "submap-1__text": 1, image: 2 },
        nodes: [
            { ...node("submap-1", CanvasNodeType.Submap), title: "镜头工作台" },
            node("submap-1__image-1", CanvasNodeType.Image, { content: "/api/resources/old/file", storageKey: "image:old", mediaVersions: [{ id: "v1" }] as never, composerContent: "油画风格" }, "submap-1"),
            node("submap-1__text-1", CanvasNodeType.Text, { content: "台词", referenceNodeIds: ["submap-1__image-1", "image-external"] }, "submap-1"),
            node("image-external", CanvasNodeType.Image, { content: "/api/resources/outside/file" }),
        ],
        connections: [
            { id: "conn-internal", fromNodeId: "submap-1__text-1", toNodeId: "submap-1__image-1" },
            { id: "conn-in", fromNodeId: "image-external", toNodeId: "submap-1__image-1" },
            { id: "conn-out", fromNodeId: "submap-1__image-1", toNodeId: "image-external" },
            { id: "conn-outside", fromNodeId: "image-external", toNodeId: "image-external-2" },
        ],
    };
}

describe("exportSubmapProject", () => {
    test("只保留 submap 子树，清媒体结果并丢弃外部边与引用", () => {
        const exported = exportSubmapProject(project(), "submap-1");
        expect(exported.project.nodes.map((item) => item.id)).toEqual(["submap-1", "submap-1__image-1", "submap-1__text-1"]);
        expect(exported.project).not.toHaveProperty("connections");
        expect(exported.droppedConnections).toBe(2);
        expect(exported.droppedReferences).toBe(1);
        expect(exported.project.nodes.find((item) => item.id === "submap-1__text-1")?.metadata?.referenceNodeIds).toEqual(["submap-1__image-1"]);
        const image = exported.project.nodes.find((item) => item.id === "submap-1__image-1");
        expect(image?.metadata?.content).toBeUndefined();
        expect(image?.metadata?.storageKey).toBeUndefined();
        expect(image?.metadata?.mediaVersions).toBeUndefined();
        expect(image?.metadata?.composerContent).toBe("油画风格");
        expect(image?.metadata?.copiedFromNodeId).toBeUndefined();
        expect(image?.metadata?.generationResultPlacement).toBeUndefined();
    });

    test("返回的项目只带工作流内容", () => {
        const exported = exportSubmapProject(project(), "submap-1");
        expect(exported.project.title).toBe("镜头工作台");
        expect(exported.project.projectId).toBeUndefined();
        expect(exported.project.chatSessions).toEqual([]);
        expect(exported.project.activeChatId).toBeNull();
        expect(exported.project.previsScenes).toEqual([]);
        expect(exported.project.timeline).toBeUndefined();
        expect(exported.project.idCounters).toEqual({ "submap-1__image": 1, "submap-1__text": 1, image: 2 });
    });

    test("找不到子画布时抛错", () => {
        expect(() => exportSubmapProject(project(), "missing")).toThrow("找不到要导出的子画布");
        expect(() => exportSubmapProject(project(), "image-external")).toThrow("找不到要导出的子画布");
    });
});
