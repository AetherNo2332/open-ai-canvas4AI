import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { CanvasModelRetry, CanvasRunTerminated, CanvasBridge, type PiCanonical, type PiSnapshot, type PiToolCall } from "../src/bridge.js";
import type { CanvasModelResult } from "../src/pi-stream.js";
import type { CanvasToolSpec } from "../src/tool-disclosure.js";
import { assembleSystemPrompt, canvasModel, fromCanonical, runCanvasAgent, withServerPolicy } from "../src/runner.js";
import type { PromptParts } from "../src/system-prompt.js";
import { sessionEntriesFromMessages } from "../src/session-tools.js";
import { createHash } from "node:crypto";

const objectSchema = { type: "object", properties: {}, required: [], additionalProperties: false };
const tools: CanvasToolSpec[] = [
  { name: "agent_tools_canvas_read", description: "Read the canvas", parameters: objectSchema, allowed: true },
  { name: "canvas_get_state", category: "agent_tools_canvas_read", description: "Get state", parameters: objectSchema, allowed: true },
];

test("runner uses the current frozen prompt instead of a persisted Pi prompt", () => {
  const source = readFileSync(join(process.cwd(), "src/runner.ts"), "utf8");
  assert.doesNotMatch(source, /session\?\.agent\.state\.systemPrompt \|\| session\?\.systemPrompt \|\| systemPrompt/);
  assert.doesNotMatch(source, /session\.agent\.state\.systemPrompt \|\| session\.systemPrompt \|\| systemPrompt/);
});

test("withServerPolicy restores the frozen server policy for a Pi stream canonical request", () => {
  const canonical: PiCanonical = { systemPrompt: "", messages: [], tools: [], toolChoice: "auto" };
  const restored = withServerPolicy(canonical, "SERVER POLICY: 影策画布助手。", "SERVER POLICY: 影策画布助手。\nHarness rules");
  assert.match(restored.systemPrompt, /SERVER POLICY: 影策画布助手。/);
  assert.match(restored.systemPrompt, /Harness rules/);
});

function promptParts(): PromptParts {
  return { system: "House style: reply in Chinese.", appendSystem: undefined,
    context: [{ name: "AGENT_HARNESS.md", text: "Harness rules: never invent ids." }] };
}

interface FakeState {
  steps: number;
  checkpoints: { sequence: number; role: string; taskId?: string; text: string; isError?: boolean; interjectionIds?: string[] }[];
  batches: PiToolCall[][];
  executions: string[];
  /**
   * ops 把检查点、批次准入与工具执行按**真实先后**记在同一个数组里。
   *
   * 分开的数组只能证明"都发生过"，证明不了顺序；而"assistant 检查点先于批次准入"正是
   * Go 侧用 ActiveTaskID 校验的顺序合同，必须能在这里断言。
   */
  ops: string[];
  canonical: PiCanonical[];
  status: string;
  noToolTurns: string[];
  contextCompactions: string[];
}

