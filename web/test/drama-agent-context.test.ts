import { describe, expect, test } from "bun:test";
import { dramaAgentTextModel, validatedDramaAgentRunId } from "@/services/agent/drama-agent-context";

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
