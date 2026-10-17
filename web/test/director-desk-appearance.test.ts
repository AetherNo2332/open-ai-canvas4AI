import { expect, test } from "bun:test";
import { applyDirectorDeskAppearance } from "@desk/theme";
import { directorDeskAppearance } from "@/integrations/director-desk/appearance";
import { normalizeSkinDefinition } from "@/lib/skin-themes";

function rootFixture() {
    const properties = new Map<string, string>();
    const root = {
        dataset: {},
        style: {
            colorScheme: "",
            setProperty: (key: string, value: string) => {
                properties.set(key, value);
            },
        },
    };
    return { root: root as unknown as HTMLElement, properties };
}

test("default/custom skins preserve both modes and zero/nonzero radii inside the iframe", () => {
    const skin = structuredClone(normalizeSkinDefinition(null));
    skin.id = "desk-custom";
    skin.locked = false;
    skin.tokens.light.surface = "#fef8e1";
    skin.tokens.dark.surface = "#18201a";
    for (const radius of [0, 8]) {
        skin.tokens.components.buttonRadius = radius;
        for (const mode of ["light", "dark"] as const) {
            const { root, properties } = rootFixture();
            const appearance = directorDeskAppearance(mode, skin);
            expect(applyDirectorDeskAppearance(appearance, root)).toBe(true);
            expect(properties.get("--desk-surface")).toBe(skin.tokens[mode].surface);
            expect(properties.get("--desk-radius-button")).toBe(`${radius}px`);
            expect(root.dataset.theme).toBe(mode);
            expect(root.style.colorScheme).toBe(mode);
        }
    }
    const { root, properties } = rootFixture();
    expect(applyDirectorDeskAppearance(directorDeskAppearance("light", null), root)).toBe(true);
    expect(properties.get("--desk-text")).toBe(normalizeSkinDefinition(null).tokens.light.text);
});

test("invalid appearance is rejected atomically and unknown CSS slots are ignored", () => {
    for (const invalid of [null, { mode: "light" }, { ...directorDeskAppearance("dark", null), colors: { text: "url(https://invalid)" } }, { ...directorDeskAppearance("light", null), radii: { button: -1 } }]) {
        const { root, properties } = rootFixture();
        expect(applyDirectorDeskAppearance(invalid, root)).toBe(false);
        expect(properties.size).toBe(0);
        expect(root.dataset.theme).toBeUndefined();
    }
    const { root, properties } = rootFixture();
    const payload = directorDeskAppearance("dark", null);
    Object.assign(payload.colors!, { "background-image": "url(https://invalid)" });
    expect(applyDirectorDeskAppearance(payload, root)).toBe(true);
    expect(properties.has("--desk-background-image")).toBe(false);
});

test("iframe geometry follows rendered host radii within the bridge limits", () => {
    const values: Record<string, string> = { "--button-radius": "999px", "--input-radius": "999px", "--card-radius": "16px", "--overlay-radius": "20px" };
    const geometry = { getPropertyValue: (key: string) => values[key] ?? "" };
    const appearance = directorDeskAppearance("light", null, geometry);
    expect(appearance.radii).toEqual({ button: 64, input: 64, card: 16, overlay: 20 });
    expect(applyDirectorDeskAppearance(appearance, rootFixture().root)).toBe(true);
});
