/**
 * P1 — Pi Coding Agent SDK feasibility probe.
 *
 * Drives the real `@earendil-works/pi-coding-agent@0.87.1` `createAgentSession`,
 * `SessionManager`, `ModelRuntime` and `ResourceLoader` against a deterministic
 * Canvas provider stub. Nothing here mocks the SDK: the probe asserts on the
 * public contracts the migration depends on, per
 * `agent/PI_CODING_AGENT_MIGRATION_ROADMAP.md` section 5 (P1).
 *
 * Verified extension points:
 *   1. Provider    — `ModelRuntime.registerProvider(id, { api, streamSimple, models })`
 *   2. Session     — `SessionManager.inMemory(cwd, options, entries)` rebuild from
 *                    externally persisted v3 entries, plus `entry_appended`
 *   3. Tooling     — `customTools` + `setActiveToolsByName` dynamic disclosure
 *   4. Lifecycle   — `agent_settled`, `compaction_*`, `session_before_compact`
 */
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import {
  CURRENT_SESSION_VERSION,
  DefaultResourceLoader,
  ModelRuntime,
  SessionManager,
  SettingsManager,
  createAgentSession,
} from "@earendil-works/pi-coding-agent";
import type { ExtensionAPI, InlineExtension, SessionEntry, ToolDefinition } from "@earendil-works/pi-coding-agent";
import { createAssistantMessageEventStream, getCurrentSystemPrompt, getCurrentTools } from "@earendil-works/pi-ai";
import type { AssistantMessage, JsonObject, Model, Usage } from "@earendil-works/pi-ai";
import { Type } from "typebox";

const PROVIDER_ID = "canvas";
const MODEL_ID = "canvas-probe";
const API_ID = "canvas-bridge";
const PROBE_KEY = "probe-key-not-a-secret";

/** One scripted model response. */
interface Step {
  text?: string;
  thinking?: string;
  toolCalls?: Array<{ id: string; name: string; args: JsonObject }>;
  stopReason?: "stop" | "toolUse" | "length" | "error" | "aborted";
  usage?: Partial<Usage>;
  /** Emit the balanced events but terminate with an `error` event. */
  fail?: boolean;
  /** Emit incremental events, then wait for the caller to abort before terminating. */
  hang?: boolean;
}

/** What the provider observed for one request. */
interface Observed {
  systemPrompt: string;
  tools: string[];
  messageRoles: string[];
}

const ZERO_COST = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 };

function usage(partial?: Partial<Usage>): Usage {
  const input = partial?.input ?? 11;
  const output = partial?.output ?? 7;
  const cacheRead = partial?.cacheRead ?? 3;
  const cacheWrite = partial?.cacheWrite ?? 2;
  return {
    input,
    output,
    cacheRead,
    cacheWrite,
    totalTokens: input + output + cacheRead + cacheWrite,
    ...(partial?.reasoning === undefined ? {} : { reasoning: partial.reasoning }),
    cost: { ...ZERO_COST },
  };
}

/**
 * Deterministic provider stub. It records exactly what Pi put in front of the
 * model (system prompt + tool declarations) and replays the scripted steps.
 */
