import { expect, test } from "bun:test";
import { App } from "antd";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
Object.assign(globalThis, { __APP_VERSION__: "test", __APP_CHANGELOG__: "" });
const { default: AgentSettingsPage } = await import("../src/pages/admin/settings/agent-settings-page");

test("Agent beta always exposes the Crew administrator switch even while disabled", () => {
    const html = renderToStaticMarkup(
        <MemoryRouter initialEntries={["/admin/settings/agent"]}>
            <QueryClientProvider client={new QueryClient()}>
                <App>
                    <AgentSettingsPage />
                </App>
            </QueryClientProvider>
        </MemoryRouter>,
    );
    expect(html).toContain('aria-label="启用 Crew 子代理"');
    expect(html).toContain('role="switch"');
    expect(html).toContain('href="#crew"');
    expect(html).toContain('aria-checked="false"');
});
