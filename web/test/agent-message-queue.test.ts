import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("Agent queue controls preserve draft and item revision", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/agent-message-queue-ui.harness.tsx"], 30_000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