/** 假 Go Bridge：只保留跨进程合同的可见行为（模型步骤、检查点、批次、回执、终态）。 */
function fakeBridge(replies: (CanvasModelResult | (() => Promise<CanvasModelResult>))[], options: {
  status?: string; failCheckpoint?: boolean; receiptText?: string;
  pendingContextCompaction?: PiSnapshot["pendingContextCompaction"];
  pendingInterjections?: PiSnapshot["pendingInterjections"];
  interjectAfterTool?: PiSnapshot["pendingInterjections"];
  piSessionEntries?: PiSnapshot["piSessionEntries"];
  piActiveLeafId?: string;
  firstKeptEntryId?: string;
} = {}): { bridge: CanvasBridge; state: FakeState; snapshot: PiSnapshot } {
  const state: FakeState = { steps: 0, checkpoints: [], batches: [], executions: [], ops: [],
    canonical: [], status: options.status || "running", noToolTurns: [], contextCompactions: [] };
  const pending = [...replies];
  const snapshot: PiSnapshot = {
    runId: "run-1", userId: "user-1", revision: 1, status: "running",
    request: { prompt: "请给主角换一身衣服", model: "canvas-model", channelModelKey: "canvas-model" },
    modelLimits: { contextWindowTokens: 64_000, maxOutputTokens: 8_192, configured: true, source: "channel-model" },
    canonical: { systemPrompt: "SERVER POLICY: 影策画布助手。", messages: [{ role: "user", content: "请给主角换一身衣服" }],
      tools: [], toolChoice: "auto" },
    tools, piMessages: [],
  };
  if (options.pendingContextCompaction) snapshot.pendingContextCompaction = options.pendingContextCompaction;
  if (options.pendingInterjections) snapshot.pendingInterjections = options.pendingInterjections;
  if (options.piSessionEntries) snapshot.piSessionEntries = options.piSessionEntries;
  if (options.piActiveLeafId) snapshot.piActiveLeafId = options.piActiveLeafId;
  const bridge = {
    async modelStep(_run: PiSnapshot, canonical: PiCanonical, signal?: AbortSignal, onTextDelta?: (delta: string) => void,
      harnessHash?: string, harness?: PromptParts) {
      void onTextDelta;
      state.steps += 1;
      state.canonical.push(canonical);
      const next = pending.shift();
      if (!next) throw new Error("unexpected extra model step");
      if (typeof next === "function") return { taskId: `task-${state.steps}`, result: await next() };
      // 服务端会在首个模型步固化提示合同；这里断言 runner 确实提交了正文与哈希。
      if (state.steps === 1) {
        assert.ok(harnessHash, "首个模型步必须带提示合同哈希");
        assert.ok(harness && assembleSystemPrompt(harness, "SERVER POLICY: 影策画布助手。").includes("Harness rules"));
      }
      return { taskId: `task-${state.steps}`, result: next };
    },
    async checkpoint(_run: PiSnapshot, sequence: number, message: Record<string, unknown>, taskId?: string,
      _signal?: AbortSignal, session?: { interjectionIds?: string[] }) {
      if (options.failCheckpoint) throw new Error("injected checkpoint failure");
      const content = message.content;
      const text = typeof content === "string" ? content
        : Array.isArray(content) ? content.map((part: any) => part?.text ?? "").join("") : "";
      state.checkpoints.push({ sequence, role: String(message.role), taskId, text, isError: Boolean(message.isError), interjectionIds: session?.interjectionIds });
      state.ops.push(`checkpoint:${String(message.role)}`);
      if (taskId) snapshot.activeTaskId = undefined;
      if (session?.interjectionIds?.length) {
        const delivered = new Set(session.interjectionIds);
        snapshot.pendingInterjections = (snapshot.pendingInterjections || []).filter(({ id }) => !delivered.has(id));
      }
    },
    async startToolBatch(_run: PiSnapshot, taskId: string, calls: PiToolCall[]) {
      state.batches.push(calls);
      state.ops.push(`batch:${taskId}`);
      snapshot.activeTaskId = undefined;
      snapshot.lastTaskId = taskId;
    },
    async executeTool(_run: PiSnapshot, taskId: string, callId: string) {
      state.executions.push(callId);
      state.ops.push(`execute:${taskId}:${callId}`);
      if (options.interjectAfterTool) snapshot.pendingInterjections = options.interjectAfterTool;
      return { callId, pending: false, result: options.receiptText ?? "已写入画布" };
    },
    async snapshot() { return { ...snapshot, status: state.status, activeTaskId: undefined,
      canonical: { ...snapshot.canonical, messages: [...snapshot.canonical.messages] } }; },
    async renew() {},
    async noToolTurn(_run: PiSnapshot, taskId: string) {
      state.noToolTurns.push(taskId);
      state.status = "completed";
      snapshot.status = "completed";
      return { status: "completed" };
    },
    async compactContext() { throw new Error("recovery must not start another compaction task"); },
    async resumeContextCompaction(_run: PiSnapshot, operationId: string) {
      state.contextCompactions.push(`resume:${operationId}`);
      return { operationId, status: "succeeded", summary: "<agent-context-checkpoint/>",
        firstKeptEntryId: options.firstKeptEntryId, tokensBefore: 40_000,
        details: { protocolVersion: "canvas-pi-compaction/v1", operationId } };
    },
    async commitContextCompaction(_run: PiSnapshot, operationId: string, revision: number, entry: Record<string, unknown>) {
      state.contextCompactions.push(`commit:${operationId}:${revision}`);
      snapshot.pendingContextCompaction = undefined;
      snapshot.piSessionRevision = revision + 1;
      snapshot.piActiveLeafId = String(entry.id || "");
      return { committed: true, sessionRevision: revision + 1 };
    },
    async failRun() {},
  } as unknown as CanvasBridge;
  return { bridge, state, snapshot };
}

test("Go model recovery continues through a persisted Pi user message", async () => {
  const nudge = "上一步模型调用超时；已关闭思考。请重试同一步。";
  const { bridge, state, snapshot } = fakeBridge([
    async () => { throw new CanvasModelRetry({ status: "continue", nudge }); },
    { text: "已完成" },
  ]);
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  assert.equal(state.steps, 2);
  assert.equal(state.status, "completed");
  assert.deepEqual(state.noToolTurns, ["task-2"]);
  const retry = state.checkpoints.find(({ role, text }) => role === "user" && text === nudge);
  assert.ok(retry, "recovery prompt must be durable before the second model request");
  assert.match(JSON.stringify(state.canonical[1]?.messages), /上一步模型调用超时/);
  assert.equal(state.checkpoints.filter(({ role }) => role === "assistant").length, 1);
});

test("Pi sends queued Go interjections in the next user message and checkpoints their delivery", async () => {
  const { bridge, state, snapshot } = fakeBridge([{ text: "我会保留当前构图" }], {
    pendingInterjections: [{ id: "interjection-1", text: "先不要改构图" }],
  });
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  const request = state.canonical[0];
  assert.ok(request, "must issue the model request");
  assert.match(JSON.stringify(request.messages), /【用户插话】先不要改构图/);
  const delivered = state.checkpoints.find(({ role, interjectionIds }) =>
    role === "user" && interjectionIds?.includes("interjection-1"));
  assert.ok(delivered, "the Go delivery id must accompany the durable user checkpoint");
  assert.match(delivered.text, /【用户插话】先不要改构图/);
});

test("Pi steers a newly queued Go interjection into the live run before its next model step", async () => {
  const { bridge, state } = fakeBridge([
    { toolCalls: [{ id: "call-1", function: { name: "canvas_get_state", arguments: "{}" } }] },
    { text: "收到，继续保留构图" },
  ], {
    interjectAfterTool: [{ id: "interjection-live", text: "不要改变镜头位置" }],
  });
  await runCanvasAgent(bridge, snapshotFor(), undefined, promptParts());
  assert.equal(state.steps, 2);
  assert.match(JSON.stringify(state.canonical[1]?.messages), /【用户插话】不要改变镜头位置/);
  assert.ok(state.checkpoints.some(({ role, interjectionIds }) =>
    role === "user" && interjectionIds?.includes("interjection-live")),
  "live steering must be acknowledged by its Pi user checkpoint");
});

