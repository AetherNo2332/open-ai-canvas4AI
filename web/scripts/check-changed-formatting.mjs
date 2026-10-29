import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import * as prettier from "prettier";

export async function checkChangedFormatting({ cwd = process.cwd(), baseSha = process.env.BASE_SHA, headSha = process.env.GITHUB_SHA || "HEAD", log = console.log } = {}) {
    const git = (args) => {
        const result = spawnSync("git", args, { cwd, encoding: "utf8", maxBuffer: 32 * 1024 * 1024 });
        if (result.error) throw result.error;
        if (result.status !== 0) throw new Error(`git ${args.join(" ")}: ${result.stderr}`);
        return result.stdout;
    };
    const head = git(["rev-parse", "--verify", `${headSha}^{commit}`]).trim();
    const headParents = git(["rev-list", "--parents", "-n", "1", head]).trim().split(" ").slice(1);
    const parent = headParents[0] || null;
    let base = parent;
    if (baseSha && !/^0+$/.test(baseSha)) {
        // 强制改写提交历史后，push 事件里的 before 可能已不在 checkout 中；
        // 对合法但失效的 SHA 退回当前父提交，仍校验本次提交的完整改动。
        if (!/^[0-9a-f]{40}$/i.test(baseSha)) throw new Error(`Invalid base SHA: ${baseSha}`);
        const resolved = spawnSync("git", ["rev-parse", "--verify", `${baseSha}^{commit}`], { cwd, encoding: "utf8" });
        if (resolved.status === 0) {
            base = resolved.stdout.trim();
        } else {
            log(`Base SHA ${baseSha} is unavailable; checking against the head parent instead.`);
        }
    }
    const files = (base ? git(["diff", "--relative", "--name-only", "--diff-filter=ACMR", "-z", base, head, "--", "."]) : git(["ls-tree", "-r", "--name-only", "-z", head, "--", "."]))
        .split("\0")
        .filter((file) => /\.(css|html|json|js|jsx|md|mdx|mjs|cjs|ts|tsx|yaml|yml)$/.test(file));
    const prefix = git(["rev-parse", "--show-prefix"]).trim();
    // 分支内部合并进来的外部历史（如上游同步）带入的格式问题按旧文件处理：
    // 只取分支首父链上合并提交的其余父提交，避免展开被合并分支内部的全部历史。
    // 直接落在 base 上的合并（GitHub 的 PR 合成合并或单次推送的合并）本身就是
    // 被检查的改动：其第二父提交即改动内容，不能当作旧文件，只沿它的首父链继续找。
    const legacyRefs = base ? [base] : [];
    if (base) {
        const tip = headParents.length > 1 && headParents[0] === base ? headParents[1] : head;
        for (const line of git(["rev-list", "--merges", "--first-parent", "--parents", tip, `^${base}`]).split("\n")) {
            legacyRefs.push(...line.trim().split(" ").slice(2).filter(Boolean));
        }
    }
    const refFiles = new Map(legacyRefs.map((ref) => [ref, new Set(git(["ls-tree", "-r", "--name-only", "-z", ref, "--", "."]).split("\0"))]));
    const failures = [];
    for (const file of files) {
        const filepath = path.join(cwd, file);
        const info = await prettier.getFileInfo(filepath, { ignorePath: path.join(cwd, ".prettierignore") });
        if (info.ignored) continue;
        const options = { ...(await prettier.resolveConfig(filepath)), filepath };
        // 只允许明确的历史格式问题跳过；语法、Git、文件读取错误全部向上抛出。
        let legacy = false;
        for (const ref of legacyRefs) {
            if (!refFiles.get(ref).has(file)) continue;
            if (!(await prettier.check(git(["show", `${ref}:${prefix}${file}`]), options))) {
                legacy = true;
                break;
            }
        }
        if (legacy) {
            log(`Skipping legacy unformatted file: ${file}`);
            continue;
        }
        if (!(await prettier.check(await readFile(filepath, "utf8"), options))) failures.push(file);
    }
    if (failures.length) throw new Error(`Code style issues found:\n${failures.join("\n")}\nRun Prettier with --write to fix.`);
    log("Changed web files passed formatting checks.");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
    checkChangedFormatting().catch((error) => {
        console.error(error.message);
        process.exitCode = 1;
    });
}
