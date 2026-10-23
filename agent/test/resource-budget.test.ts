import assert from "node:assert/strict";
import test from "node:test";
import { ResourceBudget, resourceBudgetFromEnv } from "../src/resource-budget.js";
import { RunEvents, runEventSessions } from "../src/event-scheduler.js";
import type { PiSnapshot } from "../src/bridge.js";

const run = (id: string): PiSnapshot => ({ runId: id, userId: "user", piSessionId: id,
  request: { canvasId: "canvas" } }) as PiSnapshot;
const tick = () => new Promise<void>(resolve => setTimeout(resolve, 10));

test("resource budget pauses at high water and resumes after two low samples", () => {
  const budget = new ResourceBudget({ maxResidentSessions: 2, memoryBudgetMb: 128, eventLoopLimitMs: 250 });
  const mb = 1024 * 1024;
  assert.equal(budget.canClaim(1, 0), true);
  assert.equal(budget.canClaim(2, 0), false);
  assert.equal(budget.observe({ rssBytes: 103 * mb, eventLoopLagMs: 0 }), true);
  assert.equal(budget.reason, "rss budget pressure");
  assert.equal(budget.canClaim(0, 0), false);
  assert.equal(budget.observe({ rssBytes: 82 * mb, eventLoopLagMs: 90 }), false);
  assert.equal(budget.observe({ rssBytes: 82 * mb, eventLoopLagMs: 90 }), true);
  assert.equal(budget.canClaim(0, 0), true);
  assert.equal(budget.observe({ rssBytes: 82 * mb, eventLoopLagMs: 250 }), true);
  assert.equal(budget.reason, "event loop pressure");
  assert.equal(budget.observe({ rssBytes: 82 * mb, eventLoopLagMs: 101 }), false);
  assert.equal(budget.observe({ rssBytes: 82 * mb, eventLoopLagMs: 0 }), false);
  assert.equal(budget.observe({ rssBytes: 82 * mb, eventLoopLagMs: 0 }), true);
});

test("resource limits reject malformed settings", () => {
  assert.throws(() => resourceBudgetFromEnv({ CANVAS_AGENT_RESIDENT_LIMIT: "0" }), /Invalid Agent resource budget/);
  assert.throws(() => resourceBudgetFromEnv({ CANVAS_AGENT_MEMORY_BUDGET_MB: "bad" }), /Invalid CANVAS_AGENT_MEMORY_BUDGET_MB/);
});

test("pressure drains active runs, releases an in-flight claim, and later resumes", async () => {
  const budget = new ResourceBudget({ maxResidentSessions: 2, memoryBudgetMb: 128, eventLoopLimitMs: 250 });
  const events = new RunEvents();
  const stop = new AbortController();
  let releaseRun!: () => void;
  const heldRun = new Promise<void>(resolve => { releaseRun = resolve; });
  let releaseClaim!: () => void;
  const heldClaim = new Promise<void>(resolve => { releaseClaim = resolve; });
  let claims = 0, started = 0, released = 0;
  const errors: string[] = [];
  const loop = runEventSessions({ concurrency: 2, maxSessions: 2, signal: stop.signal, events,
    canClaim: (active, reserved) => budget.canClaim(active, reserved),
    createBridge: () => ({ claim: async () => {
      const index = claims++;
      if (index === 1) await heldClaim;
      return index < 3 ? run(String(index)) : null;
    }, failRun: async () => {}, release: async () => { released++; } }),
    run: async (_bridge, snapshot) => { started++; if (snapshot.runId === "0") await heldRun; },
    onError: (id, error) => errors.push(`${id}: ${String(error)}`),
  });
  try {
    for (let n = 0; (started < 1 || claims < 2) && n < 100; n++) await tick();
    assert.equal(started, 1);
    budget.observe({ rssBytes: 110 * 1024 * 1024, eventLoopLagMs: 0 });
    events.wake("capacity");
    releaseClaim();
    for (let n = 0; released < 1 && n < 100; n++) await tick();
    assert.equal(released, 1);
    assert.equal(started, 1);
    releaseRun();
    for (let n = 0; claims > 2 && n < 10; n++) await tick();
    assert.equal(claims, 2);
    budget.observe({ rssBytes: 40 * 1024 * 1024, eventLoopLagMs: 0 });
    budget.observe({ rssBytes: 40 * 1024 * 1024, eventLoopLagMs: 0 });
    events.wake("capacity");
    for (let n = 0; started < 2 && n < 100; n++) await tick();
    assert.equal(started, 2);
    assert.deepEqual(errors, []);
  } finally { stop.abort(); releaseClaim(); releaseRun(); await loop; }
});

test("one run exception releases its resident slot while another run continues", async () => {
  const stop = new AbortController();
  const events = new RunEvents();
  let claimIndex = 0, healthyStarted = 0;
  let finishHealthy!: () => void;
  const healthyWait = new Promise<void>(resolve => { finishHealthy = resolve; });
  const errors: string[] = [];
  const loop = runEventSessions({ concurrency: 2, maxSessions: 2, signal: stop.signal, events,
    createBridge: () => ({ claim: async () => [run("bad"), run("healthy"), run("replacement")][claimIndex++] ?? null,
      failRun: async () => {} }),
    run: async (_bridge, snapshot) => {
      if (snapshot.runId === "bad") throw new Error("injected run error");
      if (snapshot.runId === "healthy") { healthyStarted++; await healthyWait; }
      if (snapshot.runId === "replacement") healthyStarted++;
    },
    onError: (id, error) => errors.push(`${id}: ${String(error)}`),
  });
  try {
    for (let n = 0; healthyStarted < 2 && n < 100; n++) await tick();
    assert.equal(healthyStarted, 2, "a failed run must release capacity for the replacement");
    assert.deepEqual(errors, ["bad: Error: injected run error"]);
  } finally { stop.abort(); finishHealthy(); await loop; }
});
