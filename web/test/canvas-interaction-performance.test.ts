import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("canvas toolbar and fullscreen storyboard bound their DOM work", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/canvas-interaction-performance.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 30000 });
    expect(result.exitCode, new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr)).toBe(0);
}, 35000);
