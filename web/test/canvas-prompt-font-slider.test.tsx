import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("font slider isolates browser storage and respects the configured size limit", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/canvas-prompt-font-slider.harness.tsx"], 15000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("1 pass");
    expect(output).toContain("0 fail");
});
