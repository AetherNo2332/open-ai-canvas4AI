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

describe("workspace sidebar", () => {
    test("does not mount the removed checkin card in either sidebar mode", () => {
        const sidebar = readFileSync(resolve(import.meta.dir, "../src/components/layout/workspace-sidebar-nav.tsx"), "utf8");
        expect(sidebar).not.toContain("WorkspaceSidebarCheckin");
        expect(sidebar).toContain("<WorkspaceSidebarStorageMeter collapsed={collapsed} />");
    });

    test("canary badge consumes the active skin instead of a fixed yellow", () => {
        const css = readFileSync(resolve(import.meta.dir, "../src/styles/home-skin.css"), "utf8");
        const start = css.indexOf(".app-workspace-canary-badge {");
        const badge = css.slice(start, css.indexOf("}", start));
        expect(badge).toContain("background: var(--control-selected-bg)");
        expect(badge).toContain("color: var(--control-selected-fg)");
    });

});
