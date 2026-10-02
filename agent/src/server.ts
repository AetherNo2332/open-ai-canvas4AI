import { hostname } from "node:os";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { CanvasBridge } from "./bridge.js";
import type { ToolSchemaArtifact } from "./tool-disclosure.js";
import { loadHarnessPrompt, runCanvasAgent } from "./runner.js";
import { RuntimeConfigController } from "./runtime-config.js";
import { EventScheduler, RunEvents, runEventSessions } from "./event-scheduler.js";
import { FatalWorkerError } from "./tool-disclosure.js";

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

const scheduler = new EventScheduler(4);
const events = new RunEvents();
const eventBridge = new CanvasBridge(backend, token, `${hostname()}-${process.pid}-events`, scheduler, events);
// Fail closed before any claim when the authoritative configuration is unavailable.
const config=new RuntimeConfigController(await eventBridge.schedulerConfig(controller.signal));
scheduler.setConcurrency(config.current.dispatchConcurrency);
let refreshing=false;
const refresh=async()=>{
  if(refreshing || controller.signal.aborted)return;
  refreshing=true;
  try {
    if(config.apply(await eventBridge.schedulerConfig(controller.signal))) {
      scheduler.setConcurrency(config.current.dispatchConcurrency);
      events.wake("capacity");events.wake("dispatch");
    }
  } catch(error) {if(!controller.signal.aborted)console.error("Agent configuration recovery:",error instanceof Error?error.message:String(error));}
  finally {refreshing=false;}
};
const unsubscribeConfig=events.subscribe("config",()=>{void refresh();});
const configRecovery=setInterval(()=>{void refresh();},5000);
const eventStream = eventBridge.consumeEvents(controller.signal);
await runEventSessions({
  concurrency:config.current.dispatchConcurrency,
  maxSessions:config.current.maxResidentSessions,
  getConfig:()=>config.current,
  events,
  reportState: (active,reserved,capacity) => {
    const report={active,capacity,claimReservations:reserved,dispatchActive:scheduler.active,readyQueued:scheduler.queued,draining:active+reserved>capacity,appliedConfigRevision:config.current.revision};
    if(process.env.CANVAS_AGENT_METRICS === "true") console.log(JSON.stringify({type:"scheduler_capacity",residentSessions:active,schedulerPeak:scheduler.peak,...report}));
    return eventBridge.capacityReport(report,controller.signal);
  },
  signal: controller.signal,
  createBridge: (index) => new CanvasBridge(backend, token,
    `${hostname()}-${process.pid}-${index + 1}`.slice(0, 80), scheduler, events),
  run: async (bridge, run, signal) => {
    try { await runCanvasAgent(bridge, run, signal, harness, toolSchema); }
    catch (error) {
      if (error instanceof FatalWorkerError && !signal.aborted) await bridge.failRun(run, error.message).catch(() => {});
      throw error;
    }
  },
  onError: (workerId, error) => {
    // Leave the lease to expire. The next worker reconciles persisted messages
    // and receipts before it sends another model or canvas operation.
    console.error(`Pi worker ${workerId} run failed:`, error instanceof Error ? error.message : String(error));
  },
});
clearInterval(configRecovery);unsubscribeConfig();
await eventStream;
