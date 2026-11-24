import { describe, expect, test } from "bun:test";
import { agentModelFilter, agentModelSupportsRequirements } from "@/lib/agent/model-requirements";
import type { AiConfig } from "@/stores/use-config-store";

const config = {
    channels: [
        {
            id: "test",
            models: ["tool-less", "non-strict", "vision-less", "stream-less", "full", "logical-primary"],
            modelCosts: [
                { model: "tool-less", capabilityConfig: { version: 1, text: { strictTools: true, toolCallingSupported: false } } },
                { model: "non-strict", capabilityConfig: { version: 1, text: { strictTools: false, toolCallingSupported: true } } },
                { model: "vision-less", capabilityConfig: { version: 1, text: { visionSupported: false } } },
                { model: "stream-less", capabilityConfig: { version: 1, text: { streaming: false } } },
                { model: "full", capabilityConfig: { version: 1, text: { strictTools: true, visionSupported: true, streaming: true } } },
                { model: "logical-primary", logicalModelId: "logical", capabilityConfig: { version: 1, text: { toolCallingSupported: true } } },
            ],
        },
        {
            id: "backup",
            models: ["logical-backup"],
            modelCosts: [{ model: "logical-backup", logicalModelId: "logical", capabilityConfig: { version: 1, text: { toolCallingSupported: false } } }],
        },
    ],
} as unknown as AiConfig;

const requirements = { text: true, toolCalling: true, vision: true, streamOutput: true };

describe("Agent model requirements", () => {
    test("rejects models whose declared text capabilities miss a server requirement", () => {
        expect(agentModelSupportsRequirements(config, "test::tool-less", requirements)).toBe(false);
        expect(agentModelSupportsRequirements(config, "test::non-strict", requirements)).toBe(true);
        expect(agentModelSupportsRequirements(config, "test::logical-primary", requirements)).toBe(false);
        expect(agentModelSupportsRequirements(config, "test::vision-less", requirements)).toBe(false);
        expect(agentModelSupportsRequirements(config, "test::stream-less", requirements)).toBe(false);
        expect(agentModelSupportsRequirements(config, "test::full", requirements)).toBe(true);
    });

    test("builds the same predicate used by the picker and fallback selection", () => {
        const filter = agentModelFilter(config, requirements);
        expect(filter).toBeFunction();
        expect(filter?.("test::tool-less")).toBe(false);
        expect(filter?.("test::full")).toBe(true);
        expect(agentModelFilter(config, undefined)).toBeUndefined();
    });

    test("treats unknown local metadata as compatible so the server stays authoritative", () => {
        expect(agentModelSupportsRequirements(config, "unknown-channel::unknown-model", requirements)).toBe(true);
    });
});
