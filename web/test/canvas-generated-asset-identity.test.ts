import { describe, expect, test } from "bun:test";
import { findCanvasNodeAsset } from "@/lib/canvas/canvas-node-asset";
import { registeredGenerationOutput } from "@/lib/canvas/registered-generation-output";
import type { GenerationTask } from "@/services/api/task-center";
import type { ImageAsset } from "@/stores/use-asset-store";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

const asset: ImageAsset = {
    id: "generation-server-id",
    kind: "image",
    title: "用户修改的标题",
    coverUrl: "",
    tags: [],
    createdAt: "",
    updatedAt: "",
    metadata: { source: "generation-task", taskId: "task-1", outputIndex: 1 },
    data: { dataUrl: "", storageKey: "resource:output-1", width: 1254, height: 1254, bytes: 12, mimeType: "image/png" },
};
const node: CanvasNodeData = { id: "node-1", type: CanvasNodeType.Image, title: "图片", position: { x: 0, y: 0 }, width: 320, height: 320, metadata: { taskId: "task-1", storageKey: "resource:output-1" } };

describe("canvas generated asset identity", () => {
    const task = {
        id: "task-1",
        status: "succeeded",
        resultJson: JSON.stringify({
            images: [
                { storageKey: "resource:output-0", assetId: "server-0" },
                { storageKey: "resource:output-1", assetId: "server-1" },
            ],
        }),
    } as GenerationTask;
    test("uses the server identity for the correct output even with an empty local cache", () => {
        expect(registeredGenerationOutput(task, undefined, "resource:output-1")).toEqual({ assetId: "server-1", outputIndex: 1 });
        expect(registeredGenerationOutput(task, 0).assetId).toBe("server-0");
    });
    test("missing registration or mismatched output fails instead of creating another ID", () => {
        expect(() => registeredGenerationOutput(task, 0, "resource:output-1")).toThrow("不匹配");
        expect(() => registeredGenerationOutput({ ...task, resultJson: JSON.stringify({ images: [{ storageKey: "resource:output-0" }] }) }, 0)).toThrow("尚未登记");
    });
    test("reuses the server task output even without nodeId", () => {
        expect(findCanvasNodeAsset([asset], node, "canvas-1", "task-1")?.id).toBe(asset.id);
    });
    test("different outputs of one task remain distinct", () => {
        expect(findCanvasNodeAsset([asset], { ...node, metadata: { ...node.metadata, storageKey: "resource:output-0" } }, "canvas-1", "task-1")).toBeUndefined();
    });
    test("regenerating the same node cannot reuse the previous resource", () => {
        expect(findCanvasNodeAsset([{ ...asset, metadata: { ...asset.metadata, nodeId: node.id, canvasId: "canvas-1" } }], { ...node, metadata: { ...node.metadata, storageKey: "resource:changed" } }, "canvas-1", "task-1")).toBeUndefined();
    });
});
