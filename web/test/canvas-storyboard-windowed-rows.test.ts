import { expect, test } from "bun:test";
import { resolve } from "node:path";

test("分镜可见行窗口移动时不重建已挂载行，编辑中的行保持焦点", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/canvas-storyboard-windowed-rows.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 15000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    expect(result.exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
});
