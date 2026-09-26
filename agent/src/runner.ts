import { Agent, type AgentMessage } from "@earendil-works/pi-agent-core";
import { getCurrentSystemPrompt, getCurrentTools, type AssistantMessage, type Message, type Model, type ToolResultMessage } from "@earendil-works/pi-ai";
import { CanvasBridge, type PiCanonical, type PiSnapshot, type PiToolCall } from "./bridge.js";
import { createCanvasStreamFn } from "./pi-stream.js";
import { ToolDisclosure } from "./tool-disclosure.js";

const emptyUsage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };

function canvasModel(snapshot: PiSnapshot): Model<any> {
  const name = snapshot.request.channelModelKey || snapshot.request.model || "canvas-model";
  return { id: name, name, provider: "canvas", api: "openai-completions", baseUrl: "http://backend",
    reasoning: true, input: snapshot.request.visionEnabled ? ["text", "image"] : ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1_000_000, maxTokens: 32_768 };
}

function textContent(content: unknown): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content.filter((item): item is { text: string } => Boolean(item && typeof item === "object" &&
    "text" in item && typeof item.text === "string")).map((item) => item.text).join("\n");
}

function canvasUserMessage(content: unknown): AgentMessage {
  const message = { role: "user", content: textContent(content), timestamp: Date.now() } as AgentMessage;
  if (Array.isArray(content) && content.some((part) => part && typeof part === "object" &&
    (part as Record<string, unknown>).type === "image_url")) {
    (message as unknown as Record<string, unknown>).canvasContent = content;
  }
  return message;
}

function fromCanonical(snapshot: PiSnapshot, model: Model<any>): AgentMessage[] {
  const messages: AgentMessage[] = [];
  for (const source of snapshot.canonical.messages) {
    const role = source.role;
    if (role === "system") continue;
    if (role === "user") messages.push(canvasUserMessage(source.content));
    if (role === "assistant") {
      const blocks: AssistantMessage["content"] = [];
      const text = textContent(source.content);
      if (text) blocks.push({ type: "text", text });
      for (const call of (Array.isArray(source.tool_calls) ? source.tool_calls : []) as PiToolCall[]) {
        try { blocks.push({ type: "toolCall", id: call.id, name: call.function.name,
          arguments: JSON.parse(call.function.arguments) }); } catch { /* historical invalid calls are omitted */ }
      }
      messages.push({ role: "assistant", content: blocks, api: model.api, provider: model.provider,
        model: model.id, usage: emptyUsage, stopReason: blocks.some((b) => b.type === "toolCall") ? "toolUse" : "stop",
        timestamp: Date.now() });
    }
    if (role === "tool") messages.push({ role: "toolResult", toolCallId: String(source.tool_call_id || ""),
      toolName: String(source.name || ""), content: [{ type: "text", text: textContent(source.content) }],
      isError: false, timestamp: Date.now() });
  }
  return messages;
}

export function toCanonical(messagesIn: readonly Message[], cacheKey?: string): PiCanonical {
  const tools = getCurrentTools(messagesIn).map((tool) => ({ type: "function", function: {
    name: tool.name, description: tool.description, parameters: tool.parameters,
  } }));
  const messages: Record<string, unknown>[] = [];
  for (const message of messagesIn) {
    if (message.role === "system") continue;
    if (message.role === "user") messages.push({ role: "user", content:
      (message as unknown as Record<string, unknown>).canvasContent || textContent(message.content) });
    if (message.role === "assistant") {
      const calls = message.content.filter((block) => block.type === "toolCall").map((block) => ({
        id: block.id, type: "function", function: { name: block.name, arguments: JSON.stringify(block.arguments) },
      }));
      messages.push({ role: "assistant", content: message.content.filter((block) => block.type === "text")
        .map((block) => block.text).join("\n"), ...(calls.length ? { tool_calls: calls } : {}) });
    }
    if (message.role === "toolResult") messages.push({ role: "tool", tool_call_id: message.toolCallId,
      content: textContent(message.content) });
  }
  return { systemPrompt: getCurrentSystemPrompt(messagesIn), messages, tools, toolChoice: "auto",
    promptCacheKey: cacheKey };
}

function callsFromAssistant(message: AssistantMessage): PiToolCall[] {
  return message.content.filter((block) => block.type === "toolCall").map((block) => ({
    id: block.id, type: "function", function: { name: block.name, arguments: JSON.stringify(block.arguments) },
  }));
}

async function recoverToolResults(bridge: CanvasBridge, snapshot: PiSnapshot, messages: AgentMessage[],
  checkpoint: (message: AgentMessage, taskId?: string) => Promise<void>): Promise<void> {
  let assistantIndex = -1;
  for (let index = messages.length - 1; index >= 0; index--) {
    const item = messages[index];
    if (item?.role === "assistant" && item.content.some((part) => part.type === "toolCall")) {
      assistantIndex = index;
      break;
    }
  }
  if (assistantIndex < 0) return;
  const assistant = messages[assistantIndex] as AssistantMessage;
  const calls = callsFromAssistant(assistant);
  if (calls.length === 0) return;
  const completed = new Set(messages.slice(assistantIndex + 1).filter((item): item is ToolResultMessage =>
    item.role === "toolResult").map((item) => item.toolCallId));
  if (completed.size === calls.length) return;
  await bridge.startToolBatch(snapshot, calls);
  for (const call of calls) {
    if (completed.has(call.id)) continue;
    const receipt = await bridge.executeTool(snapshot, call.id);
    const result: ToolResultMessage = { role: "toolResult", toolCallId: call.id, toolName: call.function.name,
      content: [{ type: "text", text: typeof receipt.result === "string" ? receipt.result : JSON.stringify(receipt.result) }],
      isError: Boolean(receipt.isError), timestamp: Date.now() };
    await checkpoint(result);
    messages.push(result);
  }
}

