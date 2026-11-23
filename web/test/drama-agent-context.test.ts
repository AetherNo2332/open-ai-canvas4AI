import { describe, expect, test } from "bun:test";
import { validatedDramaAgentRunId } from "@/services/agent/drama-agent-context";

describe("legacy drama Agent links", () => {
    test("accepts a drama run from the addressed project", () => {
        expect(
            validatedDramaAgentRunId(
                { id: "run-1", scope: { type: "drama", id: "project-1" }, agentDefinitionId: "drama.analysis" },
                "project-1",
            ),
        ).toBe("run-1");
    });

    test("rejects another project, another business and a run without persisted scope", () => {
        expect(() => validatedDramaAgentRunId({ id: "run-1", scope: { type: "drama", id: "project-2" } }, "project-1")).toThrow("其他作品");
        expect(() => validatedDramaAgentRunId({ id: "run-1", scope: { type: "canvas", id: "project-1" } }, "project-1")).toThrow("其他作品");
        expect(() => validatedDramaAgentRunId({ id: "run-1" }, "project-1")).toThrow("缺少业务归属");
    });
});
