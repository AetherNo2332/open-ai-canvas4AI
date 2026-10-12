import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";

/**
 * 按 pi.dev 的约定装配系统提示。
 *
 * `@earendil-works/pi-agent-core` 只导出 `formatSkillsForSystemPrompt`，文件式配置装配
 * （`SYSTEM.md` / `APPEND_SYSTEM.md` / `AGENTS.md`）属于 `pi-coding-agent`。既然本仓库
 * 决定不保留第二套 agent，Node 侧就按同一约定自行装配，而不是等 Go 组装好整段提示。
 *
 * 约定（见 https://pi.dev/docs/latest/configuration）：
 * - `SYSTEM.md` 替换默认系统提示；
 * - `APPEND_SYSTEM.md` 追加到系统提示；
 * - `AGENTS.override.md` > `AGENTS.md` > `CLAUDE.md` 作为上下文文件；
 * - `SOUL.md` / `TOOLS.md` 是本仓库既有 Harness 文件，一并纳入。
 */
export interface PromptParts {
  /** SYSTEM.md 的正文；存在时替换默认提示。 */
  system?: string;
  /** APPEND_SYSTEM.md 的正文；追加在系统提示末尾。 */
  appendSystem?: string;
  /** 上下文文件（AGENTS/CLAUDE/SOUL/TOOLS）的正文，按固定顺序拼接。 */
  context: { name: string; text: string }[];
}

export interface LoadPromptOptions {
  /** `<agent-dir>`，等价于 Pi 的 PI_CODING_AGENT_DIR。 */
  agentDir?: string;
  /** 工作目录，用于发现项目级上下文文件。 */
  cwd?: string;
  /** 读取上限，默认 20 KiB，与 Go 侧 Harness 文件限制一致。 */
  maxBytes?: number;
}

const CONTEXT_FILES = ["AGENTS.override.md", "AGENTS.md", "CLAUDE.md", "CLAUDE.MD", "SOUL.md", "TOOLS.md"];
// 老 agent 的系统提示素材（`agent/scripts/sync-harness.mjs` 从 backend/ 同步而来）。
// 顺序即拼装顺序：行为策略 -> 媒体策略 -> 工具描述。
const HARNESS_FILES = ["SYSTEM_POLICY.md", "MEDIA_POLICY.md", "TOOL_DESCRIPTIONS.md"];
const DEFAULT_MAX_BYTES = 20 * 1024;

async function readLimited(path: string, maxBytes: number): Promise<string | undefined> {
  try {
    const raw = await readFile(path);
    if (raw.byteLength > maxBytes) throw new Error(`${path} exceeds ${maxBytes} bytes`);
    const text = raw.toString("utf8");
    // 空文件按"不存在"处理，避免把空段拼进系统提示。
    return text.trim() === "" ? undefined : text.trim();
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return undefined;
    throw error;
  }
}

/** 只读取存在的文件；不存在的文件不报错，这是 Pi 的发现语义。 */
export async function loadPromptParts(options: LoadPromptOptions = {}): Promise<PromptParts> {
  const maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
  const dirs: string[] = [];
  if (options.agentDir) {
    if (!isAbsolute(options.agentDir)) throw new Error("agentDir must be an absolute path");
    dirs.push(resolve(options.agentDir));
  }
  if (options.cwd) dirs.push(resolve(options.cwd));

  const parts: PromptParts = { context: [] };
  const seen = new Set<string>();
  for (const dir of dirs) {
    // SYSTEM.md / APPEND_SYSTEM.md 只在各自目录取一次；后出现的目录不覆盖先出现的。
    if (parts.system === undefined) parts.system = await readLimited(join(dir, "SYSTEM.md"), maxBytes);
    if (parts.appendSystem === undefined) parts.appendSystem = await readLimited(join(dir, "APPEND_SYSTEM.md"), maxBytes);
    for (const name of [...HARNESS_FILES, ...CONTEXT_FILES]) {
      if (seen.has(name)) continue;
      const text = await readLimited(join(dir, name), maxBytes);
      if (text === undefined) continue;
      seen.add(name);
      parts.context.push({ name, text });
      // AGENTS.override.md 只覆盖同目录的 AGENTS.md/CLAUDE.md。
      if (name === "AGENTS.override.md") {
        seen.add("AGENTS.md");
        seen.add("CLAUDE.md");
      }
    }
  }
  return parts;
}

/**
 * 把装配结果渲染成最终系统提示。
 *
 * `base` 是**服务端策略**（能力边界、工具权限、安全规则、执行上下文），它是不可替换的
 * 强制层：`SYSTEM.md` 只能**追加**自己的工作区规则，不能把策略整段顶掉。
 *
 * 语义变更说明：早期实现是 `SYSTEM.md 存在时替换 base`，那等于让一个工作区文件就能
 * 解除服务端对工具权限与安全边界的约束 —— 与"强制策略来自服务端"的合同冲突。
 * 现在两段都在，策略在前；只有 base 为空时 SYSTEM.md 才单独充当系统提示。
 * 上下文文件（AGENTS.md 等）与 APPEND_SYSTEM.md 一律追加。
 */
export function renderSystemPrompt(parts: PromptParts, base = ""): string {
  const sections: string[] = [];
  const policy = base.trim() !== "" ? base : "";
  const fileLayer = parts.system !== undefined && parts.system.trim() !== "" ? parts.system : "";
  if (policy !== "") sections.push(policy);
  if (fileLayer !== "") sections.push(fileLayer);
  for (const entry of parts.context) {
    // Go 侧在过渡期仍会把同一个 Harness 目录追加进策略前缀（`## Workspace <name>`）。
    // 同一份文件不重复装配：已在任一前置层里出现过的段直接跳过。
    const marker = `## Workspace ${entry.name}\n`;
    if (policy.includes(marker) || fileLayer.includes(marker)) continue;
    sections.push(`## Workspace ${entry.name}\n${entry.text}`);
  }
  if (parts.appendSystem !== undefined) sections.push(parts.appendSystem);
  return sections.filter((section) => section.trim() !== "").join("\n\n");
}

/**
 * Harness 的稳定内容身份（sha256）。
 *
 * 为什么需要它：`server.ts` 在 worker 启动时读一次 Harness，然后把**同一个对象**用于
 * 它领取的每一条运行（`harness` 在 while 循环之外）。运维改了 SYSTEM.md / AGENTS.md
 * 再重启进程，**所有在途运行会静默换系统提示** —— 既违反"首步与后续步使用同一份不可变
 * 快照"，也违反"恢复不得重读新版磁盘文件"。工具 schema 早有等价的漂移检查
 * （`assertToolSnapshotMatchesSchema`），提示层此前完全没有。
 *
 * 编码方式：对每一层先写入 `标签:字节长度:`，再写入内容。不能只用分隔符拼接 ——
 * 文件内容本身可能包含任何分隔符，长度前缀才是无歧义的。标签顺序固定，
 * 因此键序不影响结果。
 */
export function harnessHash(parts: PromptParts): string {
  const hash = createHash("sha256");
  const feed = (label: string, value: string | undefined): void => {
    const text = value ?? "";
    hash.update(`${label}:${Buffer.byteLength(text, "utf8")}:`);
    hash.update(text, "utf8");
  };
  feed("system", parts.system);
  for (const entry of parts.context) feed(`context:${entry.name}`, entry.text);
  feed("appendSystem", parts.appendSystem);
  return hash.digest("hex");
}
