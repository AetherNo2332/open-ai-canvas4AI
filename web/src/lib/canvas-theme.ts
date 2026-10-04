import { normalizeSkinDefinition, type SkinDefinition } from "@/lib/skin-themes";
import { useAppearanceStore } from "@/stores/use-appearance-store";

export type CanvasColorTheme = "light" | "dark";
export type CanvasBackgroundMode = "dots" | "lines" | "blank";
export const DEFAULT_CANVAS_COLOR_THEME: CanvasColorTheme = "dark";

export type CanvasTheme = {
    canvas: Record<"background" | "dot" | "line" | "selectionFill", string>;
    node: Record<"label" | "agentUserMessage" | "fill" | "panel" | "stroke" | "edge" | "shadow" | "hoverShadow" | "activeStroke" | "placeholder" | "text" | "muted" | "faint", string>;
    frame: Record<"fill" | "stroke" | "activeFill" | "activeStroke" | "preview", string>;
    toolbar: Record<"panel" | "border" | "item" | "itemHover" | "activeBg" | "activeText", string>;
    spatial: Record<"surface" | "elevated" | "dropzone" | "glow" | "glowStrong" | "shadow", string>;
    timeline: Record<"trackFill" | "trackBorder" | "clipVideo" | "clipAudio" | "clipSubtitle" | "clipSelectedBorder" | "handle" | "rulerTick" | "rulerLabel" | "playhead" | "entryActive" | "entryHover", string>;
    accent: Record<"primary" | "primarySoft" | "onPrimary" | "danger", string>;
};

/** Actual values also work in renderers that cannot resolve CSS variables. */
export function getCanvasSkinTheme(mode: CanvasColorTheme, skinValue: unknown): CanvasTheme {
    const c = normalizeSkinDefinition(skinValue).tokens[mode];
    const alpha = (color: string, opacity: number) => `${color.slice(0, 7)}${Math.round(opacity * 255).toString(16).padStart(2, "0")}`;
    return {
        canvas: { background: c.workspace, dot: c.workspaceGrid, line: c.workspaceGrid, selectionFill: c.selected },
        node: {
            label: c.textMuted, agentUserMessage: c.selected, fill: c.surface, panel: c.surface,
            stroke: c.border, edge: c.border, shadow: "none", hoverShadow: "none",
            activeStroke: c.primary, placeholder: c.controlDisabledForeground,
            text: c.text, muted: c.textMuted, faint: c.iconMuted,
        },
        frame: { fill: alpha(c.text, .025), stroke: c.border, activeFill: c.selected, activeStroke: c.primary, preview: c.overlay },
        toolbar: { panel: c.overlay, border: c.border, item: c.icon, itemHover: c.controlHover, activeBg: c.selected, activeText: c.selectedForeground },
        spatial: { surface: c.surface, elevated: c.overlay, dropzone: c.surfaceSubtle, glow: alpha(c.primary, .14), glowStrong: alpha(c.primary, .42), shadow: "transparent" },
        timeline: {
            trackFill: c.surfaceSubtle, trackBorder: c.border,
            clipVideo: alpha(c.primary, .16), clipAudio: alpha(c.success, .14), clipSubtitle: alpha(c.warning, .16),
            clipSelectedBorder: c.primary, handle: c.surfaceRaised, rulerTick: c.border,
            rulerLabel: c.textMuted, playhead: c.primary, entryActive: c.selected, entryHover: c.controlHover,
        },
        accent: { primary: c.primary, primarySoft: c.selected, onPrimary: c.primaryForeground, danger: c.danger },
    };
}

// Compatibility facade for existing renderers; read the live site skin.
const paletteCache = new WeakMap<SkinDefinition, Partial<Record<CanvasColorTheme, CanvasTheme>>>();
function livePalette(mode: CanvasColorTheme) {
    const skin = useAppearanceStore.getState().appearance.activeSkin;
    const cached = paletteCache.get(skin) ?? {};
    if (!cached[mode]) cached[mode] = getCanvasSkinTheme(mode, skin);
    paletteCache.set(skin, cached);
    return cached[mode]!;
}
export const canvasThemes: Record<CanvasColorTheme, CanvasTheme> = {
    get light() { return livePalette("light"); },
    get dark() { return livePalette("dark"); },
};