test("首个 system 由服务端策略与 Harness 正文组成，模型只看到画布工具", async () => {
  const { bridge, state } = fakeBridge([{ text: "好" }]);
  await runCanvasAgent(bridge, snapshotFor(), undefined, promptParts());
  const canonical = state.canonical[0];
  assert.ok(canonical, "必须发出模型请求");
  // 系统提示 = 服务端策略 + 仓库 Harness，且策略在前（服务端强制层不可被覆盖）。
  assert.ok(canonical.systemPrompt.startsWith("SERVER POLICY: 影策画布助手。"), canonical.systemPrompt);
  assert.ok(canonical.systemPrompt.includes("House style: reply in Chinese."));
  assert.ok(canonical.systemPrompt.includes("Harness rules: never invent ids."));
  // 工具声明只含画布工具：不能把 Pi 默认 coding 工具带给上游。
  const names = canonical.tools.map((tool) => (tool.function as { name: string }).name);
  assert.deepEqual(names.sort(), ["canvas_get_state"]);
  for (const forbidden of ["read", "write", "edit", "bash", "grep", "find", "ls", "powershell"]) {
    assert.equal(names.includes(forbidden), false, `默认工具 ${forbidden} 不该出现在模型请求里`);
  }
  // 用户消息必须是 canonical 里的原话，不能被拼进系统提示。
  assert.equal(canonical.messages.at(-1)?.content, "请给主角换一身衣服");
});

test("native Skill read checkpoints without a canvas tool batch", async () => {
  const entry = '---\nname: "skill-abc"\ndescription: "For scripts"\n---\n# Body\n';
  const hash = createHash("sha256").update(entry).digest("hex");
  const { bridge, state, snapshot } = fakeBridge([
    async () => {
      const location = state.canonical[0]!.systemPrompt.match(/<location>([^<]+)<\/location>/)?.[1];
      assert.ok(location);
      return { toolCalls: [{ id: "read-1", function: { name: "read", arguments: JSON.stringify({ path: location }) } }] };
    },
    { text: "Used Skill" },
  ]);
  snapshot.skillRuntimeMode = "pi-native";
  snapshot.skills = [{ id: "skill-1", nativeName: "skill-abc", displayName: "Scripts", description: "For scripts",
    versionId: "v1", version: "1", contentHash: "package-hash", entryPath: "SKILL.md", entryContent: entry,
    files: [{ path: "SKILL.md", sha256: hash, size: Buffer.byteLength(entry), text: true }] }];
  snapshot.tools = [{ name: "read", category: "native_skill", description: "Read selected Skill text", allowed: true,
    parameters: { type: "object", properties: { path: { type: "string" } }, required: ["path"], additionalProperties: false } }];
  authorizeNativeEntry(bridge, snapshot);
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  assert.equal(state.steps, 2);
  assert.deepEqual(state.batches, []);
  assert.ok(state.ops.indexOf("checkpoint:assistant") < state.ops.indexOf("checkpoint:toolResult"));
  assert.match(state.canonical[0]!.systemPrompt, /available_skills/);
  assert.ok(state.canonical[0]!.tools.some((tool) => (tool.function as { name: string }).name === "read"));
});

