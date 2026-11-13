import { readdirSync } from "node:fs";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Preserve the two historical pretest processes. The first eight source/SSR
// checks precede the large suite; canvas imports React rendering independently.
export const isolatedGroups = [
    {
        id: "regressions",
        files: [
            "test/tiptap-dependency-versions.test.ts",
            "test/admin-ui-regressions.test.ts",
            "test/announcement-prompt-regressions.test.ts",
            "test/password-reset.test.ts",
            "test/oss-settings.test.ts",
            "test/channel-model-editor-form.test.ts",
            "test/channel-model-price-tier-form.test.ts",
            "test/admin-logical-models.test.ts",
        ],
    },
    { id: "canvas-grid", files: ["test/canvas-grid-viewport.test.tsx"] },
];

export function discoverTests(root) {
    const found = [];
    function visit(dir) {
        for (const entry of readdirSync(path.join(root, dir), { withFileTypes: true })) {
            if (entry.isDirectory()) {
                if (!["node_modules", ".git", ".local", "dist", "build", "coverage"].includes(entry.name)) visit(path.posix.join(dir, entry.name));
                continue;
            }
            if (entry.isFile() && /[._](test|spec)\.[cm]?[jt]sx?$/.test(entry.name)) found.push(path.posix.join(dir, entry.name));
        }
    }
    visit("");
    return found.sort();
}

export function buildTestGroups(files) {
    const all = new Set(files),
        isolated = new Set(isolatedGroups.flatMap((g) => g.files));
    for (const file of isolated) if (!all.has(file)) throw new Error("Missing isolated test: " + file);
    return [...isolatedGroups.map((g) => ({ ...g, files: [...g.files] })), { id: "ordinary", files: [...all].filter((f) => !isolated.has(f)).sort() }];
}

export function runGroups(groups, { root, spawn = spawnSync, executable = "bun" } = {}) {
    for (const group of groups) {
        if (!group.files.length) continue;
        const start = Date.now();
        console.log(`[web-tests] ${group.id}: ${group.files.length} files`);
        // Explicit ./ paths prevent Bun from treating filenames as substring filters.
        const result = spawn(executable, ["test", ...group.files.map((f) => "./" + f)], { cwd: root, stdio: "inherit", windowsHide: true });
        console.log(`[web-tests] ${group.id}: ${((Date.now() - start) / 1000).toFixed(2)}s`);
        if (result.error || result.status !== 0) return result.status || 1;
    }
    return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
    const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
    const groups = buildTestGroups(discoverTests(root));
    if (process.argv.includes("--list")) console.log(JSON.stringify(groups, null, 2));
    else process.exitCode = runGroups(groups, { root });
}
