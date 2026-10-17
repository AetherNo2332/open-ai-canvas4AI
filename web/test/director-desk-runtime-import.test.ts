import { expect, test } from "bun:test";
import { createDirectorRuntime, disposeDirectorRuntime, DIRECTOR_DESK_VERSION } from "@/integrations/director-desk";

test("director desk runtime is browser-safe and owns its renderer lifecycle", () => {
    const calls: string[] = [];
    const host = {
        stage: {} as HTMLElement,
        shot: {} as HTMLElement,
        createRenderer: (container: HTMLElement) => {
            calls.push(container === host.stage ? "stage:create" : "shot:create");
            return { dispose: () => calls.push(container === host.stage ? "stage:dispose" : "shot:dispose") };
        },
    };
    const runtime = createDirectorRuntime(host);
    expect(DIRECTOR_DESK_VERSION).toBe("0.4.11");
    expect(runtime.disposed).toBe(false);
    expect(runtime.renderers).toHaveLength(2);
    disposeDirectorRuntime(runtime);
    expect(runtime.disposed).toBe(true);
    expect(calls).toEqual(["stage:create", "shot:create", "stage:dispose", "shot:dispose"]);
    disposeDirectorRuntime(runtime);
    expect(calls).toHaveLength(4);
});
