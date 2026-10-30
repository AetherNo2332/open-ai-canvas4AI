import { expect, test } from "bun:test";
import { resolve } from "node:path";

// The harness mocks API modules; a separate process keeps the mocks out of other suites.
test("legacy canvas reference migration entry", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/canvas-reference-migration.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 30_000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    expect(result.exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
