import assert from "node:assert/strict";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import test from "node:test";
import { CanvasBridge } from "../src/bridge.js";
import { EventScheduler, RunEvents, runEventSessions } from "../src/event-scheduler.js";
import { RuntimeConfigController } from "../src/runtime-config.js";
import type { PiSnapshot } from "../src/bridge.js";

const tick = () => new Promise<void>((resolve) => setImmediate(resolve));
const deferred = () => {
  let resolve!: () => void;
  const promise = new Promise<void>((yes) => { resolve = yes; });
  return { promise, resolve };
};

test("shrinking resident capacity drains existing sessions before claiming replacements", async () => {
  const stop = new AbortController();
  const events = new RunEvents();
  const setting = { dispatchConcurrency: 2, maxResidentSessions: 2 };
  const releases = [deferred(), deferred(), deferred()];
  let claims = 0;
  let entered = 0;
  const loop = runEventSessions({
    concurrency: 2, maxSessions: 2, signal: stop.signal, events,
    getConfig: () => setting,
    createBridge: () => ({
      claim: async () => {
        const index = claims++;
        return index < 3 ? { runId: `audit-${index}`, piSessionId: `session-${index}`, userId: "u", request: { canvasId: "c" } } as PiSnapshot : null;
      },
      failRun: async () => undefined,
    }),
    run: async (_bridge, run) => { const index = Number(run.runId.slice(-1)); entered++; await releases[index]!.promise; },
    onError: (_id, error) => assert.fail(String(error)),
  });
  try {
    const deadline = Date.now() + 3000;
    while (entered < 2 && Date.now() < deadline) await new Promise((resolve) => setTimeout(resolve, 5));
    assert.equal(entered, 2);
    setting.maxResidentSessions = 1;
    releases[0]!.resolve();
    await tick(); await tick();
    assert.equal(entered, 2, "a replacement must wait while resident count equals the shrunken limit");
    assert.equal(claims, 2, "no claim should be reserved at the shrunken limit");
    releases[1]!.resolve();
    const nextDeadline = Date.now() + 3000;
    while (entered < 3 && Date.now() < nextDeadline) await new Promise((resolve) => setTimeout(resolve, 5));
    assert.equal(entered, 3, "capacity release should resume claiming after the drain finishes");
  } finally {
    stop.abort();
    releases.forEach((release) => release.resolve());
    await loop;
  }
});

test("unscoped scheduler config SSE notification wakes config subscribers", async () => {
  const server = createServer((_request, response) => {
    response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
    response.write(`id: 1\ndata: ${JSON.stringify({ sequence: 1, kind: "scheduler_config_changed", revision: 2 })}\n\n`);
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address() as AddressInfo;
  const events = new RunEvents();
  const scheduler = new EventScheduler(4);
  const bridge = new CanvasBridge(`http://127.0.0.1:${address.port}`, "audit-token", "audit-worker", scheduler, events);
  const stop = new AbortController();
  let woke = false;
  const wake = new Promise<void>((resolve) => { events.subscribe("config", () => { woke = true; resolve(); }); });
  const consuming = bridge.consumeEvents(stop.signal);
  try {
    await Promise.race([wake, new Promise<never>((_, reject) => setTimeout(() => reject(new Error("config subscriber was not woken")), 3000))]);
    assert.equal(woke, true);
  } finally {
    stop.abort();
    await consuming;
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
});

test("shrinking execution slots keeps admitted steps and caps newly started steps", async () => {
  const scheduler = new EventScheduler(3);
  const gate = deferred();
  let entered = 0;
  const work = Array.from({ length: 6 }, () => scheduler.step("canvas", async () => { entered++; await gate.promise; }));
  await tick();
  assert.equal(entered, 3);
  scheduler.setConcurrency(1);
  await tick();
  assert.equal(entered, 3, "the reduction must not cancel admitted work or start queued work");
  gate.resolve();
  await Promise.all(work);
  assert.equal(scheduler.active, 0);
  assert.equal(entered, 6);
});
