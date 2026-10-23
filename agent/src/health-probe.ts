import { readFileSync } from "node:fs";
import type { WorkerHealthSnapshot } from "./health-state.js";

try {
  const state = JSON.parse(readFileSync(process.env.CANVAS_AGENT_HEALTH_FILE || "/tmp/canvas-agent-health.json", "utf8")) as WorkerHealthSnapshot;
  if (state.readiness !== "ready" || !Number.isFinite(state.updatedAt) || Date.now() - state.updatedAt > 45_000) process.exitCode = 1;
} catch {
  process.exitCode = 1;
}
