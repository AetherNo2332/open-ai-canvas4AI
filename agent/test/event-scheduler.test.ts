import assert from "node:assert/strict";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import test from "node:test";
import type { PiSnapshot } from "../src/bridge.js";
import { CanvasBridge } from "../src/bridge.js";
import { EventScheduler, RunEvents, runEventSessions } from "../src/event-scheduler.js";

function deferred<T = void>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function snapshot(runId: string, piSessionId = runId): PiSnapshot {
  return {
    runId, piSessionId, userId: "user", request: { prompt: "test", canvasId: "canvas" },
  } as PiSnapshot;
}

test("queued reporting aborts immediately and never sends after a slot opens", async () => {
  const scheduler = new EventScheduler(1);
  const gate = deferred();
  const occupied = scheduler.step("busy", () => gate.promise);
  const abort = new AbortController();
  let sent = 0;
  const waiting = scheduler.step("report", async () => { sent++; }, abort.signal);
  abort.abort(new Error("report deadline"));
  const result = await Promise.race([waiting.catch(error => error.message), new Promise(resolve => setTimeout(() => resolve("still queued"), 40))]);
  gate.resolve(); await occupied;
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(result, "report deadline");
  assert.equal(sent, 0);
  assert.equal(scheduler.queued, 0);
});

test("EventScheduler bounds concurrent steps and rotates fairly across projects", async () => {
  const scheduler = new EventScheduler(4);
  const release = deferred();
  const entered: string[] = [];
  let running = 0;
  let peak = 0;
  const step = (project: string, name: string) => scheduler.step(project, async () => {
    entered.push(name);
    running += 1;
    peak = Math.max(peak, running);
    await release.promise;
    running -= 1;
    return name;
  });

  const tasks = [
    step("a", "a1"), step("a", "a2"), step("a", "a3"), step("a", "a4"),
    step("b", "b1"), step("c", "c1"),
  ];
  // Let the initial four admissions settle before releasing capacity.
  while (entered.length < 4) await new Promise((resolve) => setImmediate(resolve));
  assert.equal(peak, 4);
  release.resolve();
  assert.deepEqual(await Promise.all(tasks), ["a1", "a2", "a3", "a4", "b1", "c1"]);
  assert.equal(peak, 4);
  // The second round should visit b and c before allowing project a's backlog to dominate.
  assert.deepEqual(entered.slice(4), ["b1", "c1"]);
});

test("RunEvents subscribes before durable read so a wake during read is not lost", async () => {
  const events = new RunEvents();
  let reads = 0;
  const result = await events.wait("r1", async () => {
    reads += 1;
    if (reads === 1) events.wake("r1");
    return reads === 1 ? "pending" : "done";
  }, (value) => value === "pending");
  assert.equal(result, "done");
  assert.equal(reads, 2);
});

test("RunEvents wake is a repeatable hint and duplicate wakes do not duplicate a waiter", async () => {
  const events = new RunEvents();
  const readGate = deferred<void>();
  let reads = 0;
  const waiting = events.wait("r2", async () => {
    reads += 1;
    if (reads === 1) await readGate.promise;
    return reads < 2 ? "pending" : "done";
  }, (value) => value === "pending");
  await new Promise((resolve) => setImmediate(resolve));
  events.wake("r2");
  events.wake("r2");
  readGate.resolve();
  assert.equal(await waiting, "done");
  assert.equal(reads, 2);
});

test("RunEvents aborts a pending wait and recover wakes current subscribers", async () => {
  const events = new RunEvents();
  const controller = new AbortController();
  const waiting = events.wait("r3", async () => "pending", () => true, controller.signal);
  await new Promise((resolve) => setImmediate(resolve));
  controller.abort(new Error("stopped"));
  await assert.rejects(waiting, /stopped/);

  let reads = 0;
  const recovered = events.wait("r4", async () => ++reads === 1 ? "pending" : "done", (v) => v === "pending");
  await new Promise((resolve) => setImmediate(resolve));
  events.recover();
  assert.equal(await recovered, "done");
});

