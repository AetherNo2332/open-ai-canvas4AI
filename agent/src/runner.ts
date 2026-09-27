import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { getCurrentSystemPrompt, getCurrentTools,
  type AssistantMessage, type Message, type Model, type ToolResultMessage } from "@earendil-works/pi-ai";
import type { AgentMessage } from "@earendil-works/pi-agent-core";
import {
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
  createAgentSession,
  type AgentSession,
  type ExtensionAPI,
  type FileEntry,
  type InlineExtension,
  type SessionBeforeCompactEvent,
  type SessionCompactEvent,
  type SessionCompactFailedEvent,
  type SessionEntry,
} from "@earendil-works/pi-coding-agent";
import { CanvasBridge, type PiCanonical, type PiSnapshot, type PiToolCall } from "./bridge.js";
import { createCanvasStreamFn } from "./pi-stream.js";
import { FatalWorkerError, assertToolSnapshotMatchesSchema, type ToolSchemaArtifact } from "./tool-disclosure.js";
import { createCanvasToolsExtension, SessionToolDisclosure, sessionEntriesFromMessages } from "./session-tools.js";
import { harnessHash, loadPromptParts, renderSystemPrompt, type PromptParts } from "./system-prompt.js";

/**
 * 本轮终态：不再有工具回执，也不得再发起后续模型请求。
 * 与 Go 的 `cloudAgentRunTerminal` 同义（见 backend/internal/app）。
 */
const TERMINAL_RUN_STATUSES = new Set(["completed", "failed", "cancelled", "rejected"]);
function isTerminalRunStatus(status: string | undefined): boolean {
  return status !== undefined && TERMINAL_RUN_STATUSES.has(status);
}

/**
 * 恢复时的续跑输入。`AgentSession.prompt()` 必须带一条用户消息（SDK 没有"只继续当前
 * 上下文"的公开入口），而"崩溃点正好停在工具结果之后"本该由模型接着工具结果说话。
 *
 * 这条哨兵只进 Pi 会话、**不进 Go 转录**（runner 不为它发检查点），所以不会污染服务端
 * 权威上下文。C3 用 Pi v3 entry 与 operation 回执做精确恢复后会删掉它（见 cutover C3）。
 */
const CONTINUATION_PROMPT = "请继续（恢复上次中断的步骤）。";

const emptyUsage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };

const CANVAS_PROVIDER_ID = "canvas";
const CANVAS_API_ID = "canvas-bridge";
/** 自定义 provider 必须声明 baseUrl；`.invalid` 由 RFC 2606 保留，永远不会被访问。 */
const CANVAS_BASE_URL = "http://canvas-bridge.invalid/v1";
/** Pi 需要正数 maxTokens 才能建立模型；真实输出限制仍由 Go 的模型任务链路决定。 */
const PI_FALLBACK_OUTPUT_TOKENS = 16_384;

/**
 * 模型标识只用于 Pi 的会话记账与缓存键：真实渠道、模型能力、协议与费用全部由 Go 决定
 * （见 `PiModelStep`）。这里声明的窗口/输出上限是占位值，**不进入真实准入**。
 */
export function canvasModel(snapshot: PiSnapshot): Model<any> {
  const name = snapshot.request.channelModelKey || snapshot.request.model || "canvas-model";
  const limits = snapshot.modelLimits;
  const contextWindowTokens = limits?.contextWindowTokens;
  const declaredMaxOutputTokens = limits?.maxOutputTokens;
  if (!Number.isSafeInteger(contextWindowTokens) || contextWindowTokens < 1) {
    throw new FatalWorkerError("Go snapshot is missing effective Pi model limits");
  }
  // A model may declare its context window without declaring an output ceiling. Keep Pi's
  // scheduler usable with a conservative placeholder; Go remains authoritative for the request.
  const maxOutputTokens = Number.isSafeInteger(declaredMaxOutputTokens) && declaredMaxOutputTokens > 0
    ? declaredMaxOutputTokens
    : Math.min(PI_FALLBACK_OUTPUT_TOKENS, Math.floor(contextWindowTokens / 2));
  return { id: name, name, provider: CANVAS_PROVIDER_ID, api: CANVAS_API_ID, baseUrl: CANVAS_BASE_URL,
    reasoning: true, input: snapshot.request.visionEnabled ? ["text", "image"] : ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: contextWindowTokens, maxTokens: maxOutputTokens };
}

function textContent(content: unknown): string {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  return content.filter((item): item is { text: string } => Boolean(item && typeof item === "object" &&
    "text" in item && typeof (item as { text: unknown }).text === "string"))
    .map((item) => item.text).join("\n");
}

function hasImagePart(content: unknown): boolean {
  return Array.isArray(content) && content.some((part) => Boolean(part && typeof part === "object" &&
    (part as Record<string, unknown>).type === "image_url"));
}

