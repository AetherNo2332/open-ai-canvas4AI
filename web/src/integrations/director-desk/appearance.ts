import type { DirectorDeskAppearance } from "@desk/theme";
import { normalizeSkinDefinition } from "@/lib/skin-themes";

export function directorDeskAppearance(mode: "light" | "dark", skin: unknown, geometry: Pick<CSSStyleDeclaration, "getPropertyValue"> | undefined = typeof document === "undefined" ? undefined : document.defaultView?.getComputedStyle(document.documentElement)): DirectorDeskAppearance {
    const tokens = normalizeSkinDefinition(skin).tokens;
    const c = tokens[mode];
    const radius = (name: "button" | "input" | "card" | "overlay") => {
        const rendered = Number.parseFloat(geometry?.getPropertyValue(`--${name}-radius`) ?? "");
        // 嵌入页的桥接协议上限为 64px，足以让紧凑控件呈胶囊形。
        return Number.isFinite(rendered) && rendered >= 0 ? Math.min(rendered, 64) : tokens.components[`${name}Radius`];
    };
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
        radii: { button: radius("button"), input: radius("input"), card: radius("card"), overlay: radius("overlay") },
    };
}
