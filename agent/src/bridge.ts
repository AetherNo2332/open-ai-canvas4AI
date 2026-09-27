import { setTimeout as delay } from "node:timers/promises";
import type { CanvasModelResult } from "./pi-stream.js";
import { FatalWorkerError, type CanvasToolSpec } from "./tool-disclosure.js";
import type { PromptParts } from "./system-prompt.js";

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
  protocolVersion: "canvas-pi-wire/v1",
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

export interface PiSnapshot {
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
  request: { prompt: string; model?: string; channelModelKey?: string; visionEnabled?: boolean };
  modelLimits: { contextWindowTokens: number; maxOutputTokens: number; configured: boolean; source: string };
  canonical: PiCanonical;
  activeTaskId?: string;
  lastTaskId?: string;
  noToolTaskId?: string;
  noToolNudge?: string;
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
  taskId: string;
  status: string;
  textDraft?: string;
  result?: CanvasModelResult;
  error?: string;
}

interface PiCheckpointResult {
  saved: boolean;
  sessionRevision?: number;
}

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
}

interface PiToolReceipt {
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
  constructor(private readonly baseUrl: string, private readonly token: string, readonly workerId: string) {
    if (!baseUrl || !token || !workerId) throw new Error("Pi bridge configuration is incomplete");
  }

  async claim(signal?: AbortSignal): Promise<PiSnapshot | null> {
    const result = await this.request<{ run: PiSnapshot | null }>("POST", "/claim", { owner: this.workerId }, undefined, signal);
    return result.run;
  }

  async snapshot(run: PiSnapshot, signal?: AbortSignal): Promise<PiSnapshot> {
    const result = await this.request<{ run: PiSnapshot }>("GET", `/runs/${encodeURIComponent(run.runId)}`, undefined, run, signal);
    return result.run;
  }

  async renew(run: PiSnapshot, signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/renew`, {}, run, signal);
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
    let step = await this.request<PiModelStepView>("POST", path, body, run, signal);
    // 模型任务执行期间 textDraft 会持续增长；把新增部分当增量正文上报，
    // 这样 Pi 事件流是真实增量，而不是任务结束后一次性补齐。
    while (step.status === "queued" || step.status === "running") {
      if (onTextDelta && step.textDraft) onTextDelta(step.textDraft);
      await delay(700, undefined, { signal });
      step = await this.request<PiModelStepView>("GET", `${path}/${encodeURIComponent(step.taskId)}`, undefined, run, signal);
    }
    if (onTextDelta && step.textDraft) onTextDelta(step.textDraft);
    if (step.status !== "succeeded" || !step.result) {
      if (step.status !== "succeeded") await this.request("POST", `${path}/${encodeURIComponent(step.taskId)}/fail`, {}, run, signal);
      throw new Error(`Canvas model step ${step.status}`);
    }
    return { taskId: step.taskId, result: step.result };
  }

  async acknowledgeModel(run: PiSnapshot, taskId: string, signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/model-steps/${encodeURIComponent(taskId)}/ack`, {}, run, signal);
  }

  async checkpoint(run: PiSnapshot, sequence: number, message: Record<string, unknown>, taskId?: string, signal?: AbortSignal,
    session?: { revision: number; activeLeafId: string; entries: Record<string, unknown>[] }): Promise<PiCheckpointResult> {
    return this.request<PiCheckpointResult>("POST", `/runs/${encodeURIComponent(run.runId)}/messages`, {
      sequence, message,
      ...(taskId ? { taskId } : {}),
      ...(session ? { sessionRevision: session.revision, activeLeafId: session.activeLeafId, sessionEntries: session.entries } : {}),
    }, run, signal);
  }

  async compactContext(run: PiSnapshot, request: { sessionRevision: number; activeLeafId: string; reason: string;
    willRetry: boolean; tokensBefore: number }, signal?: AbortSignal): Promise<PiContextCompactionView> {
    const path = `/runs/${encodeURIComponent(run.runId)}/context-compactions`;
    let operation = await this.request<PiContextCompactionView>("POST", path, request, run, signal);
    while (operation.status === "queued" || operation.status === "running") {
      await delay(700, undefined, { signal });
      operation = await this.request<PiContextCompactionView>("GET",
        `${path}/${encodeURIComponent(operation.operationId)}`, undefined, run, signal);
    }
    if (operation.status !== "succeeded" || !operation.summary || !operation.firstKeptEntryId || !operation.details) {
      throw new FatalWorkerError(`Go context compaction did not return a checkpoint (${operation.status})`);
    }
    return operation;
  }

  async resumeContextCompaction(run: PiSnapshot, operationId: string, signal?: AbortSignal): Promise<PiContextCompactionView> {
    const path = `/runs/${encodeURIComponent(run.runId)}/context-compactions/${encodeURIComponent(operationId)}`;
    let operation = await this.request<PiContextCompactionView>("GET", path, undefined, run, signal);
    while (operation.status === "queued" || operation.status === "running") {
      await delay(700, undefined, { signal });
      operation = await this.request<PiContextCompactionView>("GET", path, undefined, run, signal);
    }
    if (operation.operationId !== operationId || operation.status !== "succeeded" || !operation.summary ||
        !operation.firstKeptEntryId || !operation.details) {
      throw new FatalWorkerError(`Go context compaction could not be resumed (${operation.status})`);
    }
    return operation;
  }

  async commitContextCompaction(run: PiSnapshot, operationId: string, sessionRevision: number,
    entry: Record<string, unknown>, signal?: AbortSignal): Promise<{ committed: boolean; sessionRevision: number }> {
    return this.request("POST", `/runs/${encodeURIComponent(run.runId)}/context-compactions/${encodeURIComponent(operationId)}/commit`,
      { sessionRevision, entry }, run, signal);
  }

  async startToolBatch(run: PiSnapshot, calls: PiToolCall[], signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/tool-batches`, { calls }, run, signal);
  }

  async executeTool(run: PiSnapshot, callId: string, signal?: AbortSignal): Promise<PiToolReceipt> {
    const path = `/runs/${encodeURIComponent(run.runId)}/tool-calls/${encodeURIComponent(callId)}/advance`;
    for (;;) {
      const receipt = await this.request<PiToolReceipt>("POST", path, {}, run, signal);
      // 终态即返回：拒绝/取消等控制面决策按合同不产生工具结果，
      // 无限轮询会占死 worker 并持续续租。
      if (!receipt.pending || receipt.terminated) return receipt;
      await delay(900, undefined, { signal });
    }
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
    const response = await fetch(`${this.baseUrl.replace(/\/+$/, "")}/internal-agent${path}`, {
      method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal,
    });
    if (!response.ok) {
      const detail = `Canvas bridge HTTP ${response.status} on ${method} ${path}`;
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
