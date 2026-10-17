import type { AgentPermissionMode, CreateAgentRunInput } from "@/services/api/agent";
import type { AiConfig } from "@/stores/use-config-store";
import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { buildImageToDirectorAgentPrompt, DIRECTOR_IMAGE_TOOL_ORDER, modelSupportsImageInspection, selectImageInspectionModel } from "@/lib/canvas/image-to-director-agent";
import type { DirectorProject } from "./previs-runtime-types";
import type { RuntimeViewState } from "./scene-sync";

export type PrevisPatch = {
    document?: DirectorProject;
    project?: DirectorProject;
    changes?: Partial<DirectorProject>;
    [key: string]: unknown;
};

export type AgentCanvasBridgeApproval = {
    kind: "previs_apply_patch";
    canvasId: string;
    sceneId: string;
    revision: string;
    patch: PrevisPatch;
};

export type AgentCanvasBridgeOptions = {
    canvasId: string;
    readDocument: () => DirectorProject;
    readRevision: () => string;
    readViewState?: () => RuntimeViewState | undefined;
    bridge: { notify: (type: string, payload?: unknown) => void };
    approve?: (approval: AgentCanvasBridgeApproval) => Promise<boolean> | boolean;
    imageReference?: CanvasResourceReference;
    imageConfig?: AiConfig;
    imageModel?: string;
    supportsImage?: boolean;
    createRun?: (input: CreateAgentRunInput) => Promise<{ run?: { id: string }; id?: string }>;
    permissionMode?: AgentPermissionMode;
};

export type AgentCanvasBridge = {
    applyAgentScenePatch(input: { sceneId: string; patch: PrevisPatch; revision: string }): Promise<{ appliedRevision: string; selectionPreserved: boolean }>;
    publishLocalEdit(input: { sceneId: string; project: DirectorProject }): Promise<{ revision: string }>;
    startImageToDirectorFlow(input: { canvasId: string; nodeId: string }): Promise<{ runId: string }>;
};

function nextRevision(revision: string) {
    const match = /^rev-(\d+)$/.exec(revision);
    return match ? `rev-${Number(match[1]) + 1}` : `${revision}:next`;
}

function documentForPatch(current: DirectorProject, patch: PrevisPatch): DirectorProject {
    const candidate = patch.document ?? patch.project;
    if (candidate && typeof candidate === "object" && candidate.format === "director-desk") return structuredClone(candidate);
    if (patch.format === "director-desk") return structuredClone(patch as unknown as DirectorProject);
    if (patch.changes && typeof patch.changes === "object") return { ...structuredClone(current), ...structuredClone(patch.changes) };
    return { ...structuredClone(current), ...structuredClone(patch as Partial<DirectorProject>) };
}

/** Backend enforces this order too; keeping the small guard in the bridge prevents UI flows from advertising an unsafe shortcut. */
export function validateDirectorToolOrder(toolNames: string[]) {
    const required = ["director_block_scene", "director_design_shot", "director_check_continuity"];
    let cursor = -1;
    for (const name of required) {
        const next = toolNames.indexOf(name);
        if (next < 0 || next <= cursor) throw new Error("director_tool_order_invalid");
        cursor = next;
    }
    return true;
}

export function createAgentCanvasBridge(options: AgentCanvasBridgeOptions): AgentCanvasBridge {
    let revision = options.readRevision();
    return {
        async applyAgentScenePatch(input) {
            if (input.sceneId !== options.readDocument().extensions?.["infinite-canvas"]?.sourceSceneId) throw new Error("director_scene_scope_invalid");
            if (input.revision !== revision || input.revision !== options.readRevision()) throw new Error("revision_conflict");
            if (!options.approve) throw new Error("approval_required");
            const approved = await options.approve({ kind: "previs_apply_patch", canvasId: options.canvasId, sceneId: input.sceneId, revision: input.revision, patch: input.patch });
            if (!approved) throw new Error("approval_required");
            const next = nextRevision(input.revision);
            const viewState = options.readViewState?.();
            options.bridge.notify("host.scene.load", { canvasId: options.canvasId, document: documentForPatch(options.readDocument(), input.patch), revision: next, source: "agent", preserveViewState: true, viewState });
            revision = next;
            return { appliedRevision: next, selectionPreserved: Boolean(viewState) };
        },
        async publishLocalEdit(input) {
            if (input.sceneId !== options.readDocument().extensions?.["infinite-canvas"]?.sourceSceneId) throw new Error("director_scene_scope_invalid");
            const current = options.readRevision();
            revision = current;
            options.bridge.notify("desk.scene.changed", { canvasId: options.canvasId, sceneId: input.sceneId, revision: current, document: input.project });
            return { revision: current };
        },
        async startImageToDirectorFlow(input) {
            if (input.canvasId !== options.canvasId || input.nodeId !== options.imageReference?.nodeId || options.imageReference?.kind !== "image") throw new Error("director_image_reference_required");
            const model = options.imageConfig ? selectImageInspectionModel(options.imageConfig, options.imageModel || "") : options.imageModel || "";
            if (!model || options.supportsImage === false || (options.imageConfig && !modelSupportsImageInspection(options.imageConfig, model))) throw new Error("director_image_model_unsupported");
            if (!options.createRun) throw new Error("director_agent_unavailable");
            const request: CreateAgentRunInput = { canvasId: options.canvasId, prompt: options.imageConfig ? buildImageToDirectorAgentPrompt(options.imageReference) : `根据画布图片生成可编辑的 3D 导演预演场景。按顺序执行 ${DIRECTOR_IMAGE_TOOL_ORDER.join("、")}。`, workflow: "image_to_director", model, permissionMode: options.permissionMode || "request_approval", contextScope: ["canvas"], idempotencyKey: `image-to-director:${options.canvasId}:${input.nodeId}` };
            const result = await options.createRun(request);
            const runId = result.run?.id || result.id;
            if (!runId) throw new Error("director_agent_run_invalid");
            return { runId };
        },
    };
}
