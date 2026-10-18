import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { CanvasBridge } from "../src/bridge.js";
import { ToolSnapshotRegistration, loadToolSchema } from "../src/tool-snapshot-registration.js";
import { assertToolSnapshotMatchesSchema } from "../src/tool-disclosure.js";

const artifact = { schemaVersion: "test/v1", tools: [{ type: "function", function: { name: "test", description: "full manifest", parameters: { type: "object" } } }] };
const report = { active: 0, capacity: 4, claimReservations: 0, dispatchActive: 0, readyQueued: 0, draining: false, appliedConfigRevision: 1 };
const ok = (data: unknown) => new Response(JSON.stringify({ code: 0, data }));

test("one loaded manifest registers once; all slots and heartbeats share process identity", async () => {
  const original = globalThis.fetch;
  const calls: Array<{ path: string; body: any }> = [];
  const registration = new ToolSnapshotRegistration({ artifact }, "build-test", "process", "boot");
  const bridges = ["slot-1", "slot-2"].map(owner => new CanvasBridge("http://backend", "token", owner, undefined, undefined, undefined, registration));
  globalThis.fetch = (async (url, init) => {
    const path = String(url); calls.push({ path, body: JSON.parse(String(init?.body)) });
    return ok(path.endsWith("/register") ? { snapshotId: "snapshot" } : { run: null });
  }) as typeof fetch;
  try {
    await Promise.all(bridges.map(bridge => bridge.claim()));
    await bridges[0]!.capacityReport(report);
    assert.equal(calls.filter(call => call.path.endsWith("/register")).length, 1);
    assert.deepEqual(calls[0]!.body.artifact, artifact);
    for (const call of calls.slice(1)) {
      assert.equal(call.body.instanceId, "process"); assert.equal(call.body.bootId, "boot");
      assert.equal(call.body.snapshotId, "snapshot"); assert.equal(call.body.loadGeneration, 1);
      assert.equal(call.body.artifact, undefined);
    }
    assert.deepEqual(calls.filter(call => call.path.endsWith("/claim")).map(call => call.body.owner), ["slot-1", "slot-2"]);
  } finally { globalThis.fetch = original; }
});

