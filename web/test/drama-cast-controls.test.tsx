import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

// React 在首次导入时缓存 DOM 事件能力；隔离环境保证输入与浮层测试不受其他用例的全局 DOM 影响。
test("短剧角色、参考与画布采用在独立 DOM 中通过全部交互用例", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/drama-cast-controls.harness.tsx"], 60000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("22 pass");
    expect(output).toContain("0 fail");
}, 60000);
