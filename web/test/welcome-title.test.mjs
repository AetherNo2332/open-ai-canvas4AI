import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("welcome runtime checks stay isolated from the parent Bun test process", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/welcome-title.harness.mjs"], { cwd: resolve(import.meta.dir, ".."), timeout: 30_000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    expect(result.exitCode, output).toBe(0);
    expect(output).toContain("2 pass");
    expect(output).toContain("0 fail");
}, 35_000);
