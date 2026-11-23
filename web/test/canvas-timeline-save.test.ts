import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("timeline keeps edits made during a pending save", async () => {
    const result = await runBunHarness(["test", "./test/fixtures/canvas-timeline-save.harness.tsx"], 15000);
    expect(result.output).toContain("1 pass");
    expect(result.exitCode).toBe(0);
});
