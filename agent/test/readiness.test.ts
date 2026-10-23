import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { WorkerHealthState } from "../src/health-state.js";
import { validateHarnessArtifacts } from "../src/bootstrap-check.js";
import { CANVAS_PI_WIRE_IDENTITY } from "../src/bridge.js";

test("local health follows startup, recovery, and drain without a network listener", () => {
  const directory = mkdtempSync(join(tmpdir(), "agent-health-"));
  try {
    const path = join(directory, "health.json");
    const state = new WorkerHealthState(path);
    assert.equal(JSON.parse(readFileSync(path, "utf8")).readiness, "starting");
    state.set("degraded", "backend disconnected");
    assert.equal(state.current.readiness, "degraded");
    state.set("ready");
    assert.equal(JSON.parse(readFileSync(path, "utf8")).readiness, "ready");
    state.set("draining", "shutdown");
    assert.equal(state.current.readiness, "draining");
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("harness bootstrap rejects missing schema and protocol mismatch", async () => {
  const directory = mkdtempSync(join(tmpdir(), "agent-harness-"));
  try {
    writeFileSync(join(directory, "PI_WIRE_IDENTITY.json"), JSON.stringify(CANVAS_PI_WIRE_IDENTITY));
    await assert.rejects(validateHarnessArtifacts(directory));
    writeFileSync(join(directory, "TOOL_SCHEMA.json"), JSON.stringify({ schemaVersion: "cloud-agent-tools/v1", tools: [{}] }));
    await validateHarnessArtifacts(directory);
    writeFileSync(join(directory, "PI_WIRE_IDENTITY.json"), JSON.stringify({ ...CANVAS_PI_WIRE_IDENTITY, protocolVersion: "old" }));
    await assert.rejects(validateHarnessArtifacts(directory), /incompatible/);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});