function canvasUserMessage(content: unknown): AgentMessage {
  const message = { role: "user", content: textContent(content), timestamp: Date.now() } as AgentMessage;
  if (hasImagePart(content)) (message as unknown as Record<string, unknown>).canvasContent = content;
  return message;
}

/** canonical 信封 → Pi 消息：恢复时用它重建会话上下文（与旧实现的 initialState.messages 同源）。 */
export function fromCanonical(snapshot: PiSnapshot, model: Model<any>): AgentMessage[] {
  const messages: AgentMessage[] = [];
  for (const source of snapshot.canonical.messages) {
    const role = source.role;
    if (role === "system") continue;
    if (role === "user") messages.push(canvasUserMessage(source.content));
    if (role === "assistant") {
      const blocks: AssistantMessage["content"] = [];
      const text = textContent(source.content);
      if (text) blocks.push({ type: "text", text });
      const rawCalls = Array.isArray(source.tool_calls) ? source.tool_calls as PiToolCall[] : [];
      for (const call of rawCalls) {
        try {
          blocks.push({ type: "toolCall", id: call.id, name: call.function.name,
            arguments: JSON.parse(call.function.arguments) });
        } catch { /* 历史里解析不出来的调用不再回放：它不可能被重新准入。 */ }
      }
      messages.push({ role: "assistant", content: blocks, api: model.api, provider: model.provider,
        model: model.id, usage: emptyUsage,
        stopReason: blocks.some((block) => block.type === "toolCall") ? "toolUse" : "stop",
        timestamp: Date.now() });
    }
    if (role === "tool") messages.push({ role: "toolResult", toolCallId: String(source.tool_call_id || ""),
      toolName: String(source.name || ""), content: [{ type: "text", text: textContent(source.content) }],
      isError: false, timestamp: Date.now() });
  }
  return messages;
}

/**
 * 把 Pi 会话里的消息翻成 Go 的 canonical 信封（服务端模型步骤的输入）。
 *
 * 系统提示与工具声明由 Pi 自己的上下文携带，所以这里从消息里读回来
 * （`getCurrentSystemPrompt` / `getCurrentTools`）：服务端策略校验、工具披露校验
 * 用的就是同一份事实，不存在"Go 以为发了什么"和"模型真收到什么"两套说法。
 */