test("missing or invalid loaded artifacts register errors and heartbeat but never claim", async () => {
  const directory = await mkdtemp(join(process.cwd(), "dist", "canvas-tool-schema-"));
  try {
  for (const contents of [undefined, "{broken", JSON.stringify({ schemaVersion: "x", tools: [artifact.tools[0], artifact.tools[0]] })]) {
    if (contents !== undefined) await writeFile(join(directory, "TOOL_SCHEMA.json"), contents);
    const loaded = await loadToolSchema(directory);
    assert.ok(loaded.loadError); assert.equal(loaded.artifact, undefined);
    const original = globalThis.fetch; const paths: string[] = [];
    globalThis.fetch = (async (url, init) => {
      paths.push(String(url)); const body = JSON.parse(String(init?.body));
      if (String(url).endsWith("/register")) { assert.ok(body.loadError); return ok({ snapshotId: "invalid" }); }
      assert.equal(body.snapshotId, "invalid"); return ok({});
    }) as typeof fetch;
    try {
      const registration = new ToolSnapshotRegistration(loaded, "build");
      const bridge = new CanvasBridge("http://backend", "token", "slot", undefined, undefined, undefined, registration);
      assert.equal(await bridge.claim(), null); await bridge.capacityReport(report);
      assert.equal(paths.some(path => path.endsWith("/claim")), false);
    } finally { globalThis.fetch = original; }
  }
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("lost server registration recovers once without granting stale identities", async () => {
  const original = globalThis.fetch; let registers = 0; const claimed: string[] = [];
  globalThis.fetch = (async (url, init) => {
    if (String(url).endsWith("/register")) return ok({ snapshotId: `snapshot-${++registers}` });
    const body = JSON.parse(String(init?.body)); claimed.push(body.snapshotId);
    if (body.snapshotId === "snapshot-1") return new Response(JSON.stringify({ reason: "tool_snapshot_registration_required" }), { status: 409 });
    return ok({ run: null });
  }) as typeof fetch;
  try {
    const registration = new ToolSnapshotRegistration({ artifact }, "build");
    const bridge = new CanvasBridge("http://backend", "token", "slot", undefined, undefined, undefined, registration);
    await bridge.claim(); assert.equal(registers, 2); assert.deepEqual(claimed, ["snapshot-1", "snapshot-2"]);
    registration.invalidate("snapshot-1");
    await bridge.claim(); assert.equal(registers, 2); assert.equal(claimed.at(-1), "snapshot-2");
  } finally { globalThis.fetch = original; }
});

test("registration failure backs off and never reaches claim or legacy bypass", async () => {
  const original = globalThis.fetch; let requests = 0;
  globalThis.fetch = (async () => { requests++; return new Response("", { status: 404 }); }) as typeof fetch;
  try {
    const registration = new ToolSnapshotRegistration({ artifact }, "build");
    const bridge = new CanvasBridge("http://backend", "token", "slot", undefined, undefined, undefined, registration);
    await assert.rejects(bridge.claim(), /404/); await assert.rejects(bridge.claim()); assert.equal(requests, 1);
  } finally { globalThis.fetch = original; }
});

test("registration and the final checker keep the loaded artifact after disk edits", async () => {
  const directory = await mkdtemp(join(process.cwd(), "dist", "canvas-tool-schema-"));
  try {
    await writeFile(join(directory, "TOOL_SCHEMA.json"), JSON.stringify(artifact));
    const loaded = await loadToolSchema(directory);
    await writeFile(join(directory, "TOOL_SCHEMA.json"), JSON.stringify({ ...artifact, tools: [] }));
    const registration = new ToolSnapshotRegistration(loaded, "build");
    await registration.ensure(async request => {
      assert.deepEqual(request.artifact, artifact); return { snapshotId: "loaded" };
    });
    assertToolSnapshotMatchesSchema([{ name: "test", description: "", parameters: { type: "object" }, allowed: true }], loaded.artifact!);
    assert.throws(() => assertToolSnapshotMatchesSchema([{ name: "test", description: "", parameters: { type: "object", description: "drift" }, allowed: true }], loaded.artifact!), /schema differs/);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("outage recovery uses bounded backoff and a single registration retry", async () => {
  const originalNow = Date.now; let now = 1000; let attempts = 0;
  Date.now = () => now;
  try {
    const registration = new ToolSnapshotRegistration({ artifact }, "build");
    const register = async () => { if (++attempts === 1) throw new Error("backend unavailable"); return { snapshotId: "recovered" }; };
    await assert.rejects(registration.ensure(register), /unavailable/);
    await assert.rejects(registration.ensure(register), /unavailable/); assert.equal(attempts, 1);
    now += 1000;
    await Promise.all([registration.ensure(register), registration.ensure(register)]);
    assert.equal(attempts, 2); assert.equal(registration.identity.snapshotId, "recovered");
  } finally { Date.now = originalNow; }
});

let deepValue: unknown = { type: "string" };
for (let i = 0; i < 49; i++) deepValue = { nested: deepValue };
const invalidManifests: Array<[string, unknown]> = [
  ["Go-escaped Unicode separators exceed 2 MiB", { ...artifact, tools: [{ ...artifact.tools[0], function: { ...artifact.tools[0]!.function, description: "\u2028".repeat(350000) } }] }],
  ["parameters without object type", { ...artifact, tools: [{ ...artifact.tools[0], function: { ...artifact.tools[0]!.function, parameters: {} } }] }],
  ["parameters nested beyond 48 levels", { ...artifact, tools: [{ ...artifact.tools[0], function: { ...artifact.tools[0]!.function, parameters: { type: "object", extra: deepValue } } }] }],
  ["file larger than 2 MiB", { ...artifact, extra: "x".repeat(2 * 1024 * 1024) }],
  ["more than 256 tools", { ...artifact, tools: Array.from({ length: 257 }, (_, i) => ({ type: "function", function: { name: `tool-${i}`, parameters: { type: "object" } } })) }],
  ["schema version larger than 120 UTF-8 bytes", { ...artifact, schemaVersion: "界".repeat(41) }],
  ["tool name larger than 160 UTF-8 bytes", { ...artifact, tools: [{ ...artifact.tools[0], function: { ...artifact.tools[0]!.function, name: "界".repeat(54) } }] }],
];

test("Go-size validation does not escape ordinary ASCII letters", async () => {
  const directory = await mkdtemp(join(process.cwd(), "dist", "canvas-tool-schema-"));
  try {
    await writeFile(join(directory, "TOOL_SCHEMA.json"), JSON.stringify({ ...artifact, tools: [{ ...artifact.tools[0], function: { ...artifact.tools[0]!.function, description: "u".repeat(400000) } }] }));
    const loaded = await loadToolSchema(directory);
    assert.equal(loaded.loadError, undefined);
    assert.ok(loaded.artifact);
  } finally { await rm(directory, { recursive: true, force: true }); }
});
for (const [label, invalidManifest] of invalidManifests) {
  test(`invalid ${label} registers a load error and heartbeats without claiming`, async () => {
    const directory = await mkdtemp(join(process.cwd(), "dist", "canvas-tool-schema-"));
    const original = globalThis.fetch;
    const paths: string[] = [];
    try {
      await writeFile(join(directory, "TOOL_SCHEMA.json"), JSON.stringify(invalidManifest));
      const loaded = await loadToolSchema(directory);
      assert.ok(loaded.loadError, "Invalid artifact must become an observable load error");
      assert.equal(loaded.artifact, undefined);
      const registration = new ToolSnapshotRegistration(loaded, "build");
      assert.equal(registration.eligible, false);
      globalThis.fetch = (async (url, init) => {
        const path = String(url); paths.push(path); const body = JSON.parse(String(init?.body));
        if (path.endsWith("/register")) {
          assert.ok(body.loadError); assert.deepEqual(body.artifact.tools, []); return ok({ snapshotId: "invalid-load" });
        }
        assert.equal(body.snapshotId, "invalid-load"); return ok({});
      }) as typeof fetch;
      const bridge = new CanvasBridge("http://backend", "token", "slot", undefined, undefined, undefined, registration);
      assert.equal(await bridge.claim(), null); await bridge.capacityReport(report);
      assert.equal(paths.filter(path => path.endsWith("/register")).length, 1);
      assert.equal(paths.some(path => path.endsWith("/claim")), false);
      assert.ok(paths.some(path => path.endsWith("/capacity")));
    } finally { globalThis.fetch = original; await rm(directory, { recursive: true, force: true }); }
  });
}

test("a static server registration rejection never supplies a healthy snapshot or claims", async () => {
  const original = globalThis.fetch;
  const calls: Array<{ path: string; body: any }> = [];
  globalThis.fetch = (async (url, init) => {
    const path = String(url); const body = JSON.parse(String(init?.body)); calls.push({ path, body });
    if (path.endsWith("/register")) return new Response(JSON.stringify({ msg: "Unsupported artifact" }), { status: 400 });
    return ok({});
  }) as typeof fetch;
  try {
    const registration = new ToolSnapshotRegistration({ artifact }, "build");
    const bridge = new CanvasBridge("http://backend", "token", "slot", undefined, undefined, undefined, registration);
    await assert.rejects(bridge.claim(), /400/);
    await assert.rejects(bridge.capacityReport(report), /400/);
    assert.equal(registration.identity.snapshotId, undefined);
    assert.equal(calls.filter(call => call.path.endsWith("/register")).length, 1);
    assert.equal(calls.some(call => call.path.endsWith("/claim")), false);
    const heartbeat = calls.find(call => call.path.endsWith("/capacity"));
    assert.ok(heartbeat); assert.equal(heartbeat.body.snapshotId, undefined);
    assert.equal(heartbeat.body.instanceId, undefined);
    assert.equal(heartbeat.body.owner, registration.identity.instanceId);
  } finally { globalThis.fetch = original; }
});

test("heartbeats observe file metadata changes without replacing the loaded snapshot or eligibility", async () => {
  const directory = await mkdtemp(join(process.cwd(), "dist", "canvas-tool-schema-"));
  const path = join(directory, "TOOL_SCHEMA.json");
  const original = globalThis.fetch;
  const calls: Array<{ path: string; body: any }> = [];
  try {
    await writeFile(path, JSON.stringify(artifact));
    const loaded = await loadToolSchema(directory);
    const registration = new ToolSnapshotRegistration(loaded, "build");
    globalThis.fetch = (async (url, init) => {
      const route = String(url); const body = JSON.parse(String(init?.body)); calls.push({ path: route, body });
      return ok(route.endsWith("/register") ? { snapshotId: "original-loaded" } : { run: null });
    }) as typeof fetch;
    const bridge = new CanvasBridge("http://backend", "token", "slot", undefined, undefined, undefined, registration);
    await bridge.capacityReport(report);
    assert.equal(calls.at(-1)!.body.artifactFileState, "loaded");
    await writeFile(path, JSON.stringify({ ...artifact, diskOnly: "changed-size" }));
    await bridge.capacityReport(report);
    assert.equal(calls.at(-1)!.body.artifactFileState, "changed");
    await rm(path);
    await bridge.capacityReport(report);
    assert.equal(calls.at(-1)!.body.artifactFileState, "unavailable");
    await bridge.claim();
    assert.equal(calls.at(-1)!.body.snapshotId, "original-loaded");
    assert.equal(calls.filter(call => call.path.endsWith("/register")).length, 1);
    assert.deepEqual(registration.request.artifact, artifact);
    assert.equal(registration.eligible, true);
  } finally { globalThis.fetch = original; await rm(directory, { recursive: true, force: true }); }
});
