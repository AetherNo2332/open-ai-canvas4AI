import { setTimeout as delay } from "node:timers/promises";
import type { CanvasModelResult } from "./pi-stream.js";
import type { CanvasToolSpec } from "./tool-disclosure.js";

export interface PiCanonical {
  systemPrompt: string;
  messages: Record<string, unknown>[];
  tools: Record<string, unknown>[];
  toolChoice: "auto" | "required";
  promptCacheKey?: string;
}

export interface PiSnapshot {
  runId: string;
  userId: string;
  revision: number;
  status: string;
  request: { prompt: string; model?: string; channelModelKey?: string; visionEnabled?: boolean };
  canonical: PiCanonical;
  activeTaskId?: string;
  lastTaskId?: string;
  noToolTaskId?: string;
  noToolNudge?: string;
  previousStepTemplate?: string;
  tools: CanvasToolSpec[];
  piMessages?: Record<string, unknown>[];
  openedCategories?: string[];
}

interface PiModelStepView {
  taskId: string;
  status: string;
  textDraft?: string;
  result?: CanvasModelResult;
  error?: string;
}

interface PiToolReceipt {
  callId: string;
  pending: boolean;
  result?: unknown;
  isError?: boolean;
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

  async modelStep(run: PiSnapshot, canonical: PiCanonical, signal?: AbortSignal): Promise<{ taskId: string; result: CanvasModelResult }> {
    const path = `/runs/${encodeURIComponent(run.runId)}/model-steps`;
    let step = await this.request<PiModelStepView>("POST", path, { canonical }, run, signal);
    while (step.status === "queued" || step.status === "running") {
      await delay(700, undefined, { signal });
      step = await this.request<PiModelStepView>("GET", `${path}/${encodeURIComponent(step.taskId)}`, undefined, run, signal);
    }
    if (step.status !== "succeeded" || !step.result) {
      if (step.status !== "succeeded") await this.request("POST", `${path}/${encodeURIComponent(step.taskId)}/fail`, {}, run, signal);
      throw new Error(`Canvas model step ${step.status}`);
    }
    return { taskId: step.taskId, result: step.result };
  }

  async acknowledgeModel(run: PiSnapshot, taskId: string, signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/model-steps/${encodeURIComponent(taskId)}/ack`, {}, run, signal);
  }

  async checkpoint(run: PiSnapshot, sequence: number, message: Record<string, unknown>, taskId?: string, signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/messages`, { sequence, message, ...(taskId ? { taskId } : {}) }, run, signal);
  }

  async startToolBatch(run: PiSnapshot, calls: PiToolCall[], signal?: AbortSignal): Promise<void> {
    await this.request("POST", `/runs/${encodeURIComponent(run.runId)}/tool-batches`, { calls }, run, signal);
  }

  async executeTool(run: PiSnapshot, callId: string, signal?: AbortSignal): Promise<PiToolReceipt> {
    const path = `/runs/${encodeURIComponent(run.runId)}/tool-calls/${encodeURIComponent(callId)}/advance`;
    for (;;) {
      const receipt = await this.request<PiToolReceipt>("POST", path, {}, run, signal);
      if (!receipt.pending) return receipt;
      await delay(900, undefined, { signal });
    }
  }

  async noToolTurn(run: PiSnapshot, taskId: string, signal?: AbortSignal): Promise<{ status: string; nudge?: string }> {
    return this.request("POST", `/runs/${encodeURIComponent(run.runId)}/no-tool-turn`, { taskId }, run, signal);
  }

  private async request<T>(method: string, path: string, body?: unknown, run?: PiSnapshot, signal?: AbortSignal): Promise<T> {
    const headers: Record<string, string> = { Authorization: `Bearer ${this.token}`, Accept: "application/json" };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (run) {
      headers["X-Agent-User-ID"] = run.userId;
      headers["X-Agent-Worker-ID"] = this.workerId;
    }
    const response = await fetch(`${this.baseUrl.replace(/\/+$/, "")}/internal-agent${path}`, {
      method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal,
    });
    if (!response.ok) throw new Error(`Canvas bridge HTTP ${response.status} on ${method} ${path}`);
    const envelope: unknown = await response.json();
    if (!envelope || typeof envelope !== "object" || (envelope as any).code !== 0) {
      throw new Error(`Canvas bridge rejected ${method} ${path}`);
    }
    return (envelope as { data: T }).data;
  }
}
