import { SessionManager, buildSessionContext } from "@earendil-works/pi-coding-agent";
import { CanvasBridge } from "../dist/src/bridge.js";
import { loadHarnessPrompt, runCanvasAgent } from "../dist/src/runner.js";
import { runLeasedPiSession } from "../dist/src/worker-runtime.js";

const [url, owner, phase] = process.argv.slice(2);
const [identity, slot] = owner.split(":");
const input = [];
for await (const chunk of process.stdin) input.push(chunk);
const { run, runAuthorization } = JSON.parse(Buffer.concat(input).toString("utf8"));
run.runAuthorization = runAuthorization;
const files = [run.piSessionHeader, ...(run.piSessionEntries ?? []).map(item => item.entry)];
const manager = SessionManager.inMemory(process.cwd(), { id: run.piSessionId }, files);
const context = buildSessionContext(manager.getEntries(), run.piActiveLeafId);
if (manager.getHeader()?.id !== run.piSessionId || !Array.isArray(context.messages)) {
  throw new Error("Pi SDK could not decode the claimed session");
}
if (phase === "resume" && !context.messages.some(message => message.role === "toolResult")) {
  throw new Error("Pi SDK did not recover the durable tool result");
}

const bridge = new CanvasBridge(url, identity.repeat(8), slot, undefined, undefined, undefined, identity);
const harness = await loadHarnessPrompt();
const checkpoint = bridge.checkpoint.bind(bridge);
let executions = 0;
const execute = bridge.executeTool.bind(bridge);
bridge.executeTool = async (...args) => {
  executions++;
  if (phase === "resume") throw new Error("checkpointed tool was submitted again");
  return execute(...args);
};
bridge.checkpoint = async (...args) => {
  const reply = await checkpoint(...args);
  if (phase === "checkpoint" && args[2].role === "toolResult") {
    if (executions !== 1) throw new Error("expected exactly one tool invocation");
    console.log("CHECKPOINTED");
    await new Promise(resolve => setTimeout(resolve, 60_000));
    throw new Error("test did not stop the worker at its checkpoint");
  }
  return reply;
};
await runLeasedPiSession(bridge, run, AbortSignal.timeout(45_000), (client, snapshot, shutdown) =>
  runCanvasAgent(client, snapshot, shutdown, harness));
if (phase !== "resume" || executions !== 0) throw new Error("unexpected worker termination");
console.log("FINISHED");
