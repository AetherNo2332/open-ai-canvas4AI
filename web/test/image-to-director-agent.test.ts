import { expect, test } from "bun:test";
import { buildImageToDirectorAgentPrompt, buildImageToDirectorAgentRequest, IMAGE_TO_DIRECTOR_AGENT_INSTRUCTION } from "@/lib/canvas/image-to-director-agent";

const reference = { id: "node-1", nodeId: "node-1", kind: "image" as const, label: "参考图", title: "参考图", active: true };

test("image entry requires real inspection and orders director tools without creating a partial scene", () => {
    const prompt = buildImageToDirectorAgentPrompt(reference);
    expect(prompt).toContain("@[node:node-1]");
    expect(prompt.indexOf("canvas_inspect_image")).toBeLessThan(prompt.indexOf("director_block_scene"));
    expect(prompt.indexOf("director_block_scene")).toBeLessThan(prompt.indexOf("director_design_shot"));
    expect(prompt.indexOf("director_design_shot")).toBeLessThan(prompt.indexOf("director_check_continuity"));
    expect(IMAGE_TO_DIRECTOR_AGENT_INSTRUCTION).toContain("保持画布不变");
});

test("request selection refuses models without image input", () => {
    const config = { model: "text-only", channels: [{ id: "system", scope: "system", models: ["text-only"], modelCosts: [{ model: "text-only", capabilityConfig: { text: { references: { maxImages: 0 } } } }] }], apiKey: "", baseUrl: "", apiFormat: "openai" } as any;
    expect(() => buildImageToDirectorAgentRequest({ canvasId: "canvas", reference, config, preferredModel: "text-only" })).toThrow("director_image_model_unsupported");
});

test("explicitly disabled vision capability is rejected even with a stale image limit", () => {
    const config = { model: "text-no-vision", channels: [{ id: "system", scope: "system", models: ["text-no-vision"], modelCosts: [{ model: "text-no-vision", capabilityConfig: { text: { visionSupported: false, references: { maxImages: 4 } } } }] }], apiKey: "", baseUrl: "", apiFormat: "openai" } as any;
    expect(() => buildImageToDirectorAgentRequest({ canvasId: "canvas-1", reference, config, preferredModel: "text-no-vision" })).toThrow("director_image_model_unsupported");
});
