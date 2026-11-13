import test from "node:test";
import assert from "node:assert/strict";
import {
  executePlan,
  requireCompleteResults,
  partitionChecks,
  commandRunner,
} from "./verification-execution.mjs";
import { mkdtempSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

test("orphaned child output pipes fail within a bounded drain window", async () => {
  const dir = mkdtempSync(path.join(tmpdir(), "verify-orphan-")),
    pidFile = path.join(dir, "child.pid");
  const script = `const {spawn}=require('node:child_process'); const fs=require('node:fs'); const child=spawn(process.execPath,['-e','setTimeout(()=>{},60000)'],{detached:true,windowsHide:true,stdio:['ignore',process.stdout,process.stderr]});fs.writeFileSync(${JSON.stringify(pidFile)},String(child.pid));child.unref();process.exit(0);`;
  try {
    const run = commandRunner(dir, dir, {
      drainTimeoutMs: 100,
      timeoutMs: 5000,
    });
    const result = await Promise.race([
      run({
        id: "orphan",
        cwd: ".",
        cmd: process.execPath,
        args: ["-e", script],
      }),
      new Promise((resolve) =>
        setTimeout(() => resolve({ status: "late" }), 1000),
      ),
    ]);
    assert.equal(result.status, "failed");
  } finally {
    if (existsSync(pidFile)) {
      try {
        process.kill(Number(readFileSync(pidFile, "utf8")));
      } catch {}
    }
  }
});

const check = (id, dependencies = []) => ({
  id,
  cwd: ".",
  cmd: "node",
  args: [],
  dependencies,
});
test("failure propagates and dependent checks never execute", async () => {
  const seen = [];
  const p = { selectedChecks: [check("a"), check("b", ["a"])] };
  const r = await executePlan(p, {
    run: async (c) => {
      seen.push(c.id);
      return { status: "failed" };
    },
    jobs: 2,
  });
  assert.deepEqual(seen, ["a"]);
  assert.throws(() => requireCompleteResults(p, [r]));
});
test("missing, unexpected, duplicate, unavailable results all fail closed", () => {
  const p = { selectedChecks: [check("a")] };
  for (const results of [
    [],
    [{ id: "a", status: "unavailable" }],
    [{ id: "other", status: "success" }],
    [
      { id: "a", status: "success" },
      { id: "a", status: "success" },
    ],
  ])
    assert.throws(() => requireCompleteResults(p, [{ results }]));
  assert.doesNotThrow(() =>
    requireCompleteResults(p, [{ results: [{ id: "a", status: "success" }] }]),
  );
});
test("bad dependency graphs fail instead of hanging", async () => {
  await assert.rejects(() =>
    executePlan(
      { selectedChecks: [check("a", ["missing"])] },
      { run: async () => ({ status: "success" }) },
    ),
  );
  await assert.rejects(() =>
    executePlan(
      { selectedChecks: [check("a", ["b"]), check("b", ["a"])] },
      { run: async () => ({ status: "success" }) },
    ),
  );
});
test("backend partitions cover plan exactly once and preserve affected tests", () => {
  const p = {
    selectedChecks: [
      check("backend:full"),
      check("backend:app-shard-1"),
      check("backend:app:memory"),
      check("web:test"),
    ],
  };
  const parts = [
    "backend-rest",
    "backend-app-1",
    "backend-app-2",
    "backend-app-3",
    "backend-app-4",
    "web",
  ].flatMap((part) => partitionChecks(p, part));
  assert.deepEqual(
    parts.map((c) => c.id).sort(),
    p.selectedChecks.map((c) => c.id).sort(),
  );
});
test("command launch rejection becomes failed result, formatting stdout is checked", async () => {
  const p = { selectedChecks: [check("a")] };
  const r = await executePlan(p, {
    run: async () => {
      throw new Error("cannot launch");
    },
  });
  assert.throws(() => requireCompleteResults(p, [r]));
});
test("actual missing executable and formatting output cannot pass", async () => {
  const dir = mkdtempSync(path.join(tmpdir(), "verify-run-"));
  const run = commandRunner(dir, dir);
  const missing = await run({
    id: "missing",
    cwd: ".",
    cmd: "nonexistent-canvas-verification-command",
    args: [],
  });
  assert.equal(missing.status, "failed");
  const format = await run({
    id: "format",
    cwd: ".",
    cmd: process.execPath,
    args: ["-e", 'console.log("unformatted.go")'],
    expectEmptyOutput: true,
  });
  assert.equal(format.status, "failed");
});
test("an app integration selector matching no tests cannot pass", async () => {
  const dir = mkdtempSync(path.join(tmpdir(), "verify-no-tests-"));
  const result = await commandRunner(
    dir,
    dir,
  )({
    id: "backend:app:fixture",
    cwd: ".",
    cmd: process.execPath,
    args: ["-e", 'console.log("ok  package 0.1s [no tests to run]")'],
  });
  assert.equal(result.status, "failed");
});
test(
  "installed npm can execute without shell on Windows",
  { skip: process.platform !== "win32" },
  async () => {
    const dir = mkdtempSync(path.join(tmpdir(), "verify-npm-"));
    const result = await commandRunner(
      dir,
      dir,
    )({ id: "npm", cwd: ".", cmd: "npm", args: ["--version"] });
    assert.equal(result.status, "success");
  },
);
