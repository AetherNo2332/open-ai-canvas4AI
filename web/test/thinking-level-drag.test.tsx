import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

// React caches DOM event support at import time; keep the range event test in its own DOM runtime.
test("thinking slider follows continuous dragging and commits once on release", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/thinking-level-drag.harness.tsx"], 15000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("1 pass");
    expect(output).toContain("0 fail");
});
