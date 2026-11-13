import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("Agent panel preserves conversation and queue drafts while dismissing owned portals", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/cloud-agent-panel-visibility.harness.tsx"], 45_000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 50_000);