/** One leased run. No model or canvas side effect is sent without its Go receipt. */
export async function runCanvasAgent(bridge: CanvasBridge, initial: PiSnapshot): Promise<void> {
  let snapshot = initial;
  const model = canvasModel(snapshot);
  const messages = snapshot.piMessages?.length ? snapshot.piMessages as unknown as AgentMessage[] : fromCanonical(snapshot, model);
  let sequence = snapshot.piMessages?.length || 0;
  const checkpoint = async (message: AgentMessage, taskId?: string): Promise<void> => {
    await bridge.checkpoint(snapshot, ++sequence, message as unknown as Record<string, unknown>, taskId);
  };
  if (sequence === 0) for (const message of messages) await checkpoint(message);
  await recoverToolResults(bridge, snapshot, messages, checkpoint);
  const tail = messages.at(-1);
  if (!snapshot.activeTaskId && snapshot.lastTaskId && tail?.role === "assistant" &&
    callsFromAssistant(tail).length === 0) {
    const decision = await bridge.noToolTurn(snapshot, snapshot.lastTaskId);
    if (decision.status === "completed" || decision.status === "failed") return;
    if (decision.status === "continue" && decision.nudge) {
      const nudge = canvasUserMessage(decision.nudge);
      await checkpoint(nudge);
      messages.push(nudge);
    }
  }

  const disclosure = new ToolDisclosure(snapshot.tools, async (_name, _args, callId, signal) => {
    const receipt = await bridge.executeTool(snapshot, callId, signal);
    const refreshed = await bridge.snapshot(snapshot, signal);
    for (const message of refreshed.canonical.messages.slice(canonicalCount)) {
      if (message.role === "user" && Array.isArray(message.content) &&
        message.content.some((part) => part && typeof part === "object" && part.type === "image_url")) {
        agent.steer(canvasUserMessage(message.content));
      }
    }
    canonicalCount = refreshed.canonical.messages.length;
    snapshot = refreshed;
    return { result: receipt.result, terminate: _name === "finish_run" && !receipt.isError };
  }, snapshot.previousStepTemplate);
  for (const name of snapshot.openedCategories || []) disclosure.opened.add(name);
  let activeTaskId = "";
  let latestTaskId = "";
  let lastAssistant: AssistantMessage | undefined;
  let batchId = "";
  let canonicalCount = snapshot.canonical.messages.length;
  const agent = new Agent({
    initialState: { model, systemPrompt: snapshot.canonical.systemPrompt, messages },
    toolExecution: "sequential", sessionId: snapshot.runId,
    streamFn: createCanvasStreamFn(async ({ messages, signal }) => {
      const step = await bridge.modelStep(snapshot, toCanonical(messages as Message[], snapshot.canonical.promptCacheKey), signal);
      activeTaskId = latestTaskId = step.taskId;
      return step.result;
    }),
    beforeToolCall: async ({ assistantMessage }, signal) => {
      const calls = callsFromAssistant(assistantMessage);
      const key = calls.map((call) => call.id).join(":");
      if (key !== batchId) {
        await bridge.startToolBatch(snapshot, calls, signal);
        batchId = key;
      }
      return undefined;
    },
    finishTurn: async ({ message, toolResults }, signal) => {
      if (message.role !== "assistant") return;
      if (toolResults.length || callsFromAssistant(message).length) {
        snapshot = await bridge.snapshot(snapshot, signal);
        return snapshot.status === "completed" || snapshot.status === "failed" || snapshot.status === "cancelled"
          ? { action: "end" } : undefined;
      }
      const decision = await bridge.noToolTurn(snapshot, latestTaskId, signal);
      if (decision.status === "continue") {
        if (decision.nudge) agent.steer({ role: "user", content: decision.nudge, timestamp: Date.now() });
        return { action: "continue" };
      }
      return { action: "end" };
    },
  });
  disclosure.attach(agent);
  agent.subscribe(async (event) => {
    if (event.type === "message_end") {
      if (event.message.role === "assistant") lastAssistant = event.message;
      await checkpoint(event.message, event.message.role === "assistant" && activeTaskId ? activeTaskId : undefined);
      if (event.message.role === "assistant") activeTaskId = "";
    }
    if (event.type === "turn_end" && lastAssistant) {
      disclosure.recordStepCalls(callsFromAssistant(lastAssistant).map((call) => call.function.name));
      snapshot = await bridge.snapshot(snapshot);
      canonicalCount = snapshot.canonical.messages.length;
    }
  });
  const lease = setInterval(() => { void bridge.renew(snapshot).catch(() => agent.abort()); }, 15_000);
  try { await agent.continue(); } finally { clearInterval(lease); }
}
