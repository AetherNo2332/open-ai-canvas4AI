import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { WorkspaceMotionLink, WorkspaceSidebarDisclosure } from "../src/components/layout/workspace-sidebar-motion";
import { WorkspaceSidebarNav } from "../src/components/layout/workspace-sidebar-nav";

describe("workspace sidebar motion accessibility", () => {
    for (const collapsed of [false, true]) {
        test(`sidebar keeps route selection and toggle semantics when collapsed=${collapsed}`, () => {
            const noop = () => {};
            const markup = renderToStaticMarkup(
                <MemoryRouter initialEntries={["/canvas"]}>
                    <WorkspaceSidebarNav collapsed={collapsed} onNavigate={noop} onOpenSearch={noop} onExpand={noop} onCollapse={noop} />
                </MemoryRouter>,
            );
            expect(markup).toContain('aria-current="page"');
            expect(markup).toContain('aria-label="自由画布"');
            expect(markup).toContain('href="/assets"');
            expect(markup).toContain(`aria-expanded="${!collapsed}"`);
            expect(markup).toContain('aria-controls="workspace-sidebar"');
            expect(markup.match(/aria-current="page"/g)).toHaveLength(1);
        });
    }

    test("closed groups do not expose focusable navigation", () => {
        const markup = renderToStaticMarkup(
            <WorkspaceSidebarDisclosure open={false}>
                <a href="/assets">资产</a>
            </WorkspaceSidebarDisclosure>,
        );
        expect(markup).toContain('inert=""');
        expect(markup).not.toContain('href="/assets"');
    });

    test("expanded groups retain their real links", () => {
        const markup = renderToStaticMarkup(
            <WorkspaceSidebarDisclosure open>
                <a href="/assets">资产</a>
            </WorkspaceSidebarDisclosure>,
        );
        expect(markup).toContain('href="/assets"');
        expect(markup).not.toContain('inert=""');
    });

    test("motion navigation preserves router href and current page semantics", () => {
        const markup = renderToStaticMarkup(
            <MemoryRouter>
                <WorkspaceMotionLink to="/canvas" aria-current="page" aria-label="自由画布">
                    画布
                </WorkspaceMotionLink>
            </MemoryRouter>,
        );
        expect(markup).toContain('href="/canvas"');
        expect(markup).toContain('aria-current="page"');
        expect(markup).toContain('aria-label="自由画布"');
    });
});

describe("workspace sidebar motion tokens", () => {
    test("reduced motion collapses the sidebar transition to zero while normal motion follows the skin duration", async () => {
        const { workspaceSidebarTransition } = await import("../src/components/layout/workspace-sidebar-motion");
        expect(workspaceSidebarTransition(true, 180).duration).toBe(0);
        expect(workspaceSidebarTransition(false, 180).duration).toBeCloseTo(0.18);
        // 0ms 是有效的皮肤配置，不能被回退成默认时长。
        expect(workspaceSidebarTransition(false, 0).duration).toBe(0);
    });

    test("sidebar motion CSS uses skin motion tokens, no transition: all, and honors reduced motion", async () => {
        const css = await Bun.file(new URL("../src/components/layout/workspace-sidebar-motion.css", import.meta.url)).text();
        expect(css).not.toMatch(/transition\s*:\s*all\b/);
        const durations = css.match(/\b\d+(?:\.\d+)?m?s\b/g) ?? [];
        expect(durations).toEqual([]);
        expect(css).toMatch(/var\(--(?:motion-[a-z-]+|skin-motion-state)\)/);
        expect(css).toContain("@media (prefers-reduced-motion: reduce)");
        expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgba?\(|oklch\(/);
        expect(css).toContain("var(--menu-radius)");
        expect(css).toContain("var(--ring)");
    });

    test("workspace shell mounts the motion sidebar with stable navigation landmarks", async () => {
        const source = await Bun.file(new URL("../src/components/layout/app-top-nav.tsx", import.meta.url)).text();
        expect(source).toContain('import "./workspace-sidebar-motion.css"');
        expect(source).toContain("<motion.aside");
        expect(source).toContain("data-motion-sidebar");
        expect(source).toContain('id="workspace-sidebar"');
        expect(source).toContain("workspaceSidebarTransition(");
    });
});
