import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import AgentToolCount from "../src/pages/admin/settings/components/agent-tool-count";

test("Agent tool count displays the server value including zero", () => {
    for (const count of [44, 0, 123]) {
        const html = renderToStaticMarkup(<AgentToolCount count={count} />);
        expect(html).toContain('aria-label="当前工具总数"');
        expect(html).toContain(`>${count}</output>`);
    }
});

test("unavailable and loading counts are not presented as zero", () => {
    const unavailable = renderToStaticMarkup(<AgentToolCount />);
    const loading = renderToStaticMarkup(<AgentToolCount loading />);
    expect(unavailable).toContain("暂不可用");
    expect(loading).toContain("读取中…");
    expect(loading).toContain('aria-busy="true"');
    expect(unavailable).not.toContain(">0</output>");
    expect(loading).not.toContain(">0</output>");
});
