export const DESK_COLOR_TOKENS = [
  "canvas",
  "surface",
  "subtle",
  "raised",
  "overlay",
  "text",
  "muted",
  "border",
  "control",
  "hover",
  "primary",
  "onPrimary",
  "selected",
  "selectedText",
  "danger",
  "focus",
] as const;
export const DESK_RADIUS_TOKENS = [
  "button",
  "input",
  "card",
  "overlay",
] as const;
export type DirectorDeskAppearance = {
  mode: "light" | "dark";
  colors?: Record<(typeof DESK_COLOR_TOKENS)[number], string>;
  radii?: Record<(typeof DESK_RADIUS_TOKENS)[number], number>;
};

/** Only fixed color/radius slots may cross the iframe boundary, never arbitrary CSS. */
export function applyDirectorDeskAppearance(
  payload: unknown,
  root: HTMLElement = document.documentElement,
): boolean {
  if (!payload || typeof payload !== "object") return false;
  const value = payload as DirectorDeskAppearance;
  if (value.mode !== "light" && value.mode !== "dark") return false;
  if (!value.colors || !value.radii) return false;
  if (
    DESK_COLOR_TOKENS.some(
      (key) => !/^#[0-9a-f]{6}([0-9a-f]{2})?$/i.test(value.colors![key] ?? ""),
    )
  )
    return false;
  if (
    DESK_RADIUS_TOKENS.some(
      (key) =>
        !Number.isFinite(value.radii![key]) ||
        value.radii![key] < 0 ||
        value.radii![key] > 64,
    )
  )
    return false;
  for (const key of DESK_COLOR_TOKENS)
    root.style.setProperty(`--desk-${key}`, value.colors[key]);
  for (const key of DESK_RADIUS_TOKENS)
    root.style.setProperty(`--desk-radius-${key}`, `${value.radii[key]}px`);
  root.dataset.theme = value.mode;
  root.dataset.deskThemed = "true";
  root.style.colorScheme = value.mode;
  return true;
}
