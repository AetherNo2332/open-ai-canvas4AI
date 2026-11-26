import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

// Bun 1.3 的 Bun.spawnSync({ timeout }) 会在父进程空闲后误杀子进程并返回 null
// 退出码（oven-sh/bun#33932）。沿用仓库里已存在的异步 spawn 包装，子进程隔离与
// 超时上限不变，断言仍是「退出 0、子进程自测全过」。
test("welcome runtime checks stay isolated from the parent Bun test process", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/welcome-title.harness.mjs"], 30_000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("2 pass");
    expect(output).toContain("0 fail");
}, 35_000);