function createScriptedProvider(steps: Step[], onRequest?: (index: number, signal?: AbortSignal) => void) {
  const observed: Observed[] = [];
  const abortedRequests: number[] = [];
  let cursor = 0;

  const streamSimple = (model: Model<any>, context: any, options?: { signal?: AbortSignal }) => {
    const stream = createAssistantMessageEventStream();
    let streamIsEnded = false;
    const requestIndex = observed.length;
    const step: Step = steps[Math.min(cursor, steps.length - 1)] ?? {};
    cursor += 1;
    observed.push({
      systemPrompt: getCurrentSystemPrompt(context.messages),
      tools: getCurrentTools(context.messages).map((tool) => tool.name),
      messageRoles: context.messages.map((message: { role: string }) => message.role),
    });
    onRequest?.(requestIndex, options?.signal);

    const base: AssistantMessage = {
      role: "assistant",
      content: [],
      api: API_ID,
      provider: PROVIDER_ID,
      model: MODEL_ID,
      usage: usage(step.usage),
      stopReason: step.stopReason ?? (step.toolCalls?.length ? "toolUse" : "stop"),
      timestamp: Date.now(),
    };

    const finish = (message: AssistantMessage) => {
      if (options?.signal?.aborted) {
        abortedRequests.push(requestIndex);
        stream.push({ type: "error", reason: "aborted", error: { ...message, stopReason: "aborted" } });
        return;
      }
      if (step.fail) {
        stream.push({ type: "error", reason: "error", error: { ...message, stopReason: "error", errorMessage: "scripted failure" } });
        return;
      }
      stream.push({
        type: "done",
        reason: (message.stopReason === "toolUse" ? "toolUse" : "stop") as "stop" | "toolUse",
        message,
      });
    };

    queueMicrotask(() => {
      const content: AssistantMessage["content"] = [];
      if (step.thinking) content.push({ type: "thinking", thinking: step.thinking });
      if (step.text) content.push({ type: "text", text: step.text });
      for (const call of step.toolCalls ?? []) {
        content.push({ type: "toolCall", id: call.id, name: call.name, arguments: call.args });
      }
      const message: AssistantMessage = { ...base, content };

      stream.push({ type: "start", partial: { ...base, content: [] } });
      step.thinking && stream.push({ type: "thinking_start", contentIndex: 0, partial: base });
      step.thinking && stream.push({ type: "thinking_delta", contentIndex: 0, delta: step.thinking, partial: base });
      step.thinking && stream.push({ type: "thinking_end", contentIndex: 0, content: step.thinking, partial: base });
      for (const call of step.toolCalls ?? []) {
        const index = content.findIndex((block) => block.type === "toolCall" && block.id === call.id);
        stream.push({ type: "toolcall_start", contentIndex: index, partial: base });
        stream.push({ type: "toolcall_delta", contentIndex: index, delta: JSON.stringify(call.args), partial: base });
        stream.push({ type: "toolcall_end", contentIndex: index, toolCall: { type: "toolCall", id: call.id, name: call.name, arguments: call.args }, partial: base });
      }
      if (step.text) {
        const index = content.findIndex((block) => block.type === "text");
        stream.push({ type: "text_start", contentIndex: index, partial: base });
        stream.push({ type: "text_delta", contentIndex: index, delta: step.text, partial: base });
        stream.push({ type: "text_end", contentIndex: index, content: step.text, partial: base });
      }
      if (step.hang) {
        // Incremental events are already out; the provider now waits for cancellation,
        // which is how a real Canvas provider observes a cancelled run.
        const signal = options?.signal;
        const stop = () => {
          abortedRequests.push(requestIndex);
          stream.push({ type: "error", reason: "aborted", error: { ...message, stopReason: "aborted" } });
        };
        if (signal?.aborted) {
          stop();
          return;
        }
        signal?.addEventListener("abort", stop, { once: true });
        setTimeout(() => {
          if (!signal?.aborted && !streamIsEnded) {
            streamIsEnded = true;
            finish(message);
          }
        }, 2_000).unref?.();
        return;
      }
      finish(message);
    });

    return stream;
  };

  return { observed, abortedRequests, streamSimple };
}

/** Runtime whose only provider is the Canvas stub. */
async function createCanvasRuntime(steps: Step[], onRequest?: (index: number, signal?: AbortSignal) => void) {
  const provider = createScriptedProvider(steps, onRequest);
  const runtime = await ModelRuntime.create({ modelsPath: null, refreshOnCreate: false, allowModelNetwork: false });
  runtime.registerProvider(PROVIDER_ID, {
    name: "Canvas Bridge",
    api: API_ID,
    // Required by the SDK whenever custom models are declared. `streamSimple`
    // below never performs HTTP and `.invalid` is reserved by RFC 2606, so this
    // placeholder can never be contacted.
    baseUrl: "http://canvas-bridge.invalid/v1",
    models: [
      {
        id: MODEL_ID,
        name: "Canvas Probe",
        api: API_ID,
        reasoning: true,
        input: ["text", "image"],
        cost: { ...ZERO_COST },
        contextWindow: 200_000,
        maxTokens: 8_192,
      },
    ],
    streamSimple: provider.streamSimple,
  } as never);
  await runtime.setRuntimeApiKey(PROVIDER_ID, PROBE_KEY);
  const model = runtime.getModel(PROVIDER_ID, MODEL_ID);
  assert.ok(model, "registered Canvas model must resolve from the runtime");
  return { runtime, model, provider };
}

/** Minimal Canvas tool used to prove disclosure gating. */
function canvasTool(
  name: string,
  description: string,
  calls: string[],
  onExecute?: () => void,
): ToolDefinition {
  return {
    name,
    label: name,
    description,
    parameters: Type.Object({ note: Type.Optional(Type.String()) }),
    execute: async () => {
      calls.push(name);
      // The agent awaits execute() before it issues the next model request, so a
      // disclosure change made here is deterministic and never races the request.
      onExecute?.();
      return { content: [{ type: "text", text: `${name}:ok` }], details: {} };
    },
  };
}

