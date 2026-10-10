import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentSubagentList } from "../src/components/canvas/canvas-agent-subagent-list";
import { canvasThemes } from "../src/lib/canvas-theme";

test("empty subagent list creates no markup or layout space", () => {
    expect(renderToStaticMarkup(<AgentSubagentList items={[]} theme={canvasThemes.light} onSelect={() => {}} />)).toBe("");
});
test("collaborators have distinct vector icons and accessible conversation buttons", () => {
    const html = renderToStaticMarkup(
        <AgentSubagentList
            theme={canvasThemes.light}
            onSelect={() => {}}
            items={[
                { id: "director", name: "导演", state: "running" },
                { id: "writer", name: "编剧", state: "waiting" },
                { id: "reviewer", name: "审稿", state: "done" },
                { id: "research", name: "研究", state: "failed" },
            ]}
        />,
    );
    expect(html).toContain("agent-subagent-list");
    expect(html).toContain('aria-label="查看导演的会话 · 运行中"');
    expect(html).toContain('aria-label="查看编剧的会话 · 等待中"');
    expect(html.match(/<svg /g)).toHaveLength(4);
    expect(new Set(html.match(/<svg[\s\S]*?<\/svg>/g)).size).toBe(4);
    expect(html).not.toContain("<img");
    expect(html.match(/class="agent-subagent-status"/g)).toHaveLength(4);
    expect(html).toContain('data-state="running"');
});
test("avatar button opens the matching agent rather than its neighbor", () => {
    let selected = "";
    const tree = AgentSubagentList({
        items: [{ id: "child-7", name: "研究", state: "waiting" }],
        theme: canvasThemes.light,
        onSelect: (id) => {
            selected = id;
        },
    });
    const button = tree!.props.children[1].props.children[0].props.children;
    button.props.onClick();
    expect(selected).toBe("child-7");
});
