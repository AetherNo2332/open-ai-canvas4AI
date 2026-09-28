import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { sidebarCheckinTitle, shouldShowSidebarCheckin } from "../src/lib/sidebar-checkin";

describe("sidebar checkin offer", () => {
    test("shows only when credits and a checkin bonus are enabled and today is unclaimed", () => {
        expect(shouldShowSidebarCheckin({ creditsEnabled: true, checkinBonusMicrocredits: 100_000_000, checkedInToday: false })).toBe(true);
        expect(shouldShowSidebarCheckin({ creditsEnabled: false, checkinBonusMicrocredits: 100_000_000, checkedInToday: false })).toBe(false);
        expect(shouldShowSidebarCheckin({ creditsEnabled: true, checkinBonusMicrocredits: 0, checkedInToday: false })).toBe(false);
        expect(shouldShowSidebarCheckin({ creditsEnabled: true, checkinBonusMicrocredits: 100_000_000, checkedInToday: true })).toBe(false);
    });

    test("uses the configured brand name instead of Buddy", () => {
        expect(sidebarCheckinTitle("影策")).toBe("影策加油站");
        expect(sidebarCheckinTitle(" 本地工作室 ")).toBe("本地工作室加油站");
        expect(sidebarCheckinTitle("")).toBe("影策加油站");
    });
});

describe("workspace sidebar checkin card", () => {
    test("sits above account storage and uses the claim card colors without a light-blue stage", () => {
        const sidebar = readFileSync(resolve(import.meta.dir, "../src/components/layout/workspace-sidebar-nav.tsx"), "utf8");
        const card = readFileSync(resolve(import.meta.dir, "../src/components/layout/workspace-sidebar-checkin.tsx"), "utf8");
        const css = readFileSync(resolve(import.meta.dir, "../src/styles/globals.css"), "utf8");
        const checkinIndex = sidebar.indexOf("<WorkspaceSidebarCheckin collapsed={collapsed} />");
        const storageIndex = sidebar.indexOf("<WorkspaceSidebarStorageMeter collapsed={collapsed} />");
        const start = css.indexOf(".app-user-workspace .app-workspace-sidebar-checkin {");
        const end = css.indexOf(".app-user-workspace .app-workspace-sidebar-storage {", start);
        const block = css.slice(start, end);

        expect(checkinIndex).toBeGreaterThan(-1);
        expect(storageIndex).toBeGreaterThan(checkinIndex);
        expect(card).toContain("立即领取");
        expect(card).toContain("今日可领");
        expect(card).toContain("checkinCredits()");
        expect(block).toContain("background: #fff;");
        expect(block).toContain("background: #171717;");
        expect(block).toContain("color: #12c8a0;");
        expect(block).not.toMatch(/#(?:dbeafe|e8f1ff|eef4ff|e6f0ff|d6e8ff)/i);
    });

    test("shows a yellow canary badge beside the brand name", () => {
        const sidebar = readFileSync(resolve(import.meta.dir, "../src/components/layout/workspace-sidebar-nav.tsx"), "utf8");
        const css = readFileSync(resolve(import.meta.dir, "../src/styles/globals.css"), "utf8");
        const start = css.indexOf(".app-user-workspace .app-workspace-canary-badge {");
        const end = css.indexOf("}", start);
        const badge = css.slice(start, end);
        const brandNameIndex = sidebar.indexOf("{appearance.brandName}");
        const badgeIndex = sidebar.indexOf("className=\"app-workspace-canary-badge\"");

        expect(brandNameIndex).toBeGreaterThan(-1);
        expect(badgeIndex).toBeGreaterThan(brandNameIndex);
        expect(sidebar.slice(brandNameIndex, badgeIndex)).toContain("app-workspace-brand-title-row");
        expect(sidebar).toContain(">canary</span>");
        expect(badge).toContain("background: #facc15;");
        expect(badge).toContain("color: #111;");
        expect(badge).toContain("font-family: Inter, ui-sans-serif, system-ui, sans-serif;");
    });
});
