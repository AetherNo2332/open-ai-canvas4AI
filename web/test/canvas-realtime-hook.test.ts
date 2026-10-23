import { expect, test } from "bun:test";
import { resolve } from "node:path";
test("real realtime hook isolates scope and binds canvas viewport/drag presence", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/canvas-realtime-hook.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 30_000 });
    expect(new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr)).toContain("0 fail");
    expect(result.exitCode).toBe(0);
});
