import { hostname } from "node:os";
import { setTimeout as delay } from "node:timers/promises";
import { CanvasBridge } from "./bridge.js";
import { runCanvasAgent } from "./runner.js";

const token = process.env.CANVAS_AGENT_INTERNAL_TOKEN;
const backend = process.env.CANVAS_BACKEND_INTERNAL_URL || "http://backend:8080";
if (!token) throw new Error("CANVAS_AGENT_INTERNAL_TOKEN is required");

const controller = new AbortController();
process.once("SIGTERM", () => controller.abort());
process.once("SIGINT", () => controller.abort());
const bridge = new CanvasBridge(backend, token, `${hostname()}-${process.pid}`.slice(0, 80));

while (!controller.signal.aborted) {
  try {
    const run = await bridge.claim(controller.signal);
    if (run) {
      await runCanvasAgent(bridge, run);
      continue;
    }
    await delay(1000, undefined, { signal: controller.signal });
  } catch (error) {
    if (controller.signal.aborted) break;
    // Leave the lease to expire. The next worker reconciles persisted messages
    // and receipts before it sends another model or canvas operation.
    console.error("Pi worker run failed:", error instanceof Error ? error.message : String(error));
    await delay(3000, undefined, { signal: controller.signal }).catch(() => {});
  }
}
