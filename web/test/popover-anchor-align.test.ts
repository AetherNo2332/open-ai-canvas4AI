import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("移动父块内的原生卡片跟随、菜单收起与订阅清理", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/popover-anchor-align.harness.tsx"], 15000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
});
