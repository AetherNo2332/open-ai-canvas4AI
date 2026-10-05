import { setTimeout as delay } from "node:timers/promises";
import type { CanvasModelResult } from "./pi-stream.js";
import { FatalWorkerError, type CanvasToolSpec } from "./tool-disclosure.js";
import type { PromptParts } from "./system-prompt.js";
import { EventScheduler, RunEvents } from "./event-scheduler.js";
import type { SubagentRuntime } from "./subagent-wire.js";

/**
 * 内部协议里的**确定性**客户端错误：同一条请求重发不可能成功。
 *
 * 这些状态若按普通错误处理，worker 会拿着同样的请求无限重试；而它每次重试都会
 * 续租，于是租约永不过期、停滞看门狗（依赖 lease_expires_at 超时）也永不触发 ——
 * 实测表现为运行停在 running、revision 每 ~75s 缓慢 +1（领取 + 续租），前端永远
 * 显示"运行中"。408（超时）/409（并发冲突，重读后可能成功）/425/429（限流）与
 * 5xx、网络错误仍按可重试处理。
 */
const NON_RETRYABLE_BRIDGE_STATUSES = new Set([400, 401, 403, 404, 405, 406, 410, 413, 414, 415, 422, 426]);

export const CANVAS_PI_WIRE_IDENTITY = {
  protocolVersion: "canvas-pi-wire/v3",
  piSdkVersion: "0.87.1",
  sessionFormatVersion: "3",
} as const;

export interface PiCanonical {
  systemPrompt: string;
  messages: Record<string, unknown>[];
  tools: Record<string, unknown>[];
  toolChoice: "auto" | "required";
  promptCacheKey?: string;
}

export interface PiSkillFile {
  path: string;
  sha256: string;
  size: number;
  mimeType?: string;
  text: boolean;
}

export interface PiSkillSnapshot {
  id: string;
  nativeName: string;
  displayName: string;
  description: string;
  versionId: string;
  version: string;
  contentHash: string;
  entryPath: string;
  entryContent: string;
  files: PiSkillFile[];
}

export interface PiSkillReadPage {
  nativeName: string;
  skillId: string;
  versionId: string;
  contentHash: string;
  path: string;
  sha256: string;
  isEntry: boolean;
  offset: number;
  limit: number;
  content: string;
  hasMore: boolean;
  totalRunes: number;
}

export interface PiSnapshot {
	subagent?: SubagentRuntime;

  skillRuntimeMode?: "pi-native" | "legacy-go";
  skills?: PiSkillSnapshot[];
  runId: string;
  piSessionId?: string;
  piSessionRevision?: number;
  piSessionHeader?: Record<string, unknown>;
  piSessionEntries?: Array<{ runId: string; entry: Record<string, unknown> }>;
  piActiveLeafId?: string;
  piSessionLeaseEpoch?: number;
  userId: string;
  revision: number;
  status: string;
  request: { prompt: string; canvasId?: string; model?: string; channelModelKey?: string; visionEnabled?: boolean; subagentEnabled?: boolean };
  modelLimits: { contextWindowTokens: number; maxOutputTokens: number; reservedOutputTokens?: number;
    overheadTokens?: number; inputBudgetTokens?: number; compactAtTokens?: number; configured: boolean; source: string;
    version?: string; digest?: string; compactionReserveTokens?: number; keepRecentTokens?: number; summaryOutputTokens?: number };
  canonical: PiCanonical;
  activeTaskId?: string;
  lastTaskId?: string;
  noToolTaskId?: string;
  noToolNudge?: string;
  modelFailureTaskId?: string;
  modelFailureNudge?: string;
  pendingInterjections?: Array<{ id: string; text: string; source?: string; createdAt?: string }>;
  /** A Go-backed compaction whose model task survived a worker restart. */
  pendingContextCompaction?: {
    operationId: string;
    sessionRevision: number;
    activeLeafId: string;
    reason: string;
    willRetry: boolean;
    tokensBefore: number;
  };
  previousStepTemplate?: string;
  tools: CanvasToolSpec[];
  piMessages?: Record<string, unknown>[];
  openedCategories?: string[];
  /**
   * 首步合同（v2）冻结的 Harness 正文。存在即代表"这一轮的提示已经定型"：
   * runner 必须用它，而不是重读磁盘 —— 运维改文件再重启不会换掉在途运行的系统提示。
   */
  harness?: PromptParts;
  /** 合同版本：>= 2 才有快照与首步换单语义。 */
  contractVersion?: number;
}

