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
  for (const message of [...sources].reverse()) {
    const parts = imageParts(message);
    for (let index = parts.length - 1; index >= 0; index--) {
      const part = parts[index];
      const key = imageKey(part);
      if (!key || visible.has(key)) continue;
      const caption = parts[index - 1] as { type?: string; text?: string } | undefined;
      // Keep the inspection's data caption, not an old user's instruction.
      if (caption?.type === "text" && imageReceipt(caption.text)?.nodeId) content.unshift(caption, part);
      else content.unshift(part);
      visible.add(key);
    }
  }
  return content;
}

function imageReceipt(text: unknown): Record<string, unknown> | undefined {
  if (typeof text !== "string") return;
  const start = text.indexOf("{");
  if (start < 0) return;
  try { return JSON.parse(text.slice(start)) as Record<string, unknown>; } catch { return; }
}

function observationKey(receipt?: Record<string, unknown>): string | undefined {
  if (typeof receipt?.nodeId !== "string" || typeof receipt.sha256 !== "string") return;
  return `${receipt.nodeId}:${receipt.sha256}`;
}

function projectImageSummaries(messages: readonly AgentMessage[], summaries: Map<string, unknown>): AgentMessage[] {
  return messages.map(message => {
    const parts = imageParts(message);
    let changed = false;
    const projected = parts.map((part, index) => {
      if (!imageKey(part)) return part;
      const caption = parts[index - 1] as { text?: unknown } | undefined;
      const key = observationKey(imageReceipt(caption?.text));
      if (!key || !summaries.has(key)) return part;
      changed = true;
      return { type: "text", text: `图片 SHA 未变化，使用已保存的结构化摘要：${JSON.stringify(summaries.get(key))}` };
    });
    return changed ? { ...message, canvasContent: projected } as unknown as AgentMessage : message;
  });
}

function missingSummaryContent(source: readonly AgentMessage[], existing: readonly AgentMessage[], summaries: Map<string, unknown>): unknown[] {
  const visible = new Set<string>();
  for (const message of existing) {
    const parts = message.role === "toolResult" ? message.content : imageParts(message);
    for (const part of parts) {
      const block = part as { type?: string; text?: unknown };
      if (block.type !== "text") continue;
      const key = observationKey(imageReceipt(block.text));
      if (key) visible.add(key);
    }
  }
  const restored: unknown[] = [];
  for (const message of source) {
    if (message.role !== "toolResult" || message.toolName !== "canvas_inspect_image" || message.isError) continue;
    for (const block of message.content) {
      const receipt = block.type === "text" ? imageReceipt(block.text) : undefined;
      const key = observationKey(receipt);
      if (!key || visible.has(key) || !summaries.has(key)) continue;
      restored.push({ type: "text", text: `已保存的图片观察（仅适用于该 nodeId 与 SHA）：${JSON.stringify({
        nodeId: receipt!.nodeId, sha256: receipt!.sha256, imageAttached: false, visionCache: summaries.get(key),
      })}` });
      visible.add(key);
    }
  }
  return restored;
}

/** Preserve durable images, but replace observed SHAs with saved summaries in
 * the model projection. Only the latest unsummarized image is sent per request.
 */
export function createImageContextExtension(): InlineExtension {
  return {
    name: "canvas-original-image-context",
    hidden: true,
    factory: (pi) => {
      pi.on("context", (event, context) => {
        const source = context.sessionManager.getBranch().flatMap(entry => entry.type === "message" ? [entry.message] : []);
        const summaries = new Map<string, unknown>();
        for (const message of source) {
          if (message.role !== "toolResult" || message.toolName !== "canvas_inspect_image" || message.isError) continue;
          for (const block of message.content) {
            const receipt = block.type === "text" ? imageReceipt(block.text) : undefined;
            const key = observationKey(receipt);
            if (key && receipt?.visionCache && receipt.imageAttached === false) summaries.set(key, receipt.visionCache);
          }
        }
        let messages = projectImageSummaries(event.messages, summaries);
        const content = [
          ...missingSummaryContent(source, messages, summaries),
          ...missingImageContent(projectImageSummaries(source, summaries), messages),
        ];
        if (content.length > 0) {
          const restored = { role: "user", content: "", canvasContent: content, timestamp: 0 } as unknown as AgentMessage;
          messages = [restored, ...messages];
        }
        let remaining = messages.flatMap(message => imageParts(message)).filter(imageKey).length;
        messages = messages.map(message => {
          const parts = imageParts(message);
          let changed = false;
          const projected = parts.map(part => {
            if (!imageKey(part) || remaining-- <= 1) return part;
            changed = true;
            return { type: "text", text: "本次未附送这张旧图片；无结构化摘要时不能推断其内容，需要时通过识图工具重新读取。" };
          });
          return changed ? { ...message, canvasContent: projected } as unknown as AgentMessage : message;
        });
        if (content.length === 0 && messages.every((message, index) => message === event.messages[index])) return;
        // Prepend a data-only message so an image cannot split an assistant's
        // tool call batch from its results. Do not persist a duplicate transcript.
        return { messages };
      });
    },
  };
}
