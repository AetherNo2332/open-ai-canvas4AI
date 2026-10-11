import { canvasNodeMentionToken, canvasResourceMentionToken, type CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { resolveModelChannel, selectableModelsByCapability, type AiConfig } from "@/stores/use-config-store";
import type { AgentPermissionMode, CreateAgentRunInput } from "@/services/api/agent";

export const IMAGE_TO_DIRECTOR_AGENT_INSTRUCTION = [
    "请把这张画布图片复现为可编辑的 3D 导演预演场景，而不是把图片放进预演台。",
    "第一步必须调用 canvas_inspect_image 读取真实画面；不能根据节点标题、提示词或文件名猜测内容。",
    "如果本轮没有 canvas_inspect_image，立即停止并报告当前模型不支持图片输入，不得创建场景。",
    "按顺序执行：canvas_inspect_image → director_block_scene → director_design_shot → director_check_continuity。",
    "根据实际观察建立可移动的人物、道具、环境、机位和灯光；人物使用 actor，对象必须能在预演台中单独移动、旋转和调整姿态。",
    "不要把原图作为 billboard、背景板、参考平面或图片对象加入场景；无法确认的细节写入 uncertainties。",
    "如果任一步失败或取消，保持画布不变，不创建半成品场景；成功后重新读取导演场景确认对象数量和镜头设置。",
].join("\n");

export function buildImageToDirectorAgentPrompt(reference: CanvasResourceReference) {
    const mention = reference.mentionToken || (reference.nodeId ? canvasNodeMentionToken(reference.nodeId) : canvasResourceMentionToken(reference));
    return `${mention}\n${IMAGE_TO_DIRECTOR_AGENT_INSTRUCTION}`;
}

export function buildImageToDirectorDisplayText() {
    return "根据此图生成 3D 导演预演台";
}

export function modelSupportsImageInspection(config: AiConfig, model: string) {
    const capability = modelCapabilityConfigFor(config, model).text;
    return capability?.visionSupported !== false && (capability?.references.maxImages || 0) > 0;
}

export function selectImageInspectionModel(config: AiConfig, preferredModel: string) {
    const canUse = (model: string) => resolveModelChannel(config, model).scope === "system" && modelSupportsImageInspection(config, model);
    if (preferredModel && canUse(preferredModel)) return preferredModel;
    return selectableModelsByCapability(config, "text").find(canUse) || "";
}

export type ImageToDirectorAgentRequestOptions = {
    canvasId: string;
    reference: CanvasResourceReference;
    config: AiConfig;
    preferredModel: string;
    permissionMode?: AgentPermissionMode;
};

export function buildImageToDirectorAgentRequest(options: ImageToDirectorAgentRequestOptions): CreateAgentRunInput {
    if (options.reference.kind !== "image" || !options.reference.nodeId) throw new Error("director_image_reference_required");
    const model = selectImageInspectionModel(options.config, options.preferredModel);
    if (!model) throw new Error("director_image_model_unsupported");
    return {
        canvasId: options.canvasId,
        prompt: buildImageToDirectorAgentPrompt(options.reference),
        model,
        permissionMode: options.permissionMode || "request_approval",
        contextScope: ["canvas"],
        idempotencyKey: `image-to-director:${options.canvasId}:${options.reference.nodeId}`,
    };
}
