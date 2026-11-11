import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("real collaboration layer shares its connection and projects presence through the live viewport", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/canvas-collaboration.harness.tsx"], 15000);
    expect(output).toContain("5 pass");
    expect(output).toContain("0 fail");
    expect(exitCode).toBe(0);
});
