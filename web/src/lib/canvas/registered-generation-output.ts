import type { BackendGenerationResult } from "@/services/api/generation-task";
import type { GenerationTask } from "@/services/api/task-center";

/** Asset IDs come from the owner-checked server task, including on HTTP origins. */
export function registeredGenerationOutput(task: GenerationTask, outputIndex?: number, storageKey?: string) {
    if (task.status !== "succeeded" || !task.resultJson) throw new Error("生成结果尚未保存完成");
    const result = JSON.parse(task.resultJson) as BackendGenerationResult;
    const outputs = result.images || result.audios || (result.video ? [result.video] : result.audio ? [result.audio] : []);
    const index = outputIndex ?? (storageKey ? outputs.findIndex((output) => output.storageKey === storageKey) : -1);
    const output = outputs[index];
    if (!output || (storageKey && output.storageKey !== storageKey)) throw new Error("当前图片或媒体与任务输出不匹配，未重复保存素材");
    if (typeof output.assetId !== "string" || !output.assetId.trim()) throw new Error("任务输出尚未登记为素材，请在任务中心重试保存");
    return { assetId: output.assetId, outputIndex: index };
}
