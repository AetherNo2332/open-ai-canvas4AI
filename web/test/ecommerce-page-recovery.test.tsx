import { expect, test } from "bun:test";
import { runBunHarness } from "./run-bun-harness";

// Install the DOM before React initializes, as in the existing canvas harnesses.
// Keep the viewport module mock and DOM globals inside this child process.
// Full modal, upload and restore flows need a bounded DOM budget of their own.
test("电商页面完整生命周期在独立 DOM 中通过全部用例", async () => {
    const { exitCode, output } = await runBunHarness(["test", "--timeout", "10000", "./test/fixtures/ecommerce-page-recovery.harness.tsx"], 180000);
    expect(exitCode, output).toBe(0);
    expect(output).toContain("48 pass");
    expect(output).toContain("0 fail");
}, 180000);