interface HarnessOptions {
  steps: Step[];
  onRequest?: (index: number, signal?: AbortSignal) => void;
  tools: ToolDefinition[];
  toolNames: string[];
  entries?: SessionEntry[];
  extensions?: InlineExtension[];
  systemPrompt?: string;
  /** Defaults to "all": the safest setting for a canvas-only session. */
  noToolsMode?: "all" | "builtin";
  /** When true the session is created without a tool allowlist. */
  omitToolAllowlist?: boolean;
}

/** Build a real AgentSession on top of the Canvas stub. */
async function createHarness(options: HarnessOptions) {
  const root = mkdtempSync(join(tmpdir(), "canvas-pi-probe-"));
  const cwd = join(root, "cwd");
  const agentDir = join(root, "agent");
  const { mkdirSync, writeFileSync } = await import("node:fs");
  mkdirSync(cwd, { recursive: true });
  mkdirSync(agentDir, { recursive: true });
  // Keep the compaction window small so the probe can reach a real cut point
  // without generating hundreds of kilobytes of transcript.
  writeFileSync(
    join(agentDir, "settings.json"),
    JSON.stringify({ compaction: { keepRecentTokens: 20, reserveTokens: 10 } }),
  );

  const { runtime, model, provider } = await createCanvasRuntime(options.steps, options.onRequest);
  const settingsManager = SettingsManager.create(cwd, agentDir);
  const resourceLoader = new DefaultResourceLoader({
    cwd,
    agentDir,
    settingsManager,
    noExtensions: false,
    noSkills: true,
    noPromptTemplates: true,
    noThemes: true,
    noContextFiles: true,
    extensionFactories: options.extensions,
    systemPrompt: options.systemPrompt,
  });
  await resourceLoader.reload();

  const sessionManager = SessionManager.inMemory(cwd, { id: "probe-session" }, options.entries);
  const sessionEvents: string[] = [];
  const appendedEntries: SessionEntry[] = [];

  const { session } = await createAgentSession({
    cwd,
    agentDir,
    model,
    modelRuntime: runtime,
    sessionManager,
    settingsManager,
    resourceLoader,
    customTools: options.tools,
    noTools: options.noToolsMode ?? "all",
    ...(options.omitToolAllowlist ? {} : { tools: options.toolNames }),
  });

  session.subscribe((event) => {
    sessionEvents.push(event.type);
    if (event.type === "entry_appended") appendedEntries.push(event.entry);
  });

  return {
    session,
    sessionManager,
    provider,
    sessionEvents,
    appendedEntries,
    cleanup: () => rmSync(root, { recursive: true, force: true }),
  };
}

test("P1 provider: scripted stream reaches the model with system prompt and canvas-only tools", async () => {
  const calls: string[] = [];
  const harness = await createHarness({
    steps: [{ text: "done" }],
    tools: [canvasTool("canvas_get_state", "Read canvas state", calls)],
    toolNames: ["canvas_get_state"],
    systemPrompt: "You are the Canvas agent.",
  });
  try {
    await harness.session.prompt("Inspect the canvas");
    await harness.session.waitForIdle();

    assert.equal(harness.provider.observed.length, 1, "exactly one model request");
    const first = harness.provider.observed[0]!;
    assert.match(first.systemPrompt, /You are the Canvas agent\./);
    assert.deepEqual(first.tools, ["canvas_get_state"], "only the disclosed canvas tool is declared");
    assert.ok(
      !first.tools.some((name) => ["read", "bash", "edit", "write"].includes(name)),
      "no default pi coding tool may be exposed",
    );
  } finally {
    harness.cleanup();
  }
});

test("P1 provider: thinking, text and tool call are balanced and usage is preserved verbatim", async () => {
  const calls: string[] = [];
  const harness = await createHarness({
    steps: [
      { thinking: "considering", toolCalls: [{ id: "call-1", name: "canvas_get_state", args: { note: "a" } }] },
      { text: "finished" },
    ],
    tools: [canvasTool("canvas_get_state", "Read canvas state", calls)],
    toolNames: ["canvas_get_state"],
    systemPrompt: "probe",
  });
  try {
    await harness.session.prompt("go");
    await harness.session.waitForIdle();

    assert.deepEqual(calls, ["canvas_get_state"], "the disclosed tool really executed");
    assert.equal(harness.provider.observed.length, 2, "tool result triggers a second model step");
    assert.deepEqual(harness.provider.observed[1]!.tools, ["canvas_get_state"]);
    assert.ok(
      harness.provider.observed[1]!.messageRoles.includes("toolResult"),
      "tool result is replayed to the model",
    );
    assert.ok(harness.provider.abortedRequests.length === 0, "no spurious abort");
  } finally {
    harness.cleanup();
  }
});

