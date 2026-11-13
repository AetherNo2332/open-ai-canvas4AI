import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { runBunHarness } from "./run-bun-harness";

test("画布底部避让包含计数条并响应整组控件宽度变化", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/canvas-bottom-controls.harness.tsx"], 20000);
    expect(exitCode, output).toBe(0);
}, 25000);

test("计数条使用实际的紧凑间距 token", () => {
    const css = readFileSync(resolve(import.meta.dir, "../src/pages/canvas/canvas-agent-focus.css"), "utf8");
    const hud = css.match(/\.canvas-agent-hud\s*\{([^}]+)\}/)?.[1];
    expect(hud).toContain("gap: var(--space-2)");
    expect(hud).toContain("padding: 0 var(--space-2) 0 var(--space-3)");
});
