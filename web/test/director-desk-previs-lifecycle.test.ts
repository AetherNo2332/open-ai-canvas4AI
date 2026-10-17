import { expect, test } from "bun:test";
import { createDirectorRuntime, disposeDirectorRuntime } from "@/integrations/director-desk";

test("runtime lifecycle is idempotent for workbench close/dispose", () => {
    const disposed: string[] = [];
    const stage = {} as HTMLElement;
    const shot = {} as HTMLElement;
    const runtime = createDirectorRuntime({
        stage,
        shot,
        createRenderer: () => ({ dispose: () => disposed.push("renderer") }),
    });
    disposeDirectorRuntime(runtime);
    disposeDirectorRuntime(runtime);
    expect(runtime.disposed).toBe(true);
    expect(disposed).toEqual(["renderer", "renderer"]);
});
