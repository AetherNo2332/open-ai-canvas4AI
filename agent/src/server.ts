import { hostname } from "node:os";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { CanvasBridge } from "./bridge.js";
import type { ToolSchemaArtifact } from "./tool-disclosure.js";
import { loadHarnessPrompt, runCanvasAgent } from "./runner.js";
import { parsePiWorkerConcurrency, runPiWorkerPool } from "./worker-pool.js";

const token = process.env.CANVAS_AGENT_INTERNAL_TOKEN;
const backend = process.env.CANVAS_BACKEND_INTERNAL_URL || "http://backend:8080";
if (!token) throw new Error("CANVAS_AGENT_INTERNAL_TOKEN is required");

const controller = new AbortController();
process.once("SIGTERM", () => controller.abort());
process.once("SIGINT", () => controller.abort());
// 等价于 Pi 的 PI_CODING_AGENT_DIR：Harness 文件与 SYSTEM.md/APPEND_SYSTEM.md 的发现目录。
const harnessDir = process.env.CANVAS_AGENT_HARNESS_DIR;
const harness = await loadHarnessPrompt(harnessDir);
// 共用工具 schema 制品在启动时读一次：与服务端快照的漂移在启动阶段就暴露。
let toolSchema: ToolSchemaArtifact | undefined;
if (harnessDir) {
  try {
    toolSchema = JSON.parse(await readFile(join(harnessDir, "TOOL_SCHEMA.json"), "utf8")) as ToolSchemaArtifact;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
  }
}

const concurrency = parsePiWorkerConcurrency(process.env.CANVAS_AGENT_CONCURRENCY);
await runPiWorkerPool({
  concurrency,
  signal: controller.signal,
  createBridge: (index) => new CanvasBridge(backend, token,
    `${hostname()}-${process.pid}-${index + 1}`.slice(0, 80)),
  run: (bridge, run, signal) => runCanvasAgent(bridge as CanvasBridge, run, signal, harness, toolSchema),
  onError: (workerId, error) => {
    // Leave the lease to expire. The next worker reconciles persisted messages
    // and receipts before it sends another model or canvas operation.
    console.error(`Pi worker ${workerId} run failed:`, error instanceof Error ? error.message : String(error));
  },
});
