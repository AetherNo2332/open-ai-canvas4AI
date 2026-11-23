import { expect, test } from "bun:test";
import { resolveAgentPanelConfig, agentPanelControlEnabled } from "@/services/agent/panel-config";
import "@/services/agent/configs";

test("canvas, drama and platform resolve to the same complete control contract", () => {
    for (const id of ["canvas", "drama", "platform"]) {
        const config = resolveAgentPanelConfig(id);
        expect(config).not.toBeNull();
        expect(config?.id).toBe(id);
        expect(config?.title.trim()).toBeTruthy();
        expect(config?.placeholder.trim()).toBeTruthy();
        for (const control of ["model", "reasoning", "skills", "contextUsage", "sendMode"] as const) {
            expect(agentPanelControlEnabled(config!, control)).toBe(true);
        }
    }
    expect(resolveAgentPanelConfig("missing-business")).toBeNull();
});

test("business extensions and docking stay explicit data", () => {
    const canvas = resolveAgentPanelConfig("canvas");
    const drama = resolveAgentPanelConfig("drama");
    const platform = resolveAgentPanelConfig("platform");
    expect(canvas?.docking).toBe("floating-and-docked");
    expect(canvas?.extensions).toContain("canvas-context");
    expect(drama?.docking).toBe("floating");
    expect(drama?.extensions).toContain("episode-target");
    expect(platform?.docking).toBe("floating");
    expect(platform?.extensions).toEqual([]);
});
