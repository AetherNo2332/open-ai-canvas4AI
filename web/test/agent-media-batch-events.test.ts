import { expect, test } from "bun:test";
import { applyAgentEvent } from "@/lib/canvas/cloud-agent-events";
import { isAgentCarrierRecord } from "@/lib/canvas/agent-operation-feed";
import { latestAgentMediaBatch } from "@/lib/canvas/cloud-agent-media-batch";
import type { CloudAgentChatMessage } from "@/components/canvas/canvas-cloud-agent-chat-ui";
import type { AgentEvent } from "@/services/api/agent";

test("media batch events merge deltas into a persisted carrier message", () => {
    let messages: CloudAgentChatMessage[] = [];
    const deliver = (seq: number, payload: AgentEvent["payload"]) =>
        applyAgentEvent(
            { eventId: `event-${seq}`, runId: "run-1", seq, type: "media_batch_progress", payload, createdAt: "" },
            (update) => {
                messages = typeof update === "function" ? update(messages) : update;
            },
            () => {},
            () => {},
        );
    deliver(1, {
        batchId: "batch-1",
        mode: "image",
        phase: "running",
        total: 2,
        items: [
            { index: 0, nodeId: "node-1", status: "pending" },
            { index: 1, nodeId: "node-2", status: "submitted" },
        ],
    });
    deliver(2, { batchId: "batch-1", delta: true, items: [{ index: 0, nodeId: "node-1", status: "succeeded" }] });
    expect(messages).toHaveLength(1);
    expect(isAgentCarrierRecord(messages[0])).toBe(true);
    expect(latestAgentMediaBatch(messages)?.items.map((item) => item.status)).toEqual(["succeeded", "submitted"]);
    deliver(2, { batchId: "batch-1", delta: true, items: [{ index: 0, nodeId: "node-1", status: "succeeded" }] });
    expect(messages).toHaveLength(1);
    expect(latestAgentMediaBatch(JSON.parse(JSON.stringify(messages)))?.batchId).toBe("batch-1");
});
