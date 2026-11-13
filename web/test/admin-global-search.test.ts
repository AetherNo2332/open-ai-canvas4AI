import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("后台全局搜索点击结果跳转，键盘选择与无结果状态正常", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/admin-global-search.harness.tsx"], 30000);
    expect(exitCode, output).toBe(0);
}, 35000);
