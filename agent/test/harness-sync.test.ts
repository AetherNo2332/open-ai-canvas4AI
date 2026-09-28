import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { access, readFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

/**
 * Harness 素材由 `scripts/sync-harness.mjs` 从 backend/ 同步而来（Go 侧仍是唯一来源）。
 * 这里守住漂移：有人改了 backend 的策略或工具描述却忘记重跑同步时，必须失败，
 * 否则 Pi 会拿着一份过期系统提示继续跑。
 */
const here = dirname(fileURLToPath(import.meta.url));

/** 测试可能从 src/ 或编译后的 dist/ 运行，向上找到含 agent/harness 的仓库根。 */
async function findRepoRoot(): Promise<string> {
  let current = here;
  for (let depth = 0; depth < 6; depth++) {
    try {
      await access(join(current, "agent", "harness", "SOURCE.json"));
      return current;
    } catch {
      current = resolve(current, "..");
    }
  }
  throw new Error(`找不到仓库根（从 ${here} 起）`);
}

const repo = await findRepoRoot();
const harnessDir = join(repo, "agent", "harness");

function sha256(text: string): string {
  return createHash("sha256").update(text).digest("hex");
}

function stripFrontmatter(raw: string): string {
  const text = raw.replace(/\r\n/g, "\n");
  if (!text.startsWith("---")) return text.trim();
  const end = text.indexOf("\n---", 3);
  if (end < 0) throw new Error("frontmatter header is not closed");
  return text.slice(text.indexOf("\n", end + 1) + 1).trim();
}

test("harness 与 backend 源一致（漂移检测）", async () => {
  const manifest = JSON.parse(await readFile(join(harnessDir, "SOURCE.json"), "utf8")) as {
    files: { file: string; source: string; stripFrontmatter: boolean; sha256: string }[];
  };
  assert.ok(manifest.files.length >= 3, "SOURCE.json 应至少记录策略与工具描述");

  for (const entry of manifest.files) {
    const original = await readFile(join(repo, entry.source), "utf8");
    const body = entry.stripFrontmatter ? stripFrontmatter(original) : original.replace(/\r\n/g, "\n").trim();
    const expected = `${body}\n`;
    assert.equal(
      sha256(expected),
      entry.sha256,
      `${entry.source} 已更新但 agent/harness/${entry.file} 未重新同步；请运行 node scripts/sync-harness.mjs`,
    );
    const synced = await readFile(join(harnessDir, entry.file), "utf8");
    assert.equal(sha256(synced.replace(/\r\n/g, "\n")), entry.sha256, `agent/harness/${entry.file} 与清单哈希不一致`);
  }
});

test("同步后的 harness 不含 YAML frontmatter", async () => {
  for (const file of ["SYSTEM_POLICY.md", "MEDIA_POLICY.md"]) {
    const text = await readFile(join(harnessDir, file), "utf8");
    assert.equal(text.startsWith("---"), false, `${file} 不应把元数据头带进提示正文`);
  }
});

test("共用工具 schema 制品可被 Node 读取且结构完整", async () => {
  const artifact = JSON.parse(await readFile(join(harnessDir, "TOOL_SCHEMA.json"), "utf8")) as {
    schemaVersion: string;
    tools: { type: string; function: { name: string; parameters: Record<string, unknown> } }[];
  };
  assert.match(artifact.schemaVersion, /^cloud-agent-tools\/v\d+$/);
  assert.ok(artifact.tools.length > 0, "工具 schema 制品不能为空");
  for (const tool of artifact.tools) {
    assert.equal(tool.type, "function");
    assert.ok(tool.function.name, "每个工具都要有名字");
    assert.equal(typeof tool.function.parameters, "object");
  }
  // 只注册具体业务工具；类别标识仅用于分组，不属于模型可调用目录。
  const names = artifact.tools.map((tool) => tool.function.name);
  for (const category of ["agent_tools_control", "agent_tools_canvas_read", "agent_tools_canvas_edit"]) {
    assert.equal(names.includes(category), false, `schema 不应暴露类型入口 ${category}`);
  }
  for (const concrete of ["canvas_get_state", "canvas_apply_ops", "generate_media"]) {
    assert.ok(names.includes(concrete), `schema 缺少具体工具 ${concrete}`);
  }
});
