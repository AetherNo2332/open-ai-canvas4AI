import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import {
  buildVerificationPlan,
  collectChanges,
  collectPlan,
} from "./verification-plan.mjs";

const graph = [
  { importPath: "yingce/backend/internal/generation/polling", imports: [] },
  {
    importPath: "yingce/backend/internal/app",
    imports: ["yingce/backend/internal/generation/polling"],
  },
];
const plan = (paths, extra = {}) =>
  buildVerificationPlan({
    mode: "affected",
    base: "base",
    head: "head",
    changes: paths.map((path) => ({ status: "M", path })),
    goPackages: graph,
    ...extra,
  });
const ids = (p) => p.selectedChecks.map((c) => c.id);
test("ordinary docs only, contract docs broaden", () => {
  assert.deepEqual(ids(plan(["docs/content/docs/overview/intro.mdx"])), [
    "docs:check",
    "docs:types",
  ]);
  assert.ok(
    ids(plan(["docs/content/docs/backend/http-api.mdx"])).includes(
      "backend:full",
    ),
  );
});
test("mapped package includes reverse callers and integration group", () => {
  const p = plan(["backend/internal/generation/polling/poll_test.go"]);
  assert.ok(
    ids(p).includes(
      "backend:package:yingce/backend/internal/generation/polling",
    ),
  );
  assert.ok(ids(p).includes("backend:app:polling"));
  assert.ok(!ids(p).includes("backend:full"));
});
test("shared, workflow, unknown app, mixed stacks", () => {
  for (const file of [
    "backend/internal/app/service.go",
    ".github/workflows/quality.yml",
    "backend/internal/app/unknown.go",
  ])
    assert.ok(ids(plan([file])).includes("backend:full"));
  const p = plan(["web/src/a.ts", "backend/internal/unknown/a.go"]);
  assert.ok(ids(p).includes("web:test"));
  assert.ok(ids(p).includes("backend:full"));
  assert.ok(p.unclassifiedPaths.length);
});
test("RenameAndDeleteSelectBothOwners", () => {
  const p = plan([], {
    changes: [
      {
        status: "R100",
        oldPath: "web/a.test.ts",
        path: "backend/internal/app/x.go",
      },
      { status: "D", path: "agent/x.ts" },
    ],
  });
  for (const id of ["web:test", "backend:full", "agent:check"])
    assert.ok(ids(p).includes(id));
});
test("graph failure expands, full ignores narrow package mapping, stable unique tasks", () => {
  assert.ok(
    ids(
      plan(["backend/internal/generation/polling/a.go"], { goPackages: null }),
    ).includes("backend:full"),
  );
  assert.ok(
    ids(
      plan(["backend/internal/generation/polling/a.go"], { mode: "full" }),
    ).includes("backend:full"),
  );
  const p = plan(["web/a.ts", "web/b.ts"]);
  assert.equal(new Set(ids(p)).size, ids(p).length);
  assert.deepEqual(p, plan(["web/b.ts", "web/a.ts"]));
});
function fixture() {
  const root = mkdtempSync(path.join(tmpdir(), "verification-fixture-"));
  const git = (...args) => {
    const r = spawnSync("git", args, { cwd: root, encoding: "utf8" });
    assert.equal(r.status, 0, r.stderr);
    return r.stdout.trim();
  };
  git("init");
  git("config", "user.email", "fixture@example.invalid");
  git("config", "user.name", "Fixture");
  writeFileSync(path.join(root, "file space.txt"), "first");
  git("add", ".");
  git("commit", "-m", "base");
  return { root, git, base: git("rev-parse", "HEAD") };
}
test("Git NUL paths, tracked, untracked, rename, delete and explicit head excludes worktree", () => {
  const { root, git, base } = fixture();
  git("mv", "file space.txt", "renamed space.txt");
  git("commit", "-m", "rename");
  writeFileSync(path.join(root, "new test.ts"), "new");
  const changes = collectChanges(root, base);
  assert.ok(
    changes.some(
      (c) => c.oldPath === "file space.txt" && c.path === "renamed space.txt",
    ),
  );
  assert.ok(changes.some((c) => c.path === "new test.ts"));
  assert.equal(collectChanges(root, base, "HEAD").length, 1);
  git("rm", "renamed space.txt");
  assert.ok(collectChanges(root, base).some((c) => c.status === "D"));
});
test("missing base and Git failure fail closed; graph failure is explicit", () => {
  const { root, base, git } = fixture();
  assert.throws(() => collectChanges(root, "missing"));
  assert.throws(() => collectChanges(tmpdir(), base));
  mkdirSync(path.join(root, "backend"));
  writeFileSync(path.join(root, "backend/go.mod"), "module broken\n");
  git("add", ".");
  git("commit", "-m", "backend");
  const p = collectPlan({ root, base, mode: "affected", head: "HEAD" });
  assert.ok(ids(p).includes("backend:full"));
  assert.ok(p.notCovered.some((s) => s.includes("package graph")));
});

test("committed and worktree changes cannot cancel each other out", () => {
  const { root, git, base } = fixture();
  writeFileSync(path.join(root, "file space.txt"), "committed change");
  git("add", ".");
  git("commit", "-m", "change");
  writeFileSync(path.join(root, "file space.txt"), "first");
  assert.ok(
    collectChanges(root, base).some((c) => c.path === "file space.txt"),
  );
  git("add", ".");
  assert.ok(
    collectChanges(root, base).some((c) => c.path === "file space.txt"),
  );
});