interface PiModelStepView {
  decision?: "model" | "compact";
  modelLimits?: PiSnapshot["modelLimits"];
  projectedTokens?: number;
  taskId: string;
  status: string;
  textDraft?: string;
  result?: CanvasModelResult;
  error?: string;
}

export interface PiTurnDecision { status: string; nudge?: string }

export class CanvasCompactionNeeded extends Error {
  constructor(readonly modelLimits?: PiSnapshot["modelLimits"]) { super("Go requested context compaction before model admission"); }
}

export class CanvasModelRetry extends Error {
  constructor(readonly decision: PiTurnDecision) {
    super("Go requested a bounded model retry");
  }
}

/** Go already finalized the run; no further lease-bound callback is legal. */
export class CanvasRunTerminated extends Error {
  constructor(readonly status: string) {
    super(`Canvas run ${status}`);
    this.name = "CanvasRunTerminated";
  }
}

export class CanvasRunSuspended extends CanvasRunTerminated {
  constructor() { super("suspended"); this.name = "CanvasRunSuspended"; }
}

/** The run remains active, but this worker no longer owns its lease. */
export class CanvasLeaseLost extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CanvasLeaseLost";
  }
}

interface PiCheckpointResult {
  saved: boolean;
  sessionRevision?: number;
  terminated?: boolean;
  status?: string;
}

const TERMINAL_RUN_STATUSES = new Set(["completed", "failed", "cancelled", "rejected"]);

export interface PiContextCompactionView {
  operationId: string;
  status: string;
  taskId?: string;
  summary?: string;
  firstKeptEntryId?: string;
  tokensBefore?: number;
  sourceDigest?: string;
  checkpointDigest?: string;
  mode?: string;
  reason?: string;
  fallback?: boolean;
  details?: Record<string, unknown>;
  sessionRevision?: number;
  nativePreparation?: Record<string, unknown>;
  summaryMaxTokens?: number;
  usage?: import("@earendil-works/pi-ai").Usage;
}

interface PiToolReceipt {
	suspended?: boolean;
	operationId?: string;
  callId: string;
  pending: boolean;
  result?: unknown;
  isError?: boolean;
  /** 本轮已进入终态（拒绝/失败/取消/完成），该调用不会有回执。 */
  terminated?: boolean;
}

export interface PiToolCall {
  id: string;
  type: "function";
  function: { name: string; arguments: string };
  thoughtSignature?: string;
}

export class CanvasBridge {
  constructor(private readonly baseUrl: string, private readonly token: string, readonly workerId: string,
    private readonly scheduler?: EventScheduler, private readonly events?: RunEvents) {
    if (!baseUrl || !token || !workerId) throw new Error("Pi bridge configuration is incomplete");
  }

  async claim(signal?: AbortSignal): Promise<PiSnapshot | null> {
    const result = await this.request<{ run: PiSnapshot | null }>("POST", "/claim", { owner: this.workerId }, undefined, signal);
    return result.run;
  }

  async capacity(active: number, capacity: number, signal?: AbortSignal): Promise<void> {
    await this.request("POST", "/capacity", { owner: this.workerId, active, capacity }, undefined, signal);
  }
  async schedulerConfig(signal?:AbortSignal):Promise<import("./runtime-config.js").AgentSchedulerSetting> {
    const result=await this.request<{setting:import("./runtime-config.js").AgentSchedulerSetting}>("GET","/config",undefined,undefined,signal);
    return result.setting;
  }
  async capacityReport(report:import("./runtime-config.js").CapacityReport,signal?:AbortSignal):Promise<void> {
    await this.request("POST","/capacity",{owner:this.workerId,...report},undefined,signal);
  }

  async snapshot(run: PiSnapshot, signal?: AbortSignal): Promise<PiSnapshot> {
    const result = await this.request<{ run: PiSnapshot }>("GET", `/runs/${encodeURIComponent(run.runId)}`, undefined, run, signal);
    return result.run;
  }

  async readSkillFile(run: PiSnapshot, nativeName: string, path: string, offset = 0, limit = 12_000,
    signal?: AbortSignal): Promise<PiSkillReadPage> {
    const query = new URLSearchParams({ path, offset: String(offset), limit: String(limit) });
    return this.request<PiSkillReadPage>("GET",
      `/runs/${encodeURIComponent(run.runId)}/skills/${encodeURIComponent(nativeName)}/file?${query}`, undefined, run, signal);
  }

