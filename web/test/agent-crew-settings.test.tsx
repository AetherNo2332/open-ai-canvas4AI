import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { CrewSettings } from "../src/components/canvas/canvas-agent-crew-settings";
import { CrewRunCard } from "../src/components/canvas/canvas-agent-crew-run-card";
import type { CrewView, CrewRunView } from "../src/services/api/agent-crew";
import { canvasThemes } from "../src/lib/canvas-theme";

const crew: CrewView = { id: "crew", workspaceId: "ws", canvasId: "canvas", revision: 2, name: "剧组", description: "规则", status: "enabled", members: [] };
const run: CrewRunView = {
    id: "run",
    crewId: "crew",
    canvasId: "canvas",
    coordinatorRunId: "coord",
    status: "waiting_approval",
    revision: 1,
    workspaceHash: "hash",
    workspaceRevision: 2,
    latestSequence: 1,
    createdAt: "now",
    budget: { maxCredits: 10, maxSteps: 20, maxGenerationTasks: 0, maxVideoSeconds: 0, maxConcurrentMembers: 2 },
    members: [{ id: "m", memberId: "m", agentRunId: "a", role: "member", name: "编剧", permissionMode: "propose", status: "completed", attempt: 1, summary: "结果" }],
    approval: { approvalId: "approval", snapshotHash: "hash", summary: "结果", preview: { kind: "canvas", title: "", description: "", items: [] }, decision: "approve", operationId: "" },
};
test("Crew settings and run card expose accessible controls and approval boundary", () => {
    const html = renderToStaticMarkup(<CrewSettings crew={crew} onSave={async () => {}} onDelete={async () => {}} />);
    expect(html).toContain("Crew 子代理");
    expect(html).toContain('aria-label="Crew 名称"');
    expect(html).toContain("删除 Crew");
    const card = renderToStaticMarkup(<CrewRunCard run={run} theme={canvasThemes.light} onCommit={() => {}} onApprove={() => {}} />);
    expect(card).toContain("统一审批预览");
    expect(card).toContain("提交画布");
    expect(card).toContain("活动子智能体");
});
