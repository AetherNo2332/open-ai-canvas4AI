import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { policy as defaultPolicy } from "./verification-policy.mjs";

function reverseClosure(seeds, packages) {
  const found = new Set(seeds);
  let changed = true;
  while (changed) {
    changed = false;
    for (const p of packages) {
      if (
        !found.has(p.importPath) &&
        [
          ...(p.imports || []),
          ...(p.testImports || []),
          ...(p.xTestImports || []),
        ].some((i) => found.has(i))
      ) {
        found.add(p.importPath);
        changed = true;
      }
    }
  }
  return [...found].sort();
}

export function buildVerificationPlan({
  mode = "affected",
  base,
  head,
  changes,
  goPackages,
  policy = defaultPolicy,
}) {
  if (!["fast", "affected", "full"].includes(mode))
    throw new Error("invalid mode");
  if (!base || !Array.isArray(changes))
    throw new Error("trusted base and changes required");
  const paths = [
    ...new Set(changes.flatMap((c) => [c.path, c.oldPath]).filter(Boolean)),
  ].sort();
  const checks = new Map(),
    reasons = [],
    unclassifiedPaths = [],
    notCovered = [];
  const add = (id, cwd, cmd, args, why, dependencies = []) => {
    checks.set(id, { id, cwd, cmd, args, dependencies });
    reasons.push({ id, path: why });
  };
  const backend = (why) => {
    add(
      "backend:full",
      "backend",
      "node",
      ["../scripts/go-test-shard.mjs", "rest"],
      why,
    );
    for (let shard = 1; shard <= 4; shard++)
      add(
        `backend:app-shard-${shard}`,
        "backend",
        "node",
        ["../scripts/go-test-shard.mjs", String(shard)],
        why,
      );
    add("backend:gofmt", "backend", "gofmt", ["-l", "."], why);
    add("backend:vet", "backend", "go", ["vet", "./..."], why);
    add(
      "backend:agent-lint",
      "backend",
      "golangci-lint",
      [
        "run",
        "--config=.golangci.yml",
        "./internal/agentcontext/...",
        "./internal/agentmemory/...",
        "./internal/architecture/...",
      ],
      why,
    );
    add(
      "backend:tool-contract",
      "backend",
      "go",
      [
        "test",
        "./internal/app",
        "-run",
        "^(TestCloudAgent(PatchSchema|ToolsFollowCanvasCapabilityRegistry|FunctionSchemasAreWellFormed|ToolArgumentValidation)|TestAgentToolSchemaArtifactMatchesRuntime)",
        "-count=1",
      ],
      why,
    );
    add(
      "backend:baseline",
      "backend",
      "go",
      ["run", "./cmd/check-architecture-baseline", "--base", base],
      why,
    );
  };
  const web = (why) => {
    for (const script of ["typecheck", "lint", "test"])
      add(`web:${script}`, "web", "bun", ["run", script], why);
    add(
      "web:format",
      "web",
      "bun",
      ["scripts/check-changed-formatting.mjs"],
      why,
    );
  };
  const agent = (why) => add("agent:check", "agent", "npm", ["test"], why);
  const docs = (why) => {
    add("docs:check", "docs", "bun", ["run", "build"], why);
    add("docs:types", "docs", "bun", ["run", "types:check"], why);
    add(
      "docs:metadata",
      ".",
      "node",
      ["--test", "scripts/docs-release-metadata.test.mjs"],
      why,
    );
    add(
      "docs:deployment",
      ".",
      "python3",
      [
        "-B",
        "-m",
        "unittest",
        "scripts.test_docs_deployment",
        "scripts.test_update_docs",
        "scripts.test_docs_release_snapshot",
      ],
      why,
    );
  };
  const global = (why) => {
    backend(why);
    web(why);
    agent(why);
    docs(why);
    add("changelog", ".", "node", ["scripts/changelog.mjs", "--check"], why);
    add(
      "verification:tests",
      ".",
      "node",
      [
        "--test",
        "scripts/verification-plan.test.mjs",
        "scripts/verification-execution.test.mjs",
        "web/scripts/run-tests.test.mjs",
      ],
      why,
    );
    add(
      "deployment:commands",
      ".",
      "python3",
      [
        "-B",
        "-m",
        "unittest",
        "scripts.test_production_deployment",
        "scripts.test_agent_parity_container_commands",
        "scripts.test_production_agent_smoke",
        "scripts.test_install_agent_worker_id",
        "scripts.test_docs_deployment",
        "scripts.test_update_docs",
        "scripts.test_docs_release_snapshot",
      ],
      why,
    );
    add(
      "payment:artifacts",
      ".",
      "sh",
      ["plugin-packages/verify-payment-packages.sh", "--all", "linux", "amd64"],
      why,
    );
    add(
      "payment:platforms",
      ".",
      "docker",
      [
        "buildx",
        "build",
        "--platform",
        "linux/amd64,linux/arm64",
        "--target",
        "payment-package-smoke",
        "-f",
        "backend/Dockerfile",
        ".",
      ],
      why,
      ["payment:artifacts"],
    );
    add(
      "agent:smoke",
      ".",
      "python3",
      [
        "scripts/verify-production-agent-smoke.py",
        "--output",
        ".local/cache/verify/agent-smoke/report.json",
      ],
      why,
      ["deployment:commands"],
    );
    add("web:e2e", "web", "bun", ["run", "test:previs:e2e"], why);
  };
  notCovered.push(
    "A plan is not an execution result; required PostgreSQL/Redis, Docker, browser and tool availability are checked by execution and CI summaries",
  );
  if (mode === "fast")
    notCovered.push(
      "fast: development feedback only; full delivery checks not covered",
    );
  if (mode === "full") global("explicit full regression");
  for (const file of paths) {
    if (
      file.startsWith("agent/") ||
      file === "web/bun.lock" ||
      file === "web/package.json"
    ) {
      global(file);
      continue;
    }
    if (policy.globalPrefixes.some((p) => file.startsWith(p))) {
      global(file);
      continue;
    }
    if (file.startsWith("backend/")) {
      if (policy.backendShared.some((p) => file.startsWith(p))) {
        global(file);
        continue;
      }
      const owner = policy.packages.find((p) =>
        file.startsWith(p.directory + "/"),
      );
      if (!owner || !goPackages || /\/(types|errors|service)\.go$/.test(file)) {
        global(file);
        if (!owner) unclassifiedPaths.push(file);
        continue;
      }
      const closure = reverseClosure([owner.importPath], goPackages);
      if (!goPackages.some((p) => p.importPath === owner.importPath)) {
        backend(file);
        notCovered.push(
          "package graph does not contain mapped package: " + owner.importPath,
        );
        continue;
      }
      for (const pkg of closure) {
        if (pkg === policy.module + "/internal/app")
          add(
            "backend:app:" + owner.group,
            "backend",
            "go",
            ["test", "-count=1", "./internal/app", "-run", owner.pattern],
            file,
          );
        else
          add(
            "backend:package:" + pkg,
            "backend",
            "go",
            ["test", "-count=1", pkg],
            file,
          );
      }
      add(
        "backend:architecture",
        "backend",
        "go",
        ["test", "-count=1", "./internal/architecture"],
        file,
      );
      add("backend:gofmt", "backend", "gofmt", ["-l", "."], file);
      add("backend:vet", "backend", "go", ["vet", "./..."], file);
      add(
        "backend:agent-lint",
        "backend",
        "golangci-lint",
        [
          "run",
          "--config=.golangci.yml",
          "./internal/agentcontext/...",
          "./internal/agentmemory/...",
          "./internal/architecture/...",
        ],
        file,
      );
      add(
        "backend:baseline",
        "backend",
        "go",
        ["run", "./cmd/check-architecture-baseline", "--base", base],
        file,
      );
      continue;
    }
    if (file.startsWith("web/")) {
      web(file);
      if (/previs/i.test(file))
        add("web:e2e", "web", "bun", ["run", "test:previs:e2e"], file);
      continue;
    }
    if (file.startsWith("agent/")) {
      agent(file);
      continue;
    }
    if (file.startsWith("docs/")) {
      if (
        file.startsWith("docs/plans/") ||
        file.startsWith("docs/design/") ||
        file === "docs/index.md"
      )
        continue;
      docs(file);
      if (policy.contractDocs.some((p) => file.startsWith(p))) backend(file);
      continue;
    }
    if (
      file === "AGENTS.md" ||
      file === "VERSION" ||
      file === "CHANGELOG.md" ||
      file === "README.md"
    ) {
      add("changelog", ".", "node", ["scripts/changelog.mjs", "--check"], file);
      continue;
    }
    unclassifiedPaths.push(file);
    global(file);
  }
  if (goPackages === null && paths.some((f) => f.startsWith("backend/")))
    notCovered.push(
      "Go package graph unavailable; expanded to full backend checks",
    );
  if (checks.has("backend:full"))
    for (const id of checks.keys())
      if (
        id.startsWith("backend:package:") ||
        id.startsWith("backend:app:") ||
        id === "backend:architecture"
      )
        checks.delete(id);
  if (checks.has("web:e2e")) checks.get("web:e2e").dependencies = ["web:test"];
  if (checks.has("docs:check"))
    checks.get("docs:check").dependencies = ["docs:types"];
  if (checks.has("backend:gofmt"))
    checks.get("backend:gofmt").expectEmptyOutput = true;
  if (checks.has("web:format"))
    checks.get("web:format").env = {
      BASE_SHA: base,
      GITHUB_SHA: head || "HEAD",
    };
  return {
    schemaVersion: 1,
    mode,
    base,
    head: head || "worktree",
    selectedChecks: [...checks.values()].sort((a, b) =>
      a.id.localeCompare(b.id),
    ),
    reasons: reasons
      .filter((r) => checks.has(r.id))
      .sort((a, b) => a.id.localeCompare(b.id) || a.path.localeCompare(b.path)),
    unclassifiedPaths: [...new Set(unclassifiedPaths)].sort(),
    notCovered: [...new Set(notCovered)].sort(),
  };
}

