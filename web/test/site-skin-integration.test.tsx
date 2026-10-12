import { afterEach, expect, test } from "bun:test";
import { buildNav } from "../src/components/layout/workspace-sidebar-nav";
import { buildWorkspaceCommandEntries } from "../src/components/layout/workspace-command-palette";
import { getAntThemeConfig, getWorkspaceAntThemeConfig } from "../src/lib/app-theme";
import { getIsolatedAdminAntTheme } from "../src/pages/admin/theme/admin-ant-theme";
import { getCanvasSkinTheme } from "../src/lib/canvas-theme";
import { DEFAULT_CLASSIC_SKIN, duplicateSkinDefinition } from "../src/lib/skin-themes";
import { useUserStore } from "../src/stores/use-user-store";

const original = useUserStore.getState().features;
afterEach(() => useUserStore.getState().setFeatures(original));

for (const enabled of [false, true]) {
    test(`short drama entrances follow availability (${enabled}) for users and administrators`, () => {
        const features = { ...original, shortDramaEnabled: enabled };
        useUserStore.getState().setFeatures(features);
        for (const admin of [false, true]) {
            const links = buildNav(features, admin).groups.flatMap((group) => group.items);
            expect(links.some((item) => item.to === "/projects")).toBe(enabled);
            expect(links.some((item) => item.to === "/canvas")).toBe(true);
        }
        expect(buildWorkspaceCommandEntries(features).some((entry) => entry.to === "/projects")).toBe(enabled);
    });
}

for (const dark of [false, true]) {
    test(`all UI themes consume the site's zero radii and palette (${dark})`, () => {
        const skin = duplicateSkinDefinition(DEFAULT_CLASSIC_SKIN, ["classic"]);
        const mode = dark ? "dark" : "light";
        Object.assign(skin.tokens[mode], { primary: "#234567", surface: "#345678", adminSurface: "#456789", text: "#abcdef" });
        for (const theme of [getAntThemeConfig(dark, skin), getWorkspaceAntThemeConfig(dark, skin), getIsolatedAdminAntTheme(dark, skin)]) {
            expect(theme.token?.borderRadius).toBe(0);
            expect(theme.components?.Button?.borderRadius).toBe(0);
            expect(theme.components?.Input?.borderRadius).toBe(0);
            expect(theme.components?.Modal?.borderRadiusLG).toBe(0);
        }
        expect(getCanvasSkinTheme(mode, skin).node).toMatchObject({ fill: "#345678", text: "#abcdef", activeStroke: "#234567" });
        expect(getIsolatedAdminAntTheme(dark, skin).token?.colorBgContainer).toBe("#456789");
    });
}
