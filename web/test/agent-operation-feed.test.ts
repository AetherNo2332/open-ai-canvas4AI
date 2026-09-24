import { describe, expect, it } from "bun:test";
import { agentOperationFailed, agentOperationLabel, buildAgentFeedSegments, isAgentCarrierRecord, isAgentOperationRecord } from "@/lib/canvas/agent-operation-feed";

type Row = { id: string; role: string; title?: string; text: string; detail?: unknown; planItems?: { id: string }[]; question?: unknown };

const step = (id: string, title: string, text: string, detail: unknown = { eventType: "tool_completed" }): Row => ({ id, role: "tool", title, text, detail });
const kinds = (rows: Row[]) => buildAgentFeedSegments(rows).map((segment) => segment.kind);
const idsOf = (segment: ReturnType<typeof buildAgentFeedSegments<Row>>[number]) => (segment.kind === "operations" ? segment.items.map((item) => item.id) : [segment.item.id]);

describe("Agent operation feed", () => {
    it("folds consecutive tool records into one segment keyed by the first step", () => {
        const rows: Row[] = [
            { id: "u1", role: "user", text: "读一下画布" },
            step("t1", "canvas_get_state", "已读取当前画布"),
            step("t2", "model_list", "已获取可用模型"),
            { id: "a1", role: "assistant", text: "画布里有 1 个节点" },
            step("t3", "canvas_apply_ops", "画布内容已保存至服务端", { eventType: "canvas_updated" }),
        ];
        const segments = buildAgentFeedSegments(rows);
        expect(kinds(rows)).toEqual(["message", "operations", "message", "operations"]);
        expect(segments[1]).toMatchObject({ kind: "operations", key: "t1" });
        expect(idsOf(segments[1])).toEqual(["t1", "t2"]);
    });

    it("appends later steps to the same segment so its key and expanded state survive", () => {
        const before: Row[] = [step("t1", "canvas_get_state", "已读取当前画布"), step("t2", "model_list", "已获取可用模型")];
        const after = buildAgentFeedSegments([...before, step("t3", "task_get", "已查询任务状态")]);
        expect(after).toHaveLength(1);
        expect(after[0]).toMatchObject({ kind: "operations", key: "t1" });
        expect(idsOf(after[0])).toEqual(["t1", "t2", "t3"]);
    });

    it("keeps the approval card and unrelated roles out of the folded group", () => {
        const pending: Row = { id: "p1", role: "tool", title: "canvas_apply_ops", text: "准备更新画布内容", detail: { status: "pending" } };
        const rows: Row[] = [step("t1", "canvas_get_state", "已读取当前画布"), pending, step("t2", "model_list", "已获取可用模型")];
        expect(kinds(rows)).toEqual(["operations", "message", "operations"]);
        expect(isAgentOperationRecord(pending)).toBe(false);
        expect(isAgentOperationRecord(rows[0])).toBe(true);
    });

    it("drops plan and question carriers that carry no body of their own", () => {
        const plan: Row = { id: "plan-run1", role: "tool", text: "", planItems: [{ id: "a" }] };
        const question: Row = { id: "question-run1", role: "assistant", text: "", question: { question: "选哪个？" } };
        const answered: Row = { id: "a1", role: "assistant", text: "那就用第一个方案", question: { question: "选哪个？" } };
        expect(buildAgentFeedSegments([plan, question, answered]).map((segment) => segment.key)).toEqual(["a1"]);
        expect(isAgentCarrierRecord(plan)).toBe(true);
        expect(isAgentCarrierRecord(question)).toBe(true);
        expect(isAgentCarrierRecord(answered)).toBe(false);
    });

    it("reports the latest step, including an in-flight and a failed one", () => {
        expect(agentOperationLabel(step("t1", "canvas_get_state", "已读取当前画布"))).toBe("已读取当前画布");
        expect(agentOperationLabel(step("t2", "canvas_apply_ops", "", {}))).toBe("准备更新画布内容");
        expect(agentOperationLabel(step("t3", "canvas_apply_ops", "", { eventType: "tool_failed" }))).toBe("更新画布内容失败");
        expect(agentOperationFailed(step("t3", "canvas_apply_ops", "", { eventType: "tool_failed" }))).toBe(true);
        expect(agentOperationFailed(step("t1", "canvas_get_state", "已读取当前画布"))).toBe(false);
    });

    it("reports the retry group instead of a stale completion", () => {
        const retrying = step("t1", "canvas_apply_ops", "缺少操作类型", { eventType: "tool_failed", retry: { groupId: "run:repair:call", attempt: 2, maxAttempts: 3, status: "retrying" } });
        expect(agentOperationLabel(retrying)).toBe("自动纠正记录 · 2/3 次尝试");
        const recovered = step("t1", "canvas_apply_ops", "缺少操作类型", { eventType: "tool_failed", retry: { groupId: "run:repair:call", attempt: 2, maxAttempts: 3, status: "recovered" } });
        expect(agentOperationLabel(recovered)).toBe("自动纠正后已恢复 · 2/3 次尝试");
    });
});
