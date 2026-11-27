import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

// Bun 1.3 的 Bun.spawnSync({ timeout }) 会在父进程空闲后误杀子进程并返回 null
// 退出码（oven-sh/bun#33932）。沿用仓库里已存在的异步 spawn 包装；这里只放开
// 子进程冷启动预算（宿主负载下整包并行时子 bun 启动会远超 30s），断言仍是
// 「退出 0、子进程自测全过」，不降低任何行为要求。
test("welcome runtime checks stay isolated from the parent Bun test process", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/welcome-title.harness.mjs"], 90_000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("2 pass");
    expect(output).toContain("0 fail");
}, 95_000);
