import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { CANVAS_PI_WIRE_IDENTITY, CanvasBridge, type PiSnapshot, type PiToolCall } from "../src/bridge.js";

const testDirectory = dirname(fileURLToPath(import.meta.url));

const run: PiSnapshot = {
  runId: "run-wire",
  piSessionLeaseEpoch: 7,
  userId: "user-wire",
  revision: 1,
  status: "running",
  request: { prompt: "wire contract", model: "test-model" },
  modelLimits: { contextWindowTokens: 128_000, maxOutputTokens: 16_384, configured: false, source: "default" },
  canonical: { systemPrompt: "probe", messages: [], tools: [], toolChoice: "auto" },
  tools: [],
};

/** Capture the single request a bridge call issues and return its parsed body. */
async function captureBody(call: (bridge: CanvasBridge) => Promise<unknown>, responseData: unknown = {}) {
  const original = globalThis.fetch;
  const captured: { url: string; method: string; body: unknown; headers: Record<string, string> }[] = [];
  globalThis.fetch = (async (url: string | URL | Request, init?: RequestInit) => {
    captured.push({
      url: String(url),
      method: String(init?.method),
      body: init?.body === undefined ? undefined : JSON.parse(String(init.body)),
      headers: init?.headers as Record<string, string>,
    });
    return new Response(JSON.stringify({ code: 0, data: responseData }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  try {
    await call(new CanvasBridge("http://backend:8080", "token", "worker-1"));
  } finally {
    globalThis.fetch = original;
  }
  assert.equal(captured.length, 1, "exactly one request is issued");
  return captured[0]!;
}

test("每个运行请求都携带会话租约 epoch", async () => {
  const request = await captureBody((bridge) => bridge.failRun(run, "probe", undefined));
  assert.equal(request.headers["X-Agent-Session-Epoch"], "7");
});

test("native restart resumes its in-flight model task without reposting changed worker context", async () => {
  const request = await captureBody(bridge => bridge.modelStep({ ...run, skillRuntimeMode: "pi-native", activeTaskId: "old-task" },
    { ...run.canonical, systemPrompt: "new worker paths" }), {
    taskId: "old-task", status: "succeeded", result: { toolCalls: [{ id: "old-read", function: { name: "read", arguments: '{"path":"/tmp/old/skills/skill-abc/SKILL.md"}' } }] },
  });
  assert.equal(request.method, "GET");
  assert.equal(request.url, "http://backend:8080/internal-agent/runs/run-wire/model-steps/old-task");
  assert.equal(request.headers["Authorization"], "Bearer token");
  assert.equal(request.headers["X-Agent-Worker-ID"], "worker-1");
  assert.equal(request.headers["X-Agent-Session-Epoch"], "7");
});

test("a Go-finalized model failure is a terminal control signal", async () => {
  const original = globalThis.fetch;
  const routes: string[] = [];
  globalThis.fetch = (async (url: string | URL | Request) => {
    routes.push(String(url));
    const data = String(url).endsWith("/fail") ? { status: "failed" }
      : { taskId: "failed-model-task", status: "failed", error: "provider failed" };
    return new Response(JSON.stringify({ code: 0, data }), { status: 200 });
  }) as typeof fetch;
  try {
    const bridge = new CanvasBridge("http://backend:8080", "token", "worker-1");
    await assert.rejects(bridge.modelStep(run, run.canonical), error =>
      error instanceof Error && error.name === "CanvasRunTerminated");
    assert.equal(routes.length, 2, "failure admission is reported once");
  } finally { globalThis.fetch = original; }
});

test("内部 wire identity 与共享制品和锁定的 Pi SDK 版本一致", () => {
  const packagePath = [resolve(testDirectory, "../package.json"), resolve(testDirectory, "../../package.json")]
    .find(existsSync);
  assert.ok(packagePath, "agent package manifest must be available from source and compiled tests");
  const harnessPath = [resolve(testDirectory, "../harness/PI_WIRE_IDENTITY.json"),
    resolve(testDirectory, "../../harness/PI_WIRE_IDENTITY.json")].find(existsSync);
  assert.ok(harnessPath, "wire identity artifact must be available from source and compiled tests");
  const shared = JSON.parse(readFileSync(harnessPath, "utf8"));
  const packageManifest = JSON.parse(readFileSync(packagePath, "utf8"));
  assert.deepEqual(CANVAS_PI_WIRE_IDENTITY, shared);
  assert.equal(CANVAS_PI_WIRE_IDENTITY.piSdkVersion, packageManifest.dependencies["@earendil-works/pi-coding-agent"]);
});

test("所有内部请求都标注独立的 Pi wire、SDK 和 session 格式版本", async () => {
  const request = await captureBody((bridge) => bridge.failRun(run, "probe", undefined));
  assert.equal(request.headers["X-Agent-Protocol-Version"], "canvas-pi-wire/v1");
  assert.equal(request.headers["X-Pi-SDK-Version"], "0.87.1");
  assert.equal(request.headers["X-Pi-Session-Format"], "3");
});

/** Mirrors what runner.ts callsFromAssistant() produces. */
function callFromModel(id: string, name: string): PiToolCall {
  return { id, type: "function", function: { name, arguments: "{}" } };
}

// The Go route /internal-agent/runs/:id/tool-batches decodes with
// DisallowUnknownFields into PiToolBatchRequest{TaskID, Calls}. Any key
// this test emits that Go does not declare rejects the WHOLE batch with an empty
// 400 — which used to strand every tool-using run in `running` forever.
test("startToolBatch emits exactly the keys the Go wire contract declares", async () => {
  const request = await captureBody((bridge) => bridge.startToolBatch(run, "task-wire", [callFromModel("call_1", "canvas_get_state")], undefined));

  assert.equal(request.method, "POST");
  assert.equal(request.url, "http://backend:8080/internal-agent/runs/run-wire/tool-batches");
  const body = request.body as { taskId: string; calls: Array<Record<string, unknown>> };
  assert.deepEqual(Object.keys(body), ["taskId", "calls"], "top level keys");
  assert.equal(body.taskId, "task-wire");
  const call = body.calls[0]!;
  assert.deepEqual(
    Object.keys(call).sort(),
    ["function", "id", "type"],
    "call keys must be a subset of the Go struct's id/type/function/thoughtSignature",
  );
  assert.equal(call.type, "function");
  assert.deepEqual(Object.keys(call.function as object).sort(), ["arguments", "name"]);
});

test("executeTool includes the model task ID that owns the call ID", async () => {
  const request = await captureBody((bridge) => bridge.executeTool(run, "task-wire", "call_4", undefined));
  assert.equal(request.method, "POST");
  assert.deepEqual(request.body, { taskId: "task-wire" });
});

test("startToolBatch forwards thoughtSignature when the upstream provides one", async () => {
  const withSignature: PiToolCall = {
    ...callFromModel("call_2", "canvas_get_state"),
    thoughtSignature: "sig-abc",
  };
  const request = await captureBody((bridge) => bridge.startToolBatch(run, "task-wire", [withSignature], undefined));
  const call = (request.body as { calls: Array<Record<string, unknown>> }).calls[0]!;
  assert.equal(call.thoughtSignature, "sig-abc");
  // Go declares thoughtSignature too, so this key must not be dropped.
  assert.deepEqual(Object.keys(call).sort(), ["function", "id", "thoughtSignature", "type"]);
});

/** Run one bridge call against a fixed HTTP status. */
async function statusError(status: number, body = "{}"): Promise<Error> {
  const original = globalThis.fetch;
  globalThis.fetch = (async () => new Response(body, { status, headers: { "Content-Type": "application/json" } })) as typeof fetch;
  try {
    await new CanvasBridge("http://backend:8080", "token", "worker-1").startToolBatch(
      run,
      "task-wire",
      [callFromModel("call_3", "canvas_get_state")],
      undefined,
    );
  } catch (error) {
    return error as Error;
  } finally {
    globalThis.fetch = original;
  }
  throw new Error("expected the bridge call to fail");
}

// server.ts only calls bridge.failRun for FatalWorkerError. A deterministic 4xx that
// stays a plain Error makes the worker retry the same request forever while still
// renewing its lease, so the run never reaches a terminal state and never gets
// reclaimed by the stalled-run watchdog.
test("deterministic 4xx responses are fatal, so the run is reported as failed instead of retried forever", async () => {
  for (const status of [400, 401, 403, 404, 422]) {
    const error = await statusError(status);
    assert.equal(error.name, "FatalWorkerError", `HTTP ${status} must be fatal`);
    assert.match(error.message, new RegExp(`HTTP ${status}`));
  }
});

test("bridge fatal errors retain only the server's public message for diagnosis", async () => {
  const error = await statusError(403, JSON.stringify({ code: 403, data: null, msg: "Agent 尚有未完成的模型或工具步骤" }));
  assert.equal(error.name, "FatalWorkerError");
  assert.match(error.message, /Agent 尚有未完成的模型或工具步骤/);
});

test("transient responses stay retryable", async () => {
  for (const status of [408, 409, 429, 500, 502, 503]) {
    const error = await statusError(status);
    assert.notEqual(error.name, "FatalWorkerError", `HTTP ${status} must stay retryable`);
  }
});

test("checkpoint persists Pi session entries with a revision and active leaf", async () => {
  const original = globalThis.fetch;
  let body: Record<string, unknown> = {};
  globalThis.fetch = (async (_url: string | URL | Request, init?: RequestInit) => {
    body = JSON.parse(String(init?.body)) as Record<string, unknown>;
    return new Response(JSON.stringify({ code: 0, data: { saved: true, sessionRevision: 2 } }), {
      status: 200, headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  try {
    const result = await new CanvasBridge("http://backend:8080", "token", "worker-1").checkpoint(
      run, 1, { role: "user", content: "continue" }, undefined, undefined,
      { revision: 1, activeLeafId: "entry-1", entries: [{ type: "message", id: "entry-1", parentId: null }] },
    );
    assert.equal(result.sessionRevision, 2);
  } finally {
    globalThis.fetch = original;
  }
  assert.deepEqual(Object.keys(body), ["sequence", "message", "sessionRevision", "activeLeafId", "sessionEntries"]);
  assert.equal(body.sessionRevision, 1);
  assert.equal(body.activeLeafId, "entry-1");
  assert.deepEqual(body.sessionEntries, [{ type: "message", id: "entry-1", parentId: null }]);
});

test("context compaction commit uses the Go operation and session revision contract", async () => {
  const entry = { type: "compaction", id: "compact-1", parentId: "leaf-1", summary: "checkpoint" };
  const request = await captureBody((bridge) => bridge.commitContextCompaction(run, "op-1", 9, entry, undefined));
  assert.equal(request.method, "POST");
  assert.equal(request.url, "http://backend:8080/internal-agent/runs/run-wire/context-compactions/op-1/commit");
  assert.deepEqual(request.body, { sessionRevision: 9, entry });
});

test("context compaction starts with the persisted Pi source revision and leaf", async () => {
  const request = await captureBody((bridge) => bridge.compactContext(run, {
    sessionRevision: 8, activeLeafId: "leaf-8", reason: "overflow", willRetry: true, tokensBefore: 14_000,
  }, undefined), {
    operationId: "op-2", status: "succeeded", summary: "checkpoint", firstKeptEntryId: "kept-2",
    details: { protocolVersion: "canvas-pi-compaction/v1", operationId: "op-2" },
  });
  assert.equal(request.method, "POST");
  assert.equal(request.url, "http://backend:8080/internal-agent/runs/run-wire/context-compactions");
  assert.deepEqual(request.body, {
    sessionRevision: 8, activeLeafId: "leaf-8", reason: "overflow", willRetry: true, tokensBefore: 14_000,
  });
});

test("restarting a worker resumes the existing Go compaction by operation ID", async () => {
  const original = globalThis.fetch;
  const requests: Array<{ url: string; method: string }> = [];
  globalThis.fetch = (async (url: string | URL | Request, init?: RequestInit) => {
    requests.push({ url: String(url), method: String(init?.method) });
    return new Response(JSON.stringify({ code: 0, data: {
      operationId: "op-resume", status: "succeeded", summary: "checkpoint",
      firstKeptEntryId: "kept-entry", details: { protocolVersion: "canvas-pi-compaction/v1", operationId: "op-resume" },
    } }), { status: 200, headers: { "Content-Type": "application/json" } });
  }) as typeof fetch;
  try {
    const result = await new CanvasBridge("http://backend:8080", "token", "worker-2")
      .resumeContextCompaction(run, "op-resume", undefined);
    assert.equal(result.operationId, "op-resume");
    assert.equal(result.summary, "checkpoint");
  } finally {
    globalThis.fetch = original;
  }
  assert.deepEqual(requests, [{
    url: "http://backend:8080/internal-agent/runs/run-wire/context-compactions/op-resume",
    method: "GET",
  }]);
});

// 提示合同：modelStep 必须把 Harness 身份发给服务端，服务端才能在首个模型步固化它、
// 之后拒绝漂移。漏发不会报错（字段是可选的），只会让"在途运行静默换提示"这个缺陷
// 无声无息地回来 —— 所以这条 wire 断言是必要的。
//
// 这里不能用上面的 captureBody：它返回空的 data，modelStep 会因为拿不到步骤状态而抛错。
// 本 helper 回一个已成功的步骤，让调用走完并留下请求体。
async function captureModelStepBody(call: (bridge: CanvasBridge) => Promise<unknown>) {
  const original = globalThis.fetch;
  const captured: { url: string; method: string; body: Record<string, unknown> }[] = [];
  globalThis.fetch = (async (url: string | URL | Request, init?: RequestInit) => {
    captured.push({
      url: String(url),
      method: String(init?.method),
      body: init?.body === undefined ? {} : JSON.parse(String(init.body)),
    });
    return new Response(
      JSON.stringify({ code: 0, data: { taskId: "task-wire", status: "succeeded", result: { text: "ok" } } }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  }) as typeof fetch;
  try {
    await call(new CanvasBridge("http://backend:8080", "token", "worker-1"));
  } finally {
    globalThis.fetch = original;
  }
  assert.equal(captured.length, 1, "exactly one request is issued");
  return captured[0]!;
}

test("modelStep 携带 harnessHash", async () => {
  const captured = await captureModelStepBody((bridge) =>
    bridge.modelStep(
      run,
      { systemPrompt: "probe", messages: [], tools: [], toolChoice: "auto" },
      undefined,
      undefined,
      "deadbeefcafe",
    ),
  );
  assert.equal(captured.body.harnessHash, "deadbeefcafe");
  assert.ok(captured.body.canonical, "原有的 canonical 必须保留");
});

test("没有 Harness 时不发送 harnessHash", async () => {
  const captured = await captureModelStepBody((bridge) =>
    bridge.modelStep(run, { systemPrompt: "probe", messages: [], tools: [], toolChoice: "auto" }),
  );
  assert.equal(captured.body.harnessHash, undefined, "未配置 Harness 的部署不应凭空带上字段");
});
