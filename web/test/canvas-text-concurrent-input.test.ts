import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

// React DOM caches browser feature detection on first import. Use a fresh
// runtime so this real-component test is independent of prior SSR suites.
test("真实输入组件的中文组词并发合并、光标与无重试交互", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/canvas-text-concurrent-input.harness.tsx"], 15000);
    expect(output).toContain("2 pass");
    expect(output).toContain("0 fail");
    expect(exitCode).toBe(0);
});
