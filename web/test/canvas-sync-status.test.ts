import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("canvas save guidance keeps recovery failures visible and distinguishes rejected edits", async () => {
    const child = Bun.spawn([process.execPath, "test", "./test/fixtures/canvas-sync-status.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), stdout: "pipe", stderr: "pipe" });
    const timer = setTimeout(() => child.kill(), 25_000);
    try {
        const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]);
        expect(code, stdout + stderr).toBe(0);
    } finally {
        clearTimeout(timer);
    }
}, 30_000);
