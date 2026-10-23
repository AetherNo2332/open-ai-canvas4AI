import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { evaluateWorkerHealth, HEALTH_MAX_AGE_MS, WorkerHealthState, type WorkerHealthSnapshot } from "../src/health-state.js";
import { validateHarnessArtifacts } from "../src/bootstrap-check.js";
import { CANVAS_PI_WIRE_IDENTITY } from "../src/bridge.js";

const probe = fileURLToPath(new URL("../src/health-probe.js", import.meta.url));
function probeExit(path: string): number | null {
  return spawnSync(process.execPath, [probe], { env: { ...process.env, CANVAS_AGENT_HEALTH_FILE: path } }).status;
}

test("configuration failure survives claim and capacity success until configuration recovers", () => {
  const directory = mkdtempSync(join(tmpdir(), "agent-health-"));
  try {
    const path = join(directory, "health.json");
    const health = new WorkerHealthState(path);
    assert.equal(health.current.readiness, "starting");
    assert.equal(probeExit(path), 1);
    health.failed("configuration", "backend disconnected");
    health.succeeded("claim");
    health.succeeded("capacity");
    assert.equal(health.current.readiness, "degraded");
    assert.match(health.current.reason, /configuration: backend disconnected/);
    assert.equal(probeExit(path), 1);
    health.succeeded("configuration");
    assert.equal(health.current.readiness, "ready");
    assert.equal(probeExit(path), 0);
    health.failed("capacity", "report rejected");
    health.succeeded("claim");
    health.succeeded("configuration");
    assert.match(health.current.reason, /capacity: report rejected/);
    health.succeeded("capacity");
    assert.equal(health.current.readiness, "ready");
    health.drain();
    assert.equal(probeExit(path), 1);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("each channel is independently fresh; a full worker need not keep claiming", () => {
  const directory = mkdtempSync(join(tmpdir(), "agent-health-"));
  try {
    const path = join(directory, "health.json");
    const health = new WorkerHealthState(path);
    health.succeeded("configuration"); health.succeeded("claim"); health.succeeded("capacity");
    const state = JSON.parse(readFileSync(path, "utf8")) as WorkerHealthSnapshot;
    state.channels.claim.checkedAt = Date.now() - HEALTH_MAX_AGE_MS - 1;
    assert.match(evaluateWorkerHealth(state).reason, /claim check is stale/);
    state.claimRequired = false;
    assert.equal(evaluateWorkerHealth(state).readiness, "ready");
    state.channels.configuration.checkedAt = Date.now() - HEALTH_MAX_AGE_MS - 1;
    assert.match(evaluateWorkerHealth(state).reason, /configuration check is stale/);
    state.channels.configuration.checkedAt = Date.now();
    state.channels.capacity.checkedAt = Date.now() - HEALTH_MAX_AGE_MS - 1;
    writeFileSync(path, JSON.stringify(state));
    assert.match(evaluateWorkerHealth(state).reason, /capacity check is stale/);
    assert.equal(probeExit(path), 1);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("health snapshot remains parseable during repeated updates", { skip: process.platform === "win32" && "Windows readers can lock the rename target" }, async () => {
  const directory = mkdtempSync(join(tmpdir(), "agent-health-"));
  try {
    const path = join(directory, "health.json");
    const health = new WorkerHealthState(path);
    const reads: Promise<unknown>[] = [];
    for (let index = 0; index < 100; index++) {
      reads.push(readFile(path, "utf8").then(JSON.parse));
      health.succeeded(index % 2 ? "configuration" : "capacity");
    }
    const snapshots = await Promise.all(reads) as WorkerHealthSnapshot[];
    assert.equal(snapshots.length, 100);
    assert.ok(snapshots.every(snapshot => snapshot.channels && snapshot.readiness));
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test("harness bootstrap only requires current wire identity", async () => {
  const directory = mkdtempSync(join(tmpdir(), "agent-harness-"));
  try {
    writeFileSync(join(directory, "PI_WIRE_IDENTITY.json"), JSON.stringify(CANVAS_PI_WIRE_IDENTITY));
    await validateHarnessArtifacts(directory);
    writeFileSync(join(directory, "PI_WIRE_IDENTITY.json"), JSON.stringify({ ...CANVAS_PI_WIRE_IDENTITY, protocolVersion: "old" }));
    await assert.rejects(validateHarnessArtifacts(directory), /incompatible/);
  } finally { rmSync(directory, { recursive: true, force: true }); }
});