function git(root, args) {
  const r = spawnSync("git", args, {
    cwd: root,
    encoding: "utf8",
    windowsHide: true,
    maxBuffer: 64 * 1024 * 1024,
  });
  if (r.status !== 0)
    throw new Error("Git could not establish trusted diff (" + args[0] + ")");
  return r.stdout;
}

export function parseNameStatus(raw) {
  const fields = raw.split("\0");
  const changes = [];
  for (let i = 0; i < fields.length && fields[i];) {
    const status = fields[i++];
    const first = fields[i++];
    if (first === undefined) throw new Error("invalid Git name-status");
    if (/^[RC]/.test(status)) {
      const next = fields[i++];
      if (next === undefined) throw new Error("invalid Git rename");
      changes.push({ status, oldPath: first, path: next });
    } else changes.push({ status, path: first });
  }
  return changes;
}

export function collectChanges(root, base, head) {
  if (!base || /^0+$/.test(base) || base.startsWith("-"))
    throw new Error("trusted base required");
  git(root, ["rev-parse", "--verify", base + "^{commit}"]);
  if (head) git(root, ["rev-parse", "--verify", head + "^{commit}"]);
  const diff = (args) =>
    parseNameStatus(
      git(root, [
        "diff",
        "--name-status",
        "-z",
        "--find-renames",
        ...args,
        "--",
      ]),
    );
  const changes = head
    ? diff([base, head])
    : [...diff([base, "HEAD"]), ...diff(["--cached", "HEAD"]), ...diff([])];
  if (!head)
    for (const file of git(root, [
      "ls-files",
      "--others",
      "--exclude-standard",
      "-z",
    ])
      .split("\0")
      .filter(Boolean))
      changes.push({ status: "?", path: file });
  return [...new Map(changes.map((c) => [JSON.stringify(c), c])).values()];
}

