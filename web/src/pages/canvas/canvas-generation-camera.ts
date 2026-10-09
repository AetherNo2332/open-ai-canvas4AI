import { appendCameraControlPrompt } from "@/lib/canvas/camera-prompt-library";
import { buildPortraitTexturePrompt } from "@/lib/canvas/canvas-portrait-texture";
import type { CanvasNodeMetadata } from "@/types/canvas";

type CameraPromptMetadata = Pick<CanvasNodeMetadata, "cameraControl" | "portraitTexture">;

export function buildCanvasVisualGenerationPrompt(prompt: string, mode: string, metadata?: CameraPromptMetadata): string {
    const visualPrompt = mode === "image" && metadata?.portraitTexture ? buildPortraitTexturePrompt(prompt, metadata.portraitTexture) : prompt;
    return appendCameraControlPrompt(visualPrompt, metadata?.cameraControl, mode);
}

export function canvasCameraControlSnapshot(metadata?: CameraPromptMetadata): Pick<CanvasNodeMetadata, "cameraControl"> {
    return metadata?.cameraControl ? { cameraControl: { ...metadata.cameraControl } } : {};
}

export function taskCameraControlSnapshot(inputJson?: string): Pick<CanvasNodeMetadata, "cameraControl"> {
    if (!inputJson) return {};
    let value: unknown;
    try {
        value = JSON.parse(inputJson);
    } catch {
        return {};
    }
    if (!value || typeof value !== "object" || !("metadata" in value)) return {};
    const metadata = value.metadata;
    if (!metadata || typeof metadata !== "object" || !("cameraControl" in metadata)) return {};
    const control = metadata.cameraControl;
    if (!control || typeof control !== "object") return {};
    const fields = control as Record<string, unknown>;
    if (typeof fields.enabled !== "boolean" || typeof fields.camera !== "string" || typeof fields.lens !== "string" || typeof fields.focalLength !== "number" || !Number.isFinite(fields.focalLength) || typeof fields.aperture !== "number" || !Number.isFinite(fields.aperture)) return {};
    return { cameraControl: { enabled: fields.enabled, camera: fields.camera, lens: fields.lens, focalLength: fields.focalLength, aperture: fields.aperture } };
}
