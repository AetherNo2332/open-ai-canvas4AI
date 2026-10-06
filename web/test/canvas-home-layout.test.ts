import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import CreatePage from "../src/pages/create";

test("homepage renders a single canvas entrance without the removed gallery", () => {
    const markup = renderToStaticMarkup(createElement(MemoryRouter, null, createElement(CreatePage)));
    expect(markup.match(/href="\/canvas"/g)).toHaveLength(1);
    expect(markup).toContain("从上一次离开的地方继续");
    expect(markup.match(/<img /g)).toHaveLength(1);
    expect(markup).toContain('class="canvas-home-character"');
    expect(markup).toContain('aria-hidden="true"');
    expect(markup).not.toContain("项目封面展示区");
    expect(markup).not.toContain("从一个想法开始");
});

for (const route of ["/", "/create"]) {
    test(`homepage ${route} directs creation to the canvas without a generation composer`, () => {
        const markup = renderToStaticMarkup(createElement(MemoryRouter, { initialEntries: [route] }, createElement(CreatePage)));
        expect(markup.match(/<a\b/g)).toHaveLength(1);
        expect(markup).toContain('href="/canvas"');
        expect(markup).toContain("从上一次离开的地方继续");
        expect(markup).not.toMatch(/<(input|textarea|form|button)\b/);
        expect(markup).not.toContain("打开素材库");
        expect(markup).not.toContain("创作模式");
    });
}

test("homepage removes the kicker and keeps the compact upward layout", async () => {
    const source = await Bun.file(new URL("../src/pages/create/index.tsx", import.meta.url)).text();
    const styles = await Bun.file(new URL("../src/pages/create/canvas-home.css", import.meta.url)).text();
    expect(source).not.toContain("canvas-home-kicker");
    expect(source).not.toContain("canvas-home-showcase");
    expect(source).not.toContain("canvas-home-art");
    expect(source).not.toContain("projectPreviewMedia");
    expect(styles).toContain("translateY(-");
    expect(styles).not.toContain("canvas-home-showcase");
});

test("skin scrollbars use the active canvas as their track", async () => {
    const styles = await Bun.file(new URL("../src/styles/site-skin.css", import.meta.url)).text();
    expect(styles).toContain("::-webkit-scrollbar-track");
    expect(styles).toContain("var(--site-canvas)");
});
