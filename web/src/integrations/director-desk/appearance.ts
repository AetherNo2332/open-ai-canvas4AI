import type { DirectorDeskAppearance } from "@desk/theme";
import { normalizeSkinDefinition } from "@/lib/skin-themes";

export function directorDeskAppearance(mode: "light" | "dark", skin: unknown): DirectorDeskAppearance {
    const tokens = normalizeSkinDefinition(skin).tokens;
    const c = tokens[mode];
    return {
        mode,
        colors: {
            canvas: c.workspace,
            surface: c.surface,
            subtle: c.surfaceSubtle,
            raised: c.surfaceRaised,
            overlay: c.overlay,
            text: c.text,
            muted: c.textMuted,
            border: c.border,
            control: c.control,
            hover: c.controlHover,
            primary: c.primary,
            onPrimary: c.primaryForeground,
            selected: c.selected,
            selectedText: c.selectedForeground,
            danger: c.danger,
            focus: c.controlFocus,
        },
        radii: { button: tokens.components.buttonRadius, input: tokens.components.inputRadius, card: tokens.components.cardRadius, overlay: tokens.components.overlayRadius },
    };
}
