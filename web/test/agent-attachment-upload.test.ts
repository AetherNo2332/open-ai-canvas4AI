import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("Agent attachment upload reuses durable identities and settles aborted placeholders", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/agent-attachment-upload.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 30_000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    expect(result.exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
