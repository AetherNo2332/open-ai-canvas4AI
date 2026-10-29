import { createHash } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { isAbsolute, join, relative, resolve, sep } from "node:path";
import type { InlineExtension, LoadSkillsResult } from "@earendil-works/pi-coding-agent";
import type { CanvasBridge, PiSkillSnapshot, PiSnapshot } from "./bridge.js";
import { FatalWorkerError } from "./tool-disclosure.js";

const MAX_ENTRY_BYTES = 512 << 10;
const MAX_PAGE_RUNES = 12_000;
const digest = (value: string): string => createHash("sha256").update(value).digest("hex");

/** Local, model-correctable input rejection; protocol/identity faults stay fatal. */
export class NativeSkillReadRejected extends Error {}

function validateSkill(skill: PiSkillSnapshot): void {
  if (!/^skill-[a-z0-9]+(?:-[a-z0-9]+)*$/.test(skill.nativeName) || skill.nativeName.length > 64 ||
      !skill.id || !skill.versionId || !skill.contentHash || skill.entryPath !== "SKILL.md") {
    throw new FatalWorkerError("Invalid native Skill identity");
  }
  const entry = skill.files.find((file) => file.path === "SKILL.md" && file.text);
  if (!entry || skill.files.some((file) => file.path.includes("\\") || file.path.split("/").some((part) => !part || part === "." || part === "..")) ||
      Buffer.byteLength(skill.entryContent, "utf8") > MAX_ENTRY_BYTES ||
      Buffer.byteLength(skill.entryContent, "utf8") !== entry.size || digest(skill.entryContent) !== entry.sha256) {
    throw new FatalWorkerError("Native Skill entry differs from the frozen snapshot");
  }
}

/** Materialize only validated entry files; references remain Go-backed. */
export function materializeNativeSkills(skills: PiSkillSnapshot[], root: string): void {
  const seen = new Set<string>();
  for (const skill of skills) {
    validateSkill(skill);
    if (seen.has(skill.nativeName)) throw new FatalWorkerError("Duplicate native Skill name");
    seen.add(skill.nativeName);
  }
  mkdirSync(root, { recursive: true });
  for (const skill of skills) {
    const directory = join(root, skill.nativeName);
    mkdirSync(directory, { recursive: true });
    writeFileSync(join(directory, "SKILL.md"), skill.entryContent, { encoding: "utf8", flag: "wx" });
  }
}

export function verifyNativeSkillDiscovery(skills: PiSkillSnapshot[], result: LoadSkillsResult): void {
  const expected = new Set(skills.map((skill) => skill.nativeName));
  const found = result.skills.map((skill) => skill.name);
  if (result.diagnostics.length || found.length !== expected.size || found.some((name) => !expected.has(name))) {
    throw new FatalWorkerError("Pi native Skill discovery differs from the frozen run snapshot");
  }
}

/** The model sees run-independent locations so its prompt stays cacheable. */
export const NATIVE_SKILLS_DIR = "skills";
export const NATIVE_RUN_CWD = "canvas-agent-run";
export const nativeSkillLocation = (nativeName: string): string => `${NATIVE_SKILLS_DIR}/${nativeName}/SKILL.md`;

/**
 * Pi renders `<cwd>` and each Skill `<location>` from its prompt options. The
 * on-disk run directory is a fresh temp dir, so rendering the real paths would
 * change the system prompt on every run and defeat upstream prompt caching.
 * `before_agent_start` is Pi's supported hook for per-run prompt options; it
 * runs before each prompt and its options persist for every turn of that run,
 * so fresh and resumed sessions both send the same stable text. Restored
 * sessions with an older absolute-path system message are overridden by Pi's
 * section diff, which keeps the original section order.
 */
export function createStableSkillPromptExtension(skills: PiSkillSnapshot[]): InlineExtension {
  const names = new Set(skills.map((skill) => skill.nativeName));
  return {
    name: "canvas-stable-skill-locations",
    hidden: true,
    factory: (pi) => {
      pi.on("before_agent_start", (event) => {
        const options = event.systemPromptOptions;
        options.cwd = NATIVE_RUN_CWD;
        options.skills = (options.skills || []).map((skill) =>
          names.has(skill.name) ? { ...skill, filePath: nativeSkillLocation(skill.name) } : skill);
      });
    },
  };
}

const SEPARATOR = /[\\/]/;
const hasDotSegment = (input: string): boolean => input.split(SEPARATOR).some((part) => part === ".." || part === ".");

