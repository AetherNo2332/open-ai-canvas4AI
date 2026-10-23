import { hostname } from "node:os";
import { monitorEventLoopDelay } from "node:perf_hooks";
import { setTimeout as delay } from "node:timers/promises";
import { CanvasBridge } from "./bridge.js";
import { loadHarnessPrompt, runCanvasAgent } from "./runner.js";
import { RuntimeConfigController } from "./runtime-config.js";
import { EventScheduler, RunEvents, runEventSessions } from "./event-scheduler.js";
import { runLeasedPiSession, workerErrorSummary } from "./worker-runtime.js";
import { WorkerHealthState } from "./health-state.js";
import { validateHarnessArtifacts } from "./bootstrap-check.js";
import { resourceBudgetFromEnv } from "./resource-budget.js";

const health = new WorkerHealthState();
const token = process.env.CANVAS_AGENT_WORKER_TOKEN;
const identity = process.env.CANVAS_AGENT_WORKER_ID;
const backend = process.env.CANVAS_BACKEND_INTERNAL_URL || "http://backend:8080";
if (!token || !identity) { health.failed("configuration", "CANVAS_AGENT_WORKER_ID and CANVAS_AGENT_WORKER_TOKEN are required"); throw new Error("CANVAS_AGENT_WORKER_ID and CANVAS_AGENT_WORKER_TOKEN are required"); }
const budget = resourceBudgetFromEnv();

const controller = new AbortController();
let reportDrain: (() => Promise<void>) | undefined;
let shutdownReport: Promise<void> = Promise.resolve();
const stop = () => {
  health.drain();
  shutdownReport = reportDrain?.() ?? Promise.resolve();
  controller.abort();
};
process.once("SIGTERM", stop);
process.once("SIGINT", stop);
// 等价于 Pi 的 PI_CODING_AGENT_DIR：Harness 文件与 SYSTEM.md/APPEND_SYSTEM.md 的发现目录。
const harnessDir = process.env.CANVAS_AGENT_HARNESS_DIR;
if (harnessDir) await validateHarnessArtifacts(harnessDir).catch((error) => { health.failed("configuration", workerErrorSummary(error)); throw error; });
const harness = await loadHarnessPrompt(harnessDir).catch((error) => { health.failed("configuration", workerErrorSummary(error)); throw error; });

const scheduler = new EventScheduler(4);
const events = new RunEvents();
const availability={nextProbeAt:0};
const eventBridge = new CanvasBridge(backend, token, `${hostname()}-${process.pid}-events`, scheduler, events,availability,identity);
// Fail closed before any claim when the authoritative configuration is unavailable.
async function loadInitialConfig(): Promise<RuntimeConfigController | undefined> {
  while (!controller.signal.aborted) {
    try {
      const initial = new RuntimeConfigController(await eventBridge.schedulerConfig(controller.signal));
      health.succeeded("configuration");
      return initial;
    } catch (error) {
      health.failed("configuration", workerErrorSummary(error));
      if (!controller.signal.aborted) console.error("Agent startup recovery:", workerErrorSummary(error));
      await delay(5000, undefined, { signal: controller.signal }).catch(() => {});
    }
  }
}
const initialConfig = await loadInitialConfig();
if (!initialConfig) { await shutdownReport; process.exit(0); }
const config = initialConfig;
scheduler.setConcurrency(Math.min(config.current.dispatchConcurrency, budget.maxResidentSessions));
const localConfig = () => ({ ...config.current, dispatchConcurrency: Math.min(config.current.dispatchConcurrency, budget.maxResidentSessions),
  maxResidentSessions: Math.min(config.current.maxResidentSessions, budget.maxResidentSessions) });
