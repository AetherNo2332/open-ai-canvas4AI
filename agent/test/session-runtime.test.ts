import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import { ModelRuntime, type SessionEntry } from "@earendil-works/pi-coding-agent";

import { createSessionRuntime } from "../src/session-runtime.js";

test("session runtime restores one durable entry tree with isolated workspace state", async () => {
  const root = mkdtempSync(join(tmpdir(), "canvas-session-runtime-"));
  try {
    const cwd = join(root, "cwd");
    const agentDir = join(root, "agent");
    const runtime = await ModelRuntime.create({ modelsPath: null, refreshOnCreate: false, allowModelNetwork: false });
    runtime.registerProvider("probe", {
      name: "Session Runtime Probe",
      api: "probe-api",
      baseUrl: "http://session-runtime.invalid/v1",
      models: [{ id: "probe-model", name: "Probe", api: "probe-api", reasoning: false,
        input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 8_192, maxTokens: 1_024 }],
      streamSimple: () => createAssistantMessageEventStream(),
    } as never);
    await runtime.setRuntimeApiKey("probe", "session-runtime-test");
    const model = runtime.getModel("probe", "probe-model");
    assert.ok(model);

    const entry = { type: "message", id: "entry-1", parentId: null, timestamp: new Date().toISOString(),
      message: { role: "user", content: "恢复" } } as Record<string, unknown>;
    const boot = await createSessionRuntime({
      workspace: { cwd, agentDir, cleanup: () => {} }, sessionId: "session-runtime-test",
      entries: [entry as unknown as SessionEntry], systemPrompt: "只用于测试", extensionFactories: [], toolNames: [],
      provider: { id: "probe", name: "Session Runtime Probe", api: "probe-api",
        baseUrl: "http://session-runtime.invalid/v1", model, streamSimple: () => createAssistantMessageEventStream() },
    });
    try {
      assert.ok(boot.sessionManager.getEntries().some((item) => item.id === "entry-1"));
      assert.equal(boot.sessionManager.getSessionId(), "session-runtime-test");
      assert.notEqual(boot.session.settingsManager, undefined);
    } finally {
      boot.cleanup();
    }
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
