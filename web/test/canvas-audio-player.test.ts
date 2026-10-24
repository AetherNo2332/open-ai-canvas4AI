import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("audio player keeps decoded data local and releases replaced sources", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/canvas-audio-player.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 30000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    if (result.exitCode) console.error(output);
    expect(output).toContain("0 fail");
    expect(result.exitCode).toBe(0);
});
