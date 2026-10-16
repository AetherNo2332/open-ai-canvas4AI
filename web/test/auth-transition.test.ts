import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const source = (path: string) => readFileSync(new URL(`../src/${path}`, import.meta.url), "utf8");

test("auth route transitions use staged content motion with reduced-motion fallback", () => {
    const scene = source("pages/auth/auth-scene.tsx");
    const css = source("pages/auth/auth-scene.css");

    expect(scene).toContain("auth-scene-form-transition");
    expect(scene).toContain("data-auth-mode");
    expect(scene).toContain("useAuthMotion(authMode)");
    expect(scene).toContain("context={{ onValidationError }}");
    expect(scene).not.toContain("key={authMode}");
    expect(css).not.toContain("@keyframes auth-scene-enter");
    expect(css).toContain("prefers-reduced-motion");
    expect(css).toContain("animation: none");
    expect(css).not.toContain("transition: all");
    expect(css).toContain("border-radius: 999px !important");
    expect(css).toMatch(/\.auth-scene-form-body :where\(\.ant-input, \.ant-input-affix-wrapper\)\s*\{[^}]*background: var\(--auth-card-bg, var\(--background\)\) !important/);
    expect(css).toContain("background: inherit !important");
    expect(css).toContain("border-radius: 0 !important");
    expect(css).toContain("background: #fff");
});