test("RunEvents.pause wakes immediately on dispatch and capacity notifications", async () => {
  const events = new RunEvents();
  const dispatch = events.pause("dispatch", 10_000);
  events.wake("dispatch");
  await dispatch;
  const capacity = events.pause("capacity", 10_000);
  events.wake("capacity");
  await capacity;
});

test("consumeEvents resumes from its cursor and ignores replayed sequences", async () => {
  const connections: string[] = [];
  const responses = new Set<import("node:http").ServerResponse>();
  let secondConnectionSent = false;
  const server = createServer((request, response) => {
    if (request.url !== "/internal-agent/events") {
      response.writeHead(404).end();
      return;
    }
    const cursorHeader = request.headers["last-event-id"];
    connections.push(Array.isArray(cursorHeader) ? (cursorHeader[0] ?? "") : (cursorHeader ?? ""));
    response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
    responses.add(response);
    request.on("close", () => responses.delete(response));
    if (connections.length === 1) {
      response.write(`id: 5\ndata: ${JSON.stringify({ sequence: 5, runId: "resume-run", kind: "tool_changed" })}\n\n`);
      response.end();
    } else if (connections.length === 2) {
      response.write(`id: 5\ndata: ${JSON.stringify({ sequence: 5, runId: "resume-run", kind: "tool_changed" })}\n\n`);
      response.write(`id: 6\ndata: ${JSON.stringify({ sequence: 6, runId: "resume-run", kind: "run_changed" })}\n\n`);
      secondConnectionSent = true;
    }
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address() as AddressInfo;
  const events = new RunEvents();
  let wakeCount = 0;
  let controlWakeCount = 0;
  let dispatchWakeCount = 0;
  const unsubscribe = events.subscribe("resume-run", () => { wakeCount += 1; });
  const unsubscribeControl = events.subscribe("control:resume-run", () => { controlWakeCount += 1; });
  const unsubscribeDispatch = events.subscribe("dispatch", () => { dispatchWakeCount += 1; });
  const controller = new AbortController();
  const bridge = new CanvasBridge(`http://127.0.0.1:${address.port}`, "test-token", "test-worker", undefined, events);
  const consuming = bridge.consumeEvents(controller.signal);
  try {
    const deadline = Date.now() + 5000;
    while ((!secondConnectionSent || wakeCount < 2 || controlWakeCount < 1 || dispatchWakeCount < 1) && Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    assert.deepEqual(connections.slice(0, 2), ["0", "5"]);
    assert.equal(wakeCount, 2, "replayed sequence 5 should be ignored while sequence 6 wakes once");
    assert.equal(controlWakeCount, 1, "run_changed should wake the control subscriber");
    assert.equal(dispatchWakeCount, 1, "run_changed should wake dispatch waiters");
  } finally {
    controller.abort();
    await consuming;
    unsubscribe();
    unsubscribeControl();
    unsubscribeDispatch();
    for (const response of responses) response.end();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
});

test("runEventSessions keeps sixteen blocked sessions resident while four claimers keep claiming", async () => {
  const controller = new AbortController();
  const releaseRuns = deferred<void>();
  const snapshots = Array.from({ length: 16 }, (_, i) => snapshot(`run-${i}`, `session-${i}`));
  let claimIndex = 0;
  let claims = 0;
  let running = 0;
  let peak = 0;
  const errors: unknown[] = [];
  const runLoop = runEventSessions({
    concurrency: 4,
    maxSessions: 64,
    signal: controller.signal,
    createBridge: () => ({
      claim: async () => {
        claims += 1;
        const index = claimIndex++;
        return snapshots[index] ?? null;
      },
      failRun: async () => undefined,
    }),
    run: async () => {
      running += 1;
      peak = Math.max(peak, running);
      await releaseRuns.promise;
      running -= 1;
    },
    onError: (_id, error) => errors.push(error),
    events: new RunEvents(),
  });

  const deadline = Date.now() + 3000;
  while ((running < 16 || claims < 17) && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  assert.equal(running, 16, "all resident sessions should run concurrently");
  assert.ok(claims >= 17, "claim slots should continue after sessions enter their long waits");
  assert.equal(peak, 16);
  assert.deepEqual(errors, []);
  controller.abort();
  releaseRuns.resolve();
  await runLoop;
});

test("runEventSessions accepts the 64 resident-session ceiling and rejects limits above it", async () => {
  const controller = new AbortController();
  const noop = {
    concurrency: 4,
    signal: controller.signal,
    createBridge: () => ({ claim: async () => null, failRun: async () => undefined }),
    run: async () => undefined,
    onError: () => undefined,
    events: new RunEvents(),
  };
  const accepted = runEventSessions({ ...noop, maxSessions: 64 });
  controller.abort();
  await accepted;

  const invalidController = new AbortController();
  await assert.rejects(runEventSessions({
    ...noop, signal: invalidController.signal, maxSessions: 65,
  }), /Invalid resident session limit/);
  invalidController.abort();
});

test("runEventSessions wakes capacity waiters when a resident session releases its slot", async () => {
  const controller = new AbortController();
  const releaseFirst = deferred<void>();
  const events = new RunEvents();
  const runs = [snapshot("capacity-1"), snapshot("capacity-2")];
  let claimIndex = 0;
  let starts = 0;
  const loop = runEventSessions({
    concurrency: 1,
    maxSessions: 1,
    signal: controller.signal,
    createBridge: () => ({
      claim: async () => runs[claimIndex++] ?? null,
      failRun: async () => undefined,
    }),
    run: async () => {
      starts += 1;
      if (starts === 1) await releaseFirst.promise;
    },
    onError: (_id, error) => assert.fail(String(error)),
    events,
  });
  const deadline = Date.now() + 3000;
  while (starts < 1 && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(starts, 1);
  releaseFirst.resolve();
  while (starts < 2 && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(starts, 2, "completed session should wake a dispatcher paused at capacity");
  controller.abort();
  await loop;
});

test("runEventSessions reports capacity while every resident slot remains occupied", async () => {
  const controller = new AbortController();
  const releaseRun = deferred<void>();
  const capacityReport = deferred<{ active: number; capacity: number }>();
  const events = new RunEvents();
  let claimCount = 0;
  const loop = runEventSessions({
    concurrency: 1,
    maxSessions: 1,
    signal: controller.signal,
    createBridge: () => ({
      claim: async () => claimCount++ === 0 ? snapshot("capacity-watchdog") : null,
      failRun: async () => undefined,
    }),
    run: async () => releaseRun.promise,
    onError: (_id, error) => assert.fail(String(error)),
    events,
    reportCapacity: async (active, capacity) => capacityReport.resolve({ active, capacity }),
  });
  let heartbeatTimeout: ReturnType<typeof setTimeout> | undefined;
  try {
    const report = await Promise.race([
      capacityReport.promise,
      new Promise<never>((_, reject) => { heartbeatTimeout = setTimeout(() => reject(new Error("capacity heartbeat did not fire")), 18_000); }),
    ]);
    assert.deepEqual(report, { active: 1, capacity: 1 });
  } finally {
    if (heartbeatTimeout) clearTimeout(heartbeatTimeout);
    controller.abort();
    releaseRun.resolve();
    await loop;
  }
});

test("runEventSessions isolates duplicate active leases for the same session", async () => {
  const controller = new AbortController();
  const releaseRuns = deferred<void>();
  const first = snapshot("run-a", "same-session");
  const duplicate = snapshot("run-b", "same-session");
  let next = 0;
  let starts = 0;
  const errors: Array<{ id: string; error: unknown }> = [];
  const loop = runEventSessions({
    concurrency: 2,
    maxSessions: 4,
    signal: controller.signal,
    createBridge: () => ({
      claim: async () => [first, duplicate][next++] ?? null,
      failRun: async () => undefined,
    }),
    run: async () => { starts += 1; await releaseRuns.promise; },
    onError: (id, error) => errors.push({ id, error }),
    events: new RunEvents(),
  });
  const deadline = Date.now() + 2000;
  while (errors.length === 0 && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(starts, 1);
  assert.equal(errors.length, 1);
  assert.match(String(errors[0]?.error), /Duplicate active session lease/);
  controller.abort();
  releaseRuns.resolve();
  await loop;
});
