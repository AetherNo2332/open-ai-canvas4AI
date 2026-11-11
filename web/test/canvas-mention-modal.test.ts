import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("引用菜单留在放大编辑弹窗内并可选择资产", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/canvas-mention-modal.harness.tsx"], 15000);
    expect(exitCode, output).toBe(0);
}, 20000);
