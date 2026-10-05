import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const adminUI = readFileSync(join(import.meta.dir, "..", "src", "pages", "admin", "components", "admin-ui.tsx"), "utf8");
const appearancePage = readFileSync(join(import.meta.dir, "..", "src", "pages", "admin", "settings", "appearance-settings-page.tsx"), "utf8");
const adminCSS = readFileSync(join(import.meta.dir, "..", "src", "styles", "admin-ui.css"), "utf8");

// swiss-design：外观页区块用直角发丝线外壳，不再用圆角卡片阴影。
test("appearance settings page uses the swiss section shell", () => {
    expect(appearancePage).not.toContain("SettingsSectionCard");
    expect(appearancePage).toContain("SettingsSection");
});

test("SettingsSection stays square and shadowless", () => {
    const start = adminUI.indexOf("export function SettingsSection(");
    expect(start).toBeGreaterThan(-1);
    const nextExport = adminUI.indexOf("\nexport function ", start + 1);
    const source = adminUI.slice(start, nextExport === -1 ? undefined : nextExport);

    expect(source).toContain("admin-swiss-section");
    expect(source).not.toMatch(/\brounded-[a-z0-9[]/);
    expect(source).not.toMatch(/\bshadow-[a-z0-9[]/);
    expect(source).not.toContain("SettingsSectionCard");
});

test("swiss section styles keep hairline borders and zero radius", () => {
    const start = adminCSS.indexOf(".admin-swiss-section");
    expect(start).toBeGreaterThan(-1);
    const block = adminCSS.slice(start, adminCSS.indexOf(".admin-skin-library-toolbar", start));

    expect(block).toContain("border-top: 1px solid var(--admin-divider)");
    expect(block).toContain("letter-spacing");
    expect(block).toContain("text-transform: uppercase");
    const radiusDeclarations = [...block.matchAll(/border-radius:\s*([^;]+);/g)].map((match) => match[1].trim());
    const shadowDeclarations = [...block.matchAll(/box-shadow:\s*([^;]+);/g)].map((match) => match[1].trim());
    expect(radiusDeclarations.length).toBeGreaterThan(0);
    expect(radiusDeclarations.every((value) => value === "0")).toBe(true);
    expect(shadowDeclarations.every((value) => value === "none")).toBe(true);
});
