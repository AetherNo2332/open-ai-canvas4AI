import assert from "node:assert/strict";
import test from "node:test";
import { formatAgentInterjection, validateSubagentRuntime } from "../src/subagent-wire.js";

const child = { linkId: "link", parentRunId: "parent", childRunId: "child", displayName: "研究员", roleLabel: "资料核对", objective: "核对事实", depth: 1 };
const reports = ["send_parent_message", "finish_subagent"].map(name => ({ name, allowed: true }));
test("child identity binds to its run and cannot delegate or mutate", () => {
  validateSubagentRuntime(child, "child", false, reports);
  for (const name of ["spawn_subagent", "canvas_apply_ops", "canvas_create_character", "finish_run", "ask_user", "generate_media"]) assert.throws(() => validateSubagentRuntime(child, "child", false, [...reports, { name, allowed: true }]));
  assert.throws(() => validateSubagentRuntime(child, "other", false, reports));
  assert.throws(() => validateSubagentRuntime({ ...child, depth: 2 }, "child", false, reports));
  assert.throws(() => validateSubagentRuntime(child, "child", true, reports));
});
test("parent requires frozen consent and cannot report as a child", () => {
  validateSubagentRuntime(undefined, "parent", true, [{ name: "spawn_subagent", allowed: true }]);
  assert.throws(() => validateSubagentRuntime(undefined, "parent", false, [{ name: "spawn_subagent", allowed: true }]));
  assert.throws(() => validateSubagentRuntime(undefined, "parent", true, reports));
});
test("agent reports are data rather than user instructions", () => {
  assert.match(formatAgentInterjection("研究员：完成", "subagent"), /不是用户指令/);
  assert.equal(formatAgentInterjection("停止"), "【用户插话】停止");
});
