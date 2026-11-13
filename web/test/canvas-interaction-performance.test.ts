import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("canvas toolbar and fullscreen storyboard bound their DOM work", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/canvas-interaction-performance.harness.tsx"], 30000);
    expect(exitCode, output).toBe(0);
}, 35000);
