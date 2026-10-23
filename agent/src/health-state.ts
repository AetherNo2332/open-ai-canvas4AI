import { renameSync, unlinkSync, writeFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";

export type WorkerReadiness = "starting" | "ready" | "degraded" | "draining";
export type HealthChannel = "configuration" | "claim" | "capacity";
interface ChannelCheck { checkedAt: number | null; error: string; }
export interface WorkerHealthSnapshot {
  readiness: WorkerReadiness;
  reason: string;
  updatedAt: number;
  channels: Record<HealthChannel, ChannelCheck>;
  claimRequired: boolean;
  resourceReason?: string;
}

export const HEALTH_MAX_AGE_MS = 45_000;

export function evaluateWorkerHealth(state: WorkerHealthSnapshot, now = Date.now()): { readiness: WorkerReadiness; reason: string } {
  if (state.readiness === "draining") return { readiness: "draining", reason: state.reason };
  for (const channel of ["configuration", "capacity", "claim"] as const) {
    const check = state.channels?.[channel];
    if (!check) return { readiness: "degraded", reason: `${channel} check missing` };
    if (check.error) return { readiness: "degraded", reason: `${channel}: ${check.error}` };
    if (check.checkedAt === null) return { readiness: "starting", reason: `waiting for ${channel} check` };
    if (channel === "claim" && !state.claimRequired) continue;
    if (!Number.isFinite(check.checkedAt) || now - check.checkedAt > HEALTH_MAX_AGE_MS || check.checkedAt > now + 5_000) {
      return { readiness: "degraded", reason: `${channel} check is stale` };
    }
  }
  return { readiness: "ready", reason: state.resourceReason || "" };
}

/** Local snapshot shared with the container health command. Each channel recovers independently. */
export class WorkerHealthState {
  private state: WorkerHealthSnapshot = {
    readiness: "starting", reason: "initializing", updatedAt: Date.now(), claimRequired: true,
    channels: { configuration: { checkedAt: null, error: "" }, claim: { checkedAt: null, error: "" }, capacity: { checkedAt: null, error: "" } },
  };
  private sequence = 0;
  constructor(private readonly path = process.env.CANVAS_AGENT_HEALTH_FILE || "/tmp/canvas-agent-health.json") { this.persist(); }
  get current(): WorkerHealthSnapshot { return { ...this.state, ...evaluateWorkerHealth(this.state) }; }
  succeeded(channel: HealthChannel): void { this.update(channel, { checkedAt: Date.now(), error: "" }); }
  failed(channel: HealthChannel, reason: string): void { this.update(channel, { ...this.state.channels[channel], error: reason.slice(0, 160) }); }
  setClaimRequired(required: boolean): void { if (this.state.claimRequired !== required) { this.state.claimRequired = required; this.persist(); } }
  setResourceReason(reason: string): void { if (this.state.resourceReason !== reason) { this.state.resourceReason = reason; this.persist(); } }
  drain(reason = "shutdown"): void { this.state.readiness = "draining"; this.state.reason = reason; this.persist(); }
  private update(channel: HealthChannel, check: ChannelCheck): void {
    this.state.channels[channel] = check;
    if (this.state.readiness !== "draining") Object.assign(this.state, evaluateWorkerHealth(this.state));
    this.persist();
  }
  private persist(): void {
    this.state.updatedAt = Date.now();
    const temporary = join(dirname(this.path), `.${basename(this.path)}.${process.pid}.${++this.sequence}.tmp`);
    try {
      writeFileSync(temporary, JSON.stringify(this.state), { encoding: "utf8", mode: 0o600 });
      renameSync(temporary, this.path);
    } catch (error) {
      try { unlinkSync(temporary); } catch { /* file may not have been created */ }
      throw error;
    }
  }
}
