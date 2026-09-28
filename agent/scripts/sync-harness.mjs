#!/usr/bin/env node
/**
 * 把老 agent 的系统提示素材同步进 Pi 的 Harness 目录。
 *
 * Go 侧仍是这些文件的唯一来源（backend/ 下内嵌编译进后端）。Pi worker 用同一份内容
 * 自己装配系统提示，因此这里只做复制与轻量规范化，不修改语义：
 * - 策略文件去掉 YAML frontmatter（id/version 是给 Go 读的元数据，不进提示正文）；
 * - 生成 SOURCE.json 记录每个文件的来源路径与 sha256，便于核对是否漂移。
 *
 * 用法：node scripts/sync-harness.mjs
 */
import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repo = resolve(here, "..", "..");
const outDir = join(repo, "agent", "harness");

const SOURCES = [
  { from: "backend/internal/prompts/agent-system-policy.md", to: "SYSTEM_POLICY.md", stripFrontmatter: true },
  { from: "backend/internal/prompts/agent-media-policy.md", to: "MEDIA_POLICY.md", stripFrontmatter: true },
  { from: "backend/internal/app/agent-tool-descriptions.md", to: "TOOL_DESCRIPTIONS.md" },
];

/** 去掉 `---\n...\n---\n` 形式的元数据头，只保留正文。 */
function stripFrontmatter(raw) {
  const text = raw.replace(/\r\n/g, "\n");
  if (!text.startsWith("---")) return text.trim();
  const end = text.indexOf("\n---", 3);
  if (end < 0) throw new Error("frontmatter header is not closed");
  return text.slice(text.indexOf("\n", end + 1) + 1).trim();
}

await mkdir(outDir, { recursive: true });
const manifest = [];
for (const source of SOURCES) {
  const raw = await readFile(join(repo, source.from), "utf8");
  const text = source.stripFrontmatter ? stripFrontmatter(raw) : raw.replace(/\r\n/g, "\n").trim();
  if (text === "") throw new Error(`${source.from} is empty`);
  const body = `${text}\n`;
  await writeFile(join(outDir, source.to), body);
  manifest.push({
    file: source.to,
    source: source.from,
    stripFrontmatter: Boolean(source.stripFrontmatter),
    sha256: createHash("sha256").update(body).digest("hex"),
  });
  console.log(`${source.from} -> agent/harness/${source.to} (${body.length} bytes)`);
}
await writeFile(join(outDir, "SOURCE.json"), `${JSON.stringify({ generatedBy: "agent/scripts/sync-harness.mjs", files: manifest }, null, 2)}\n`);
console.log(`wrote agent/harness/SOURCE.json (${manifest.length} files)`);
