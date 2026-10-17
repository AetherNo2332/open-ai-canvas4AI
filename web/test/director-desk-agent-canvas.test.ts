import { describe, expect, test } from "bun:test";
import type { DirectorProject } from "@/integrations/director-desk/previs-runtime-types";
import { createAgentCanvasBridge, type AgentCanvasBridgeApproval, type PrevisPatch } from "@/integrations/director-desk/agent-canvas-bridge";

const project = {
    format: "director-desk",
    version: 1,
    name: "场景",
    duration: 5,
    fps: 24,
    aspect: "16:9",
    room: { enabled: false, width: 10, depth: 10, height: 3 },
    entities: [
        {
            id: "actor-1",
            kind: "actor",
            name: "演员",
            asset: "person",
            color: "#a7bdd7",
            position: [0, 0, 0],
            rotation: [0, 0, 0],
            scale: [1, 1, 1],
            visible: true,
            locked: false,
            height: 1.75,
            build: "normal",
            gender: "male",
            path: null,
            face: "path",
            faceTarget: "",
            clips: [],
            pose: {},
            poseKeys: [],
            camera: null,
            count: 1,
            spacing: 0.75,
            seed: 1,
            reference: "",
        },
    ],
    cuts: [{ time: 0, cameraId: "camera-1" }],
    references: [],
    extensions: {
        "infinite-canvas": {
            sourceSceneId: "scene-1",
            scene: { id: "scene-1", title: "场景", background: "#d8dde3", environmentIntensity: 0.7, environment: { mode: "color" }, gridVisible: true, activeShotId: "shot-1", createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z" },
            objects: [],
            cameras: [],
            lights: [],
            shots: [{ id: "shot-1", name: "镜头 1", duration: 5, fps: 24 }],
        },
    },
} as unknown as DirectorProject;

const patch: PrevisPatch = { document: { ...project, name: "Agent 场景" } };

function harness(overrides: Partial<Parameters<typeof createAgentCanvasBridge>[0]> = {}) {
    const messages: Array<{ type: string; payload: unknown }> = [];
    const approvals: AgentCanvasBridgeApproval[] = [];
    const bridge = createAgentCanvasBridge({
        canvasId: "canvas-1",
        readDocument: () => project,
        readRevision: () => "rev-1",
        readViewState: () => ({ selectedEntityId: "actor-1", activeShotId: "shot-1", time: 2 }),
        bridge: { notify: (type, payload) => messages.push({ type, payload }) },
        approve: async (approval) => {
            approvals.push(approval);
            return true;
        },
        ...overrides,
    });
    return { bridge, messages, approvals };
}

describe("DirectorDesk Agent ↔ 画布桥", () => {
    test("Agent 补丁经 host.scene.load 下发并保留选择/视图", async () => {
        const { bridge, messages, approvals } = harness();
        const result = await bridge.applyAgentScenePatch({ sceneId: "scene-1", patch, revision: "rev-1" });
        expect(result).toEqual({ appliedRevision: "rev-2", selectionPreserved: true });
        expect(approvals).toHaveLength(1);
        expect(messages).toHaveLength(1);
        expect(messages[0].type).toBe("host.scene.load");
        expect(messages[0].payload).toMatchObject({ canvasId: "canvas-1", preserveViewState: true, viewState: { selectedEntityId: "actor-1", activeShotId: "shot-1", time: 2 } });
    });

    test("未审批写操作零 bridge 消息、零场景变更", async () => {
        const { bridge, messages } = harness({ approve: async () => false });
        await expect(bridge.applyAgentScenePatch({ sceneId: "scene-1", patch, revision: "rev-1" })).rejects.toThrow("approval_required");
        expect(messages).toEqual([]);
    });

    test("没有审批实现时写操作也必须拒绝", async () => {
        const { bridge, messages } = harness({ approve: undefined });
        await expect(bridge.applyAgentScenePatch({ sceneId: "scene-1", patch, revision: "rev-1" })).rejects.toThrow("approval_required");
        expect(messages).toEqual([]);
    });

    test("过期 revision 拒绝且不静默覆盖", async () => {
        const { bridge, messages } = harness({ readRevision: () => "rev-2" });
        await expect(bridge.applyAgentScenePatch({ sceneId: "scene-1", patch, revision: "rev-1" })).rejects.toThrow("revision_conflict");
        expect(messages).toEqual([]);
    });

    test("用户编辑回传带 scene revision", async () => {
        const { bridge, messages } = harness();
        await expect(bridge.publishLocalEdit({ sceneId: "scene-1", project })).resolves.toEqual({ revision: "rev-1" });
        expect(messages).toEqual([{ type: "desk.scene.changed", payload: { canvasId: "canvas-1", sceneId: "scene-1", revision: "rev-1", document: project } }]);
    });

    test("不支持图片输入时明确失败且不创建半成品场景", async () => {
        const createRun = async () => {
            throw new Error("must not create run");
        };
        const { bridge, messages } = harness({ imageReference: { kind: "image", nodeId: "image-1" }, imageModel: "text-only", supportsImage: false, createRun });
        await expect(bridge.startImageToDirectorFlow({ canvasId: "canvas-1", nodeId: "image-1" })).rejects.toThrow("director_image_model_unsupported");
        expect(messages).toEqual([]);
    });
});
