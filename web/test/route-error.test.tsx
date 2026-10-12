import { expect, test } from "bun:test";
import { createMemoryRouter, RouterProvider } from "react-router";
import { renderToStaticMarkup } from "react-dom/server";

import RouteErrorPage from "../src/pages/route-error";

test("route error presents the cause and retains both recovery actions", () => {
    const router = createMemoryRouter([{ id: "broken-route", path: "/broken", element: <div />, errorElement: <RouteErrorPage /> }], {
        initialEntries: ["/broken"],
        hydrationData: { errors: { "broken-route": new Error("Unable to load this page") } },
    });

    const html = renderToStaticMarkup(<RouterProvider router={router} />);

    expect(html).toContain("Oops…");
    expect(html).toContain("出现了一些错误");
    expect(html).toContain("错误原因");
    expect(html).toContain("Unable to load this page");
    expect(html).toContain('fill="currentColor"');
    expect(html).toContain("重新加载");
    expect(html).toContain("返回主页");
    expect(html).toContain("ant-btn-default");
    expect(html).toContain("ant-btn-primary");
    expect(html).not.toContain("影策工作台");
    expect(html).not.toContain("Page recovery");
    expect(html).not.toContain("Route error");
    expect(html).toContain("route-error-page");
    expect(html).toContain("route-error-grid");
    expect(html).toContain("route-error-diagnostic");
    expect(html).toContain("route-error-primary");
    expect(html).toContain("route-error-arrow");
    expect(html).toContain('aria-labelledby="route-error-title"');
});

test("route error uses site skin tokens and the swiss-design rules", async () => {
    const css = await Bun.file(new URL("../src/pages/route-error.css", import.meta.url)).text();

    // 配色：全部来自站点外观 / 主体皮肤 token，且不写死颜色。
    expect(css).toContain("var(--background)");
    expect(css).toContain("var(--foreground)");
    expect(css).toContain("var(--border)");
    expect(css).toContain("var(--palette-status-error)");
    expect(css).toContain("var(--button-primary-bg)");
    expect(css).toContain("var(--button-primary-hover-bg)");
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}/);

    // swiss-design：透明度做层级、正文 60ch、IBM Plex Sans、直角构件、直接可用键盘焦点。
    expect(css).toContain("color-mix(in oklab, var(--foreground)");
    expect(css).toContain("60ch");
    expect(css).toContain("IBM Plex Sans");
    expect(css).toContain("border-radius: 0");
    expect(css).toContain(":focus-visible");
    expect(css).toContain("prefers-reduced-motion");

    // 悬停只允许改变颜色：不得出现缩放或位移反馈导致按钮尺寸变化。
    expect(css).not.toContain("scale(");
    expect(css).not.toContain("translateX(");
    expect(css).not.toContain("transition: transform");
});
