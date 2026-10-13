import { expect, test } from "bun:test";
import { directorDeskModeLabel, directorDeskRuntimePolicy } from "@/lib/canvas/director-desk-feature";

test("DirectorDesk feature flag fails closed and exposes explicit rollout states", () => {
    expect(directorDeskRuntimePolicy(undefined)).toBe("legacy");
    expect(directorDeskRuntimePolicy("unsafe")).toBe("legacy");
    expect(directorDeskRuntimePolicy("canary")).toBe("legacy");
    expect(directorDeskRuntimePolicy("canary", "user-1")).toBe("canary");
    expect(directorDeskRuntimePolicy("on")).toBe("authoritative");
    expect(directorDeskModeLabel("off")).toBe("已关闭");
    expect(directorDeskModeLabel("canary")).toBe("灰度开放");
    expect(directorDeskModeLabel("on")).toBe("全量开放");
});
