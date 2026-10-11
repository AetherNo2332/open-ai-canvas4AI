import { expect, test } from "bun:test";
import { buildImageToDirectorAgentRequest } from "@/lib/canvas/image-to-director-agent";

test("one-click image flow emits an agent request and no local scene mutation", () => {
    const reference = { id: "img", nodeId: "img", kind: "image" as const, label: "图片", title: "图片", active: true };
    const config = { model: "vision", channels: [{ id: "system", scope: "system", models: ["vision"], modelCosts: [{ model: "vision", capabilityConfig: { text: { references: { maxImages: 4 } } } }] }], apiKey: "", baseUrl: "", apiFormat: "openai" } as any;
    const request = buildImageToDirectorAgentRequest({ canvasId: "canvas", reference, config, preferredModel: "vision" });
    expect(request.prompt).toContain("director_block_scene");
    expect(request.idempotencyKey).toContain("image-to-director:canvas:img");
});
