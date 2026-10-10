import http from "node:http";

// Only the model protocol is replaced. Every returned call runs through Pi and Go.
const scenarios = new Map();
const records = [];
const waiting = new Map();
const respond = (res, status, body) => {
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(JSON.stringify(body));
};
const messageText = message => typeof message.content === "string" ? message.content :
  (message.content ?? []).map(part => part.text ?? "").join("");
function expand(value, context) {
  if (value === "$snapshotHash") return context.snapshotHash;
  if (value === "$firstSnapshotHash") return context.firstSnapshotHash;
  if (Array.isArray(value)) return value.map(item => expand(item, context));
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, expand(item, context)]));
  return value;
}
http.createServer(async (req, res) => {
  let raw = "";
  for await (const chunk of req) raw += chunk;
  let body;
  try { body = raw ? JSON.parse(raw) : {}; } catch { return respond(res, 400, { error: "invalid JSON" }); }
  if (req.url === "/control") {
    if (body.scenario) scenarios.set(body.scenario, { steps: body.steps, gateStep: body.gateStep, released: false });
    if (body.release) {
      const scenario = scenarios.get(body.release);
      if (scenario) scenario.released = true;
      waiting.get(body.release)?.();
      waiting.delete(body.release);
    }
    return respond(res, 200, { ok: true });
  }
  if (req.url === "/stats") return respond(res, 200, { requests: records, waiting: [...waiting.keys()] });
  if (req.method === "GET") return respond(res, 200, { object: "list", data: [{ id: "agent-parity-model", object: "model" }] });
  const messages = body.messages ?? [];
  let name;
  for (const message of messages) if (message.role === "user") {
    const match = messageText(message).match(/AGENT_PARITY_SCENARIO:([a-zA-Z0-9_-]+)/);
    if (match) name = match[1];
  }
  const scenario = scenarios.get(name);
  if (!scenario) return respond(res, 400, { error: { message: "unknown acceptance scenario" } });
  const calls = new Map();
  const context = {};
  const executed = new Set();
  const receiptFacts = [];
  for (const message of messages) {
    for (const call of message.tool_calls ?? []) calls.set(call.id, call.function?.name);
    if (message.role !== "tool") continue;
    if (!calls.has(message.tool_call_id)) return respond(res, 400, { error: { message: "tool result has no preceding call" } });
    const prefix = `parity_${name}_`;
    if (!message.tool_call_id.startsWith(prefix)) continue;
    executed.add(Number(message.tool_call_id.slice(prefix.length)));
    let result;
    try { result = JSON.parse(messageText(message)); } catch {
      receiptFacts.push({ step: Number(message.tool_call_id.slice(prefix.length)), tool: calls.get(message.tool_call_id), parsed: false });
      continue;
    }
    receiptFacts.push({ step: Number(message.tool_call_id.slice(prefix.length)), tool: calls.get(message.tool_call_id), parsed: true, resultKeys: Object.keys(result ?? {}), hasSnapshotHash: typeof result?.snapshotHash === "string" });
    if (result.snapshotHash) {
      context.snapshotHash = result.snapshotHash;
      context.sourceStep = Number(message.tool_call_id.slice(prefix.length));
      context.firstSnapshotHash ??= result.snapshotHash;
    }
  }
  let index = 0;
  while (executed.has(index)) index++;
  if (scenario.gateStep === index && !scenario.released) {
    await new Promise(resolve => { waiting.set(name, resolve); res.once("close", resolve); });
    if (res.destroyed) return;
  }
  const step = scenario.steps[index];
  const record = { scenario: name, step: index, tool: step?.tool ?? null, availableTools: (body.tools ?? []).map(item => item.function?.name), snapshotSourceStep: context.sourceStep, receipts: receiptFacts };
  records.push(record);
  const toolCalls = step ? [{ id: `parity_${name}_${index}`, type: "function", function: { name: step.tool, arguments: JSON.stringify(expand(step.args ?? {}, context)) } }] : null;
  respond(res, 200, { id: `stub_${records.length}`, object: "chat.completion", created: Math.floor(Date.now() / 1000), model: body.model,
    choices: [{ index: 0, message: { role: "assistant", content: toolCalls ? null : "Acceptance scenario completed.", ...(toolCalls ? { tool_calls: toolCalls } : {}) }, finish_reason: toolCalls ? "tool_calls" : "stop" }],
    usage: { prompt_tokens: 100, completion_tokens: 30, total_tokens: 130 } });
}).listen(8080, "0.0.0.0");
