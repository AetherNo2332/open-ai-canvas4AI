import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

// The harness mocks local storage; a separate process keeps the mock out of other suites.
test("cloud sync error messages are transient, never persisted", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/cloud-agent-conversations-transient.harness.ts"], 30_000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
