#!/usr/bin/env node
/**
 * 更新日志生成器（零依赖，Node 20+ / Bun 均可运行）。
 *
 * 仓库的更新日志不是逐条罗列 git log：`CHANGELOG.md` 的条目按用户可见影响改写，
 * 发布流程（.github/workflows/publish-images.yml）会要求 `## <VERSION>` 标题存在，
 * 并把它下面的内容直接作为 GitHub Release 说明。所以这里只做两件事：
 *
 *   1. 按提交类型归并出**草稿**，省掉手工翻 git log；
 *   2. 校验 / 写入 `## <版本>(<短 SHA>)` 标题，并把根 `VERSION` 一起 bump。
 *
 * 用法：
 *   node scripts/changelog.mjs                # 预览草稿，不改文件
 *   node scripts/changelog.mjs --write        # 写入 CHANGELOG.md 顶部并 bump VERSION
 *   node scripts/changelog.mjs --write --force# 覆盖同版本已有条目（重新生成）
 *   node scripts/changelog.mjs --check        # CI：校验 VERSION 的正式版本号有对应条目
 *   node scripts/changelog.mjs --since <sha>  # 手动指定起始提交
 *   node scripts/changelog.mjs --all          # 连 docs/test/chore 一起列进草稿
 */
import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const changelogPath = path.join(repoRoot, "CHANGELOG.md");
const versionPath = path.join(repoRoot, "VERSION");

const argv = process.argv.slice(2);
const hasFlag = (flag) => argv.includes(flag);
const flagValue = (flag) => {
    const index = argv.indexOf(flag);
    return index >= 0 ? argv[index + 1] : undefined;
};

function git(...gitArgs) {
    return execFileSync("git", gitArgs, { cwd: repoRoot, encoding: "utf8" }).trim();
}

