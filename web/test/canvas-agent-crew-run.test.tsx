import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { CrewRunCard } from "../src/components/canvas/canvas-agent-crew-run-card";
import { canvasThemes } from "../src/lib/canvas-theme";
import type { CrewRunView } from "../src/services/api/agent-crew";

const run: CrewRunView = {
    id: "run",
    crewId: "crew",
    canvasId: "canvas",
    coordinatorRunId: "coord",
    status: "waiting_approval",
    revision: 1,
    workspaceHash: "hash",
    workspaceRevision: 1,
    latestSequence: 2,
    createdAt: "now",
    budget: { maxCredits: 10, maxSteps: 20, maxGenerationTasks: 0, maxVideoSeconds: 0, maxConcurrentMembers: 2 },
    members: [{ id: "member-run", memberId: "member", agentRunId: "agent", role: "member", name: "编剧", permissionMode: "propose", status: "completed", attempt: 1, summary: "故事梗概" }],
    approval: { approvalId: "approval", snapshotHash: "hash", summary: "提案", preview: { kind: "canvas", items: [] } },
};
test("Crew approval card exposes rejection and structured member summaries", () => {
    const html = renderToStaticMarkup(<CrewRunCard run={run} theme={canvasThemes.light} onApprove={() => {}} />);
    expect(html).toContain("故事梗概");
    expect(html).toContain("拒绝提案");
    expect(html).not.toContain("提交画布");
});
