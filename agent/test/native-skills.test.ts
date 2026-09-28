import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { DefaultResourceLoader, SettingsManager } from "@earendil-works/pi-coding-agent";
import { CanvasBridge, type PiSkillSnapshot, type PiSnapshot } from "../src/bridge.js";
import { materializeNativeSkills, readNativeSkill, rebaseNativeSkillPath, verifyNativeSkillDiscovery } from "../src/native-skills.js";

const entry = '---\nname: "skill-abc"\ndescription: "Use this for scripts"\n---\n# Body\n';
const digest = (value: string) => createHash("sha256").update(value).digest("hex");
function nativeSkill(): PiSkillSnapshot {
  return { id: "private-skill", nativeName: "skill-abc", displayName: "Scripts", description: "Use this for scripts",
    versionId: "version-1", version: "1", contentHash: "package-hash", entryPath: "SKILL.md", entryContent: entry,
    files: [{ path: "SKILL.md", sha256: digest(entry), size: Buffer.byteLength(entry), text: true },
      { path: "references/a.md", sha256: digest("Reference"), size: 9, text: true }] };
}

test("native Skills materialize only the entry, are discovered by Pi, and rebuild on restart", async () => {
  const parent = mkdtempSync(join(tmpdir(), "canvas-native-test-"));
  try {
    const skills = [nativeSkill()];
    const root = join(parent, "skills");
    materializeNativeSkills(skills, root);
    assert.equal(readFileSync(join(root, "skill-abc", "SKILL.md"), "utf8"), entry);
    assert.equal(existsSync(join(root, "skill-abc", "references", "a.md")), false);
    const settingsManager = SettingsManager.create(parent, join(parent, "agent"));
    const loader = new DefaultResourceLoader({ cwd: parent, agentDir: join(parent, "agent"), settingsManager,
      noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true,
      additionalSkillPaths: [root] });
    await loader.reload();
    assert.deepEqual(loader.getSkills().diagnostics, []);
    assert.deepEqual(loader.getSkills().skills.map((skill) => skill.name), ["skill-abc"]);
    verifyNativeSkillDiscovery(skills, loader.getSkills());
    assert.throws(() => verifyNativeSkillDiscovery([{ ...skills[0]!, nativeName: "other" }], loader.getSkills()));
    rmSync(root, { recursive: true });
    materializeNativeSkills(skills, root);
    assert.equal(readFileSync(join(root, "skill-abc", "SKILL.md"), "utf8"), entry);
    assert.throws(() => materializeNativeSkills([{ ...skills[0]!, entryContent: entry + "tampered" }], join(parent, "bad")));
    assert.throws(() => materializeNativeSkills([skills[0]!, { ...skills[0]!, id: "different-id" }], join(parent, "duplicate")), /Duplicate/);
    assert.equal(existsSync(join(parent, "duplicate")), false);
  } finally { rmSync(parent, { recursive: true, force: true }); }
});

test("native read accepts only frozen Skill paths and calls the bridge", async () => {
  const parent = mkdtempSync(join(tmpdir(), "canvas-native-read-"));
  try {
    const root = join(parent, "skills");
    const skills = [nativeSkill()];
    materializeNativeSkills(skills, root);
    const run = { runId: "run", userId: "user", skillRuntimeMode: "pi-native", skills } as unknown as PiSnapshot;
    const requests: string[] = [];
    const bridge = { async readSkillFile(_run: PiSnapshot, name: string, path: string) {
      requests.push(`${name}:${path}`);
      const content = path === "SKILL.md" ? entry : "Reference";
      return { nativeName: name, path, skillId: skills[0]!.id, versionId: skills[0]!.versionId,
        contentHash: skills[0]!.contentHash, sha256: digest(content), isEntry: path === "SKILL.md",
        offset: 0, limit: 12000, totalRunes: [...content].length, hasMore: false, content };
    } } as unknown as CanvasBridge;
    const entryPath = join(root, "skill-abc", "SKILL.md");
    assert.equal(await readNativeSkill(run, root, bridge, entryPath, 0, 12000), entry);
    assert.deepEqual(requests, ["skill-abc:SKILL.md"]);
    assert.equal(await readNativeSkill(run, root, bridge, join(root, "skill-abc", "references", "a.md"), 0, 12000), "Reference");
    assert.deepEqual(requests, ["skill-abc:SKILL.md", "skill-abc:references/a.md"]);
    for (const path of [resolve(parent, "elsewhere.md"), join(root, "other", "SKILL.md"),
      join(root, "skill-abc", "..", "other", "SKILL.md"), join(root, "skill-abc", "scripts", "run.js")]) {
      await assert.rejects(readNativeSkill(run, root, bridge, path, 0, 100));
    }
  } finally { rmSync(parent, { recursive: true, force: true }); }
});