const eventLoop = monitorEventLoopDelay({ resolution: 20 });
eventLoop.enable();
let reportResourceChange = () => {};
const sampleResources = () => {
  const changed = budget.observe({ rssBytes: process.memoryUsage().rss, eventLoopLagMs: eventLoop.max / 1e6 });
  eventLoop.reset();
  health.setResourceReason(budget.reason);
  if (changed) { events.wake("capacity"); reportResourceChange(); }
  if (process.env.CANVAS_AGENT_METRICS === "true") console.log(JSON.stringify({ type: "agent_resources", rssBytes: budget.sample.rssBytes,
    memoryBudgetBytes: budget.memoryBudgetBytes, eventLoopLagMs: budget.sample.eventLoopLagMs,
    eventLoopLimitMs: budget.eventLoopLimitMs, residentLimit: budget.maxResidentSessions,
    admissionPaused: budget.pressured, reason: budget.reason }));
};
sampleResources();
const resourceSampler = setInterval(sampleResources, 5000);
let lastActivity = { active: 0, reserved: 0, capacity: localConfig().maxResidentSessions };
reportDrain = async () => {
  const { active, reserved, capacity } = lastActivity;
  try {
    await eventBridge.capacityReport({ active, capacity, claimReservations: reserved, dispatchActive: scheduler.active,
      readyQueued: scheduler.queued, draining: true, appliedConfigRevision: config.current.revision,
      readiness: "draining", readinessReason: "shutdown" }, AbortSignal.timeout(2000));
  } catch (error) { console.error("Agent drain status (local only):", workerErrorSummary(error)); }
};
const capacityReport = async (active:number, reserved:number, capacity:number) => {
  lastActivity = { active, reserved, capacity };
  health.setClaimRequired(!budget.pressured && active + reserved < capacity);
  const draining = controller.signal.aborted || budget.pressured || active + reserved > capacity;
  const status = health.current;
  const readiness = draining ? "draining" : status.readiness;
  const report = {active,capacity,claimReservations:reserved,dispatchActive:scheduler.active,readyQueued:scheduler.queued,draining,appliedConfigRevision:config.current.revision,readiness,readinessReason:budget.reason || (draining ? "capacity draining" : status.reason)} as const;
  try {
    await eventBridge.capacityReport(report,controller.signal);
    health.succeeded("capacity");
  } catch (error) {
    if (!controller.signal.aborted) health.failed("capacity", workerErrorSummary(error));
    throw error;
  }
  if(process.env.CANVAS_AGENT_METRICS === "true") console.log(JSON.stringify({type:"scheduler_capacity",residentSessions:active,schedulerPeak:scheduler.peak,...report}));
};
reportResourceChange = () => { void capacityReport(lastActivity.active,lastActivity.reserved,lastActivity.capacity).catch(() => {}); };
while (!controller.signal.aborted) {
  try { await capacityReport(0,0,localConfig().maxResidentSessions); break; }
  catch (error) {
    console.error("Agent capacity recovery:", workerErrorSummary(error));
    await delay(5000, undefined, { signal: controller.signal }).catch(() => {});
  }
}
if (controller.signal.aborted) { await shutdownReport; process.exit(0); }
let refreshing=false;
const refresh=async()=>{
  if(refreshing || controller.signal.aborted)return;
  refreshing=true;
  try {
    if(config.apply(await eventBridge.schedulerConfig(controller.signal))) {
      scheduler.setConcurrency(localConfig().dispatchConcurrency);
      events.wake("capacity");events.wake("dispatch");
    }
    health.succeeded("configuration"); events.wake("capacity");
  } catch(error) {if(!controller.signal.aborted){health.failed("configuration",workerErrorSummary(error));events.wake("capacity");console.error("Agent configuration recovery:",error instanceof Error?error.message:String(error));}}
  finally {refreshing=false;}
};
const unsubscribeConfig=events.subscribe("config",()=>{void refresh();});
const configRecovery=setInterval(()=>{void refresh();},5000);
const eventStream = eventBridge.consumeEvents(controller.signal);
await runEventSessions({
  concurrency:localConfig().dispatchConcurrency,
  maxSessions:localConfig().maxResidentSessions,
  getConfig:localConfig,
  canClaim:(active,reserved)=>budget.canClaim(active,reserved),
  events,
  reportState: capacityReport,
  signal: controller.signal,
  createBridge: (index) => new CanvasBridge(backend, token,
    `${hostname()}-${process.pid}-${index + 1}`.slice(0, 80), scheduler, events,availability,identity),
  run: async (bridge, run, signal) => {
    await runLeasedPiSession(bridge, run, signal, (b, r, s) => runCanvasAgent(b, r, s, harness));
  },
  onError: (workerId, error) => {
    // Leave the lease to expire. The next worker reconciles persisted messages
    // and receipts before it sends another model or canvas operation.
    if(workerId === "claim") { health.failed("claim",workerErrorSummary(error)); events.wake("capacity"); }
    console.error(`Pi worker ${workerId} run failed:`, workerErrorSummary(error));
  },
  onClaimSuccess: () => { health.succeeded("claim"); events.wake("capacity"); },
});
clearInterval(configRecovery);clearInterval(resourceSampler);eventLoop.disable();unsubscribeConfig();
await eventStream;
await shutdownReport;
