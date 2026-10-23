import { readFileSync } from "node:fs";
import { evaluateWorkerHealth, type WorkerHealthSnapshot } from "./health-state.js";

try {
  const state = JSON.parse(readFileSync(process.env.CANVAS_AGENT_HEALTH_FILE || "/tmp/canvas-agent-health.json", "utf8")) as WorkerHealthSnapshot;
  if (evaluateWorkerHealth(state).readiness !== "ready") process.exitCode = 1;
} catch {
  process.exitCode = 1;
}
