import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

export function isDocsInput(path) {
  return /^(docs\/(app|components|lib|content\/docs|public)\/|docs\/[^/]+\.(mjs|ts|json)$|docs\/(bun.lock|Dockerfile|\.dockerignore)$|scripts\/[^/]*docs[^/]*$|\.github\/workflows\/docs-)/.test(
    path,
  );
}

export function canPromote(changedPaths) {
  return !changedPaths.some(isDocsInput);
}

export function makeManifest(digest, build, owner) {
  const prefix = "ghcr.io/" + owner.toLowerCase() + "/open-ai-canvas-docs@";
  if (
    !digest.startsWith(prefix) ||
    !/^sha256:[a-f0-9]{64}$/.test(digest.slice(prefix.length))
  )
    throw new Error("Invalid documentation repository or digest");
  if (
    build.deploymentContract !== 1 ||
    !/^[a-f0-9]{40}$/.test(build.commit) ||
    build.buildId !== "docs-" + build.commit ||
    !Number.isFinite(Date.parse(build.builtAt))
  )
    throw new Error("Invalid documentation build identity");
  return {
    schemaVersion: 1,
    digest,
    buildId: build.buildId,
    commit: build.commit,
    builtAt: build.builtAt,
    deploymentContract: 1,
  };
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  if (process.argv[2] === "--can-promote") {
    const paths = readFileSync(process.argv[3], "utf8")
      .split("\0")
      .filter(Boolean);
    process.exit(canPromote(paths) ? 0 : 1);
  }
  if (process.argv[2] === "--validate") {
    const manifest = JSON.parse(readFileSync(process.argv[3], "utf8"));
    if (manifest.schemaVersion !== 1)
      throw new Error("Unsupported documentation snapshot schema");
    console.log(
      JSON.stringify(
        makeManifest(manifest.digest, manifest, process.argv[4]),
        null,
        2,
      ),
    );
    process.exit(0);
  }
  const [digest, labelsFile, owner, output] = process.argv.slice(2);
  const labels = JSON.parse(readFileSync(labelsFile, "utf8"));
  const build = JSON.parse(labels["tv.yingce.docs.build"] || "{}");
  writeFileSync(
    output,
    JSON.stringify(makeManifest(digest, build, owner), null, 2) + "\n",
  );
}
