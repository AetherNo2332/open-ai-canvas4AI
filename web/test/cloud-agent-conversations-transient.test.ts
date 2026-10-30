import { expect, test } from "bun:test";
import { resolve } from "node:path";

// The harness mocks local storage; a separate process keeps the mock out of other suites.
test("cloud sync error messages are transient, never persisted", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/cloud-agent-conversations-transient.harness.ts"], { cwd: resolve(import.meta.dir, ".."), timeout: 30_000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    expect(result.exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
