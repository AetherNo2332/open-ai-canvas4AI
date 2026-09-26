import { createAssistantMessageEventStream, type AssistantMessage, type Model, type ToolCall, type Usage } from "@earendil-works/pi-ai";
import type { StreamFn } from "@earendil-works/pi-agent-core";

export interface CanvasModelResult {
  text?: string;
  reasoning?: string;
  toolCalls?: Array<{ id: string; function: { name: string; arguments: string }; thoughtSignature?: string }>;
  stopReasonKind?: string;
  stopReason?: string;
  usage?: Partial<Usage>;
}

export type ModelStep = (request: {
  model: Model<any>;
  messages: unknown[];
  signal?: AbortSignal;
}) => Promise<CanvasModelResult>;

const emptyUsage: Usage = {
  input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
};

function stopReason(result: CanvasModelResult): "stop" | "length" | "toolUse" {
  const reason = result.stopReasonKind || result.stopReason || "";
  if (reason === "length" || reason === "max_tokens") return "length";
  if ((result.toolCalls?.length || 0) > 0) return "toolUse";
  return "stop";
}

export function createCanvasStreamFn(step: ModelStep): StreamFn {
  return (model, context, options) => {
    const stream = createAssistantMessageEventStream();
    const partial: AssistantMessage = {
      role: "assistant", content: [], api: model.api, provider: model.provider, model: model.id,
      usage: emptyUsage, stopReason: "pending", timestamp: Date.now(),
    };
    void (async () => {
      stream.push({ type: "start", partial });
      try {
        const result = await step({ model, messages: context.messages, signal: options?.signal });
        if (options?.signal?.aborted) throw new Error("Agent model request aborted");
        if (result.reasoning) {
          const contentIndex = partial.content.length;
          partial.content.push({ type: "thinking", thinking: "" });
          stream.push({ type: "thinking_start", contentIndex, partial });
          partial.content[contentIndex] = { type: "thinking", thinking: result.reasoning };
          stream.push({ type: "thinking_delta", contentIndex, delta: result.reasoning, partial });
          stream.push({ type: "thinking_end", contentIndex, content: result.reasoning, partial });
        }
        if (result.text) {
          const contentIndex = partial.content.length;
          partial.content.push({ type: "text", text: "" });
          stream.push({ type: "text_start", contentIndex, partial });
          partial.content[contentIndex] = { type: "text", text: result.text };
          stream.push({ type: "text_delta", contentIndex, delta: result.text, partial });
          stream.push({ type: "text_end", contentIndex, content: result.text, partial });
        }
        for (const call of stopReason(result) === "length" ? [] : result.toolCalls || []) {
          const parsed: unknown = JSON.parse(call.function.arguments);
          if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
            throw new Error(`Invalid tool arguments for ${call.function.name}`);
          }
          const toolCall: ToolCall = {
            type: "toolCall", id: call.id, name: call.function.name,
            arguments: parsed as ToolCall["arguments"],
            ...(call.thoughtSignature ? { thoughtSignature: call.thoughtSignature } : {}),
          };
          const contentIndex = partial.content.length;
          partial.content.push({ ...toolCall, arguments: {} });
          stream.push({ type: "toolcall_start", contentIndex, partial });
          stream.push({ type: "toolcall_delta", contentIndex, delta: call.function.arguments, partial });
          partial.content[contentIndex] = toolCall;
          stream.push({ type: "toolcall_end", contentIndex, toolCall, partial });
        }
        partial.usage = { ...emptyUsage, ...result.usage, cost: { ...emptyUsage.cost, ...result.usage?.cost } };
        partial.stopReason = stopReason(result);
        partial.rawStopReason = result.stopReason;
        stream.push({ type: "done", reason: partial.stopReason, message: partial });
        stream.end(partial);
      } catch (error) {
        partial.stopReason = options?.signal?.aborted ? "aborted" : "error";
        partial.errorMessage = error instanceof Error ? error.message : String(error);
        stream.push({ type: "error", reason: partial.stopReason, error: partial });
        stream.end(partial);
      }
    })();
    return stream;
  };
}
