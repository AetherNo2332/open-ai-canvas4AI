import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("Agent panel preserves its conversation and dismisses owned portals while visibility changes", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/cloud-agent-panel-visibility.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 30_000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    expect(result.exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
