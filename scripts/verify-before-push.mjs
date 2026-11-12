#!/usr/bin/env bun
// Local and CI execute the same declared argv plan. --only / --skip-e2e are
// iteration aids; a partial report is not a delivery proof.
import { spawnSync } from "node:child_process";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { collectPlan } from "./verification-plan.mjs";
import {
  executePlan,
  commandRunner,
  requireCompleteResults,
} from "./verification-execution.mjs";
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const options = { base: "origin/main", mode: "affected", jobs: 4 };
for (let i = 2; i < process.argv.length; i++) {
  const arg = process.argv[i];
  if (arg === "--all") options.mode = "full";
  else if (arg === "--plan-only") options.planOnly = true;
  else if (arg === "--skip-e2e") options.skipE2e = true;
  else if (
    ["--base", "--mode", "--jobs", "--only", "--app-shards"].includes(arg)
  ) {
    const value = process.argv[++i];
    if (!value) throw new Error("Missing option value");
    options[arg.slice(2)] = value;
  } else throw new Error("Unknown option " + arg);
}
if (options.only && !["web", "backend"].includes(options.only))
  throw new Error("--only web|backend");
if (options["app-shards"] && options["app-shards"] !== "4")
  throw new Error("The shared CI contract requires four app shards");
function git(args, env) {
  const r = spawnSync("git", args, {
    cwd: root,
    encoding: "utf8",
    env: { ...process.env, ...env },
    windowsHide: true,
    maxBuffer: 64 * 1024 * 1024,
  });
  if (r.status !== 0) throw new Error("Git operation failed: " + args[0]);
  return r.stdout.trim();
}
const base = git(["merge-base", "HEAD", options.base]);
const plan = collectPlan({ root, base, mode: options.mode });
if (options.planOnly) {
  console.log(JSON.stringify(plan, null, 2));
  process.exit(0);
}
const logDir = path.join(root, ".local/cache/verify");
mkdirSync(logDir, { recursive: true });
const dirty = git(["status", "--porcelain"]);
if (dirty && plan.selectedChecks.some((c) => c.id === "web:format")) {
  const index = path.join(tmpdir(), "canvas-verification-index-" + process.pid);
  try {
    const env = { GIT_INDEX_FILE: index };
    git(["read-tree", "HEAD"], env);
    git(["add", "-A"], env);
    const tree = git(["write-tree"], env),
      head = git([
        "commit-tree",
        tree,
        "-p",
        "HEAD",
        "-m",
        "verification snapshot",
      ]);
    plan.selectedChecks.find((c) => c.id === "web:format").env.GITHUB_SHA =
      head;
  } finally {
    rmSync(index, { force: true });
  }
}
if (dirty)
  console.log(
    "Working tree changes included; rerun on the clean commit before pushing",
  );
if (options.only || options.skipE2e) {
  plan.selectedChecks = plan.selectedChecks.filter(
    (c) =>
      (!options.only || c.id.startsWith(options.only + ":")) &&
      (!options.skipE2e || c.id !== "web:e2e"),
  );
  plan.notCovered.push(
    "Iteration-only filters applied; this is not a complete delivery result",
  );
}
writeFileSync(
  path.join(logDir, "verification-plan.json"),
  JSON.stringify(plan, null, 2),
);
const start = Date.now();
const report = await executePlan(plan, {
  jobs: Number(options.jobs),
  run: commandRunner(root, logDir),
});
writeFileSync(
  path.join(logDir, "result-local.json"),
  JSON.stringify(report, null, 2),
);
const failed = report.results.filter((r) => r.status !== "success");
console.log(
  "共 " +
    report.results.length +
    " 项，失败 " +
    failed.length +
    " 项，用时 " +
    ((Date.now() - start) / 1000).toFixed(1) +
    "s",
);
for (const item of plan.notCovered) console.log("本地未覆盖/边界：" + item);
try {
  requireCompleteResults(plan, [report]);
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