/** `skills/<nativeName>/<path>` relative to this run's cwd (the parent of `root`). */
function relativeSkillParts(input: string): string[] | undefined {
  if (isAbsolute(input) || /^[A-Za-z]:/.test(input)) return undefined;
  const parts = input.split(SEPARATOR);
  if (parts[0] !== NATIVE_SKILLS_DIR || parts.length < 3 || parts.some((part) => !part || part === "." || part === "..")) {
    throw new NativeSkillReadRejected("Skill read path must be a Skill location from the system prompt");
  }
  return parts.slice(1);
}

function resolveSkillPath(skills: PiSkillSnapshot[], root: string, input: string): { skill: PiSkillSnapshot; path: string } {
  if (hasDotSegment(input)) throw new NativeSkillReadRejected("Skill read path must not contain . or .. segments");
  const relativeParts = relativeSkillParts(input);
  const target = relativeParts ? join(root, ...relativeParts) : input;
  const rel = relative(resolve(root), resolve(target));
  const parts = rel.split(sep);
  if (parts.length < 2 || parts.some((part) => !part || part === ".." || part === ".") ||
      !resolve(target).startsWith(resolve(root) + sep)) {
    throw new NativeSkillReadRejected("Skill read path escapes this run");
  }
  const skill = skills.find((item) => item.nativeName === parts[0]);
  const path = parts.slice(1).join("/");
  if (!skill || !skill.files.some((file) => file.path === path && file.text)) {
    throw new NativeSkillReadRejected("Skill file is not listed for this run");
  }
  return { skill, path };
}

/**
 * A persisted Pi tool call contains either the stable relative location or,
 * for runs saved before stable locations, the previous worker's absolute one.
 */
export function rebaseNativeSkillPath(skills: PiSkillSnapshot[], root: string, previousPath: string): string {
  if (hasDotSegment(previousPath)) throw new NativeSkillReadRejected("Persisted Skill path is invalid");
  const relativeParts = relativeSkillParts(previousPath);
  if (relativeParts) {
    const rebased = join(root, ...relativeParts);
    resolveSkillPath(skills, root, rebased);
    return rebased;
  }
  const parts = previousPath.split(SEPARATOR);
  const index = parts.lastIndexOf(NATIVE_SKILLS_DIR);
  if (index < 0 || parts.length < index + 3) throw new NativeSkillReadRejected("Persisted Skill path is not within a Skill");
  const rebased = join(root, ...parts.slice(index + 1));
  resolveSkillPath(skills, root, rebased);
  return rebased;
}

export async function readNativeSkill(run: PiSnapshot, root: string, bridge: CanvasBridge, input: string,
  offset = 0, limit = MAX_PAGE_RUNES, signal?: AbortSignal): Promise<string> {
  if (run.skillRuntimeMode !== "pi-native") throw new FatalWorkerError("Native Skill runtime mode is invalid");
  if (!Number.isSafeInteger(offset) || offset < 0 ||
      !Number.isSafeInteger(limit) || limit < 1 || limit > MAX_PAGE_RUNES) {
    throw new NativeSkillReadRejected("Native Skill read range is invalid");
  }
  const { skill, path } = resolveSkillPath(run.skills || [], root, input);
  const file = skill.files.find((item) => item.path === path)!;
  const page = await bridge.readSkillFile(run, skill.nativeName, path, offset, limit, signal);
  if (page.nativeName !== skill.nativeName || page.skillId !== skill.id || page.versionId !== skill.versionId ||
      page.contentHash !== skill.contentHash || page.path !== path || page.sha256 !== file.sha256 || page.isEntry !== (path === "SKILL.md") ||
      page.offset !== Math.min(offset, page.totalRunes) || [...page.content].length > limit ||
      (!page.hasMore && page.offset === 0 && digest(page.content) !== file.sha256)) {
    throw new FatalWorkerError("Native Skill file response differs from the frozen snapshot");
  }
  if (/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/u.test(page.content)) {
    throw new FatalWorkerError("Native Skill file is not text");
  }
  const budget = Number.isSafeInteger(page.readCallsRemaining) ?
    `\n\n[Runtime read budget: ${JSON.stringify({ readCallsUsed: page.readCallsUsed,
      readCallsLimit: page.readCallsLimit, readCallsRemaining: page.readCallsRemaining })}]` : "";
  return page.content + (page.hasMore
    ? `\n\n[Truncated: ${page.totalRunes} Unicode characters total. Continue with read at offset=${page.offset + [...page.content].length}.]`
    : "") + budget;
}