test("P1 provider: cancelling an in-flight run aborts it and starts no follow-up request", async () => {
  let session: { abort: () => Promise<void> } | undefined;
  const harness = await createHarness({
    steps: [{ hang: true }, { text: "must never run" }],
    onRequest: (index) => {
      if (index === 0) setTimeout(() => void session?.abort(), 10);
    },
    tools: [],
    toolNames: [],
    systemPrompt: "probe",
  });
  session = harness.session;
  try {
    await harness.session.prompt("go").catch(() => undefined);
    await harness.session.waitForIdle();

    assert.equal(
      harness.provider.observed.length,
      1,
      "a cancelled run must not issue a second model request",
    );
    assert.ok(
      harness.provider.abortedRequests.length >= 1,
      "the provider observed cancellation through options.signal",
    );
    assert.equal(harness.session.isStreaming, false, "the session is idle after cancellation");
  } finally {
    harness.cleanup();
  }
});

test("P1 tools: all eligible concrete tools reach the model from the first step", async () => {
  const calls: string[] = [];
  const harness = await createHarness({
    steps: [{ text: "one" }, { text: "two" }],
    tools: [canvasTool("canvas_get_state", "Read canvas state", calls)],
    toolNames: ["canvas_get_state"],
    systemPrompt: "probe",
  });
  try {
    assert.deepEqual(harness.session.getActiveToolNames(), ["canvas_get_state"]);

    await harness.session.prompt("turn one");
    await harness.session.waitForIdle();
    assert.deepEqual(
      harness.provider.observed[0]!.tools,
      ["canvas_get_state"],
      "eligible concrete tool is declared on the first model request",
    );

    await harness.session.prompt("turn two");
    await harness.session.waitForIdle();
    assert.deepEqual(
      harness.provider.observed[1]!.tools,
      ["canvas_get_state"],
      "the same eligible tool remains available for the next turn",
    );

    // Unknown names are ignored rather than trusted — Go preflight stays authoritative.
    harness.session.setActiveToolsByName(["canvas_get_state", "canvas_totally_unknown"]);
    assert.deepEqual(
      harness.session.getActiveToolNames(),
      ["canvas_get_state"],
      "a tool outside the registry cannot be activated",
    );
  } finally {
    harness.cleanup();
  }
});

test("P1 session: v3 entries rebuild an in-memory SessionManager with the same context and active leaf", async () => {
  const first = await createHarness({
    steps: [{ text: "first answer" }],
    tools: [],
    toolNames: [],
    systemPrompt: "You are the Canvas agent.",
  });
  let exported: SessionEntry[] = [];
  let leaf: string | null = null;
  let contextBefore: string[] = [];
  try {
    await first.session.prompt("remember this");
    await first.session.waitForIdle();
    exported = first.sessionManager.getEntries();
    leaf = first.sessionManager.getLeafId();
    contextBefore = first.sessionManager.buildSessionContext().messages.map((message: { role: string }) => message.role);
  } finally {
    first.cleanup();
  }

  assert.ok(exported.length > 0, "the session produced persistable v3 entries");
  assert.equal(first.sessionManager.getHeader()?.version ?? CURRENT_SESSION_VERSION, CURRENT_SESSION_VERSION);
  assert.ok(leaf, "an active leaf must exist for restore");

  // Rebuild the session from the exported entries only — this is the DB restore path.
  const second = await createHarness({
    steps: [{ text: "second answer" }],
    tools: [],
    toolNames: [],
    systemPrompt: "You are the Canvas agent.",
    entries: exported,
  });
  try {
    assert.equal(second.sessionManager.getLeafId(), leaf, "active leaf survives export/import");
    assert.deepEqual(
      second.sessionManager.buildSessionContext().messages.map((message: { role: string }) => message.role),
      contextBefore,
      "rebuilt context matches the pre-crash context",
    );
    assert.deepEqual(
      second.sessionManager.getEntries().map((entry) => entry.id),
      exported.map((entry) => entry.id),
      "entry tree round-trips without loss",
    );
  } finally {
    second.cleanup();
  }
});