export function toCanonical(messagesIn: readonly Message[], cacheKey?: string): PiCanonical {
  const tools = getCurrentTools(messagesIn).map((tool) => ({ type: "function", function: {
    name: tool.name, description: tool.description, parameters: tool.parameters,
  } }));
  const messages: Record<string, unknown>[] = [];
  for (const message of messagesIn) {
    if (message.role === "system") continue;
    if (message.role === "user") messages.push({ role: "user",
      content: (message as unknown as Record<string, unknown>).canvasContent || textContent(message.content) });
    if (message.role === "assistant") {
      const calls = message.content.filter((block) => block.type === "toolCall").map((block) => ({
        id: block.id, type: "function", function: { name: block.name, arguments: JSON.stringify(block.arguments) },
      }));
      messages.push({ role: "assistant",
        content: message.content.filter((block) => block.type === "text").map((block) => block.text).join("\n"),
        ...(calls.length ? { tool_calls: calls } : {}) });
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

/**
 * 批次准入前的本地判据：全部调用都必须在当前披露集合里。
 *
 * 这不是授权 —— Go 的 `PiToolBatch` 才是权威；这里只是把"模型点名了不该看到的工具"
 * 这一类越权尝试挡在副作用之前，并保持"整批一起准入、整批一起执行"的语义。
 */
function batchAdmissionError(disclosure: SessionToolDisclosure, calls: PiToolCall[]): string | undefined {
  for (const call of calls) {
    if (!disclosure.isVisible(call.function.name)) return `Tool ${call.function.name} is not disclosed`;
  }
  return undefined;
}

/** Harness 素材在启动时读一次；恢复时由冻结快照覆盖（见 runCanvasAgent）。 */
export async function loadHarnessPrompt(agentDir?: string): Promise<PromptParts> {
  return loadPromptParts({ agentDir });
}

/** 服务端只给策略前缀；SYSTEM.md/APPEND_SYSTEM.md 与 Harness 文件由 Node 自己装配。 */
export function assembleSystemPrompt(parts: PromptParts, policyPrefix: string): string {
  return renderSystemPrompt(parts, policyPrefix);
}

/** 会话事件的持久化必须严格串行：Go 按递增序号校验，乱序会被判为冲突。 */
class CheckpointQueue {
  private chain: Promise<void> = Promise.resolve();
  failure: unknown;

  enqueue(task: () => Promise<void>): void {
    this.chain = this.chain.then(task).catch((error: unknown) => {
      this.failure ??= error;
    });
  }

  /** 屏障：等到已排队的持久化全部落地；基础设施错误在这里抛出，不被静默吞掉。 */
  async drain(): Promise<void> {
    await this.chain;
    if (this.failure !== undefined) throw this.failure instanceof Error ? this.failure : new Error(String(this.failure));
  }
}

function lastUserText(snapshot: PiSnapshot): string {
  const messages = snapshot.canonical.messages;
  for (let index = messages.length - 1; index >= 0; index--) {
    const message = messages[index];
    if (message?.role === "user") return textContent(message.content);
  }
  return snapshot.request.prompt;
}

/** 隔离工作目录：不读操作者的 ~/.pi，也不写任何真实项目文件。 */
function createWorkspace(): { cwd: string; agentDir: string; cleanup: () => void } {
  const root = mkdtempSync(join(tmpdir(), "canvas-pi-run-"));
  const cwd = join(root, "cwd");
  const agentDir = join(root, "agent");
  mkdirSync(cwd, { recursive: true });
  mkdirSync(agentDir, { recursive: true });
  return { cwd, agentDir, cleanup: () => rmSync(root, { recursive: true, force: true }) };
}

interface ResumePoint {
  entries: SessionEntry[];
  prompt: string;
  activeLeafId?: string;
}

/**
 * 补齐"上次崩溃时已经准入但没有回执"的工具调用。
 *
 * 回执必须写进 Go 转录：会话重建时会按 canonical 回放，缺一条工具结果会让
 * assistant(tool_calls) 悬空，上游会直接拒绝这一轮。
 */
async function recoverToolResults(
  bridge: CanvasBridge,
  snapshot: PiSnapshot,
  messages: AgentMessage[],
  disclosure: SessionToolDisclosure,
  checkpoint: (message: AgentMessage) => Promise<void>,
  signal?: AbortSignal,
): Promise<void> {
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
  const completed = new Set(messages.slice(assistantIndex + 1)
    .filter((item): item is ToolResultMessage => item.role === "toolResult")
    .map((item) => item.toolCallId));
  if (completed.size === calls.length) return;
  // Legacy category selectors never performed a business operation. Complete
  // an interrupted v1 selector locally so the restored Pi transcript has a
  // paired result; do not submit a synthetic call to Go or execute any tool.
  const executableCalls = calls.filter((call) => !call.function.name.startsWith("agent_tools_"));
  const admissionError = batchAdmissionError(disclosure, executableCalls);
  if (!admissionError && executableCalls.length > 0) await bridge.startToolBatch(snapshot, executableCalls, signal);
  for (const call of calls) {
    if (completed.has(call.id)) continue;
    const legacyCategory = call.function.name.startsWith("agent_tools_");
    const receipt = legacyCategory
      ? { result: "旧版工具分类入口已完成兼容，不执行业务操作。请直接使用当前工具表中的具体工具。" }
      : admissionError ? { result: admissionError, isError: true } :
      await bridge.executeTool(snapshot, call.id, signal);
    const result: ToolResultMessage = { role: "toolResult", toolCallId: call.id, toolName: call.function.name,
      content: [{ type: "text",
        text: typeof receipt.result === "string" ? receipt.result : JSON.stringify(receipt.result) }],
      isError: Boolean(receipt.isError), timestamp: Date.now() };
    await checkpoint(result);
    messages.push(result);
  }
}

/**
 * 决定"从哪继续、用什么作为本轮输入"。
 *
 * 上下文一律来自 canonical（Go 的权威信封），而不是 Pi 转录：canonical 里既有历史消息，
 * 也有 Go 自己追加的 assistant/工具结果，两者内容同源但 canonical 一定更完整。
 * 转录只用于判断崩溃点形态。三条路径与旧实现的 initialState + continue() 逐条对应：
 *   - 末尾是用户消息：它作为本轮 prompt，不重复进条目（新轮、插话）；
 *   - 末尾是带工具调用的 assistant：先补齐未回执的调用，再让模型接着工具结果说话；
 *   - 末尾是无工具调用的 assistant：交给 Go 的收尾判定（完成 / 追加提示 / 失败）。
 */
async function resumePoint(
  bridge: CanvasBridge,
  snapshot: PiSnapshot,
  model: Model<any>,
  disclosure: SessionToolDisclosure,
  checkpoint: (message: AgentMessage) => Promise<void>,
  signal?: AbortSignal,
): Promise<ResumePoint> {
  const history = fromCanonical(snapshot, model);
  if (snapshot.pendingContextCompaction) {
    // Compaction was started by the previous worker. Do not let normal recovery
    // call PiModelStep while Go's ActiveTaskID belongs to the compaction task.
    // Restore the persisted branch and commit the existing Go checkpoint before
    // issuing another model prompt.
    const storedEntries = (snapshot.piSessionEntries || []).map(({ entry }) => entry as unknown as SessionEntry);
    const tail = history.at(-1);
    const prompt = tail?.role === "user" ? textContent(tail.content)
      : snapshot.piMessages?.length ? CONTINUATION_PROMPT : snapshot.request.prompt;
    const entries = storedEntries.length ? storedEntries : sessionEntriesFromMessages(snapshot.runId,
      tail?.role === "user" ? history.slice(0, -1) : history);
    return { entries, prompt, activeLeafId: snapshot.pendingContextCompaction.activeLeafId };
  }
  await recoverToolResults(bridge, snapshot, history, disclosure, checkpoint, signal);

  const storedEntries = (snapshot.piSessionEntries || []).map(({ entry }) => entry as unknown as SessionEntry);
  if (storedEntries.length > 0) {
    let prompt = snapshot.piMessages?.length ? CONTINUATION_PROMPT : snapshot.request.prompt;
    let activeLeafId = snapshot.piActiveLeafId || undefined;
    const tail = history.at(-1);
    if (snapshot.piMessages?.length && tail?.role === "user") {
      // If the worker died after persisting the user's entry but before model work,
      // replay that request from its parent. The old leaf remains in the tree as a
      // recoverable abandoned branch.
      const leaf = storedEntries.find((entry) => entry.id === activeLeafId) as (SessionEntry & { message?: AgentMessage }) | undefined;
      if (leaf?.type === "message" && leaf.message.role === "user") {
        activeLeafId = leaf.parentId || undefined;
        prompt = textContent(tail.content);
      }
    }
    if (snapshot.piMessages?.length && tail?.role === "assistant" && callsFromAssistant(tail as AssistantMessage).length === 0) {
      const taskId = snapshot.lastTaskId || snapshot.noToolTaskId;
      if (taskId) {
        const decision = await bridge.noToolTurn(snapshot, taskId, signal);
        if (isTerminalRunStatus(decision.status)) return { entries: [], prompt: "" };
        if (decision.nudge) prompt = decision.nudge;
      }
    }
    return { entries: storedEntries, prompt, activeLeafId };
  }
  const tail = history.at(-1);
  if (tail?.role === "user") {
    return { entries: sessionEntriesFromMessages(snapshot.runId, history.slice(0, -1)),
      prompt: textContent(tail.content) };
  }
  if (tail?.role === "assistant" && callsFromAssistant(tail as AssistantMessage).length === 0) {
    const taskId = snapshot.lastTaskId || snapshot.noToolTaskId;
    if (taskId) {
      const decision = await bridge.noToolTurn(snapshot, taskId, signal);
      if (isTerminalRunStatus(decision.status)) return { entries: [], prompt: "" };
      if (decision.nudge) return { entries: sessionEntriesFromMessages(snapshot.runId, history),
        prompt: decision.nudge };
    }
  }
  return { entries: sessionEntriesFromMessages(snapshot.runId, history), prompt: CONTINUATION_PROMPT };
}

interface SessionBootstrap {
  session: AgentSession;
  sessionManager: SessionManager;
  cleanup: () => void;
}

interface CanvasCompactionHookDependencies {
  bridge: CanvasBridge;
  snapshot: () => PiSnapshot;
  sessionRevision: () => number;
  activeLeafId: () => string;
  pendingCompaction: () => PiSnapshot["pendingContextCompaction"];
  drain: () => Promise<void>;
  signal?: AbortSignal;
  onCommitted: (entry: Record<string, unknown>, revision: number) => void;
  onFailure: (error: Error) => void;
}

/**
 * The inline Pi extension is the only compaction adapter. Go creates and signs
 * the checkpoint; a failed Go call cancels Pi's built-in summarizer.
 */
export function createCanvasContextCompactionExtension(deps: CanvasCompactionHookDependencies): InlineExtension {
  let activeOperation = deps.pendingCompaction()?.operationId || "";
  return {
    name: "canvas-structured-context-compaction",
    hidden: true,
    factory: (pi: ExtensionAPI) => {
      pi.on("session_before_compact", async (event: SessionBeforeCompactEvent) => {
        try {
          await deps.drain();
          const pending = deps.pendingCompaction();
          if (pending && (pending.sessionRevision !== deps.sessionRevision() || pending.activeLeafId !== deps.activeLeafId())) {
            throw new FatalWorkerError("Persisted Go compaction no longer matches the restored Pi session");
          }
          const operation = pending
            ? await deps.bridge.resumeContextCompaction(deps.snapshot(), pending.operationId, event.signal)
            : await deps.bridge.compactContext(deps.snapshot(), {
              sessionRevision: deps.sessionRevision(), activeLeafId: deps.activeLeafId(), reason: event.reason,
              willRetry: event.willRetry, tokensBefore: event.preparation.tokensBefore,
            }, event.signal);
          if (pending && operation.operationId !== pending.operationId) {
            throw new FatalWorkerError("Go resumed a different context compaction operation");
          }
          activeOperation = operation.operationId;
          return { compaction: {
            summary: operation.summary!, firstKeptEntryId: operation.firstKeptEntryId!,
            tokensBefore: operation.tokensBefore ?? event.preparation.tokensBefore, details: operation.details,
          } };
        } catch (error) {
          const failure = error instanceof Error ? error : new Error(String(error));
          deps.onFailure(failure);
          // ExtensionRunner swallows handler exceptions. Cancel so Pi cannot run
          // its built-in semantic summarizer when the Go checkpoint is unavailable.
          return { cancel: true };
        }
      });
      pi.on("session_compact", async (event: SessionCompactEvent) => {
        try {
          await deps.drain();
          const entry = event.compactionEntry as unknown as Record<string, unknown>;
          const details = entry.details as Record<string, unknown> | undefined;
          const operationId = typeof details?.operationId === "string" ? details.operationId : activeOperation;
          if (!operationId || operationId !== activeOperation) {
            throw new FatalWorkerError("Pi compaction entry has no matching Go operation");
          }
          const result = await deps.bridge.commitContextCompaction(deps.snapshot(), operationId,
            deps.sessionRevision(), entry, deps.signal);
          if (!Number.isSafeInteger(result.sessionRevision) || result.sessionRevision <= deps.sessionRevision()) {
            throw new FatalWorkerError("Go did not advance the Pi session revision after compaction");
          }
          deps.onCommitted(entry, result.sessionRevision);
          activeOperation = "";
        } catch (error) {
          deps.onFailure(error instanceof Error ? error : new Error(String(error)));
        }
      });
      pi.on("session_compact_failed", (event: SessionCompactFailedEvent) => {
        if (activeOperation) {
          deps.onFailure(new FatalWorkerError(
            `Go-backed Pi context compaction failed (${event.reason}${event.errorMessage ? `: ${event.errorMessage}` : ""})`,
          ));
        }
      });
    },
  };
}

async function bootstrapSession(
  snapshot: PiSnapshot,
  systemPrompt: string,
  entries: SessionEntry[],
  streamSimple: ReturnType<typeof createCanvasStreamFn>,
  disclosure: SessionToolDisclosure,
  extensionFactories: InlineExtension[],
): Promise<SessionBootstrap> {
  const workspace = createWorkspace();
  try {
    const model = canvasModel(snapshot);
    // 不发现任何磁盘模型目录、也不访问模型网络：唯一 provider 是下面的桥。
    const runtime = await ModelRuntime.create({ modelsPath: null, refreshOnCreate: false, allowModelNetwork: false });
    runtime.registerProvider(CANVAS_PROVIDER_ID, {
      name: "Canvas Bridge",
      api: CANVAS_API_ID,
      baseUrl: CANVAS_BASE_URL,
      models: [{
        id: model.id, name: model.name, api: CANVAS_API_ID, reasoning: true, input: model.input,
        cost: { ...emptyUsage.cost }, contextWindow: model.contextWindow, maxTokens: model.maxTokens,
      }],
      streamSimple,
    } as never);
    await runtime.setRuntimeApiKey(CANVAS_PROVIDER_ID, "canvas-bridge-internal");
    const resolved = runtime.getModel(CANVAS_PROVIDER_ID, model.id);
    if (!resolved) throw new FatalWorkerError("canvas provider model did not resolve");

    const settingsManager = SettingsManager.create(workspace.cwd, workspace.agentDir);
    const resourceLoader = new DefaultResourceLoader({
      cwd: workspace.cwd, agentDir: workspace.agentDir, settingsManager,
      // 隔离运行：不加载扩展、技能、模板、主题与任何磁盘上下文文件。
      // 系统提示的唯一来源是"服务端策略 + 仓库 Harness"，见 system-prompt.ts。
      noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true,
      extensionFactories,
      systemPrompt,
    });
    await resourceLoader.reload();

    const sessionFiles: FileEntry[] = [
      ...(snapshot.piSessionHeader ? [snapshot.piSessionHeader as unknown as FileEntry] : []),
      ...entries,
    ];
    const sessionManager = SessionManager.inMemory(workspace.cwd,
      { id: snapshot.piSessionId || snapshot.runId }, sessionFiles);
    const { session } = await createAgentSession({
      cwd: workspace.cwd,
      agentDir: workspace.agentDir,
      model: resolved,
      modelRuntime: runtime,
      sessionManager,
      settingsManager,
      resourceLoader,
      // 默认 coding 工具（read/bash/edit/write/ls/grep/find/powershell）一个都不注册。
      noTools: "all",
      tools: disclosure.activeNames(),
    });
    return { session, sessionManager, cleanup: workspace.cleanup };
  } catch (error) {
    workspace.cleanup();
    throw error;
  }
}

/**
 * 驱动一条已领取的 Pi 运行。
 *
 * 循环属于 `@earendil-works/pi-coding-agent` 的 `AgentSession`；这里只做三件事：
 * 把模型请求接到 Go 的模型步骤（provider）、把会话事件接到 Go 的持久化（检查点与批次）、
 * 把 Go 的控制面决策（收尾判定、终态、取消）翻成会话动作。任何模型或画布副作用
 * 都必须先拿到 Go 的回执。
 */
export async function runCanvasAgent(
  bridge: CanvasBridge,
  initial: PiSnapshot,
  shutdown?: AbortSignal,
  harness?: PromptParts,
  toolSchema?: ToolSchemaArtifact,
): Promise<void> {
  shutdown?.throwIfAborted();
  let snapshot = initial;
  const model = canvasModel(snapshot);
  // 合同校验必须早于任何恢复副作用：schema 不兼容时不能先写检查点或执行工具。
  if (toolSchema) assertToolSnapshotMatchesSchema(snapshot.tools, toolSchema);
  // 冻结的 Harness 正文优先：恢复时用原快照，重读磁盘会静默换掉在途运行的系统提示。
  const parts = snapshot.harness ?? harness;
  const systemPrompt = parts ? assembleSystemPrompt(parts, snapshot.canonical.systemPrompt) : snapshot.canonical.systemPrompt;
  const promptContract = parts ? harnessHash(parts) : undefined;

  const queue = new CheckpointQueue();
  let sequence = snapshot.piMessages?.length || 0;
  let listenerFailure: unknown;
  let compactionFailure: unknown;
  let session: AgentSession | undefined;
  let sessionManager: SessionManager | undefined;
  let sessionRevision = snapshot.piSessionRevision || 1;
  let sessionLeafId = snapshot.piActiveLeafId || "";
  let pendingContextCompaction = snapshot.pendingContextCompaction;
  const persistedSessionEntryIds = new Set((snapshot.piSessionEntries || []).map(({ entry }) => String(entry.id || "")));
  const stagedRecoveryEntries: SessionEntry[] = [];
  let activeTaskId = "";
  let latestTaskId = "";
  let noToolTurnPending = false;
  let admittedCallIds = new Set<string>();
  let batchRejection = "";
  let canonicalCount = snapshot.canonical.messages.length;
  const pendingInterjections = new Map<string, string>();
  const injectedInterjectionIds = new Set<string>();
  const syncPendingInterjections = (next: PiSnapshot): void => {
    for (const item of next.pendingInterjections || []) {
      if (item.id && typeof item.text === "string") pendingInterjections.set(item.id, item.text);
    }
  };
  const interjectionMessage = (text: string): string => `【用户插话】${text}`;
  syncPendingInterjections(snapshot);
  const interjectionIdsForMessage = (message: AgentMessage): string[] => {
    const content = (message as unknown as { content?: unknown }).content;
    const text = textContent(content);
    return [...pendingInterjections]
      .filter(([id, body]) => injectedInterjectionIds.has(id) && text.includes(interjectionMessage(body)))
      .map(([id]) => id);
  };
  const prependPendingInterjections = (prompt: string): string => {
    const notes: string[] = [];
    for (const [id, body] of pendingInterjections) {
      if (injectedInterjectionIds.has(id)) continue;
      injectedInterjectionIds.add(id);
      notes.push(interjectionMessage(body));
    }
    if (notes.length === 0) return prompt;
    return [prompt, ...notes].filter((part) => part.trim() !== "").join("\n\n");
  };
  const steerPendingInterjections = async (): Promise<void> => {
    if (!session?.isStreaming) return;
    for (const [id, body] of pendingInterjections) {
      if (injectedInterjectionIds.has(id)) continue;
      injectedInterjectionIds.add(id);
      try {
        await session.steer(interjectionMessage(body), undefined, { source: "interactive" });
      } catch (error) {
        injectedInterjectionIds.delete(id);
        throw error;
      }
    }
  };

  const checkpoint = async (message: AgentMessage, taskId?: string, interjectionIds: string[] = []): Promise<void> => {
    let entries: SessionEntry[];
    let leafId: string;
    if (sessionManager) {
      entries = sessionManager.getEntries().filter((entry) => !persistedSessionEntryIds.has(entry.id));
      leafId = sessionManager.getLeafId() || "";
    } else {
      const role = (message as unknown as { role?: string }).role;
      if (role === "toolResult") {
        const toolMessage = message as unknown as ToolResultMessage;
        const id = `${snapshot.runId}:recovered:${toolMessage.toolCallId}`;
        if (!persistedSessionEntryIds.has(id)) {
          stagedRecoveryEntries.push({
            type: "message", id, parentId: sessionLeafId || null, timestamp: new Date().toISOString(), message,
          } as unknown as SessionEntry);
          sessionLeafId = id;
        }
      }
      entries = stagedRecoveryEntries.filter((entry) => !persistedSessionEntryIds.has(entry.id));
      leafId = sessionLeafId;
    }
    const result = await bridge.checkpoint(snapshot, ++sequence, message as unknown as Record<string, unknown>, taskId, shutdown,
      { revision: sessionRevision, activeLeafId: leafId, entries: entries as unknown as Record<string, unknown>[], interjectionIds });
    if (result && typeof result.sessionRevision === "number" && result.sessionRevision > 0) {
      sessionRevision = result.sessionRevision;
      for (const entry of entries) persistedSessionEntryIds.add(entry.id);
      sessionLeafId = leafId;
      snapshot = {
        ...snapshot,
        piSessionRevision: sessionRevision,
        piActiveLeafId: sessionLeafId,
        piSessionEntries: [
          ...(snapshot.piSessionEntries || []),
          ...entries.map((entry) => ({ runId: snapshot.runId, entry: entry as unknown as Record<string, unknown> })),
        ],
      };
    }
    for (const id of interjectionIds) {
      pendingInterjections.delete(id);
      injectedInterjectionIds.delete(id);
    }
    if (interjectionIds.length > 0) {
      snapshot = { ...snapshot, pendingInterjections: [...pendingInterjections].map(([id, text]) => ({ id, text })) };
    }
  };

  const disclosure = new SessionToolDisclosure(snapshot.tools, async (name, _args, callId, signal) => {
    // 屏障：assistant 消息（含 tool_calls）必须先落库，Go 才会接受这一批工具调用。
    await queue.drain();
    if (!admittedCallIds.has(callId)) throw new Error(batchRejection || "Tool batch was not admitted");
    const receipt = await bridge.executeTool(snapshot, callId, signal);
    const refreshed = await bridge.snapshot(snapshot, signal);
    for (const message of refreshed.canonical.messages.slice(canonicalCount)) {
      // 插话里的图片要直接进会话，不能等下一次模型步骤才补（见旧实现的 steer 分支）。
      if (message.role === "user" && hasImagePart(message.content)) {
        await session?.steer(JSON.stringify(message.content)).catch(() => undefined);
      }
    }
    canonicalCount = refreshed.canonical.messages.length;
    snapshot = refreshed;
    syncPendingInterjections(refreshed);
    await steerPendingInterjections();
    return { result: receipt.result, isError: receipt.isError,
      terminate: receipt.terminated === true || (name === "finish_run" && !receipt.isError) };
  }, snapshot.previousStepTemplate);
  // Old snapshots may still carry openedCategories. They are informational
  // migration state only; all eligible concrete tools are available each run.

  // Provider：Pi 的每次模型请求都翻成 Go 的模型步骤（带上提示合同身份）。
  const streamSimple = createCanvasStreamFn(async ({ messages, signal, onTextDelta }) => {
    await queue.drain();
    if (compactionFailure !== undefined) throw compactionFailure;
    const canonical = toCanonical(messages as Message[], snapshot.canonical.promptCacheKey);
    canonical.tools = disclosure.decorateCanonicalTools(canonical.tools);
    const step = await bridge.modelStep(snapshot, canonical, signal, onTextDelta, promptContract, parts);
    activeTaskId = latestTaskId = step.taskId;
    return step.result;
  });

  const resume = await resumePoint(bridge, snapshot, model, disclosure, checkpoint, shutdown);
  if (resume.prompt === "") return;
  const compactionExtension = createCanvasContextCompactionExtension({
    bridge,
    snapshot: () => snapshot,
    sessionRevision: () => sessionRevision,
    activeLeafId: () => sessionLeafId,
    pendingCompaction: () => pendingContextCompaction,
    drain: () => queue.drain(),
    signal: shutdown,
    onCommitted: (entry, revision) => {
      sessionRevision = revision;
      sessionLeafId = String(entry.id || "");
      pendingContextCompaction = undefined;
      persistedSessionEntryIds.add(sessionLeafId);
      snapshot = {
        ...snapshot,
        pendingContextCompaction: undefined,
        piSessionRevision: sessionRevision,
        piActiveLeafId: sessionLeafId,
        piSessionEntries: [...(snapshot.piSessionEntries || []), { runId: snapshot.runId, entry }],
      };
    },
    onFailure: (error) => { compactionFailure = error; },
  });
  const toolExtension = createCanvasToolsExtension(disclosure, (callId, toolName) => {
    if (!disclosure.isVisible(toolName)) return `Tool ${toolName} is not eligible for this run`;
    if (!admittedCallIds.has(callId)) return batchRejection || `Tool batch does not admit ${toolName}`;
    return undefined;
  });
  const boot = await bootstrapSession(snapshot, systemPrompt, resume.entries, streamSimple, disclosure,
    [compactionExtension, toolExtension]);
  session = boot.session;
  sessionManager = boot.sessionManager;
  if (resume.activeLeafId !== undefined && resume.activeLeafId !== sessionManager.getLeafId()) {
    if (resume.activeLeafId) sessionManager.branch(resume.activeLeafId);
    else sessionManager.resetLeaf();
  }
  try {
    session.subscribe((event) => {
      if (event.type !== "message_end") return;
      const message = event.message;
      if (message.role === "assistant" &&
        (message.stopReason === "error" || message.stopReason === "aborted")) return;
      const taskId = message.role === "assistant" && activeTaskId ? activeTaskId : undefined;
      if (message.role === "assistant") {
        activeTaskId = "";
        const calls = callsFromAssistant(message);
        noToolTurnPending = calls.length === 0;
        batchRejection = calls.length ? batchAdmissionError(disclosure, calls) || "" : "";
        admittedCallIds = new Set(batchRejection ? [] : calls.map((call) => call.id));
        if (admittedCallIds.size > 0) {
          const batch = calls.filter((call) => admittedCallIds.has(call.id));
          disclosure.recordStepCalls(calls.map((call) => call.function.name));
          // 顺序是跨进程合同，不是实现细节：Go 的 `PiToolBatch` 在 `ActiveTaskID` 非空时
          // 拒绝整个批次（"Agent 尚有未完成的模型或工具步骤"），而清掉它的正是这条带 taskId
          // 的 assistant 检查点（`PiCheckpointMessage` 在同一事务里写消息 + 确认模型任务）。
          // 两者同队列，因此顺序就是入队顺序：先检查点，再批次准入。反之每一个工具批次
          // 都会以 403 失败，而 worker 把确定性 4xx 当致命错误 —— 整轮直接退出。
          queue.enqueue(async () => { await checkpoint(message as unknown as AgentMessage, taskId); });
          queue.enqueue(async () => { await bridge.startToolBatch(snapshot, batch, shutdown); });
          return;
        }
      }
      const interjectionIds = message.role === "user" ? interjectionIdsForMessage(message as unknown as AgentMessage) : [];
      queue.enqueue(async () => { await checkpoint(message as unknown as AgentMessage, taskId, interjectionIds); });
    });

    let abortRequested = false;
    const abortSession = (): void => {
      abortRequested = true;
      void session?.abort().catch(() => undefined);
    };
    const onShutdown = (): void => abortSession();
    shutdown?.addEventListener("abort", onShutdown, { once: true });
    let leaseCheckRunning = false;
    const lease = setInterval(() => {
      if (leaseCheckRunning) return;
      leaseCheckRunning = true;
      void (async () => {
        await bridge.renew(snapshot, shutdown);
        snapshot = await bridge.snapshot(snapshot, shutdown);
        syncPendingInterjections(snapshot);
        if (isTerminalRunStatus(snapshot.status)) abortSession();
        else await steerPendingInterjections();
      })().catch(() => abortSession()).finally(() => { leaseCheckRunning = false; });
    }, 15_000);

    try {
      let prompt = prependPendingInterjections(resume.prompt);
      if (pendingContextCompaction) {
        await session.compact();
        if (compactionFailure !== undefined) {
          throw compactionFailure instanceof Error ? compactionFailure : new Error(String(compactionFailure));
        }
        if (pendingContextCompaction) {
          throw new FatalWorkerError("Pi did not commit the pending Go context compaction");
        }
      }
      for (;;) {
        await queue.drain();
        if (listenerFailure !== undefined || isTerminalRunStatus(snapshot.status)) break;
        await session.prompt(prompt, { expandPromptTemplates: false }).catch((error: unknown) => {
          if (compactionFailure !== undefined) {
            throw compactionFailure instanceof Error ? compactionFailure : new Error(String(compactionFailure));
          }
          if (abortRequested || shutdown?.aborted || isTerminalRunStatus(snapshot.status)) return;
          throw error;
        });
        await session.waitForIdle();
        await queue.drain();
        if (compactionFailure !== undefined && !abortRequested && !shutdown?.aborted) {
          throw compactionFailure instanceof Error ? compactionFailure : new Error(String(compactionFailure));
        }
        snapshot = await bridge.snapshot(snapshot, shutdown);
        syncPendingInterjections(snapshot);
        canonicalCount = snapshot.canonical.messages.length;
        if (listenerFailure !== undefined || isTerminalRunStatus(snapshot.status)) break;
        // 收尾判定：只有"最后一个模型步骤没有工具调用"才轮到 Go 决定完成还是追加提示。
        if (!noToolTurnPending || !latestTaskId) break;
        noToolTurnPending = false;
        const decision = await bridge.noToolTurn(snapshot, latestTaskId, shutdown);
        if (isTerminalRunStatus(decision.status)) break;
        const nextPrompt = prependPendingInterjections(decision.nudge ||
          (pendingInterjections.size > 0 ? CONTINUATION_PROMPT : ""));
        if (nextPrompt) {
          prompt = nextPrompt;
          continue;
        }
        break;
      }
    } finally {
      clearInterval(lease);
      shutdown?.removeEventListener("abort", onShutdown);
    }
    if (listenerFailure !== undefined && !abortRequested) {
      throw listenerFailure instanceof Error ? listenerFailure : new Error(String(listenerFailure));
    }
  } finally {
    boot.cleanup();
  }
}
