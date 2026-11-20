// Launched only by the Go integration test against its ephemeral HTTP server.
import { setTimeout as delay } from "node:timers/promises";
import { CanvasBridge } from "../dist/src/bridge.js";
import { runCanvasAgent } from "../dist/src/runner.js";
import { runLeasedPiSession } from "../dist/src/worker-runtime.js";

const bridge = new CanvasBridge(process.env.AGENT_SDK_INTERNAL_URL, process.env.AGENT_SDK_WORKER_TOKEN,
  process.env.AGENT_SDK_SLOT, undefined, undefined, undefined, "sdk-worker");
const signal = AbortSignal.timeout(480_000);
const config = await bridge.schedulerConfig(signal);
let active = 0;
const reportCapacity = () => bridge.capacityReport({ active, capacity: 1, claimReservations: 0, dispatchActive: active,
  readyQueued: 0, draining: false, appliedConfigRevision: config.revision, readiness: "ready" }, signal);
await reportCapacity();
setInterval(() => { void reportCapacity().catch(error => console.error("capacity report failed", error.message)); }, 10_000).unref();
console.log("READY");
const execute = bridge.executeTool.bind(bridge);
bridge.executeTool = async (...args) => {
  const receipt = await execute(...args);
  if (process.env.AGENT_SDK_CRASH_AFTER_READ === "1") {
    console.log("READ_COMMITTED");
    // The parent terminates this process here: Go has committed the receipt,
    // but Pi has not checkpointed the tool result.
    await new Promise(() => {});
  }
  return receipt;
};
while (!signal.aborted) {
  const run = await bridge.claim(signal);
  if (!run) { await delay(100, undefined, { signal }); continue; }
  console.log("CLAIMED " + run.runId);
  active = 1;
  await runLeasedPiSession(bridge, run, signal, (client, snapshot, shutdown) =>
    runCanvasAgent(client, snapshot, shutdown, { system: "SDK integration worker", context: [] }));
  active = 0;
  console.log("FINISHED " + run.runId);
}
