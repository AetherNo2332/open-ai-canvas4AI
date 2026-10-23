import { hostname } from "node:os";
import { setTimeout as delay } from "node:timers/promises";
import { CanvasBridge } from "./bridge.js";
import { loadHarnessPrompt, runCanvasAgent } from "./runner.js";
import { RuntimeConfigController } from "./runtime-config.js";
import { EventScheduler, RunEvents, runEventSessions } from "./event-scheduler.js";
import { runLeasedPiSession, workerErrorSummary } from "./worker-runtime.js";
import { WorkerHealthState } from "./health-state.js";
import { validateHarnessArtifacts } from "./bootstrap-check.js";

const health = new WorkerHealthState();
const token = process.env.CANVAS_AGENT_INTERNAL_TOKEN;
const backend = process.env.CANVAS_BACKEND_INTERNAL_URL || "http://backend:8080";
if (!token) { health.set("degraded", "CANVAS_AGENT_INTERNAL_TOKEN is required"); throw new Error("CANVAS_AGENT_INTERNAL_TOKEN is required"); }

const controller = new AbortController();
process.once("SIGTERM", () => { health.set("draining", "shutdown"); controller.abort(); });
process.once("SIGINT", () => { health.set("draining", "shutdown"); controller.abort(); });
// 等价于 Pi 的 PI_CODING_AGENT_DIR：Harness 文件与 SYSTEM.md/APPEND_SYSTEM.md 的发现目录。
const harnessDir = process.env.CANVAS_AGENT_HARNESS_DIR;
if (harnessDir) await validateHarnessArtifacts(harnessDir).catch((error) => { health.set("degraded", workerErrorSummary(error).slice(0, 160)); throw error; });
const harness = await loadHarnessPrompt(harnessDir);
health.set("starting", "harness loaded; loading scheduler configuration");

const scheduler = new EventScheduler(4);
const events = new RunEvents();
const availability={nextProbeAt:0};
const eventBridge = new CanvasBridge(backend, token, `${hostname()}-${process.pid}-events`, scheduler, events,availability);
// Fail closed before any claim when the authoritative configuration is unavailable.
async function loadInitialConfig(): Promise<RuntimeConfigController | undefined> {
  while (!controller.signal.aborted) {
    try {
      return new RuntimeConfigController(await eventBridge.schedulerConfig(controller.signal));
    } catch (error) {
      health.set("degraded", workerErrorSummary(error).slice(0, 160));
      if (!controller.signal.aborted) console.error("Agent startup recovery:", workerErrorSummary(error));
      await delay(5000, undefined, { signal: controller.signal }).catch(() => {});
    }
  }
}
const initialConfig = await loadInitialConfig();
if (!initialConfig) process.exit(0);
const config = initialConfig;
scheduler.setConcurrency(config.current.dispatchConcurrency);
const capacityReport = async (active:number, reserved:number, capacity:number) => {
  const draining = controller.signal.aborted || active + reserved > capacity;
  const readiness = draining ? "draining" : health.current.readiness === "degraded" ? "degraded" : "ready";
  const report = {active,capacity,claimReservations:reserved,dispatchActive:scheduler.active,readyQueued:scheduler.queued,draining,appliedConfigRevision:config.current.revision,readiness,readinessReason:readiness === "degraded" ? health.current.reason : ""} as const;
  try {
    await eventBridge.capacityReport(report,controller.signal);
    health.set(readiness, draining ? "capacity draining" : report.readinessReason);
  } catch (error) {
    if (!controller.signal.aborted) health.set("degraded", workerErrorSummary(error).slice(0, 160));
    throw error;
  }
  if(process.env.CANVAS_AGENT_METRICS === "true") console.log(JSON.stringify({type:"scheduler_capacity",residentSessions:active,schedulerPeak:scheduler.peak,...report}));
};
while (!controller.signal.aborted) {
  try { await capacityReport(0,0,config.current.maxResidentSessions); break; }
  catch (error) {
    console.error("Agent capacity recovery:", workerErrorSummary(error));
    await delay(5000, undefined, { signal: controller.signal }).catch(() => {});
  }
}
if (controller.signal.aborted) process.exit(0);
let refreshing=false;
const refresh=async()=>{
  if(refreshing || controller.signal.aborted)return;
  refreshing=true;
  try {
    if(config.apply(await eventBridge.schedulerConfig(controller.signal))) {
      scheduler.setConcurrency(config.current.dispatchConcurrency);
      events.wake("capacity");events.wake("dispatch");
    }
  } catch(error) {if(!controller.signal.aborted){health.set("degraded",workerErrorSummary(error).slice(0,160));console.error("Agent configuration recovery:",error instanceof Error?error.message:String(error));}}
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
  reportState: capacityReport,
  signal: controller.signal,
  createBridge: (index) => new CanvasBridge(backend, token,
    `${hostname()}-${process.pid}-${index + 1}`.slice(0, 80), scheduler, events,availability),
  run: async (bridge, run, signal) => {
    await runLeasedPiSession(bridge, run, signal, (b, r, s) => runCanvasAgent(b, r, s, harness));
  },
  onError: (workerId, error) => {
    // Leave the lease to expire. The next worker reconciles persisted messages
    // and receipts before it sends another model or canvas operation.
    if(workerId === "claim") health.set("degraded",workerErrorSummary(error).slice(0,160));
    console.error(`Pi worker ${workerId} run failed:`, workerErrorSummary(error));
  },
  onClaimSuccess: () => { if (health.current.readiness === "degraded") { health.set("ready"); events.wake("capacity"); } },
});
clearInterval(configRecovery);unsubscribeConfig();
await eventStream;
