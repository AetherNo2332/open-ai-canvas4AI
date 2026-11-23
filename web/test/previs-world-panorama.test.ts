import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("world panorama rendering preserves terrain, lights, and weather", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/previs-world-panorama.harness.tsx"], 15000);
    expect(output).toContain("2 pass");
    expect(output).toContain("0 fail");
    expect(exitCode).toBe(0);
});
