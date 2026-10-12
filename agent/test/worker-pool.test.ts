import assert from "node:assert/strict";
import test from "node:test";
import type { PiSnapshot } from "../src/bridge.js";
import { parsePiWorkerConcurrency, runPiWorkerPool, type PiWorkerBridge } from "../src/worker-pool.js";
import { FatalWorkerError } from "../src/tool-disclosure.js";

function snapshot(runId: string): PiSnapshot {
  return {
    runId,
    userId: `user-${runId}`,
    revision: 1,
    status: "running",
    request: { prompt: "test" },
    modelLimits: { contextWindowTokens: 128_000, maxOutputTokens: 16_384, configured: false, source: "default" },
    canonical: { systemPrompt: "", messages: [], tools: [], toolChoice: "auto" },
    tools: [],
  };
}

function bridge(workerId: string, claim: PiWorkerBridge["claim"], failRun: PiWorkerBridge["failRun"] = async () => {}): PiWorkerBridge {
  return { workerId, claim, failRun };
}

for (const failure of [new Error('private prompt: secret'), new TypeError('private credential: secret'), 'private body']) {
  test(`unclassified ${failure instanceof Error ? failure.name : 'throw'} terminates its leased run without exposing raw content`, async () => {
    const controller = new AbortController();
    let reported = '';
    let claimed = false;
    await runPiWorkerPool({
      concurrency: 1, signal: controller.signal,
      createBridge: () => bridge('worker-internal', async () => {
        if (claimed) return null;
        claimed = true;
        return snapshot('internal-run');
      }, async (run, reason) => { assert.equal(run.runId, 'internal-run'); reported = reason; }),
      run: async () => { throw failure; },
      onError: () => { controller.abort(); },
      sleep: async () => { controller.abort(); },
    });
    assert.ok(reported, 'an internal error must reach the backend failure endpoint');
    assert.doesNotMatch(reported, /private|secret|credential/);
  });
}

test("worker concurrency is bounded and defaults to four isolated claim loops", () => {
  assert.equal(parsePiWorkerConcurrency(undefined), 4);
  assert.equal(parsePiWorkerConcurrency("2"), 2);
  for (const invalid of ["0", "17", "1.5", "NaN"]) {
    assert.throws(() => parsePiWorkerConcurrency(invalid), /CANVAS_AGENT_CONCURRENCY/);
  }
});

test("worker pool executes distinct leased runs concurrently", async () => {
  const controller = new AbortController();
  let active = 0;
  let maxActive = 0;
  const started: string[] = [];
  const workers = [0, 1].map((index) => {
    let claimed = false;
    return bridge(`worker-${index}`, async () => {
      if (claimed) return null;
      claimed = true;
      return snapshot(`run-${index}`);
    });
  });

  const timeout = setTimeout(() => controller.abort(), 1000);
  try {
    await runPiWorkerPool({
      concurrency: 2,
      signal: controller.signal,
      createBridge: (index) => workers[index]!,
      run: async (_worker, run) => {
        active++;
        maxActive = Math.max(maxActive, active);
        started.push(run.runId);
        await Promise.resolve();
        active--;
        if (started.length === 2) controller.abort();
      },
      sleep: async (_milliseconds, signal) => {
        if (signal.aborted) return;
        await new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true }));
      },
    });
  } finally {
    clearTimeout(timeout);
    controller.abort();
  }

  assert.deepEqual(started.sort(), ["run-0", "run-1"]);
  assert.equal(maxActive, 2, "both independent Pi runs should make progress at once");
});

test("deterministic worker failures are reported to Go before the loop backs off", async () => {
  const controller = new AbortController();
  let claimed = false;
  let failed = "";
  let reported = "";
  await runPiWorkerPool({
    concurrency: 1,
    signal: controller.signal,
    createBridge: () => bridge("worker-fatal", async () => {
      if (claimed) return null;
      claimed = true;
      return snapshot("fatal-run");
    }, async (run, reason) => { failed = `${run.runId}:${reason}`; }),
    run: async () => { throw new FatalWorkerError("schema mismatch"); },
    onError: (_workerId, error) => { reported = error instanceof Error ? error.message : String(error); },
    sleep: async () => { controller.abort(); },
  });
  assert.equal(failed, "fatal-run:schema mismatch");
  assert.match(reported, /schema mismatch/);
});
