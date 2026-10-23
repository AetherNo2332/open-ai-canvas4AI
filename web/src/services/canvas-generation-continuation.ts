import type { CreateTaskInput, GenerationTask, WaitForGenerationTaskOptions } from "./api/task-center";
import type { Consumer, TaskRequest } from "../lib/canvas/command-contract.generated";
import type { CanvasCommandSession } from "./canvas-command-session";
import type { CanvasDocument } from "../lib/canvas/canvas-event-projection";

const creativeConfig = new Set([
    "channelId",
    "channelModelKey",
    "priceTierId",
    "providerModelKey",
    "model",
    "size",
    "quality",
    "transparentBackground",
    "count",
    "videoSeconds",
    "vquality",
    "videoGenerateAudio",
    "videoWatermark",
    "videoArkPrivateAssetUpload",
    "audioVoice",
    "audioFormat",
    "audioSpeed",
    "audioInstructions",
    "systemPrompt",
    "workflowId",
    "webappId",
    "workflowJson",
    "workflowFields",
    "runningHubUseWallet",
]);
const privateKeys = new Set(["apikey", "secretkey", "authorization", "password", "headers", "baseurl", "agentrequests", "cloudagent", "agentrunid", "generationid", "approvalid", "runninghubwalletapikey", "runninghubuploadapikey"]);
function publicInput(value: unknown): unknown {
    if (Array.isArray(value)) return value.map(publicInput);
    if (value && typeof value === "object")
        return Object.fromEntries(
            Object.entries(value)
                .filter(([key]) => !privateKeys.has(key.toLowerCase().replace(/[_-]/g, "")))
                .map(([key, child]) => [key, publicInput(child)]),
        );
    return value;
}
/** Host configuration supplies credentials, protocol and capabilities. The
 * renderer sends only managed selection, creative options and prepared refs. */
export function managedCanvasTaskRequest(request: CreateTaskInput): TaskRequest {
    const source = request.input || {};
    const mode = source.mode;
    if (!["text", "image", "video", "audio"].includes(String(mode))) throw new Error("canvas_generation_mode_invalid");
    const config = source.config && typeof source.config === "object" ? (source.config as Record<string, unknown>) : {};
    const input = publicInput(source) as Record<string, unknown>;
    input.config = publicInput(Object.fromEntries(Object.entries(config).filter(([key]) => creativeConfig.has(key))));
    return { mode: String(mode), type: request.type, prompt: request.prompt, operation: request.operation, logicalModelId: request.logicalModelId, input };
}
export function inferCanvasTaskBinding(document: CanvasDocument, nodeId: string, mode: string, operation?: string): { targetNodeId: string; consumer?: Consumer } {
    const nodes = (document.nodes as Array<Record<string, any>>) || [];
    const node = nodes.find((n) => n.id === nodeId);
    if (!node) throw new Error("canvas_generation_target_missing");
    if (node.type === "script" && mode === "text" && operation === "storyboard") return { targetNodeId: nodeId, consumer: { kind: "storyboard_document" } };
    if (node.type === "text" && mode === "text" && operation === "list_analysis") return { targetNodeId: nodeId, consumer: { kind: "batch_document" } };
    const bindings: Array<{ targetNodeId: string; consumer: Consumer }> = [];
    for (const parent of nodes) {
        if (parent.type === "batch-table" && mode === "image")
            for (const row of parent.metadata?.batchTable?.rows || []) if (row.outputNodeId === nodeId) bindings.push({ targetNodeId: parent.id, consumer: { kind: "batch", rowId: row.id, outputNodeId: nodeId } });
        if (parent.type === "script" && (mode === "image" || mode === "video")) {
            const field = mode === "image" ? "imageNodeId" : "videoNodeId";
            for (const row of parent.metadata?.storyboard?.rows || []) if (row[field] === nodeId) bindings.push({ targetNodeId: parent.id, consumer: { kind: "storyboard", rowId: row.id, field, outputNodeId: nodeId } });
        }
    }
    if (bindings.length > 1) throw new Error("canvas_generation_binding_ambiguous");
    return bindings[0] || { targetNodeId: nodeId };
}
export function createCanvasGenerationContinuation(deps: {
    session: CanvasCommandSession;
    queryTask: (id: string, options?: { signal?: AbortSignal }) => Promise<GenerationTask>;
    waitTask: (id: string, options?: WaitForGenerationTaskOptions) => Promise<GenerationTask>;
    commitEditor?: () => Promise<unknown>;
}) {
    async function submit(targetNodeId: string, request: CreateTaskInput, consumer?: Consumer, signal?: AbortSignal) {
        signal?.throwIfAborted();
        const metadata = request.input?.metadata as Record<string, unknown> | undefined;
        const intent = metadata?.mediaVersionIntent === "new_version" ? "new_version" : "create";
        const result = await deps.session.submitTask({ targetNodeId, consumer, intent, task: managedCanvasTaskRequest(request) }, { operationId: typeof metadata?.clientOperationId === "string" ? metadata.clientOperationId : undefined });
        // An accepted host task survives navigation; abort only stops this listener.
        signal?.throwIfAborted();
        return deps.queryTask(result.taskId, { signal });
    }
    return {
        async submitForNode(nodeId: string, request: CreateTaskInput, signal?: AbortSignal) {
            signal?.throwIfAborted();
            await deps.commitEditor?.();
            signal?.throwIfAborted();
            const binding = inferCanvasTaskBinding(deps.session.state.projected, nodeId, String(request.input?.mode || ""), request.operation);
            return submit(binding.targetNodeId, request, binding.consumer, signal);
        },
        submit,
        resume(taskId: string, options?: WaitForGenerationTaskOptions) {
            return deps.waitTask(taskId, options);
        },
    };
}
