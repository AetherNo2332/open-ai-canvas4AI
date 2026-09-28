import assert from "node:assert/strict";
import { test } from "node:test";
import { harnessHash, type PromptParts } from "../src/system-prompt.js";

const agents = { name: "AGENTS.md", text: "项目约定" };
const soul = { name: "SOUL.md", text: "人格设定" };
const base: PromptParts = {
  system: "服务端工作区规则",
  context: [agents, soul],
  appendSystem: "追加说明",
};

// harnessHash 的存在理由：server.ts 在 worker 启动时读一次 Harness，然后把同一个对象用于
// 它领取的**每一条**运行。改了 Harness 文件再重启，在途运行会静默换系统提示 ——
// 这个 hash 让服务端能在首个模型步固化提示合同，之后拒绝漂移。

test("harnessHash 对同一份内容稳定", () => {
  assert.equal(harnessHash(base), harnessHash({ ...base }));
  // 内容相同但对象/数组是新的引用，结果必须一致。
  assert.equal(
    harnessHash(base),
    harnessHash({
      system: "服务端工作区规则",
      context: [
        { name: "AGENTS.md", text: "项目约定" },
        { name: "SOUL.md", text: "人格设定" },
      ],
      appendSystem: "追加说明",
    }),
  );
});

test("改动任意一层都会改变 hash", () => {
  const original = harnessHash(base);
  assert.notEqual(original, harnessHash({ ...base, system: "服务端工作区规则（改）" }), "SYSTEM.md 变了");
  assert.notEqual(original, harnessHash({ ...base, appendSystem: "追加说明（改）" }), "APPEND_SYSTEM.md 变了");
  assert.notEqual(
    original,
    harnessHash({ ...base, context: [{ name: "AGENTS.md", text: "项目约定（改）" }, soul] }),
    "AGENTS.md 变了",
  );
  assert.notEqual(
    original,
    harnessHash({ ...base, context: [{ name: "AGENTS.md", text: "项目约定" }, { name: "SOUL.md", text: "人格设定（改）" }] }),
    "SOUL.md 变了",
  );
  assert.notEqual(
    original,
    harnessHash({ ...base, context: [agents] }),
    "少了一个上下文文件",
  );
  assert.notEqual(
    original,
    harnessHash({ ...base, context: [soul, agents] }),
    "上下文文件顺序变了",
  );
});

test("缺失与显式空串得到同一个身份", () => {
  // system 缺失 => 视为空串；显式空串必须得到同一个 hash，否则同一份装配会有两个身份。
  assert.equal(harnessHash({ context: [] }), harnessHash({ system: undefined, context: [], appendSystem: undefined }));
  assert.equal(harnessHash({ system: "", context: [] }), harnessHash({ context: [] }));
});

test("长度前缀编码不会让不同的分层拼出同一个 hash", () => {
  // 天真的 join("") 会让这两者都变成 "ab" —— 那样两个语义不同的 Harness 会被当成同一份，
  // 漂移检查就会漏判。长度前缀编码必须把它们区分开。
  const a = harnessHash({ system: "a", appendSystem: "b", context: [] });
  const b = harnessHash({ system: "ab", context: [] });
  assert.notEqual(a, b);

  // 上下文文件的 name 与 text 边界同样不能靠分隔符猜。
  const c = harnessHash({ context: [{ name: "AGENTS.md", text: "x" }] });
  const d = harnessHash({ context: [{ name: "AGENTS.mdx", text: "" }] });
  assert.notEqual(c, d);
});

test("hash 是 64 位十六进制（sha256）", () => {
  assert.match(harnessHash(base), /^[0-9a-f]{64}$/);
});

/**
 * 跨语言固定向量。
 *
 * Go 侧 `cloudAgentHarnessBodyDigest` 会**复算**这个哈希，用来拒绝"报一个哈希、
 * 发另一份正文"。两边各写一份期望值常量，等于在同一份输入上互相独立地验证算法：
 * 任何一侧改了编码（顺序、长度前缀、UTF-8 处理），这两个断言至少有一个会红，
 * 而不是等真实运行到首步才以 403 暴露。
 */
test("harnessHash 与 Go 侧复算使用同一份固定向量", () => {
  assert.equal(
    harnessHash({ system: "策略", appendSystem: "追加", context: [{ name: "AGENTS.md", text: "约定" }] }),
    "7c60d043bb93f77da9b71fc218fc9ab1866e29bd308a8ef9b94fe2e60f267d61",
  );
});