export function readGoPackages(root) {
  const r = spawnSync("go", ["list", "-json", "./..."], {
    cwd: path.join(root, "backend"),
    encoding: "utf8",
    windowsHide: true,
    maxBuffer: 64 * 1024 * 1024,
  });
  if (r.status !== 0 || !r.stdout.trim()) return null;
  try {
    return r.stdout
      .trim()
      .split(/(?<=\n\})\s*(?=\{)/)
      .filter(Boolean)
      .map((s) => {
        const p = JSON.parse(s);
        return {
          importPath: p.ImportPath,
          imports: p.Imports || [],
          testImports: p.TestImports || [],
          xTestImports: p.XTestImports || [],
        };
      });
  } catch {
    return null;
  }
}

export function collectPlan({ root, base, head, mode = "affected" }) {
  const resolvedHead = git(root, [
    "rev-parse",
    "--verify",
    (head || "HEAD") + "^{commit}",
  ]).trim();
  const changes = collectChanges(root, base, head);
  const needsGraph = changes.some((c) =>
    [c.path, c.oldPath].some((p) => p?.startsWith("backend/")),
  );
  return buildVerificationPlan({
    mode,
    base,
    head: resolvedHead,
    changes,
    goPackages: needsGraph ? readGoPackages(root) : [],
  });
}

export function parseOptions(argv) {
  const result = { mode: "affected" };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--json") continue;
    if (!["--base", "--head", "--mode"].includes(arg) || !argv[i + 1])
      throw new Error("expected --base, --head, --mode or --json");
    result[arg.slice(2)] = argv[++i];
  }
  if (!result.base) throw new Error("--base is required");
  return result;
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    console.log(
      JSON.stringify(
        collectPlan({
          root: path.resolve(
            path.dirname(fileURLToPath(import.meta.url)),
            "..",
          ),
          ...parseOptions(process.argv.slice(2)),
        }),
        null,
        2,
      ),
    );
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