function escapeRegExp(value) {
    return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** 提交类型 → 草稿里的小标签；顺序即输出顺序。 */
const TYPE_LABELS = new Map([
    ["feat", "新增"],
    ["fix", "修复"],
    ["perf", "性能"],
    ["refactor", "重构"],
    ["build", "构建"],
    ["ci", "CI"],
    ["revert", "回滚"],
    ["docs", "文档"],
    ["test", "测试"],
    ["chore", "维护"],
]);

/** 默认不进草稿的类型：它们描述过程而不是用户可见变化。`--all` 可放开。 */
const DEFAULT_SKIPPED_TYPES = new Set(["docs", "test", "chore"]);

const versionRaw = readFileSync(versionPath, "utf8").trim();
const baseVersion = versionRaw.replace(/\(.*\)$/, "");
const headSha = git("rev-parse", "--short=7", "HEAD");
const changelog = readFileSync(changelogPath, "utf8");

if (hasFlag("--check")) {
    const heading = new RegExp(`^## ${escapeRegExp(baseVersion)}(\\(|\\s|$)`, "m");
    if (!heading.test(changelog)) {
        console.error(`CHANGELOG.md 缺少 ${baseVersion} 的条目。`);
        console.error(`VERSION 当前为 ${versionRaw}，请先运行：node scripts/changelog.mjs --write`);
        process.exit(1);
    }
    if (versionRaw !== `${baseVersion}(${headSha})`) {
        // 不判失败：PR 期间还会追加提交，AGENTS.md 只要求 push 前重新 bump。
        console.warn(`提示：VERSION=${versionRaw}，当前 HEAD 是 ${headSha}，push 前记得重新 bump。`);
    }
    console.log(`CHANGELOG.md 已包含 ${baseVersion} 的条目。`);
    process.exit(0);
}

const topHeading = changelog.match(/^## (v[^\s(]+)\(([0-9a-f]{7,40})\)/m);
const baseline = flagValue("--since") || topHeading?.[2];

if (!baseline) {
    console.error("CHANGELOG.md 顶部没有 `## <版本>(<短 SHA>)` 条目，请用 --since <sha> 指定起点。");
    process.exit(1);
}

if (!isAncestor(baseline)) {
    console.error(`起点 ${baseline} 不在当前分支历史中（仓库存在多线合并历史时常见）。`);
    console.error("请用 --since <sha> 指定一个可达的起点，例如：node scripts/changelog.mjs --since HEAD~20");
    process.exit(1);
}

function isAncestor(sha) {
    try {
        execFileSync("git", ["merge-base", "--is-ancestor", sha, "HEAD"], { cwd: repoRoot });
        return true;
    } catch {
        return false;
    }
}

const includeAll = hasFlag("--all");
const log = git("log", "--no-merges", "--pretty=format:%h%x09%s", `${baseline}..HEAD`);
const commits = log
    ? log
          .split("\n")
          .map((line) => {
              const [sha, ...rest] = line.split("\t");
              return { sha, subject: rest.join("\t") };
          })
          .filter((commit) => commit.subject)
    : [];

const grouped = new Map();
const skipped = [];

for (const commit of commits) {
    const match = commit.subject.match(/^([a-z]+)(?:\(([^)]+)\))?!?:\s*(.+)$/);
    if (!match) {
        push("其他", `- ${commit.subject}`);
        continue;
    }
    const [, type, scope = "", body] = match;
    // 版本号 bump 本身不是变更内容，永远不进日志。
    if (scope === "version") continue;
    if (!includeAll && DEFAULT_SKIPPED_TYPES.has(type)) {
        skipped.push(commit.subject);
        continue;
    }
    const label = TYPE_LABELS.get(type) ?? "其他";
    const prefix = scope ? `[${scope}] ` : "";
    push(label, `- ${label}：${prefix}${body}`);
}

function push(label, line) {
    if (!grouped.has(label)) grouped.set(label, []);
    const bucket = grouped.get(label);
    if (!bucket.includes(line)) bucket.push(line);
}

if (grouped.size === 0) {
    console.error(`自 ${baseline} 以来没有可写入的提交。`);
    if (skipped.length > 0) console.error(`（已跳过 ${skipped.length} 条 docs/test/chore，可加 --all 查看）`);
    process.exit(1);
}

const order = [...TYPE_LABELS.values(), "其他"];
const bullets = order.flatMap((label) => grouped.get(label) ?? []);
const heading = `## ${baseVersion}(${headSha})`;
const entry = `${heading}\n\n${bullets.join("\n")}\n\n`;

if (!hasFlag("--write")) {
    process.stdout.write(`<!-- 起点 ${baseline}，共 ${commits.length} 条提交 -->\n${entry}`);
    if (skipped.length > 0) {
        process.stderr.write(`已跳过 ${skipped.length} 条 docs/test/chore；加 --all 可包含。\n`);
    }
    process.stderr.write("预览模式：确认内容后加 --write 写入 CHANGELOG.md 并 bump VERSION。\n");
    process.exit(0);
}

const existing = new RegExp(`^## ${escapeRegExp(baseVersion)}\\([^)]*\\)`, "m");
let nextChangelog;
if (existing.test(changelog)) {
    if (!hasFlag("--force")) {
        console.error(`CHANGELOG.md 已有 ${baseVersion} 的条目。重新生成请加 --force，或手工改写现有条目。`);
        process.exit(1);
    }
    const start = changelog.search(existing);
    const rest = changelog.slice(start);
    const nextSection = rest.slice(rest.indexOf("\n") + 1).search(/^## /m);
    const end = nextSection >= 0 ? start + rest.indexOf("\n") + 1 + nextSection : changelog.length;
    nextChangelog = `${changelog.slice(0, start)}${entry}${changelog.slice(end)}`;
} else {
    const firstSection = changelog.search(/^## /m);
    nextChangelog = firstSection >= 0 ? `${changelog.slice(0, firstSection)}${entry}${changelog.slice(firstSection)}` : `${changelog.trimEnd()}\n\n${entry}`;
}

writeFileSync(changelogPath, nextChangelog, "utf8");
writeFileSync(versionPath, `${baseVersion}(${headSha})`, "utf8");

console.log(`已写入 ${heading}（${bullets.length} 条），VERSION 更新为 ${baseVersion}(${headSha})。`);
console.log("发布前请按用户可见影响改写草稿措辞，并补充升级 / 迁移说明。");
