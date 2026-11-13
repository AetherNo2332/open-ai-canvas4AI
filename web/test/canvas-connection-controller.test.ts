import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

// React DOM 的环境检测需要在独立进程中先安装 DOM，避免受其他 SSR 用例影响。
test("真实连接控制器的悬停、释放、取消及重叠节点回归", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/canvas-connection-controller.harness.tsx"], 20000);
    if (exitCode !== 0) throw new Error(output);
    expect(exitCode).toBe(0);
});
