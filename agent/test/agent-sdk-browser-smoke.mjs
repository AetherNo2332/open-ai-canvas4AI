import { spawn } from "node:child_process";
import { mkdtemp, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const base = process.env.AGENT_SDK_BROWSER_URL;
const profile = await mkdtemp(join(tmpdir(), "canvas-sdk-browser-"));
const chrome = spawn(process.env.AGENT_SDK_CHROME || "C:/Program Files/Google/Chrome/Application/chrome.exe", [
  "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
  "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank",
], { windowsHide: true, stdio: "ignore" });

const wait = async (name, operation, timeout = 14_000) => {
  const deadline = Date.now() + timeout;
  let cause;
  while (Date.now() < deadline) {
    try { const result = await operation(); if (result) return result; }
    catch (error) { cause = error; }
    await Bun.sleep(200);
  }
  throw new Error(`${name} timed out${cause ? `: ${cause.message}` : ""}`);
};

let socket;
let evaluate;
try {
  const port = await wait("Chrome CDP port", async () => (await readFile(join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]);
  const target = await wait("Chrome page", async () => (await (await fetch(`http://127.0.0.1:${port}/json`)).json()).find(item => item.type === "page"));
  socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let next = 0;
  const pending = new Map();
  socket.onmessage = event => {
    const reply = JSON.parse(event.data);
    pending.get(reply.id)?.(reply);
    pending.delete(reply.id);
  };
  const cdp = async (method, params = {}) => {
    const id = ++next;
    const response = new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`Chrome did not answer ${method}`));
      }, 6000);
      pending.set(id, reply => { clearTimeout(timeout); resolve(reply); });
    });
    socket.send(JSON.stringify({ id, method, params }));
    const { result, error } = await response;
    if (error) throw new Error(error.message);
    return result;
  };
  evaluate = async expression => {
    const response = await cdp("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (response.exceptionDetails) throw new Error(response.result.description);
    return response.result.value;
  };
  const goto = async path => {
    await cdp("Page.navigate", { url: base + path });
    await wait(path, () => evaluate(`location.pathname + location.search === ${JSON.stringify(path)} && document.readyState === "complete"`));
  };
  const text = (value, timeout) => wait(`text ${value}`, () => evaluate(`document.body?.innerText.includes(${JSON.stringify(value)})`), timeout);
  const button = (label, timeout) => wait(`button ${label}`, () => evaluate(`(() => {
    const element = [...document.querySelectorAll("button")].find(button =>
      button.getClientRects().length && !button.disabled && button.innerText.replace(/\\s+/g,"") === ${JSON.stringify(label.replace(/\s+/g, ""))});
    if (!element) return false;
    element.click();
    return true;
  })()`), timeout);
  const input = (selector, value) => wait(`input ${selector}`, () => evaluate(`(() => {
    const element = document.querySelector(${JSON.stringify(selector)});
    if (!element || element.disabled) return false;
    const proto = element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto,"value").set.call(element,${JSON.stringify(value)});
    element.dispatchEvent(new Event("input", { bubbles: true }));
    return true;
  })()`));
  await cdp("Network.enable");
  await cdp("Network.setCookie", { url: base, name: process.env.AGENT_SDK_BROWSER_COOKIE_NAME, value: "sdk-session.sdk-token" });
  await goto("/canvas/sdk-canvas");
  await wait("canvas Agent launcher", () => evaluate(`(() => {
    const element = document.querySelector("button[title='打开 Agent 助手']");
    if (!element) return false;
    element.click();
    return true;
  })()`), 25_000);
  await wait("enable and focus the canvas Agent editor", () => evaluate(`(() => {
    const editor = document.querySelector("[contenteditable='true'][aria-placeholder*='输入消息']");
    if (!editor) return false;
    editor.focus();
    return document.activeElement === editor;
  })()`));
  await cdp("Input.insertText", { text: "SDK_CANVAS" });
  await button("发送");
  await text("SDK_RESULT: 待审阅分析建议", 50_000);
  console.log("Chrome: canvas image analysis passed");

  await goto("/settings?section=preferences");
  await text("Agent 共享输出偏好");
  await input("input[placeholder*='zh-CN']", "fr-FR");
  await button("保存共享偏好");
  await text("共享输出偏好已保存");
  await goto("/settings?section=preferences&verify=preferences");
  await wait("saved preference after page reload", () => evaluate(`document.querySelector("input[placeholder*='zh-CN']")?.value === "fr-FR"`));
  console.log("Chrome: preference save and reload passed");

  const drama = "/projects/sdk-drama/e/sdk-episode?tab=script";
  await goto(drama);
  await text("本集分析建议");
  await text("规划一组镜头");
  if (await evaluate(`document.body.innerText.includes("技能组合推荐")`)) throw new Error("canvas skill recommendations visible outside the skills library");
  await input("textarea[aria-label='分析要求']", "SDK_QUESTION");
  await button("新建分析");
  await text("选择分析重点", 45_000);
  console.log("Chrome: drama question displayed");
  await input("textarea[aria-label='分析要求']", "SDK_ANSWER");
  await button("发送自定义回答");
  await text("SDK_ANSWERED_RESULT", 50_000);
  const oldRun = await evaluate(`new URLSearchParams(location.search).get("agentRun")`);
  await goto(`/projects/isolation-0-drama-0/e/isolation-0-drama-0-ep?tab=script&agentProject=sdk-drama&agentRun=${oldRun}`);
  await text("本集分析建议");
  if (await evaluate(`document.body.innerText.includes("SDK_ANSWERED_RESULT")`)) throw new Error("old project output visible");
  await goto(drama);
  await text("本集分析建议");
  await wait("open conversation picker", () => evaluate(`(() => {
    const input = document.querySelector("[aria-label='分析会话']");
    const picker = input?.closest(".ant-select");
    if (!picker) return false;
    picker.dispatchEvent(new MouseEvent("mousedown",{bubbles:true}));
    input.focus();
    input.dispatchEvent(new KeyboardEvent("keydown",{key:"ArrowDown",code:"ArrowDown",keyCode:40,bubbles:true}));
    return true;
  })()`));
  await wait("restoring the conversation from the list", () => evaluate(`(() => {
    const option = [...document.querySelectorAll(".ant-select-item-option")].find(row => row.innerText.includes("SDK_QUESTION"));
    if (!option) return false;
    option.click(); return true;
  })()`));
  await text("SDK_ANSWERED_RESULT", 35_000);
  await button("归档会话");
  await wait("archive readback", () => evaluate(`(async () => {
    const response = await fetch("/api/agent/conversations?scopeType=drama&scopeId=sdk-drama");
    const result = await response.json();
    return result.data?.items.some(conversation => conversation.latestRunId === ${JSON.stringify(oldRun)} && conversation.archived);
  })()`));
  console.log("Chrome: drama analysis, question answer, project switch, directory restore and archive passed");

  await input("textarea[aria-label='分析要求']", "SDK_CANCEL_WAIT");
  await button("新建分析");
  await button("取消本轮");
  await text("已停止", 35_000);
  console.log("Chrome: cancelling the current turn passed");

} catch (error) {
  try { console.error("Page:", await evaluate?.(`location.href + "\\n" + document.body?.innerText.slice(-3000)`)); }
  finally { throw error; }
} finally {
  socket?.close();
  chrome.kill();
}
