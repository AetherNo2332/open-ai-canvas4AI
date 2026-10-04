import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentSubagentList } from "../src/components/canvas/canvas-agent-subagent-list";
import { canvasThemes } from "../src/lib/canvas-theme";

test("empty subagent list creates no markup or layout space", () => {
    expect(renderToStaticMarkup(<AgentSubagentList items={[]} theme={canvasThemes.light} />)).toBe("");
});
test("subagent avatars expose names, states and circular images", () => {
    const html = renderToStaticMarkup(<AgentSubagentList theme={canvasThemes.light} items={[
        { id: "director", name: "导演", avatarUrl: "/icons/subagents/director.svg", state: "running" },
        { id: "writer", name: "编剧", avatarUrl: "/icons/subagents/writer.svg", state: "waiting" },
    ]} />);
    expect(html).toContain("agent-subagent-list");
    expect(html).toContain('aria-label="导演"');
    expect(html).toContain('aria-label="编剧"');
    expect(html.match(/<img /g)?.length).toBe(2);
    expect(html).toContain("rounded-full");
    expect(html).toContain('data-state="running"');
});
test("subagent transitions respect reduced motion and keyboard focus", async () => {
    const css = await Bun.file(new URL("../src/components/canvas/canvas-agent-subagent-list.css", import.meta.url)).text();
    expect(css).toContain("var(--motion-ease-out)");
    expect(css).toContain("@media (prefers-reduced-motion: reduce)");
    expect(css).toContain("transform: none !important");
    expect(css).toContain(":focus-visible");
    expect(css).not.toContain("linear");
});
