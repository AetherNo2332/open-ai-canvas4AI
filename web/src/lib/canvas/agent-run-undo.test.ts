import assert from "node:assert/strict";
import test from "node:test";

// @ts-expect-error -- Node 原生 TypeScript 测试运行器需要保留扩展名。
import { agentRunUndoOffer, agentRunUndoneMessageId } from "./agent-run-undo.ts";

const write = { id: "canvas-run-1-call-1", role: "tool", title: "canvas_apply_ops", text: "画布操作已完成", detail: { eventType: "canvas_updated", toolName: "canvas_apply_ops" } };
const read = { id: "run-1:3", role: "tool", title: "canvas_get_state", text: "已读取", detail: { eventType: "tool_completed", toolName: "canvas_get_state" } };

test("a finished run that wrote the canvas offers undo", () => {
    assert.equal(agentRunUndoOffer({ id: "run-1", status: "completed" }, [read, write]), "run-1");
    assert.equal(agentRunUndoOffer({ id: "run-1", status: "failed" }, [write]), "run-1");
});

test("running, read-only, failed-write or other-run histories do not offer undo", () => {
    assert.equal(agentRunUndoOffer({ id: "run-1", status: "running" }, [write]), null);
    assert.equal(agentRunUndoOffer({ id: "run-1", status: "completed" }, [read]), null);
    assert.equal(agentRunUndoOffer({ id: "run-1", status: "completed" }, [{ ...write, detail: { eventType: "tool_failed", toolName: "canvas_apply_ops" } }]), null);
    assert.equal(agentRunUndoOffer({ id: "run-2", status: "completed" }, [write]), null);
    assert.equal(agentRunUndoOffer(null, [write]), null);
});

test("an undone run no longer offers undo, whether marked locally or replayed", () => {
    assert.equal(agentRunUndoOffer({ id: "run-1", status: "completed" }, [write, { id: agentRunUndoneMessageId("run-1"), role: "system", text: "已撤销" }]), null);
    assert.equal(agentRunUndoOffer({ id: "run-1", status: "completed" }, [write, { id: "run-1:9", role: "system", text: "已撤销", detail: { eventType: "canvas_undone" } }]), null);
});
