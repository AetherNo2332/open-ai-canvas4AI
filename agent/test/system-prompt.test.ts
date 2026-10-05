import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { loadPromptParts, renderSystemPrompt } from "../src/system-prompt.js";

async function fixtureDir(): Promise<string> {
  return mkdtemp(join(tmpdir(), "pi-prompts-"));
}

// 语义变更（阶段 2 要求）：服务端策略是**不可替换**的强制层，SYSTEM.md 只能追加自己的工作区规则。
//
// 旧断言是 "SYSTEM.md 替换服务端前缀，且 rendered 不含服务端策略前缀"。那条语义等于：
// 只要工作区放一个 SYSTEM.md，就能整段解除服务端对工具权限、能力边界与安全规则的约束 ——
// 与新合同"强制服务端策略不能被 SYSTEM.md 替换"直接冲突。
// 所以本用例是**按新需求显式改写**，不是放宽断言：新增了"策略仍在最前"的正向断言。
test("SYSTEM.md 不能替换服务端策略，只能追加；APPEND_SYSTEM.md 仍在末尾", async () => {
  const dir = await fixtureDir();
  await writeFile(join(dir, "SYSTEM.md"), "工作区系统规则");
  await writeFile(join(dir, "APPEND_SYSTEM.md"), "追加说明");
  const parts = await loadPromptParts({ agentDir: dir });
  const rendered = renderSystemPrompt(parts, "服务端策略前缀");
  assert.ok(rendered.startsWith("服务端策略前缀"), "服务端策略必须仍在最前");
  assert.ok(rendered.includes("工作区系统规则"), "SYSTEM.md 的内容仍要装配进去");
  assert.ok(
    rendered.indexOf("服务端策略前缀") < rendered.indexOf("工作区系统规则"),
    "顺序必须是服务端策略在前、工作区规则在后",
  );
  assert.ok(rendered.endsWith("追加说明"));
});

test("没有服务端策略时 SYSTEM.md 单独充当系统提示", async () => {
  const dir = await fixtureDir();
  await writeFile(join(dir, "SYSTEM.md"), "工作区系统规则");
  const parts = await loadPromptParts({ agentDir: dir });
  assert.equal(renderSystemPrompt(parts, ""), "工作区系统规则");
});

test("没有 SYSTEM.md 时保留服务端前缀，上下文文件按序拼接", async () => {
  const dir = await fixtureDir();
  await writeFile(join(dir, "AGENTS.md"), "项目约定");
  await writeFile(join(dir, "SOUL.md"), "人格设定");
  await writeFile(join(dir, "TOOLS.md"), "工具补充");
  const parts = await loadPromptParts({ agentDir: dir });
  const rendered = renderSystemPrompt(parts, "服务端策略前缀");
  assert.ok(rendered.startsWith("服务端策略前缀"));
  assert.deepEqual(parts.context.map((entry) => entry.name), ["AGENTS.md", "SOUL.md", "TOOLS.md"]);
  assert.ok(rendered.indexOf("AGENTS.md") < rendered.indexOf("SOUL.md"));
  assert.ok(rendered.includes("工具补充"));
});

test("AGENTS.override.md 覆盖同目录的 AGENTS.md", async () => {
  const dir = await fixtureDir();
  await writeFile(join(dir, "AGENTS.md"), "被覆盖");
  await writeFile(join(dir, "AGENTS.override.md"), "生效的覆盖版");
  const parts = await loadPromptParts({ agentDir: dir });
  const names = parts.context.map((entry) => entry.name);
  assert.deepEqual(names, ["AGENTS.override.md"]);
  assert.ok(renderSystemPrompt(parts).includes("生效的覆盖版"));
  assert.ok(!renderSystemPrompt(parts).includes("被覆盖"));
});

test("缺失文件不是错误，空文件不产生空段", async () => {
  const dir = await fixtureDir();
  await writeFile(join(dir, "APPEND_SYSTEM.md"), "   \n  ");
  const parts = await loadPromptParts({ agentDir: dir, cwd: join(dir, "nope") });
  assert.deepEqual(parts.context, []);
  assert.equal(parts.system, undefined);
  assert.equal(parts.appendSystem, undefined);
  assert.equal(renderSystemPrompt(parts, "仅有前缀"), "仅有前缀");
});

test("超限文件被拒绝，相对 agentDir 被拒绝", async () => {
  const dir = await fixtureDir();
  await writeFile(join(dir, "SYSTEM.md"), "x".repeat(2048));
  await assert.rejects(() => loadPromptParts({ agentDir: dir, maxBytes: 1024 }), /exceeds 1024 bytes/);
  await assert.rejects(() => loadPromptParts({ agentDir: "relative/dir" }), /absolute path/);
});

test("cwd 作为第二个发现目录，先出现的目录优先", async () => {
  const agentDir = await fixtureDir();
  const cwd = await fixtureDir();
  await mkdir(cwd, { recursive: true });
  await writeFile(join(agentDir, "SYSTEM.md"), "agent 级");
  await writeFile(join(cwd, "SYSTEM.md"), "cwd 级");
  await writeFile(join(cwd, "APPEND_SYSTEM.md"), "cwd 追加");
  const parts = await loadPromptParts({ agentDir, cwd });
  assert.equal(parts.system, "agent 级");
  assert.equal(parts.appendSystem, "cwd 追加");
});

test("服务端前缀已含同一 Harness 段时不重复装配", async () => {
  const dir = await fixtureDir();
  await writeFile(join(dir, "AGENTS.md"), "项目约定");
  const parts = await loadPromptParts({ agentDir: dir });
  // Go 过渡期仍会把同一目录追加进策略前缀。
  const prefix = "服务端策略\n\n## Workspace AGENTS.md\n项目约定";
  const rendered = renderSystemPrompt(parts, prefix);
  assert.equal(rendered, prefix);
  assert.equal(rendered.split("## Workspace AGENTS.md").length - 1, 1);
});

test("老 agent 的策略与工具描述被纳入装配，且顺序稳定", async () => {
  const dir = await fixtureDir();
  await writeFile(join(dir, "SYSTEM_POLICY.md"), "行为策略正文");
  await writeFile(join(dir, "MEDIA_POLICY.md"), "媒体策略正文");
  await writeFile(join(dir, "TOOL_DESCRIPTIONS.md"), "工具描述正文");
  await writeFile(join(dir, "AGENTS.md"), "项目约定");
  const parts = await loadPromptParts({ agentDir: dir });
  assert.deepEqual(parts.context.map((e) => e.name), [
    "SYSTEM_POLICY.md", "MEDIA_POLICY.md", "TOOL_DESCRIPTIONS.md", "AGENTS.md",
  ]);
  const rendered = renderSystemPrompt(parts, "服务端前缀");
  assert.ok(rendered.startsWith("服务端前缀"));
  assert.ok(rendered.indexOf("行为策略正文") < rendered.indexOf("媒体策略正文"));
  assert.ok(rendered.indexOf("媒体策略正文") < rendered.indexOf("工具描述正文"));
  assert.ok(rendered.indexOf("工具描述正文") < rendered.indexOf("项目约定"));
});
