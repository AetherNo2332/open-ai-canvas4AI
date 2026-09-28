import type { AgentMessage } from "@earendil-works/pi-agent-core";
import type { AssistantMessage, ToolCall, ToolResultMessage } from "@earendil-works/pi-ai";
import type { InlineExtension } from "@earendil-works/pi-coding-agent";
import type { PiSnapshot } from "./bridge.js";

function assistantIdentity(message: AssistantMessage): string {
  return JSON.stringify([message.timestamp, message.content]);
}

/**
 * A new run may inherit a terminal turn whose last tool results never reached
 * the Pi checkpoint. Repair only its model-facing projection: do not invent an
 * execution receipt, alter the saved tree, or retry a historical business tool.
 * Current-run gaps must still be recovered from Go's authoritative receipts.
 */
export function createTerminalHistoryExtension(snapshot: PiSnapshot): InlineExtension {
  const historical = new Set<string>();
  for (const { runId, entry } of snapshot.piSessionEntries || []) {
    if (runId === snapshot.runId || entry.type !== "message") continue;
    const message = entry.message as AgentMessage | undefined;
    if (message?.role === "assistant") historical.add(assistantIdentity(message));
  }
  return {
    name: "canvas-terminal-history",
    hidden: true,
    factory: (pi) => {
      pi.on("context", (event) => {
        const messages: AgentMessage[] = [];
        let pending: ToolCall[] = [];
        let timestamp = 0;
        let repaired = false;
        const closeHistoricalBatch = (): void => {
          for (const call of pending) {
            const result: ToolResultMessage = {
              role: "toolResult", toolCallId: call.id, toolName: call.name, isError: true, timestamp,
              content: [{ type: "text", text: `上一轮已终结，${call.name} 缺少持久化工具回执，执行结果未知。请核对已有业务结果；不要依据这条历史消息重新执行写入或收费操作。` }],
            };
            messages.push(result);
            repaired = true;
          }
          pending = [];
        };
        for (const message of event.messages) {
          if (message.role === "toolResult") {
            pending = pending.filter(call => call.id !== message.toolCallId);
          } else {
            closeHistoricalBatch();
            if (message.role === "assistant" && historical.has(assistantIdentity(message))) {
              pending = message.content.filter((part): part is ToolCall => part.type === "toolCall");
              timestamp = message.timestamp;
            }
          }
          messages.push(message);
        }
        closeHistoricalBatch();
        return repaired ? { messages } : undefined;
      });
    },
  };
}

type CanvasImagePart = { type: "image_url"; image_url: { url: string; [key: string]: unknown } };

function imageParts(message: AgentMessage): unknown[] {
  if (message.role !== "user") return [];
  const content = (message as unknown as Record<string, unknown>).canvasContent;
  return Array.isArray(content) ? content : [];
}

function imageKey(part: unknown): string | undefined {
  if (!part || typeof part !== "object" || (part as CanvasImagePart).type !== "image_url") return;
  return (part as CanvasImagePart).image_url?.url;
}

export function missingImageContent(sources: readonly AgentMessage[], existing: readonly AgentMessage[]): unknown[] {
  const visible = new Set(existing.flatMap(message => imageParts(message).map(imageKey).filter(Boolean)));
  const content: unknown[] = [];
  for (const message of sources) {
    const parts = imageParts(message);
    for (let index = 0; index < parts.length; index++) {
      const part = parts[index];
      const key = imageKey(part);
      if (!key || visible.has(key)) continue;
      const caption = parts[index - 1] as { type?: string; text?: string } | undefined;
      // Keep the inspection's data caption, not an old user's instruction.
      if (caption?.type === "text" && caption.text?.includes('{"nodeId":"')) content.push(caption);
      content.push(part);
      visible.add(key);
    }
  }
  return content;
}

/** Text compaction must never replace visual input. Read only the active branch,
 * including ancestors hidden by its compaction entries, and restore missing
 * original image references in the model projection. Resource authorization and
 * model capacity remain Go's responsibility. No image bytes or dimensions change.
 */
export function createImageContextExtension(): InlineExtension {
  return {
    name: "canvas-original-image-context",
    hidden: true,
    factory: (pi) => {
      pi.on("context", (event, context) => {
        const source = context.sessionManager.getBranch().flatMap(entry => entry.type === "message" ? [entry.message] : []);
        const content = missingImageContent(source, event.messages);
        if (content.length === 0) return;
        const restored = { role: "user", content: "", canvasContent: content, timestamp: 0 } as unknown as AgentMessage;
        // Prepend a data-only message so an image cannot split an assistant's
        // tool call batch from its results. Do not persist a duplicate transcript.
        return { messages: [restored, ...event.messages] };
      });
    },
  };
}
