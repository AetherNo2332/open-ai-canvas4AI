import assert from "node:assert/strict";
import test from "node:test";
import { workerSlotId } from "../src/worker-runtime.js";

// 后端租约契约：worker id 必须匹配 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`。
// 主机名来自操作系统，非 ASCII 或带空格的机器名此前会直接抛 "Invalid worker slot"。
const workerIdContract = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

test("worker slots stay inside the backend contract for non-ASCII hostnames", () => {
  assert.equal(workerSlotId("你猜猜我是谁", 4321, "1"), "agent-host-4321-1");
  assert.equal(workerSlotId("DESKTOP-ABC", 777, "events"), "DESKTOP-ABC-777-events");
  assert.match(workerSlotId("你猜猜我是谁", 4321, "1"), workerIdContract);
});

test("worker slots slug spaces and punctuation instead of dropping the host", () => {
  assert.equal(workerSlotId("my host!name", 42, "2"), "my-host-name-42-2");
  assert.equal(workerSlotId("...weird...", 42, "3"), "weird-42-3");
  assert.match(workerSlotId("my host!name", 42, "2"), workerIdContract);
});

test("worker slots stay bounded for very long hostnames and suffixes", () => {
  const slot = workerSlotId("a".repeat(200), 42, "b".repeat(200));
  assert.ok(slot.length <= 64, `length ${slot.length}`);
  assert.match(slot, workerIdContract);
});
