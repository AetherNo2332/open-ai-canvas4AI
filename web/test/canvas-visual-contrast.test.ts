import { describe, expect, test } from "bun:test";

import { getCanvasSkinTheme } from "../src/lib/canvas-theme";
import { DEFAULT_CLASSIC_SKIN, duplicateSkinDefinition } from "../src/lib/skin-themes";

describe("canvas visual contrast", () => {
    test("uses separately configured canvas and node surfaces in both themes", () => {
        for (const mode of ["light", "dark"] as const) {
            const skin = duplicateSkinDefinition(DEFAULT_CLASSIC_SKIN, ["classic"]);
            Object.assign(skin.tokens[mode], { workspace: "#123456", surface: "#234567" });
            const theme = getCanvasSkinTheme(mode, skin);
            expect(theme.canvas.background).toBe("#123456");
            expect(theme.node.fill).toBe("#234567");
            expect(theme.node.panel).toBe("#234567");
        }
    });

    test("does not overwrite configured node borders and overlays when the canvas changes", () => {
        for (const mode of ["light", "dark"] as const) {
            const skin = duplicateSkinDefinition(DEFAULT_CLASSIC_SKIN, ["classic"]);
            Object.assign(skin.tokens[mode], { workspace: "#123456", surface: "#234567", border: "#345678", overlay: "#456789" });
            const before = getCanvasSkinTheme(mode, skin);
            skin.tokens[mode].workspace = "#56789a";
            const after = getCanvasSkinTheme(mode, skin);
            expect(after.canvas.background).toBe("#56789a");
            expect(after.node.fill).toBe("#234567");
            expect(after.node.edge).toBe("#345678");
            expect(after.toolbar.panel).toBe("#456789");
            expect(after.spatial.elevated).toBe("#456789");
            expect(after.node).toEqual(before.node);
            expect(after.toolbar).toEqual(before.toolbar);
            expect(after.spatial).toEqual(before.spatial);
        }
    });

    test("uses configured grid colors while retaining canvas grid opacity", async () => {
        for (const mode of ["light", "dark"] as const) {
            const skin = duplicateSkinDefinition(DEFAULT_CLASSIC_SKIN, ["classic"]);
            skin.tokens[mode].workspaceGrid = "#6789ab";
            const theme = getCanvasSkinTheme(mode, skin);
            expect(theme.canvas.dot).toBe("#6789ab");
            expect(theme.canvas.line).toBe("#6789ab");
        }

        const source = await Bun.file(new URL("../src/components/canvas/infinite-canvas.tsx", import.meta.url)).text();
        expect(source).toContain('opacity: mode === "dots" ? 0.34 : 0.46');
    });

    test("keeps the original transparent edge for standard canvas nodes", async () => {
        const source = await Bun.file(new URL("../src/components/canvas/canvas-node.tsx", import.meta.url)).text();

        expect(source).toContain('border: isComposerNode ? "0" : "1px solid transparent"');
        expect(source).not.toContain('border: isComposerNode ? "0" : `1px solid ${theme.node.edge}`');
    });
});
