import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

export function isDocsInput(path) {
    return /^(docs\/(app|components|lib|content\/docs|public)\/|docs\/(package.json|bun.lock|next.config.mjs|source.config.ts|tsconfig.json|postcss.config.mjs|Dockerfile|\.dockerignore)$|scripts\/(docs-|verify-docs-|test_docs)|\.github\/workflows\/docs-)/.test(path);
}

export function makeManifest(digest, build, owner) {
    const prefix = "ghcr.io/" + owner.toLowerCase() + "/open-ai-canvas-docs@";
    if (!digest.startsWith(prefix) || !/^sha256:[a-f0-9]{64}$/.test(digest.slice(prefix.length)))
        throw new Error("Invalid documentation repository or digest");
    if (build.deploymentContract !== 1 || !/^[a-f0-9]{40}$/.test(build.commit) || build.buildId !== "docs-" + build.commit || !Number.isFinite(Date.parse(build.builtAt)))
        throw new Error("Invalid documentation build identity");
    return { schemaVersion: 1, digest, buildId: build.buildId, commit: build.commit, builtAt: build.builtAt, deploymentContract: 1 };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
    const [digest, labelsFile, owner, output] = process.argv.slice(2);
    const labels = JSON.parse(readFileSync(labelsFile, "utf8"));
    const build = JSON.parse(labels["tv.yingce.docs.build"] || "{}");
    writeFileSync(output, JSON.stringify(makeManifest(digest, build, owner), null, 2) + "\n");
}
