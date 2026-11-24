import { describe, expect, test } from "bun:test";
import { dramaAgentTextModel, validatedDramaAgentRunId } from "@/services/agent/drama-agent-context";
import { agentPanelDefaultModel, agentPanelModelRoute } from "@/services/agent/panel-model-selection";
import type { AiConfig } from "@/stores/use-config-store";

describe("drama Agent text model", () => {
    const text = { channelId: "channel-default", modelKey: "text-default", label: "默认文本" };

    test("uses the project default when the optional script override is empty", () => {
        expect(dramaAgentTextModel({ text, scriptText: {} })).toEqual(text);
    });

    test("uses a complete script override before the project default", () => {
        const scriptText = { channelId: "channel-script", modelKey: "text-script", label: "剧本文本" };
        expect(dramaAgentTextModel({ text, scriptText })).toEqual(scriptText);
    });

    test("returns no route instead of sending an empty model choice", () => {
        expect(dramaAgentTextModel({ text: {}, scriptText: {} })).toBeUndefined();
    });
});

describe("Agent panel model routing", () => {
    const config = {
        textModel: "a::shared",
        channels: ["a", "b"].map((id) => ({
            id,
            scope: "system",
            models: ["shared"],
            modelCosts: [{ model: "shared", logicalModelId: id === "b" ? "script-model" : undefined }],
        })),
    } as unknown as AiConfig;
    const models = ["a::shared", "b::shared"];

    test("does not match a project default to a different selected channel with the same model key", () => {
        const target = { channelId: "a", modelKey: "shared" };
        expect(agentPanelDefaultModel(config, models, target)).toBe("a::shared");
        expect(agentPanelModelRoute(config, "b::shared", target)).toEqual({ logicalModelId: "script-model", channelId: "b", channelModelKey: "shared" });
        expect(agentPanelModelRoute({ ...config, channels: config.channels.map((channel) => ({ ...channel, modelCosts: undefined })) }, "b::shared", target)).toEqual({ logicalModelId: "", channelId: "b", channelModelKey: "shared" });
    });

    test("finds a logical-only project default in the eligible model directory", () => {
        const target = { logicalModelId: "script-model" };
        const model = agentPanelDefaultModel(config, models, target);
        expect(model).toBe("b::shared");
        expect(agentPanelModelRoute(config, model, target).logicalModelId).toBe("script-model");
        expect(agentPanelDefaultModel(config, ["a::shared"], target)).toBe("a::shared");
    });
});

describe("legacy drama Agent links", () => {
    test("accepts a drama run from the addressed project", () => {
        expect(validatedDramaAgentRunId({ id: "run-1", scope: { type: "drama", id: "project-1" }, agentDefinitionId: "drama.analysis" }, "project-1")).toBe("run-1");
    });

    test("rejects another project, another business and a run without persisted scope", () => {
        expect(() => validatedDramaAgentRunId({ id: "run-1", scope: { type: "drama", id: "project-2" } }, "project-1")).toThrow("其他作品");
        expect(() => validatedDramaAgentRunId({ id: "run-1", scope: { type: "canvas", id: "project-1" } }, "project-1")).toThrow("其他作品");
        expect(() => validatedDramaAgentRunId({ id: "run-1" }, "project-1")).toThrow("缺少业务归属");
    });
});