  async renew(run: PiSnapshot, signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/renew`, {}, run, signal);
  }

  async release(run: PiSnapshot, signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/release`, {}, run, signal);
  }

  async phase(run: PiSnapshot, phase: string, kind = "", waitId = "", reason = "", signal?: AbortSignal): Promise<void> {
    if (!this.events) return;
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/phase`, { phase, kind, waitId, reason }, run, signal);
  }

  async control(run: PiSnapshot, signal?: AbortSignal): Promise<Pick<PiSnapshot,"status"|"pendingInterjections"|"pendingContextCompaction">> {
    return this.request("GET", `/runs/${encodeURIComponent(run.runId)}/control`, undefined, run, signal);
  }

  onControl(runId: string, listener: () => void): () => void {
    return this.events?.subscribe(`control:${runId}`, listener) ?? (() => {});
  }

  private async wait<T>(run: PiSnapshot, read: () => Promise<T>, pending: (value: T) => boolean, signal?: AbortSignal): Promise<T> {
    if (this.events) return this.events.wait(run.runId, read, pending, signal);
    // Recovery-only bridge instances used by SDK probes also avoid tight polling.
    for (;;) { const value = await read(); if (!pending(value)) return value; await delay(5000, undefined, { signal }); }
  }

  async consumeEvents(signal: AbortSignal): Promise<void> {
    if (!this.events) throw new Error("Run event hub is required");
    let cursor = 0;
    const recovery = setInterval(() => this.events!.recover(), 5000);
    try {
      while (!signal.aborted) {
        try {
          const response = await fetch(`${this.baseUrl.replace(/\/+$/, "")}/internal-agent/events`, {
            headers: { Authorization: `Bearer ${this.token}`, Accept: "text/event-stream", "Last-Event-ID": String(cursor),
              "X-Agent-Protocol-Version": CANVAS_PI_WIRE_IDENTITY.protocolVersion,
              "X-Pi-SDK-Version": CANVAS_PI_WIRE_IDENTITY.piSdkVersion, "X-Pi-Session-Format": CANVAS_PI_WIRE_IDENTITY.sessionFormatVersion }, signal,
          });
          if (!response.ok || !response.body) throw new Error(`Agent event stream HTTP ${response.status}`);
          const reader = response.body.getReader(); const decoder = new TextDecoder(); let buffer = "";
          try {
            for (;;) {
              const chunk = await reader.read(); if (chunk.done) break;
              buffer += decoder.decode(chunk.value, { stream: true });
              let boundary: number;
              while ((boundary = buffer.indexOf("\n\n")) >= 0) {
                const frame = buffer.slice(0,boundary); buffer = buffer.slice(boundary+2);
                const data = frame.split("\n").find((line) => line.startsWith("data: "));
                if (!data) continue;
                const event = JSON.parse(data.slice(6)) as { sequence: number; runId?: string; kind: string };
                if (!Number.isSafeInteger(event.sequence) || event.sequence <= cursor) continue;
                if (event.runId) {
                  this.events.wake(event.runId);
                  if (event.kind === "run_changed") { this.events.wake(`control:${event.runId}`); this.events.wake("dispatch"); }
                } else {
                  if(event.kind==="scheduler_config_changed")this.events.wake("config");
                  this.events.recover();
                }
                cursor = event.sequence;
              }
            }
          } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
        } catch (error) { if (!signal.aborted) console.error("Agent event stream reconnect:", error instanceof Error ? error.message : String(error)); }
        if (!signal.aborted) await delay(1000, undefined, { signal }).catch(() => {});
      }
    } finally { clearInterval(recovery); }
  }

  async modelPreflight(run: PiSnapshot, canonical: PiCanonical, signal?: AbortSignal, harnessHash?: string,
    harness?: PromptParts): Promise<PiModelStepView> {
    return this.request("POST", `/runs/${encodeURIComponent(run.runId)}/model-preflight`, { canonical, harnessHash, harness }, run, signal);
  }

  async modelStep(run: PiSnapshot, canonical: PiCanonical, signal?: AbortSignal,
    onTextDelta?: (delta: string) => void, harnessHash?: string,
    harness?: PromptParts): Promise<{ taskId: string; result: CanvasModelResult }> {
    const path = `/runs/${encodeURIComponent(run.runId)}/model-steps`;
    // harnessHash 让服务端在首个模型步固化提示合同、之后拒绝漂移（见 harnessHash 的注释）。
    // 服务端路由用 DisallowUnknownFields 解码，所以这两个字段必须在 Go 侧已声明。
    const body: Record<string, unknown> = { canonical };
    if (harnessHash) body.harnessHash = harnessHash;
    // 正文只在首个模型步被固化（Go 存快照）；之后每一步重发同一份用于校验，
    // 这样"哪一份 Harness 参与了这个运行"在服务端是可核对的事实，而不是 Node 的内存。
    if (harness) body.harness = harness;
    // A claimed native run may still own a billed model task using the previous
    // worker's generated paths. Resume that task rather than reposting a new prompt.
    let step = run.skillRuntimeMode === "pi-native" && run.activeTaskId
      ? await this.request<PiModelStepView>("GET", `${path}/${encodeURIComponent(run.activeTaskId)}`, undefined, run, signal)
      : await this.request<PiModelStepView>("POST", path, body, run, signal);
    if (step.decision === "compact" || step.status === "waiting_compaction") throw new CanvasCompactionNeeded(step.modelLimits);
    if (TERMINAL_RUN_STATUSES.has(step.status)) throw new CanvasRunTerminated(step.status);
    let sentTextDraft = "";
    const emitTextDraftDelta = (draft: string | undefined): void => {
      if (!onTextDelta || !draft) return;
      // Go exposes a cumulative draft. Only forward the suffix that was not
      // observed by this model-step call; the final poll may repeat the same
      // snapshot and must therefore be a no-op.
      if (sentTextDraft !== "" && !draft.startsWith(sentTextDraft)) return;
      const delta = draft.slice(sentTextDraft.length);
      if (delta === "") return;
      sentTextDraft = draft;
      onTextDelta(delta);
    };
    // 模型任务执行期间 textDraft 会持续增长；把新增部分当增量正文上报，
    // 这样 Pi 事件流是真实增量，而不是任务结束后一次性补齐。
    emitTextDraftDelta(step.textDraft);
    if (step.status === "queued" || step.status === "running") {
      let waitingStatus=step.status;
      await this.phase(run,step.status === "queued" ? "waiting_resource" : "waiting_model", "model", step.taskId, step.status === "queued" ? "等待模型请求额度" : "等待模型响应", signal);
      step = await this.wait(run, async () => {
        const next = await this.request<PiModelStepView>("GET", `${path}/${encodeURIComponent(step.taskId)}`, undefined, run, signal);
        if(next.status === "running" && waitingStatus !== "running") {
          waitingStatus=next.status;
          await this.phase(run,"waiting_model","model",step.taskId,"等待模型响应",signal);
        }
        emitTextDraftDelta(next.textDraft);
        if (TERMINAL_RUN_STATUSES.has(next.status)) throw new CanvasRunTerminated(next.status);
        return next;
      }, (next) => next.status === "queued" || next.status === "running", signal);
      await this.phase(run, "advancing", "", "", "", signal);
    }
    emitTextDraftDelta(step.textDraft);
    if (step.status !== "succeeded" || !step.result) {
      if (step.status !== "succeeded") {
        const decision = await this.request<PiTurnDecision>("POST", `${path}/${encodeURIComponent(step.taskId)}/fail`, {}, run, signal);
        if (decision.status === "continue" && decision.nudge) throw new CanvasModelRetry(decision);
        if (["completed", "failed", "cancelled", "rejected"].includes(decision.status)) {
          throw new CanvasRunTerminated(decision.status);
        }
      }
      throw new Error(`Canvas model step ${step.status}`);
    }
    return { taskId: step.taskId, result: step.result };
  }

  async acknowledgeModel(run: PiSnapshot, taskId: string, signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/model-steps/${encodeURIComponent(taskId)}/ack`, {}, run, signal);
  }

  async checkpoint(run: PiSnapshot, sequence: number, message: Record<string, unknown>, taskId?: string, signal?: AbortSignal,
    session?: { revision: number; activeLeafId: string; entries: Record<string, unknown>[]; interjectionIds?: string[] }): Promise<PiCheckpointResult> {
    return this.request<PiCheckpointResult>("POST", `/runs/${encodeURIComponent(run.runId)}/messages`, {
      sequence, message,
      ...(taskId ? { taskId } : {}),
      ...(session ? { sessionRevision: session.revision, activeLeafId: session.activeLeafId, sessionEntries: session.entries,
        ...(session.interjectionIds?.length ? { interjectionIds: session.interjectionIds } : {}) } : {}),
    }, run, signal);
  }

  async compactContext(run: PiSnapshot, request: { sessionRevision: number; activeLeafId: string; reason: string;
    willRetry: boolean; tokensBefore: number; preparation?: Record<string, unknown> }, signal?: AbortSignal): Promise<PiContextCompactionView> {
    const path = `/runs/${encodeURIComponent(run.runId)}/context-compactions`;
    let operation = await this.request<PiContextCompactionView>("POST", path, request, run, signal);
    await this.phase(run, "waiting_compaction", "compaction", operation.operationId, "压缩上下文", signal);
    if (operation.status === "queued" || operation.status === "running") operation = await this.wait(run, () => this.request<PiContextCompactionView>("GET",
      `${path}/${encodeURIComponent(operation.operationId)}`, undefined, run, signal),
      (next) => next.status === "queued" || next.status === "running", signal);
    if (operation.nativePreparation && ["prepared", "running"].includes(operation.status)) return operation;
    if (operation.status !== "succeeded" || !operation.summary || !operation.firstKeptEntryId || !operation.details) {
      throw new FatalWorkerError(`Go context compaction did not return a checkpoint (${operation.status})`);
    }
    return operation;
  }

  async resumeContextCompaction(run: PiSnapshot, operationId: string, signal?: AbortSignal): Promise<PiContextCompactionView> {
    const path = `/runs/${encodeURIComponent(run.runId)}/context-compactions/${encodeURIComponent(operationId)}`;
    let operation = await this.request<PiContextCompactionView>("GET", path, undefined, run, signal);
    await this.phase(run, "waiting_compaction", "compaction", operation.operationId, "压缩上下文", signal);
    if (operation.operationId === operationId && operation.nativePreparation && ["prepared", "running"].includes(operation.status)) return operation;
    if (operation.status === "queued" || operation.status === "running") operation = await this.wait(run, () => this.request<PiContextCompactionView>("GET", path, undefined, run, signal),
      (next) => next.status === "queued" || next.status === "running", signal);
    if (operation.operationId !== operationId || operation.status !== "succeeded" || !operation.summary ||
        !operation.firstKeptEntryId || !operation.details) {
      throw new FatalWorkerError(`Go context compaction could not be resumed (${operation.status})`);
    }
    return operation;
  }

  async nativeCompactionModel(run: PiSnapshot, operationId: string, input: {
    callId: string; systemPrompt: string; prompt: string; maxTokens: number;
  }, signal?: AbortSignal): Promise<{ status: string; result?: import("./pi-stream.js").CanvasModelResult }> {
    const path = `/runs/${encodeURIComponent(run.runId)}/context-compactions/${encodeURIComponent(operationId)}/model`;
    return this.wait(run, () => this.request("POST", path, input, run, signal),
      (view: { status: string }) => view.status === "queued" || view.status === "running", signal);
  }

  async finishNativeCompaction(run: PiSnapshot, operationId: string, input: {
    summary?: string; fallback?: boolean; usage?: unknown;
  }, signal?: AbortSignal): Promise<PiContextCompactionView> {
    return this.request("POST", `/runs/${encodeURIComponent(run.runId)}/context-compactions/${encodeURIComponent(operationId)}/complete`,
      input, run, signal);
  }

  async commitContextCompaction(run: PiSnapshot, operationId: string, sessionRevision: number,
    entry: Record<string, unknown>, signal?: AbortSignal): Promise<{ committed: boolean; sessionRevision: number }> {
    return this.request("POST", `/runs/${encodeURIComponent(run.runId)}/context-compactions/${encodeURIComponent(operationId)}/commit`,
      { sessionRevision, entry }, run, signal);
  }

  async startToolBatch(run: PiSnapshot, taskId: string, calls: PiToolCall[], signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/tool-batches`, { taskId, calls }, run, signal);
  }

  async executeTool(run: PiSnapshot, taskId: string, callId: string, signal?: AbortSignal): Promise<PiToolReceipt> {
    const path = `/runs/${encodeURIComponent(run.runId)}/tool-calls/${encodeURIComponent(callId)}/advance`;
    const first = await this.request<PiToolReceipt>("POST", path, { taskId }, run, signal);
    if (!first.pending || first.terminated || first.suspended) return first;
    await this.phase(run, "waiting_tool", "tool", first.operationId ?? `${taskId}:${callId}`, "等待工具结果", signal);
    return this.wait(run, () => this.request<PiToolReceipt>("POST", path, { taskId }, run, signal),
      (receipt) => receipt.pending && !receipt.terminated && !receipt.suspended, signal);
  }

  /** 上报无法重试的启动期错误，避免运行静默停在 running。 */
  async failRun(run: PiSnapshot, reason: string, signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/fail`, { reason }, run, signal);
  }

  async noToolTurn(run: PiSnapshot, taskId: string, signal?: AbortSignal): Promise<{ status: string; nudge?: string }> {
    return this.request("POST", `/runs/${encodeURIComponent(run.runId)}/no-tool-turn`, { taskId }, run, signal);
  }

  private async request<T>(method: string, path: string, body?: unknown, run?: PiSnapshot, signal?: AbortSignal): Promise<T> {
    const headers: Record<string, string> = {
      Authorization: `Bearer ${this.token}`,
      Accept: "application/json",
      "X-Agent-Protocol-Version": CANVAS_PI_WIRE_IDENTITY.protocolVersion,
      "X-Pi-SDK-Version": CANVAS_PI_WIRE_IDENTITY.piSdkVersion,
      "X-Pi-Session-Format": CANVAS_PI_WIRE_IDENTITY.sessionFormatVersion,
    };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (run) {
      headers["X-Agent-User-ID"] = run.userId;
      headers["X-Agent-Worker-ID"] = this.workerId;
      if (run.piSessionLeaseEpoch !== undefined) {
        headers["X-Agent-Session-Epoch"] = String(run.piSessionLeaseEpoch);
      }
    }
    const send = () => fetch(`${this.baseUrl.replace(/\/+$/, "")}/internal-agent${path}`, {
      method, headers, body: body === undefined ? undefined : JSON.stringify(body),
      signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(10000)]) : AbortSignal.timeout(10000),
    });
    const response = this.scheduler && run && !path.endsWith("/renew")
      ? await this.scheduler.step(`${run.userId}:${run.request.canvasId ?? run.runId}`, send, signal) : await send();
    if (!response.ok) {
      let publicMessage = "";
      let publicReason = "";
      try {
        const envelope: unknown = await response.clone().json();
        if (envelope && typeof envelope === "object" && "msg" in envelope && typeof envelope.msg === "string") {
          publicMessage = envelope.msg.replace(/[\r\n\t]+/g, " ").trim().slice(0, 240);
        }
        if (envelope && typeof envelope === "object" && "reason" in envelope && typeof envelope.reason === "string") {
          publicReason = envelope.reason;
        }
      } catch {
        // Keep the status and route when the server returns an empty or non-JSON error.
      }
      const terminalSnapshotPath = run ? `/runs/${encodeURIComponent(run.runId)}` : "";
      if (response.status === 403 && run && path !== terminalSnapshotPath) {
        try {
          const current = await this.request<{ run: PiSnapshot }>("GET", terminalSnapshotPath, undefined, run, signal);
          if (TERMINAL_RUN_STATUSES.has(current.run.status)) throw new CanvasRunTerminated(current.run.status);
        } catch (snapshotError) {
          if (snapshotError instanceof CanvasRunTerminated) throw snapshotError;
        }
      }
      const detail = `Canvas bridge HTTP ${response.status} on ${method} ${path}${publicMessage ? `: ${publicMessage}` : ""}`;
      if (response.status === 403 && publicReason === "agent_lease_lost") throw new CanvasLeaseLost(detail);
      // 确定性错误必须标成致命：server.ts 只对 FatalWorkerError 调 failRun，
      // 否则运行既不会失败也不会被看门狗回收，只会无限重试。
      if (NON_RETRYABLE_BRIDGE_STATUSES.has(response.status)) throw new FatalWorkerError(detail);
      throw new Error(detail);
    }
    const envelope: unknown = await response.json();
    if (!envelope || typeof envelope !== "object" || (envelope as any).code !== 0) {
      throw new Error(`Canvas bridge rejected ${method} ${path}`);
    }
    return (envelope as { data: T }).data;
  }
}
