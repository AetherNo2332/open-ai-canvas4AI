import { writeFileSync } from "node:fs";

export type WorkerReadiness = "starting" | "ready" | "degraded" | "draining";
export interface WorkerHealthSnapshot {
  readiness: WorkerReadiness;
  reason: string;
  updatedAt: number;
}

/** Local-only snapshot shared with the container health command. */
export class WorkerHealthState {
  private state: WorkerHealthSnapshot = { readiness: "starting", reason: "initializing", updatedAt: Date.now() };
  constructor(private readonly path = process.env.CANVAS_AGENT_HEALTH_FILE || "/tmp/canvas-agent-health.json") {
    this.persist();
  }
  get current(): WorkerHealthSnapshot { return this.state; }
  set(readiness: WorkerReadiness, reason = ""): void {
    this.state = { readiness, reason, updatedAt: Date.now() };
    this.persist();
  }
  private persist(): void {
    writeFileSync(this.path, JSON.stringify(this.state), { encoding: "utf8", mode: 0o600 });
  }
}
