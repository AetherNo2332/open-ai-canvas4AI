import { test } from "node:test";
import assert from "node:assert/strict";
import {
  makeManifest,
  isDocsInput,
  canPromote,
} from "./docs-release-metadata.mjs";

const commit = "b".repeat(40);
const build = {
  buildId: "docs-" + commit,
  commit,
  builtAt: "2026-11-13T00:00:00Z",
  deploymentContract: 1,
};
test("release snapshot validates repository, digest and compiled identity", () => {
  const digest = "ghcr.io/example/open-ai-canvas-docs@sha256:" + "a".repeat(64);
  assert.equal(makeManifest(digest, build, "example").schemaVersion, 1);
  for (const invalid of [
    { ...build, deploymentContract: 2 },
    { ...build, buildId: "wrong" },
    { ...build, builtAt: "wrong" },
  ])
    assert.throws(() => makeManifest(digest, invalid, "example"));
  assert.throws(() =>
    makeManifest(digest.replace("example", "foreign"), build, "example"),
  );
});
test("only public documentation inputs require publication", () => {
  for (const path of [
    "docs/content/docs/backend/http-api.mdx",
    "docs/public/a.png",
    "docs/bun.lock",
    "docs/components/mdx.tsx",
    ".github/workflows/docs-images.yml",
  ])
    assert.equal(isDocsInput(path), true, path);
  for (const path of [
    "docs/plans/a.md",
    "docs/design/a.md",
    "docs/index.md",
    "README.md",
    "backend/internal/app/a.go",
  ])
    assert.equal(isDocsInput(path), false, path);
});
test("an unrelated README commit must not strand a completed content build", () => {
  assert.equal(canPromote(["docs/README.md", "docs/plans/new-plan.md"]), true);
  assert.equal(canPromote(["docs/app/layout.tsx"]), false);
  assert.equal(canPromote([".github/workflows/docs-deploy.yml"]), false);
});
