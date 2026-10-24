import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("canvas conflict recovery requires confirmation and keeps recovery failures visible", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/canvas-sync-status.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 25_000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    expect(result.exitCode, output).toBe(0);
}, 30_000);