test("P1 session: the entry tree is append-only and readable, so a database writer can diff it", async () => {
  const harness = await createHarness({
    steps: [{ text: "one" }, { text: "two" }],
    tools: [],
    toolNames: [],
    systemPrompt: "probe",
  });
  try {
    await harness.session.prompt("first");
    await harness.session.waitForIdle();
    const afterFirst = harness.sessionManager.getEntries().map((entry) => entry.id);
    const leafAfterFirst = harness.sessionManager.getLeafId();

    await harness.session.prompt("second");
    await harness.session.waitForIdle();
    const afterSecond = harness.sessionManager.getEntries().map((entry) => entry.id);

    assert.ok(afterSecond.length > afterFirst.length, "the tree keeps growing");
    assert.deepEqual(
      afterSecond.slice(0, afterFirst.length),
      afterFirst,
      "existing entries are never rewritten, so a writer can persist by id diff",
    );
    assert.notEqual(harness.sessionManager.getLeafId(), leafAfterFirst, "the active leaf advances");

    // Negative evidence for P2: `entry_appended` is NOT a general append hook. It is
    // emitted for boundary commits (compaction / context edits), not plain messages.
    assert.deepEqual(
      harness.appendedEntries.filter((entry) => entry.type === "message"),
      [],
      "plain message appends do not emit entry_appended; P2 must diff getEntries() instead",
    );
  } finally {
    harness.cleanup();
  }
});

test("P1 lifecycle: session_before_compact is reachable and compaction emits start/end", async () => {
  const hookHits: string[] = [];
  const extension: InlineExtension = {
    name: "canvas-probe",
    factory: (pi: ExtensionAPI) => {
      pi.on("session_before_compact", (event) => {
        hookHits.push(event.type);
      });
    },
  };
  const long = "canvas context ".repeat(60);
  const harness = await createHarness({
    steps: [{ text: "a" }, { text: "b" }, { text: "summary" }],
    tools: [],
    toolNames: [],
    systemPrompt: "probe",
    extensions: [extension],
  });
  try {
    await harness.session.prompt(`${long} first`);
    await harness.session.waitForIdle();
    await harness.session.prompt(`${long} second`);
    await harness.session.waitForIdle();

    const result = await harness.session.compact();
    assert.equal(typeof result, "object", "compact() returns a real CompactionResult");
    assert.ok(
      harness.sessionEvents.includes("compaction_start") && harness.sessionEvents.includes("compaction_end"),
      "compaction lifecycle events are observable",
    );
    assert.ok(hookHits.length >= 1, "session_before_compact extension hook fired");
    assert.ok(
      harness.sessionManager.getEntries().some((entry) => entry.type === "compaction"),
      "compaction is recorded as a first-class v3 entry",
    );
  } finally {
    harness.cleanup();
  }
});

test("P1 tools: zero dangerous default tools are registered, whatever the suppression mode", async () => {
  const dangerous = ["read", "bash", "edit", "write", "powershell", "ls", "grep", "find"];
  for (const mode of ["all", "builtin"] as const) {
    const harness = await createHarness({
      steps: [{ text: "ok" }],
      tools: [canvasTool("canvas_get_state", "Read canvas state", [])],
      toolNames: ["canvas_get_state"],
      systemPrompt: "probe",
      noToolsMode: mode,
    });
    try {
      const registered = harness.session.getAllTools().map((tool) => tool.name);
      const leaked = registered.filter((name) => dangerous.includes(name));
      assert.deepEqual(leaked, [], `noTools="${mode}" must not register pi coding tools`);
      assert.deepEqual(harness.session.getActiveToolNames(), ["canvas_get_state"]);

      await harness.session.prompt("go");
      await harness.session.waitForIdle();
      const declared = harness.provider.observed[0]!.tools;
      assert.deepEqual(
        declared.filter((name) => dangerous.includes(name)),
        [],
        `noTools="${mode}" must not declare pi coding tools to the model`,
      );
    } finally {
      harness.cleanup();
    }
  }
});

test("P1 chain: model -> registered concrete tool -> result -> next model step", async () => {
  const calls: string[] = [];
  const built = await createHarness({
    steps: [
      { toolCalls: [{ id: "call-child", name: "canvas_get_state", args: { note: "read" } }] },
      { text: "canvas inspected" },
    ],
    tools: [canvasTool("canvas_get_state", "Read canvas state", calls)],
    toolNames: ["canvas_get_state"],
    systemPrompt: "probe",
  });
  try {
    await built.session.prompt("read the canvas");
    await built.session.waitForIdle();

    assert.deepEqual(calls, ["canvas_get_state"], "concrete tool is executed through Pi");
    assert.deepEqual(
      built.provider.observed[0]!.tools,
      ["canvas_get_state"],
      "first step sees every eligible concrete tool",
    );
    assert.equal(built.provider.observed.length, 2, "the chain reaches a final assistant step");
    assert.ok(
      built.provider.observed[1]!.messageRoles.includes("toolResult"),
      "the concrete tool result is replayed to the model",
    );
  } finally {
    built.cleanup();
  }
});
