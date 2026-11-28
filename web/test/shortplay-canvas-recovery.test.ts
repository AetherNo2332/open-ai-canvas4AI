import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("shortplay canvas opening reports offline failures and allows retry", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/shortplay-canvas-recovery.harness.tsx"], 30000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 30000);
