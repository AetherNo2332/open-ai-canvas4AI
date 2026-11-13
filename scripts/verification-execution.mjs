import { spawn, spawnSync } from "node:child_process";
import {
  mkdirSync,
  writeFileSync,
  readdirSync,
  readFileSync,
  createWriteStream,
  existsSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export function partitionChecks(plan, partition) {
  return plan.selectedChecks.filter((c) => {
    if (!partition || partition === "all") return true;
    const shard = /^backend:app-shard-(\d)$/.exec(c.id);
    if (partition.startsWith("backend-app-"))
      return shard && partition === `backend-app-${shard[1]}`;
    if (partition === "backend-rest")
      return c.id.startsWith("backend:") && !shard;
    if (partition === "smoke")
      return c.id === "agent:smoke" || c.id.startsWith("deployment:");
    if (partition === "agent")
      return c.id.startsWith("agent:") && c.id !== "agent:smoke";
    if (partition === "meta")
      return c.id === "changelog" || c.id.startsWith("verification:");
    return c.id.startsWith(partition + ":");
  });
}

export async function executePlan(plan, { run, jobs = 4 } = {}) {
  if (!Number.isInteger(jobs) || jobs < 1)
    throw new Error("jobs must be positive");
  const checks = plan.selectedChecks,
    ids = new Set(checks.map((c) => c.id));
  if (ids.size !== checks.length) throw new Error("duplicate check ID");
  for (const c of checks)
    for (const d of c.dependencies || [])
      if (!ids.has(d)) throw new Error(`missing dependency ${d}`);
  const pending = new Map(checks.map((c) => [c.id, c])),
    results = new Map();
  while (pending.size) {
    const ready = [...pending.values()]
      .filter((c) => (c.dependencies || []).every((d) => results.has(d)))
      .slice(0, jobs);
    if (!ready.length) throw new Error("cyclic dependency graph");
    await Promise.all(
      ready.map(async (c) => {
        pending.delete(c.id);
        const started = Date.now();
        let result;
        if (
          (c.dependencies || []).some(
            (d) => results.get(d).status !== "success",
          )
        )
          result = { status: "blocked", reason: "dependency failed" };
        else
          try {
            result = await run(c);
          } catch {
            result = { status: "failed", reason: "command could not execute" };
          }
        results.set(c.id, { ...result, id: c.id, ms: Date.now() - started });
      }),
    );
  }
  return { schemaVersion: 1, results: [...results.values()] };
}

export function requireCompleteResults(plan, reports) {
  const expected = new Set(plan.selectedChecks.map((c) => c.id)),
    actual = new Set();
  for (const report of reports)
    for (const r of report.results || []) {
      if (!expected.has(r.id) || actual.has(r.id) || r.status !== "success")
        throw new Error(`verification incomplete or failed: ${r.id}`);
      actual.add(r.id);
    }
  for (const id of expected)
    if (!actual.has(id)) throw new Error(`missing required result: ${id}`);
  return actual.size;
}

function killTree(child) {
  if (!child.pid) return;
  if (process.platform === "win32")
    spawnSync("taskkill", ["/pid", String(child.pid), "/T", "/F"], {
      windowsHide: true,
      stdio: "ignore",
    });
  else
    try {
      process.kill(-child.pid, "SIGTERM");
    } catch {}
}

export function commandRunner(
  root,
  logDir,
  { timeoutMs = 25 * 60 * 1000, drainTimeoutMs = 1000 } = {},
) {
  mkdirSync(logDir, { recursive: true });
  let interrupted = false;
  return (c) =>
    new Promise((resolve) => {
      if (interrupted) {
        resolve({ status: "blocked", reason: "interrupted" });
        return;
      }
      const log = createWriteStream(
        path.join(logDir, c.id.replace(/[^a-z0-9.-]/gi, "-") + ".log"),
      );
      const invocation = platformInvocation(c.cmd, c.args);
      const child = spawn(invocation.cmd, invocation.args, {
        cwd: path.resolve(root, c.cwd),
        env: { ...process.env, ...c.env },
        windowsHide: true,
        detached: process.platform !== "win32",
        stdio: ["ignore", "pipe", "pipe"],
      });
      let output = "",
        error = false;
      let settled = false,
        drainTimer;
      const collect = (b) => {
        if (settled) return;
        log.write(b);
        output += b;
      };
      child.stdout.on("data", collect);
      child.stderr.on("data", collect);
      child.on("error", () => {
        error = true;
      });
      const timer = setTimeout(() => {
        error = true;
        killTree(child);
        finish(null, "command timeout");
      }, timeoutMs);
      const stop = () => {
        interrupted = true;
        error = true;
        killTree(child);
        finish(null, "interrupted");
      };
      process.once("SIGINT", stop);
      process.once("SIGTERM", stop);
      child.on("exit", (code) => {
        drainTimer = setTimeout(() => {
          error = true;
          finish(code, "child output pipes did not close");
        }, drainTimeoutMs);
      });
      child.on("close", (code) => finish(code));
      function finish(code, reason) {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        clearTimeout(drainTimer);
        process.removeListener("SIGINT", stop);
        process.removeListener("SIGTERM", stop);
        log.end();
        child.stdout.destroy();
        child.stderr.destroy();
        const emptyAppSelection =
          c.id.startsWith("backend:app:") &&
          /^ok\s+\S+\s+.*\[no tests to run\]\s*$/m.test(output);
        const ok =
          !emptyAppSelection &&
          !error &&
          code === 0 &&
          !(c.expectEmptyOutput && output.trim());
        if (c.id === "backend:full" || c.id.startsWith("backend:app-shard-")) {
          writeFileSync(
            path.join(logDir, c.id.replaceAll(":", "-") + ".jsonl"),
            output
              .split(/\r?\n/)
              .filter((l) => l.startsWith("{"))
              .join("\n") + "\n",
          );
        }
        console.log(`${ok ? "PASS" : "FAIL"} ${c.id}`);
        if (!ok) console.error(output.split(/\r?\n/).slice(-12).join("\n"));
        resolve({
          status: ok ? "success" : "failed",
          exitCode: code,
          ...(reason ? { reason } : {}),
        });
      }
    });
}

function platformInvocation(cmd, args) {
  if (process.platform !== "win32") return { cmd, args };
  if (cmd === "python3") return { cmd: "python", args };
  if (cmd === "npm") {
    const located = spawnSync("where.exe", ["npm.cmd"], {
      encoding: "utf8",
      windowsHide: true,
    });
    const candidates = (located.stdout || "")
      .trim()
      .split(/\r?\n/)
      .filter(Boolean);
    for (const location of candidates) {
      const cli = path.join(
        path.dirname(location),
        "node_modules/npm/bin/npm-cli.js",
      );
      if (existsSync(cli)) return { cmd: "node", args: [cli, ...args] };
    }
  }
  if (cmd === "sh") {
    const bash = path.join(
      process.env.ProgramFiles || "C:/Program Files",
      "Git/bin/bash.exe",
    );
    if (existsSync(bash)) return { cmd: bash, args };
  }
  return { cmd, args };
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    const args = process.argv.slice(2),
      value = (flag) => {
        const i = args.indexOf(flag);
        return i < 0 ? undefined : args[i + 1];
      };
    const plan = JSON.parse(readFileSync(value("--plan"), "utf8"));
    if (args.includes("--summarize")) {
      const dir = value("--results");
      const reports = readdirSync(dir, { recursive: true })
        .filter(
          (f) => path.basename(f).startsWith("result-") && f.endsWith(".json"),
        )
        .map((f) => JSON.parse(readFileSync(path.join(dir, f), "utf8")));
      console.log(
        `Verified ${requireCompleteResults(plan, reports)} required checks; no missing, skipped or failed results`,
      );
    } else {
      const root = path.resolve(
          path.dirname(fileURLToPath(import.meta.url)),
          "..",
        ),
        partition = value("--partition") || "all";
      const scoped = {
        ...plan,
        selectedChecks: partitionChecks(plan, partition),
      };
      const logDir = path.join(root, ".local/cache/verify");
      const report = await executePlan(scoped, {
        run: commandRunner(root, logDir),
        jobs: Number(value("--jobs") || 4),
      });
      writeFileSync(
        path.join(logDir, `result-${partition}.json`),
        JSON.stringify(report, null, 2),
      );
      if (!scoped.selectedChecks.length)
        console.log(
          `No checks selected for ${partition}; this partition is not a test pass`,
        );
      requireCompleteResults(scoped, [report]);
    }
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
