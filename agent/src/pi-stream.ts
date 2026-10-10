import { createAssistantMessageEventStream, getCurrentSystemPrompt, type AssistantMessage, type Model, type ToolCall, type Usage } from "@earendil-works/pi-ai";
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
  systemPrompt?: string;
  maxTokens?: number;
  /** 模型任务的流式草稿；有增量时逐段上报，避免把整段结果当一次 delta。 */
  onTextDelta?: (delta: string) => void;
}) => Promise<CanvasModelResult>;

// Canvas uses a Go-managed provider path and does not have Pi's local price
// table. Start with an empty usage object so missing upstream measurements stay
// unknown. Undefined slots keep Pi's required Usage shape safe for runtime reads
// while JSON serialization omits every unreported metric instead of writing 0.
const unknownUsage = (): Usage => ({
  input: undefined, output: undefined, cacheRead: undefined, cacheWrite: undefined,
  totalTokens: undefined,
  cost: { input: undefined, output: undefined, cacheRead: undefined, cacheWrite: undefined, total: undefined },
} as unknown as Usage);

function piUsage(usage?: Partial<Usage>): Usage {
  const unknown = unknownUsage();
  return {
    ...unknown,
    ...usage,
    cost: { ...unknown.cost, ...usage?.cost },
  } as Usage;
}

function stopReason(result: CanvasModelResult): "stop" | "length" | "toolUse" {
  const reason = result.stopReasonKind || result.stopReason || "";
  if (reason === "length" || reason === "max_tokens") return "length";
  if (["pause", "pause_turn", "refusal", "content_filter", "incomplete_unknown"].includes(reason)) return "stop";
  if ((result.toolCalls?.length || 0) > 0) return "toolUse";
  return "stop";
}

export function createCanvasStreamFn(step: ModelStep, onFailure?: (error: unknown) => void): StreamFn {
  return (model, context, options) => {
    const stream = createAssistantMessageEventStream();
    const partial: AssistantMessage = {
      role: "assistant", content: [], api: model.api, provider: model.provider, model: model.id,
      usage: unknownUsage(), stopReason: "pending", timestamp: Date.now(),
    };
    void (async () => {
      stream.push({ type: "start", partial });
      try {
        // 上游已经流出的正文要按增量转发；结果里剩下的尾部在下面按"未发送部分"补齐。
        let sent = "";
        const contentIndexForStream = (): number => {
          for (let index = partial.content.length - 1; index >= 0; index--) {
            const block = partial.content[index];
            if (block && block.type === "text") return index;
          }
          partial.content.push({ type: "text", text: "" });
          return partial.content.length - 1;
        };
        const pushDelta = (delta: string): void => {
          if (delta === "") return;
          const index = contentIndexForStream();
          const block = partial.content[index];
          if (block && block.type === "text") block.text += delta;
          stream.push({ type: "text_delta", contentIndex: index, delta, partial });
        };
        const result = await step({
          model, messages: context.messages, signal: options?.signal,
          systemPrompt: getCurrentSystemPrompt(context.messages), maxTokens: options?.maxTokens,
          onTextDelta: (delta) => { sent += delta; pushDelta(delta); },
        });
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
          const remainder = result.text.startsWith(sent) ? result.text.slice(sent.length) : result.text;
          if (sent === "") {
            const index = contentIndexForStream();
            stream.push({ type: "text_start", contentIndex: index, partial });
            const block = partial.content[index];
            if (block && block.type === "text") block.text = "";
            pushDelta(remainder);
          } else if (remainder !== "") {
            pushDelta(remainder);
          }
          for (let index = partial.content.length - 1; index >= 0; index--) {
            const block = partial.content[index];
            if (block && block.type === "text") {
              stream.push({ type: "text_end", contentIndex: index, content: block.text, partial });
              break;
            }
          }
        }
        for (const call of stopReason(result) === "toolUse" ? result.toolCalls || [] : []) {
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
        partial.usage = piUsage(result.usage);
        partial.stopReason = stopReason(result);
        partial.rawStopReason = result.stopReason;
        stream.push({ type: "done", reason: partial.stopReason, message: partial });
        stream.end(partial);
      } catch (error) {
        // The SDK consumes error streams normally. Preserve the original error
        // for the leased-session boundary rather than losing it in a message.
        onFailure?.(error);
        partial.stopReason = options?.signal?.aborted ? "aborted" : "error";
        partial.errorMessage = error instanceof Error ? error.message : String(error);
        stream.push({ type: "error", reason: partial.stopReason, error: partial });
        stream.end(partial);
      }
    })();
    return stream;
  };
}
