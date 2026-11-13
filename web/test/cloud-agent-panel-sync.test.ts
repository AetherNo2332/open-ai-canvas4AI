import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("canvas panel discovers, refreshes and isolates cloud conversation history", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/cloud-agent-panel-sync.harness.tsx"], 30_000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
