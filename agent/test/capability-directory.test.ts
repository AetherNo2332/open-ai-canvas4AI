import assert from "node:assert/strict";
import test from "node:test";
import type { PiSnapshot } from "../src/bridge.js";
import { validateCapabilityDirectory } from "../src/capability-directory.js";

test("capability directory rejects incompatible, injected and replaced tool contracts", () => {
  const tool = { name: "plan_update", description: "Update plan", parameters: { type: "object" }, allowed: true };
  const original: PiSnapshot = {
    runId: "run-1", userId: "user-1", revision: 1, status: "running", request: { prompt: "plan" },
    modelLimits: { contextWindowTokens: 32000, maxOutputTokens: 1000, configured: false, source: "default" },
    canonical: {
      systemPrompt: "", messages: [], toolChoice: "auto",
      tools: [{ type: "function", function: { name: tool.name, description: tool.description, parameters: tool.parameters } }],
    },
    tools: [tool], toolCatalog: [tool.name],
    capabilityDirectory: { version: 2, hash: "a".repeat(64), definitionId: "canvas.default", definitionVersion: 2 },
  };
  validateCapabilityDirectory(original);
  validateCapabilityDirectory({ ...original, tools: [{ ...tool, allowed: false }] }, original);
  assert.throws(() => validateCapabilityDirectory({ ...original, capabilityDirectory: undefined }), /version or identity unavailable/);
  // 目录 1 是带业务 read 语义的旧合同，新版 Worker 必须拒绝接管。
  assert.throws(() => validateCapabilityDirectory({ ...original,
    capabilityDirectory: { ...original.capabilityDirectory!, version: 1 } }), /version or identity unavailable/);
  assert.throws(() => validateCapabilityDirectory({ ...original,
    capabilityDirectory: { ...original.capabilityDirectory!, version: 7 } }), /version or identity unavailable/);
  assert.throws(() => validateCapabilityDirectory({ ...original,
    capabilityDirectory: { ...original.capabilityDirectory!, definitionId: "" } }), /definition identity unavailable/);
  assert.throws(() => validateCapabilityDirectory({ ...original, toolCatalog: [tool.name, "canvas_apply_ops"] }), /allowlist disagrees/);
  assert.throws(() => validateCapabilityDirectory({ ...original, tools: [{ ...tool, description: "Changed" }] }), /schema disagrees/);
  assert.throws(() => validateCapabilityDirectory({ ...original,
    capabilityDirectory: { ...original.capabilityDirectory!, hash: "b".repeat(64) } }, original), /changed during recovery/);
});
