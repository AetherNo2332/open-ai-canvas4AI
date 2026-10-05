import { createHash } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { isAbsolute, join, relative, resolve, sep } from "node:path";
import type { LoadSkillsResult } from "@earendil-works/pi-coding-agent";
import type { CanvasBridge, PiSkillSnapshot, PiSnapshot } from "./bridge.js";
import { FatalWorkerError } from "./tool-disclosure.js";

const MAX_ENTRY_BYTES = 512 << 10;
const MAX_PAGE_RUNES = 12_000;
const digest = (value: string): string => createHash("sha256").update(value).digest("hex");

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

function resolveSkillPath(skills: PiSkillSnapshot[], root: string, input: string): { skill: PiSkillSnapshot; path: string } {
  if (!isAbsolute(input) || input.split(/[\\/]/).some((part) => part === ".." || part === ".")) {
    throw new FatalWorkerError("Skill read path must be an absolute path inside this run");
  }
  const rel = relative(resolve(root), resolve(input));
  const parts = rel.split(sep);
  if (parts.length < 2 || parts.some((part) => !part || part === ".." || part === ".") ||
      !resolve(input).startsWith(resolve(root) + sep)) {
    throw new FatalWorkerError("Skill read path escapes this run");
  }
  const skill = skills.find((item) => item.nativeName === parts[0]);
  const path = parts.slice(1).join("/");
  if (!skill || !skill.files.some((file) => file.path === path && file.text)) {
    throw new FatalWorkerError("Skill file is not listed for this run");
  }
  return { skill, path };
}

/** A persisted Pi tool call contains the previous worker's absolute location. */
export function rebaseNativeSkillPath(skills: PiSkillSnapshot[], root: string, previousPath: string): string {
  if (!isAbsolute(previousPath) || previousPath.split(/[\\/]/).some((part) => part === ".." || part === ".")) {
    throw new FatalWorkerError("Persisted Skill path is invalid");
  }
  const parts = previousPath.split(/[\\/]/);
  const index = parts.lastIndexOf("skills");
  if (index < 0 || parts.length < index + 3) throw new FatalWorkerError("Persisted Skill path is not within a Skill");
  const rebased = join(root, ...parts.slice(index + 1));
  resolveSkillPath(skills, root, rebased);
  return rebased;
}

export async function readNativeSkill(run: PiSnapshot, root: string, bridge: CanvasBridge, input: string,
  offset = 0, limit = MAX_PAGE_RUNES, signal?: AbortSignal): Promise<string> {
  if (run.skillRuntimeMode !== "pi-native" || !Number.isSafeInteger(offset) || offset < 0 ||
      !Number.isSafeInteger(limit) || limit < 1 || limit > MAX_PAGE_RUNES) {
    throw new FatalWorkerError("Native Skill read range or runtime mode is invalid");
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
  return page.content + (page.hasMore
    ? `\n\n[Truncated: ${page.totalRunes} Unicode characters total. Continue with read at offset=${page.offset + [...page.content].length}.]`
    : "");
}
