import { expect, test } from "bun:test";
import type { PrevisScene } from "@/types/previs";
import { createDirectorDeskCommitBridge } from "@/components/canvas/previs/director-desk-commit-bridge";

const scene = { id: "scene-1" } as PrevisScene;

test("DirectorDesk commits use the registered workbench coordinator", () => {
    const calls: string[] = [];
    const bridge = createDirectorDeskCommitBridge(() => calls.push("fallback"));

    bridge.commit(scene);
    bridge.register(() => calls.push("coordinator"));
    bridge.commit(scene);
    bridge.clear();
    bridge.commit(scene);

    expect(calls).toEqual(["fallback", "coordinator", "fallback"]);
});