test("entry reads reauthorize after bootstrap and never fall back to the local body", async () => {
  const parent = mkdtempSync(join(tmpdir(), "canvas-native-revoked-"));
  try {
    const root = join(parent, "skills");
    const skills = [nativeSkill()];
    materializeNativeSkills(skills, root);
    const run = { skillRuntimeMode: "pi-native", skills } as PiSnapshot;
    const bridge = { async readSkillFile() { throw new Error("authorization revoked"); } } as unknown as CanvasBridge;
    await assert.rejects(readNativeSkill(run, root, bridge, join(root, "skill-abc", "SKILL.md")), /authorization revoked/);
  } finally { rmSync(parent, { recursive: true, force: true }); }
});

test("native reads reject matching-hash binary text and mark Unicode pagination", async (t) => {
  const parent = mkdtempSync(join(tmpdir(), "canvas-native-page-"));
  try {
    const root = join(parent, "skills");
    for (const content of ["bad\0text", "bad\u0001text", "bad\u007ftext", "😀".repeat(12_005)]) {
      await t.test(content.startsWith("bad") ? `binary-${content.charCodeAt(3)}` : "Unicode pagination", async () => {
      const skill = nativeSkill();
      skill.files[1] = { path: "references/a.md", sha256: digest(content), size: Buffer.byteLength(content), text: true };
      const run = { skillRuntimeMode: "pi-native", skills: [skill] } as PiSnapshot;
      const bridge = { async readSkillFile(_run: PiSnapshot, name: string, path: string, offset: number, limit: number) {
        const runes = [...content];
        return { nativeName: name, skillId: skill.id, versionId: skill.versionId, contentHash: skill.contentHash,
          sha256: digest(content), path, isEntry: false, content: runes.slice(offset, offset + limit).join(""),
          offset, limit, totalRunes: runes.length, hasMore: offset + limit < runes.length };
      } } as unknown as CanvasBridge;
      const promise = readNativeSkill(run, root, bridge, join(root, "skill-abc", "references", "a.md"));
      if (content.startsWith("bad")) await assert.rejects(promise, /text/);
      else {
        const text = await promise;
        assert.ok(text.startsWith("😀".repeat(12_000)));
        assert.match(text, /offset[=: ]+12000/);
        assert.match(text, /12005/);
        assert.match(text, /Unicode/);
      }
      });
    }
  } finally { rmSync(parent, { recursive: true, force: true }); }
});

test("recovery rebases a persisted read from a previous worker root to the current run", () => {
  const skills = [nativeSkill()];
  const previous = resolve(tmpdir(), "previous-worker", "skills", "skill-abc", "references", "a.md");
  const current = resolve(tmpdir(), "new-worker", "skills");
  assert.equal(rebaseNativeSkillPath(skills, current, previous), join(current, "skill-abc", "references", "a.md"));
  for (const path of [resolve(tmpdir(), "previous-worker", "skills", "other", "SKILL.md"),
    resolve(tmpdir(), "previous-worker", "skills", "skill-abc", "scripts", "exec.js")]) {
    assert.throws(() => rebaseNativeSkillPath(skills, current, path));
  }
});