test("native recovery uses the durable Pi branch and pairs pending reads and mixed calls", async (t) => {
  for (const mode of ["read-only", "mixed", "read-completed", "rejected-read"]) await t.test(mode, async () => {
  const entry = '---\nname: "skill-abc"\ndescription: "For scripts"\n---\n# Recovered Skill\n';
  const { bridge, state, snapshot } = fakeBridge([{ text: "恢复后继续" }]);
  snapshot.skillRuntimeMode = "pi-native";
  snapshot.skills = [{ id: "skill-1", nativeName: "skill-abc", displayName: "Scripts", description: "For scripts",
    versionId: "v1", version: "1", contentHash: "package-hash", entryPath: "SKILL.md", entryContent: entry,
    files: [{ path: "SKILL.md", sha256: createHash("sha256").update(entry).digest("hex"), size: Buffer.byteLength(entry), text: true }] }];
  snapshot.tools = [{ name: "read", category: "native_skill", description: "Read Skill", allowed: true,
    parameters: { type: "object", properties: { path: { type: "string" } }, required: ["path"], additionalProperties: false } }];
  const persisted = fromCanonical({ ...snapshot, canonical: { ...snapshot.canonical, messages: [
    ...snapshot.canonical.messages, { role: "assistant", content: "", tool_calls: [{ id: "recover-read", type: "function",
      function: { name: "read", arguments: JSON.stringify({ path: mode === "rejected-read" ? "C:/private/secret.txt" : join(process.cwd(), "previous-worker", "skills", "skill-abc", "SKILL.md") }) } },
      ...(mode === "mixed" ? [{ id: "recover-canvas", type: "function", function: { name: "canvas_get_state", arguments: "{}" } }] : [])] },
    ...(mode === "read-completed" ? [{ role: "tool", name: "read", tool_call_id: "recover-read", content: entry }] : []),
  ] } }, canvasModel(snapshot));
  snapshot.piMessages = persisted as unknown as Record<string, unknown>[];
  snapshot.piSessionEntries = sessionEntriesFromMessages(snapshot.runId, persisted)
    .map(entry => ({ runId: snapshot.runId, entry: entry as unknown as Record<string, unknown> }));
  snapshot.piActiveLeafId = String(snapshot.piSessionEntries.at(-1)!.entry.id);
  snapshot.piSessionRevision = 3;
  if (mode === "mixed") snapshot.tools.push(tools[1]!);
  const checkpoint = bridge.checkpoint.bind(bridge);
  bridge.checkpoint = async (...args) => {
    await checkpoint(...args);
    return { saved: true, sessionRevision: (args[5]?.revision || 1) + 1 };
  };
  authorizeNativeEntry(bridge, snapshot);
  snapshot.lastTaskId = "old-task";
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  const result = state.checkpoints.find(({ role }) => role === "toolResult");
  if (mode === "rejected-read") {
    assert.equal(result?.isError, true);
    assert.doesNotMatch(result!.text, /secret.txt|C:\//);
  } else if (mode === "read-completed") assert.equal(result, undefined);
  else assert.equal(result?.text, entry);
  assert.deepEqual(state.executions, mode === "mixed" ? ["recover-canvas"] : []);
  assert.deepEqual(state.batches.flat().map(call => call.id), mode === "mixed" ? ["recover-canvas"] : []);
  if (mode !== "rejected-read") assert.match(JSON.stringify(state.canonical[0]?.messages), /Recovered Skill/);
  assert.deepEqual(state.canonical[0]!.messages.map(message => message.role), mode === "mixed" ? ["user", "assistant", "tool", "tool", "user"] : ["user", "assistant", "tool", "user"]);
  assert.equal(state.canonical[0]!.messages[2]!.tool_call_id, "recover-read");
  });
});

function authorizeNativeEntry(bridge: CanvasBridge, snapshot: PiSnapshot): void {
  bridge.readSkillFile = async (_run, nativeName, path, offset = 0, limit = 12_000) => {
    const skill = snapshot.skills!.find(skill => skill.nativeName === nativeName)!;
    const runes = [...skill.entryContent];
    return { nativeName, skillId: skill.id, versionId: skill.versionId, contentHash: skill.contentHash,
      path, isEntry: true, sha256: skill.files[0]!.sha256, offset, limit, content: runes.slice(offset, offset + limit).join(""),
      totalRunes: runes.length, hasMore: offset + limit < runes.length };
  };
}

test("native read HTTP503 escapes Pi tool error conversion for worker retry", async () => {
  const { bridge, state, snapshot } = fakeBridge([
    { toolCalls: [{ id: "read-503", function: { name: "read", arguments: JSON.stringify({ path: "unused" }) } }] },
    { text: "must not continue" },
  ]);
  snapshot.skillRuntimeMode = "pi-native";
  const entry = '---\nname: skill-abc\ndescription: For scripts\n---\nbody';
  snapshot.skills = [{ id: "skill-1", nativeName: "skill-abc", displayName: "Scripts", description: "For scripts", versionId: "v1", version: "1",
    contentHash: "package", entryPath: "SKILL.md", entryContent: entry,
    files: [{ path: "SKILL.md", sha256: createHash("sha256").update(entry).digest("hex"), size: Buffer.byteLength(entry), text: true }] }];
  snapshot.tools = [{ name: "read", description: "Read Skill", allowed: true,
    parameters: { type: "object", properties: { path: { type: "string" } }, required: ["path"] } }];
  const modelStep = bridge.modelStep.bind(bridge);
  bridge.modelStep = async (...args) => {
    const step = await modelStep(...args);
    if (step.result.toolCalls) step.result.toolCalls[0]!.function.arguments = JSON.stringify({ path: args[1].systemPrompt.match(/<location>([^<]+)<\/location>/)![1] });
    return step;
  };
  const original = globalThis.fetch;
  globalThis.fetch = async () => new Response("unavailable", { status: 503 });
  bridge.readSkillFile = new CanvasBridge("http://backend:8080", "token", "worker").readSkillFile.bind(new CanvasBridge("http://backend:8080", "token", "worker"));
  try {
    await assert.rejects(runCanvasAgent(bridge, snapshot, undefined, promptParts()), /HTTP 503/);
    assert.equal(state.steps, 1);
    assert.equal(state.checkpoints.filter(item => item.role === "toolResult").length, 0);
  } finally { globalThis.fetch = original; }
});

test("in-flight native model recovery rebases returned old-worker calls only for that task", async () => {
  const oldPath = join(process.cwd(), "previous-worker", "skills", "skill-abc", "SKILL.md");
  const { bridge, state, snapshot } = fakeBridge([
    { toolCalls: [{ id: "read-old", function: { name: "read", arguments: JSON.stringify({ path: oldPath }) } }] },
    { text: "continued" },
  ]);
  snapshot.skillRuntimeMode = "pi-native";
  snapshot.activeTaskId = "task-1";
  const entry = '---\nname: skill-abc\ndescription: For scripts\n---\n# Frozen old task';
  snapshot.skills = [{ id: "skill-1", nativeName: "skill-abc", displayName: "Scripts", description: "For scripts", versionId: "v1", version: "1", contentHash: "package",
    entryPath: "SKILL.md", entryContent: entry, files: [{ path: "SKILL.md", sha256: createHash("sha256").update(entry).digest("hex"), size: Buffer.byteLength(entry), text: true }] }];
  snapshot.tools = [{ name: "read", description: "Read Skill", allowed: true, parameters: { type: "object", properties: { path: { type: "string" } }, required: ["path"] } }];
  authorizeNativeEntry(bridge, snapshot);
  const modelStep = bridge.modelStep.bind(bridge);
  bridge.modelStep = async (...args) => {
    if (state.steps > 0) assert.equal(args[0].activeTaskId, undefined, "acknowledged task must not be resumed twice");
    return modelStep(...args);
  };
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  assert.equal(state.checkpoints.find(item => item.role === "toolResult")?.text, entry);
  assert.equal(state.steps, 2);
});

test("模型在没有工具调用时收尾：由 Go 判定完成，且助手正文落库一次", async () => {
  const { bridge, state } = fakeBridge([{ text: "已经换好衣服了" }]);
  await runCanvasAgent(bridge, snapshotFor(), undefined, promptParts());
  assert.equal(state.steps, 1);
  assert.deepEqual(state.noToolTurns, ["task-1"]);
  const assistant = state.checkpoints.filter((item) => item.role === "assistant");
  assert.equal(assistant.length, 1);
  assert.equal(assistant[0]?.taskId, "task-1");
  assert.match(assistant[0]?.text || "", /已经换好衣服了/);
});

test("a blocked finish_run keeps the Pi loop alive for reconciliation", async () => {
  const { bridge, state, snapshot } = fakeBridge([
    { toolCalls: [{ id: "finish-blocked", function: { name: "finish_run", arguments: '{"summary":"done"}' } }] },
    { text: "已按真实结果对账。" },
  ]);
  snapshot.tools = [...tools, { name: "finish_run", description: "Finish after reconciling the plan",
    parameters: { type: "object", properties: { summary: { type: "string" } }, required: ["summary"], additionalProperties: false },
    allowed: true }];
  bridge.executeTool = async (_run, _taskId, callId) => {
    state.executions.push(callId);
    state.ops.push(`execute:${_taskId}:${callId}`);
    return { callId, pending: false, result: { completionBlocked: true, requiredAction: "reconcile_plan" } };
  };
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  assert.equal(state.steps, 2, "a successful blocked receipt must not terminate the Pi loop");
  assert.deepEqual(state.noToolTurns, ["task-2"]);
  assert.equal(state.status, "completed");
  assert.match(JSON.stringify(state.canonical[1]?.messages), /reconcile_plan/);
});

test("continuation pairs missing receipts from prior terminal runs without replaying their tools", async () => {
  const { bridge, state, snapshot } = fakeBridge([{ text: "本轮继续分析。" }]);
  const history = fromCanonical({ ...snapshot, canonical: { ...snapshot.canonical, messages: [
    { role: "user", content: "上一轮要求" },
    { role: "assistant", content: "", tool_calls: [
      { id: "old-finish", type: "function", function: { name: "finish_run", arguments: "{}" } },
      { id: "old-write", type: "function", function: { name: "canvas_apply_ops", arguments: "{}" } },
    ] },
    { role: "tool", tool_call_id: "old-write", content: "已有写入回执" },
    { role: "user", content: "上轮结束后的留言" },
    { role: "assistant", content: "", tool_calls: [
      { id: "old-read", type: "function", function: { name: "canvas_inspect_image", arguments: "{}" } },
    ] },
  ] } }, canvasModel(snapshot));
  snapshot.piSessionEntries = sessionEntriesFromMessages("previous-run", history)
    .map(entry => ({ runId: "previous-run", entry: entry as unknown as Record<string, unknown> }));
  snapshot.piActiveLeafId = String(snapshot.piSessionEntries.at(-1)?.entry.id);
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  const messages = state.canonical[0]!.messages;
  const pending = new Set<string>();
  for (const message of messages) {
    if (message.role === "tool") {
      assert.ok(pending.delete(String(message.tool_call_id)), "tool result must belong to the current batch");
      continue;
    }
    assert.equal(pending.size, 0, "a new message must not follow an unresolved historical tool call");
    for (const call of (message.tool_calls || []) as PiToolCall[]) pending.add(call.id);
  }
  assert.equal(pending.size, 0);
  const repaired = messages.filter(message => ["old-finish", "old-read"].includes(String(message.tool_call_id)));
  assert.equal(repaired.length, 2);
  assert.match(String(repaired[0]?.content), /结果未知/);
  assert.match(String(repaired[1]?.content), /不要.*重新执行/);
  assert.equal(messages.filter(message => message.tool_call_id === "old-write").length, 1);
  assert.deepEqual(state.executions, [], "historical calls must never reach Go execution");
});

for (const status of ["completed", "failed", "cancelled", "rejected"]) {
  test(`terminal tool receipt (${status}) ends Pi without a released-lease snapshot or checkpoint`, async () => {
    const { bridge, state, snapshot } = fakeBridge([
      { toolCalls: [{ id: "terminal-call", function: { name: "canvas_get_state", arguments: "{}" } }] },
    ]);
    const checkpoint = bridge.checkpoint.bind(bridge);
    bridge.checkpoint = async (...args) => {
      assert.equal(state.status, "running", "terminal lease must not be used to append messages");
      return checkpoint(...args);
    };
    bridge.executeTool = async (_run, _taskId, callId) => {
      state.executions.push(callId);
      state.status = status;
      return { callId, pending: false, terminated: true, isError: status === "failed", result: { terminal: status } };
    };
    bridge.snapshot = async () => {
      assert.equal(state.status, "running", "terminal lease has already been released");
      return snapshot;
    };
    await runCanvasAgent(bridge, snapshot, undefined, promptParts());
    assert.equal(state.steps, 1);
    assert.deepEqual(state.executions, ["terminal-call"]);
    assert.deepEqual(state.noToolTurns, []);
  });
}

test("a Go-finalized model failure exits the Pi session without another snapshot", async () => {
  const { bridge, state, snapshot } = fakeBridge([async () => { throw new CanvasRunTerminated("failed"); }]);
  bridge.snapshot = async () => { throw new Error("the terminal session lease is released"); };
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  assert.equal(state.steps, 1);
  assert.deepEqual(state.noToolTurns, []);
  assert.equal(state.checkpoints.filter(message => message.role === "assistant").length, 0);
});

test("未披露的工具调用整批拒绝：不产生工具批次，也不执行画布副作用", async () => {
  const { bridge, state } = fakeBridge([
    { toolCalls: [{ id: "call-1", function: { name: "canvas_apply_ops", arguments: "{}" } }] },
    { text: "那我换个做法" },
  ]);
  await runCanvasAgent(bridge, snapshotFor(), undefined, promptParts());
  assert.deepEqual(state.batches, []);
  assert.deepEqual(state.executions, []);
  // 拒绝必须被模型看到：它应当拿到一条错误工具结果，而不是静默继续。
  const second = state.canonical[1];
  assert.ok(second, "拒绝之后仍要有一次模型请求");
  const toolMessages = second!.messages.filter((message) => message.role === "tool");
  assert.equal(toolMessages.length, 1);
});

test("已披露工具的批次准入先于执行，回执进入下一次模型请求", async () => {
  const { bridge, state } = fakeBridge([
    { toolCalls: [{ id: "call-1", function: { name: "canvas_get_state", arguments: "{}" } }] },
    { text: "读完了" },
  ], { receiptText: "画布上有 3 个节点" });
  await runCanvasAgent(bridge, snapshotFor(), undefined, promptParts());
  assert.equal(state.batches.length, 1);
  assert.deepEqual(state.executions, ["call-1"]);
  // 顺序合同：assistant 检查点在批次准入之前（Go 用 ActiveTaskID 校验这一点）。
  //
  // 不再断言它"是第一条检查点"：会话引导阶段会先提交 system 与 user 两条消息
  // （createAgentSession 的初始条目），那是 SDK 会话的正常形状。这里要守的是**先后**，
  // 不是"一共几条" —— 用 ops 判定，避免把引导检查点误当成契约破坏。
  const assistantAt = state.ops.indexOf("checkpoint:assistant");
  const batchAt = state.ops.findIndex((op) => op.startsWith("batch:"));
  assert.ok(assistantAt >= 0, `必须有 assistant 检查点: ${state.ops.join(" → ")}`);
  assert.ok(batchAt > assistantAt, `assistant 检查点必须先于批次准入: ${state.ops.join(" → ")}`);
  assert.ok(state.ops.includes("batch:task-1"), "tool batch must be bound to its model step");
  assert.ok(state.ops.includes("execute:task-1:call-1"), "tool execution must carry the same model step identity");
  const second = state.canonical[1];
  const toolMessage = second!.messages.find((message) => message.role === "tool");
  assert.ok(toolMessage, "工具回执必须回到模型上下文");
  assert.equal(toolMessage.content, "画布上有 3 个节点");
});

test("模型步骤失败不会被记成一次失败回答", async () => {
  const { bridge, state } = fakeBridge([() => Promise.reject(new Error("upstream exploded"))]);
  await runCanvasAgent(bridge, snapshotFor(), undefined, promptParts()).catch(() => undefined);
  // 引导阶段的 system / user 检查点是会话形状，不是"回答"；这里守的是**没有 assistant 记录**，
  // 否则一次失败的上游调用会在历史里留下一条看似正常的助手消息。
  assert.equal(state.checkpoints.some((item) => item.role === "assistant"), false,
    `失败步骤不得留下 assistant 检查点: ${JSON.stringify(state.checkpoints)}`);
  assert.deepEqual(state.batches, []);
});

test("检查点持久化失败必须抛出，不能把破损运行当成功", async () => {
  const { bridge } = fakeBridge([{ text: "好的" }], { failCheckpoint: true });
  await assert.rejects(
    runCanvasAgent(bridge, snapshotFor(), undefined, promptParts()),
    /injected checkpoint failure/,
  );
});

test("worker restart resumes the existing Go compaction before creating another model step", async () => {
  const messages: Record<string, unknown>[] = [];
  for (let turn = 0; turn < 8; turn += 1) {
    messages.push({ role: "user", content: `canvas facts ${turn} `.repeat(1_500), timestamp: turn * 2 + 1 });
    messages.push({ role: "assistant", content: `Reviewed canvas facts ${turn}. `.repeat(80), timestamp: turn * 2 + 2 });
  }
  messages.push({ role: "user", content: "请给主角换一身衣服", timestamp: 99 });
  const piMessages = messages.map((message) => message.role === "assistant"
    ? { ...message, content: [{ type: "text", text: message.content }], api: "canvas-bridge", provider: "canvas",
      model: "canvas-model", usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop" }
    : { ...message, content: [{ type: "text", text: message.content }] });
  const sessionEntries = sessionEntriesFromMessages("run-1", piMessages) as unknown as Record<string, unknown>[];
  const entries = sessionEntries.map((entry) => ({ runId: "run-1", entry })) as unknown as PiSnapshot["piSessionEntries"];
  const activeLeafId = String(sessionEntries.at(-1)?.id);
  const firstKeptEntryId = String(sessionEntries[1]?.id);
  const pendingContextCompaction: NonNullable<PiSnapshot["pendingContextCompaction"]> = {
    operationId: "op-restart", sessionRevision: 8, activeLeafId,
    reason: "overflow", willRetry: true, tokensBefore: 40_000,
  };
  const { bridge, state, snapshot } = fakeBridge([{ text: "压缩恢复后继续完成。" }], {
    pendingContextCompaction, piSessionEntries: entries, piActiveLeafId: activeLeafId, firstKeptEntryId,
  });
  snapshot.canonical.messages = messages;
  snapshot.piMessages = [{ role: "user", content: messages.at(-1)?.content }];
  snapshot.piSessionRevision = 8;

  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  assert.deepEqual(state.contextCompactions, ["resume:op-restart", "commit:op-restart:8"]);
  assert.equal(state.steps, 1, `exactly one new model step runs after commit; ${JSON.stringify({ ops: state.ops, noToolTurns: state.noToolTurns, status: state.status })}`);
  assert.deepEqual(state.noToolTurns, ["task-1"]);
});

function compactionHistory() {
  const seed = {
    runId: "run-1", userId: "user-1", revision: 1, status: "running",
    request: { prompt: "Continue this film", model: "model" },
    modelLimits: { contextWindowTokens: 64000, maxOutputTokens: 8192, configured: true, source: "fixture" },
    canonical: { systemPrompt: "SERVER POLICY: 影策画布助手。", messages: [], tools: [], toolChoice: "auto" },
    tools: [],
  } as PiSnapshot;
  const model = canvasModel(seed);
  const canonical = Array.from({ length: 8 }, (_, i) => [
    { role: "user", content: `story ${i} `.repeat(1500) }, { role: "assistant", content: `answer ${i}` },
  ]).flat();
  canonical.push({ role: "user", content: "Continue this film" });
  seed.canonical.messages = canonical;
  const messages = fromCanonical(seed, model);
  const entries = sessionEntriesFromMessages("run-1", messages as unknown as Record<string, unknown>[]);
  return { canonical, entries, views: entries.map(entry => ({ runId: "run-1", entry: entry as unknown as Record<string, unknown> })) as PiSnapshot["piSessionEntries"],
    leaf: String(entries.at(-1)!.id), keep: String(entries.at(-1)!.id) };
}

test("preflight compacts before appending the next prompt and never admits the denied model step", async () => {
  const history = compactionHistory();
  const { bridge, state, snapshot } = fakeBridge([{ text: "Done" }], {
    piSessionEntries: history.views, piActiveLeafId: history.leaf, firstKeptEntryId: history.keep,
  });
  snapshot.canonical.messages = history.canonical;
  snapshot.piSessionRevision = 8;
  let checks = 0;
  const order: string[] = [];
  const preflightPrompts: string[] = [];
  bridge.modelPreflight = async (_run, canonical) => {
    preflightPrompts.push(canonical.systemPrompt);
    order.push("preflight");
    return { status: "ready", taskId: "", decision: ++checks === 1 ? "compact" : "model" };
  };
  bridge.compactContext = async () => {
    order.push("compact");
    return { operationId: "preflight-op", status: "succeeded", summary: "<agent-context-checkpoint/>",
      firstKeptEntryId: history.keep, tokensBefore: 40000,
      details: { protocolVersion: "canvas-pi-compaction/v1", operationId: "preflight-op" } };
  };
  const checkpoint = bridge.checkpoint.bind(bridge);
  bridge.checkpoint = async (...args) => { order.push(`checkpoint:${args[2].role}`); return checkpoint(...args); };
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  assert.ok(order.indexOf("compact") < order.indexOf("checkpoint:user"));
  assert.equal(state.steps, 1);
  assert.ok(checks >= 3, "check before prompt and again before the actual provider request");
  const firstRequest = state.canonical[0];
  assert.ok(firstRequest);
  assert.ok(firstRequest.messages.some(message => String(message.content).includes("agent-context-checkpoint")));
  // 首个 preflight 发生在首个 prompt 之前：SDK 的 Agent.state.systemPrompt 仍是空串，
  // 必须回落到装配提示，否则真实服务端会以"缺少服务端策略"拒绝整个运行。
  assert.ok(preflightPrompts.length >= 2, "both preflight paths must be exercised");
  assert.ok(preflightPrompts.every((prompt) => prompt.includes("SERVER POLICY: 影策画布助手。")),
    JSON.stringify(preflightPrompts));
});

test("a preflight compaction without compactable history falls through to the prompt", async () => {
  // 真实部署暴露：本轮 prompt 尚未进入 Pi 会话时 compact() 会抛
  // "Nothing to compact (session too small)"。preflight 阶段这属于"无材料"：
  // 必须回落到 session.prompt，让 Pi 的自动压缩（含本轮消息）与 Go 准入决定，
  // 而不是终态失败、更不能让 worker 释放租约后反复重领。
  const { bridge, state, snapshot } = fakeBridge([{ text: "Done" }]);
  let checks = 0;
  bridge.modelPreflight = async () => ({ status: "ready", taskId: "", decision: ++checks === 1 ? "compact" : "model" });
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  assert.ok(state.steps >= 1, "模型步必须在本轮真实请求前被准入");
});

test("a pending operation received through control is committed before any ordinary model request", async () => {
  const history = compactionHistory();
  const { bridge, state, snapshot } = fakeBridge([{ text: "Done" }], {
    piSessionEntries: history.views, piActiveLeafId: history.leaf, firstKeptEntryId: history.keep,
  });
  snapshot.canonical.messages = history.canonical;
  snapshot.piSessionRevision = 8;
  const pending = { operationId: "control-op", sessionRevision: 8, activeLeafId: history.leaf,
    reason: "threshold", willRetry: false, tokensBefore: 40000 };
  bridge.control = async () => ({ status: "running", pendingContextCompaction: pending });
  const modelStep = bridge.modelStep.bind(bridge);
  bridge.modelStep = async (...args) => {
    assert.deepEqual(state.contextCompactions, ["resume:control-op", "commit:control-op:8"]);
    return modelStep(...args);
  };
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  assert.equal(state.steps, 1);
});

test("compaction and restart preserve the original image beyond the retained text boundary", async () => {
  const image = [{ type: "text", text: '{"nodeId":"hero-original"}' },
    { type: "image_url", image_url: { url: "resource:original-image", detail: "high" } }];
  const { bridge, state, snapshot } = fakeBridge([{ text: "原图已保留。" }]);
  const model = canvasModel(snapshot);
  const prior = fromCanonical({ ...snapshot, canonical: { ...snapshot.canonical, messages: [
    { role: "user", content: image }, { role: "assistant", content: "此前已记录画面。" },
    { role: "user", content: "上一轮文字" }, { role: "assistant", content: "已处理。" },
  ] } }, model);
  const entries = sessionEntriesFromMessages("old-run", prior) as unknown as Record<string, unknown>[];
  entries.push({ type: "compaction", id: "image-compaction", parentId: entries.at(-1)?.id,
    timestamp: new Date().toISOString(), summary: "保留文字摘要", firstKeptEntryId: entries[2]?.id,
    tokensBefore: 40_000 });
  snapshot.piSessionEntries = entries.map(entry => ({ runId: "old-run", entry }));
  snapshot.piActiveLeafId = "image-compaction";
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  const restored = state.canonical[0]?.messages.flatMap(message =>
    Array.isArray(message.content) ? message.content : []);
  assert.deepEqual(restored, image, "a summary must not replace pixels or alter the original image reference");
  assert.equal(state.executions.length, 0, "restoring an image must not call an inspection or generation tool");
});

test("an image newer than the Pi checkpoint is durably imported before the model request", async () => {
  const image = [{ type: "text", text: '{"nodeId":"new-image"}' },
    { type: "image_url", image_url: { url: "resource:new-original", detail: "high" } }];
  const { bridge, state, snapshot } = fakeBridge([{ text: "收到原图。" }]);
  snapshot.canonical.messages = [{ role: "user", content: image }];
  await runCanvasAgent(bridge, snapshot, undefined, promptParts());
  const parts = state.canonical[0]?.messages.flatMap(message => Array.isArray(message.content) ? message.content : []);
  assert.deepEqual(parts, image);
  assert.ok(state.ops.indexOf("checkpoint:user") < state.ops.indexOf("checkpoint:assistant"));
  assert.ok(state.checkpoints.some(message => message.role === "user" && message.text.includes("new-image")));
});

test("取消后不再发起模型请求，运行以取消收场", async () => {
  const controller = new AbortController();
  const { bridge, state } = fakeBridge([
    async () => {
      controller.abort();
      await new Promise(() => undefined);
      return { text: "never" };
    },
  ]);
  const run = runCanvasAgent(bridge, snapshotFor(), controller.signal, promptParts());
  await Promise.race([run, new Promise((resolve) => setTimeout(resolve, 5000))]);
  assert.equal(state.steps, 1);
  assert.deepEqual(state.batches, []);
});

test("生产 runner 不再手写 new Agent 循环（C1 出口）", () => {
  // 测试运行的是编译产物（dist/test），源码在仓库的 src/：必须往上两级再进 src，
  // 否则会去找 dist/src/runner.ts —— 那个文件不存在，断言就变成了 ENOENT 而不是内容检查。
  const testDirectory = dirname(fileURLToPath(import.meta.url));
  const sourcePath = [join(testDirectory, "..", "src", "runner.ts"),
    join(testDirectory, "..", "..", "src", "runner.ts")].find(existsSync);
  assert.ok(sourcePath, "runner source must be available from source and compiled tests");
  const source = readFileSync(sourcePath, "utf8");
  assert.equal(/new Agent\s*\(/.test(source), false, "runner.ts 不得再手写 new Agent 循环");
  // 判据只禁"值导入 Agent 类"：`import type { AgentMessage }` 是正常的类型导入。
  // 旧写法 /pi-agent-core["'][^)]*Agent/ 里的 `[^)]*` 会跨行匹配，一路扫到别处的
  // AgentSession，把"已经没有手写循环"判成失败 —— 那是判据的缺陷，不是代码的缺陷。
  assert.equal(
    /import\s*\{[^}]*\bAgent\b[^}]*\}\s*from\s*["']@earendil-works\/pi-agent-core["']/.test(source),
    false,
    "runner.ts 不得值导入 pi-agent-core 的 Agent（类型导入不受限）",
  );
  assert.match(source, /createAgentSession/, "生产 runner 必须走 createAgentSession");
});

function snapshotFor(): PiSnapshot {
  return {
    runId: "run-1", userId: "user-1", revision: 1, status: "running",
    request: { prompt: "请给主角换一身衣服", model: "canvas-model", channelModelKey: "canvas-model" },
    modelLimits: { contextWindowTokens: 64_000, maxOutputTokens: 8_192, configured: true, source: "channel-model" },
    canonical: { systemPrompt: "SERVER POLICY: 影策画布助手。", messages: [{ role: "user", content: "请给主角换一身衣服" }],
      tools: [], toolChoice: "auto" },
    tools, piMessages: [],
  };
}

test("Pi uses the Go-resolved context window and output limit", () => {
  const snapshot = snapshotFor();
  snapshot.modelLimits = { contextWindowTokens: 48_000, maxOutputTokens: 6_000, reservedOutputTokens: 12_000, configured: true, source: "channel-model" };
  const model = canvasModel(snapshot);
  assert.equal(model.contextWindow, 48_000);
  assert.equal(model.maxTokens, 6_000);
});

test("Pi uses a bounded scheduler output when the model only declares its context window", () => {
  const snapshot = snapshotFor();
  snapshot.modelLimits = { contextWindowTokens: 32_000, maxOutputTokens: 0, configured: true, source: "channel-model" };
  const model = canvasModel(snapshot);
  assert.equal(model.contextWindow, 32_000);
  assert.equal(model.maxTokens, 16_000);
});

test("Pi still rejects a snapshot without a valid context window", () => {
  const snapshot = snapshotFor();
  snapshot.modelLimits = { contextWindowTokens: 0, maxOutputTokens: 8_192, configured: false, source: "default" };
  assert.throws(() => canvasModel(snapshot), /missing effective Pi model limits/);
});

test("服务端策略是强制层：工作区文件只能追加，不能顶掉策略", () => {
  const rendered = assembleSystemPrompt({ system: "Workspace rules", appendSystem: "Append note",
    context: [{ name: "AGENTS.md", text: "repo rules" }] }, "SERVER POLICY");
  assert.ok(rendered.startsWith("SERVER POLICY"), rendered);
  assert.ok(rendered.includes("Workspace rules"));
  assert.ok(rendered.includes("## Workspace AGENTS.md\nrepo rules"));
  assert.ok(rendered.includes("Append note"));
  // 策略前缀为空时才允许 SYSTEM.md 单独充当系统提示（本地调试路径）。
  assert.equal(assembleSystemPrompt({ system: "Only file", context: [] }, ""), "Only file");
});
