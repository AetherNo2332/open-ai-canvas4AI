import { buildAudioGenerationMetadata, buildImageGenerationMetadata, buildVideoGenerationMetadata, runCanvasGenerationTaskToConsumer } from "@/lib/canvas/canvas-project-generation";
import type { CanvasGenerationExecution } from "./canvas-generation-executor-types";

/** The host owns version identity and adoption. This path submits on the stable
 * media node, without creating siblings or replacing the displayed resource. */
export async function executeManagedMediaGeneration(execution: CanvasGenerationExecution, mode: "image" | "video" | "audio") {
    const { sourceNode, nodeId, projectId, generationConfig, generationContext, controller } = execution;
    const metadata =
        mode === "video"
            ? buildVideoGenerationMetadata(sourceNode, generationContext, generationConfig)
            : mode === "audio"
              ? buildAudioGenerationMetadata(generationConfig)
              : buildImageGenerationMetadata(generationContext.referenceImages.length ? "edit" : "generation", generationConfig, Number(generationConfig.count) || 1, generationContext.referenceImages);
    execution.startGenerationRequest(nodeId, nodeId, nodeId, controller);
    try {
        await runCanvasGenerationTaskToConsumer(
            {
                projectId,
                nodeId,
                mode,
                prompt: execution.effectivePrompt,
                config: generationConfig,
                referenceImages: generationContext.referenceImages,
                referenceVideos: generationContext.referenceVideos,
                referenceAudios: generationContext.referenceAudios,
                signal: controller.signal,
                ...execution.retryContext,
                metadata: {
                    ...metadata,
                    ...execution.styleMetadata,
                    ...execution.skillMetadata,
                    ...execution.taskContext,
                    mediaVersionIntent: sourceNode?.metadata?.mediaVersion?.versionCount || sourceNode?.metadata?.taskId || sourceNode?.metadata?.content || sourceNode?.metadata?.storageKey ? "new_version" : "create",
                    resolvedCharacterVersions: generationContext.resolvedCharacterVersions,
                    resolvedCharacterVoices: generationContext.resolvedCharacterVoices,
                },
            },
            { bindTask: (task) => execution.bindGenerationTask(nodeId, task), consumeTask: (task) => execution.applyGenerationTaskResult(nodeId, task) },
        );
    } finally {
        execution.finishGenerationRequest(nodeId, controller);
    }
}
