import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("分镜可见行窗口移动时不重建已挂载行，编辑中的行保持焦点", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/canvas-storyboard-windowed-rows.harness.tsx"], 15000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
});
