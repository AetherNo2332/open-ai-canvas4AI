import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentContextRing } from "@/components/canvas/canvas-cloud-agent-panel";
import { emptyAgentContextUsage, presentAgentContextUsage } from "@/lib/canvas/agent-context-usage";

test("context indicator retains usage but has no compaction marker", () => {
    const view = presentAgentContextUsage({ ...emptyAgentContextUsage("ui-run"), reading: { modelLimitConfigured: true, estimatedInputTokens: 65400, usableInputTokens: 950800, compactAtTokens: 808180, pressureRatio: 65400 / 950800 } });
    const html = renderToStaticMarkup(<AgentContextRing view={view} />);
    expect(html).toContain("7%");
    expect(html).not.toContain("agent-context-ring-marker");
    expect(html).not.toContain("--agent-context-marker-angle");
});
