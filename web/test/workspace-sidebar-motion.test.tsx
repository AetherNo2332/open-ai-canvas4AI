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
