import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

// The harness mocks API modules; a separate process keeps the mocks out of other suites.
test("legacy canvas reference migration entry", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/canvas-reference-migration.harness.tsx"], 30_000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
