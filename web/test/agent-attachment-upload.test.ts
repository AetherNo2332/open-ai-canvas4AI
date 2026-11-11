import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

test("Agent attachment upload reuses durable identities and settles aborted placeholders", async () => {
    const { exitCode, output } = await runBunHarness(["test", "./test/fixtures/agent-attachment-upload.harness.tsx"], 30_000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
