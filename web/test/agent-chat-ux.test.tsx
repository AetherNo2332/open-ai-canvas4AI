import { expect, test } from "bun:test";
import { resolve } from "node:path";
import { isValidElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentChatMessage, AgentReasoningFeed, rewriteAgentNodeLinks, type CloudAgentChatMessage } from "@/components/canvas/canvas-cloud-agent-chat-ui";
import { AgentCreateGlyph, AgentDocumentGlyph, AgentEditGlyph, AgentInventoryGlyph, AgentOtherGlyph, AgentRetryGlyph, AgentVisionGlyph, agentActivityGlyph } from "@/components/canvas/agent-activity-icons";
import { agentReplyOptionsTarget } from "@/components/canvas/canvas-agent-reply-options";
import { extractAgentReplyOptions } from "@/lib/canvas/agent-reply-options";
import { agentErrorContent } from "@/lib/canvas/agent-error-presentation";
import { canvasThemes } from "@/lib/canvas-theme";

const videoReference = {
    id: "video-1790385749473-q4157",
    nodeId: "video-1790385749473-q4157",
    kind: "video" as const,
    label: "片头镜头",
    title: "片头镜头",
    sourceType: "video" as const,
    previewUrl: "data:image/png;base64,preview",
};

const assistant = (text: string, id = "assistant-1"): CloudAgentChatMessage => ({ id, role: "assistant", text });

test("node IDs in replies become semantic links; task IDs and fenced code stay untouched", () => {
    expect(rewriteAgentNodeLinks("新节点 ID：video-1790385749473-q4157", [videoReference])).toContain("新节点：[视频节点 · 片头镜头](#agent-node:video-1790385749473-q4157)");
    const task = "任务 ag293f6d7-55b6-4a25-8c4f-8c6b2c4d9b10 已创建";
    expect(rewriteAgentNodeLinks(task)).toBe(task);
    const fenced = "```text\nvideo-1790385749473-q4157\n```";
    expect(rewriteAgentNodeLinks(fenced, [videoReference])).toBe(fenced);
    expect(rewriteAgentNodeLinks("video-custom:take-1", [{ ...videoReference, id: "video-custom:take-1", nodeId: "video-custom:take-1" }])).toContain("(#agent-node:video-custom%3Atake-1)");
    const character = { id: "text-1-c", nodeId: "text-1-c", kind: "character" as const, label: "李莫愁", title: "李莫愁", sourceType: "text" as const };
    expect(rewriteAgentNodeLinks("text-1-c", [character])).toContain("[角色卡 · 李莫愁]");
});

test("on canvas a node mention renders as a focusable chip without leaking the raw ID", () => {
    const html = renderToStaticMarkup(<AgentChatMessage item={assistant("新节点 ID：video-1790385749473-q4157")} theme={canvasThemes.dark} references={[videoReference]} onFocusNode={() => {}} />);
    expect(html).toContain('class="agent-message-node-link"');
    expect(html).toContain('data-agent-node-id="video-1790385749473-q4157"');
    expect(html).toContain("片头镜头");
    expect(html.replace(/<[^>]+>/gu, "")).not.toContain("video-1790385749473-q4157");
});

test("off canvas the same mention is plain text, not a link or button", () => {
    const html = renderToStaticMarkup(<AgentChatMessage item={assistant("新节点 ID：video-1790385749473-q4157")} theme={canvasThemes.light} references={[videoReference]} />);
    expect(html).not.toContain("agent-message-node-link");
    expect(html).not.toContain("#agent-node:");
    expect(html).not.toContain("<button");
    expect(html).toContain('class="agent-message-node-text"');
    expect(html).toContain("视频节点 · 片头镜头");
});

test("the image-to-previs user request renders 此图 as the node chip", () => {
    const image = { ...videoReference, kind: "image" as const, sourceType: "image" as const, label: "道姑", title: "道姑" };
    const item: CloudAgentChatMessage = { id: "user-previs", role: "user", text: "根据此图生成 3D 预演台", canvasReferenceNodeId: image.nodeId };
    const html = renderToStaticMarkup(<AgentChatMessage item={item} theme={canvasThemes.dark} references={[image]} onFocusNode={() => {}} />);
    expect(html).toContain('class="agent-message-node-link"');
    expect(html).toContain('class="agent-message-node-link-preview"');
    expect(html).toContain("道姑");
    expect(html).toContain("3D 预演台");
    expect(html).not.toContain("此图");
    // 节点已不在当前引用表里时，保持原句。
    expect(renderToStaticMarkup(<AgentChatMessage item={item} theme={canvasThemes.dark} references={[]} onFocusNode={() => {}} />)).toContain("根据此图生成 3D 预演台");
});

test("error cards fold the diagnostic id behind a closed disclosure and keep tone by severity", () => {
    const generic: CloudAgentChatMessage = { id: "e", role: "error", title: "Agent 执行失败", text: "Agent 执行失败" };
    for (const theme of [canvasThemes.light, canvasThemes.dark]) {
        const html = renderToStaticMarkup(<AgentChatMessage item={generic} theme={theme} />);
        expect(html.match(/Agent 执行失败/g)).toHaveLength(1);
        expect(html).toContain("agent-status-message--error");
        expect(html).not.toContain("#ef4444");
    }
    expect(agentErrorContent("Agent 执行中断，请重新发送消息；如反复出现，请把诊断号 ag-run-1 反馈给管理员", "Agent 执行失败")).toEqual({ description: "Agent 执行中断，请重新发送消息", diagnosticId: "ag-run-1" });
    const html = renderToStaticMarkup(<AgentChatMessage item={{ ...generic, text: "Agent 执行中断，请重新发送消息；如反复出现，请把诊断号 ag-run-1 反馈给管理员" }} theme={canvasThemes.dark} />);
    expect(html).toContain("Agent 执行中断，请重新发送消息</p>");
    expect(html).toMatch(/<details class="agent-error-diagnostics">/);
    expect(html).not.toMatch(/<details[^>]* open/);
    expect(html).toContain("ag-run-1");
    const warning = renderToStaticMarkup(<AgentChatMessage item={{ ...generic, errorSeverity: "warning" }} theme={canvasThemes.dark} onRetry={() => {}} retrying />);
    expect(warning).toContain("agent-status-message--warning");
    expect(warning).toContain("重试中");
    // 没有诊断号也没有 Run ID 时不渲染空的折叠区。
    expect(renderToStaticMarkup(<AgentChatMessage item={{ ...generic, text: "余额不足 <b>x</b>" }} theme={canvasThemes.light} />)).not.toContain("<details");
});

test("reply options become cards only on the last unanswered reply", () => {
    const text = "我已经准备好两种做法，你想先做哪个？\n1. 先出分镜\n2. 直接生成视频";
    expect(extractAgentReplyOptions(text)?.options.map((option) => option.text)).toEqual(["先出分镜", "直接生成视频"]);
    const messages: CloudAgentChatMessage[] = [{ id: "u", role: "user", text: "开始" }, assistant(text, "a1"), { id: "t", role: "tool", title: "canvas_get_state", text: "ok" }];
    expect(agentReplyOptionsTarget(messages)?.id).toBe("a1");
    expect(agentReplyOptionsTarget([...messages, { id: "u2", role: "user", text: "1" }])).toBeNull();
    expect(agentReplyOptionsTarget([assistant("完成了以下修改：\n1. 改标题\n2. 改颜色")])).toBeNull();

    const target = agentReplyOptionsTarget(messages)!;
    const html = renderToStaticMarkup(<AgentChatMessage item={messages[1]} theme={canvasThemes.light} replyOptions={target.options} onReplyOption={() => {}} />);
    expect(html).toContain('role="group"');
    expect(html.match(/class="agent-reply-option"/g)).toHaveLength(2);
    expect(html).toContain("--agent-option-index:1");
    // 卡片替代了正文末尾的编号列表，正文只保留引导句。
    expect(html).not.toContain("<ol");
    // 不给回调时（画布外、ask_user 已在展示）保持原文。
    expect(renderToStaticMarkup(<AgentChatMessage item={messages[1]} theme={canvasThemes.light} replyOptions={target.options} />)).not.toContain("agent-reply-option");
});

test("activity glyphs map per tool category, canvas inventory and retry", () => {
    const glyph = (...args: Parameters<typeof agentActivityGlyph>) => {
        const element = agentActivityGlyph(...args);
        expect(isValidElement(element)).toBe(true);
        return element.type;
    };
    expect(glyph("vision")).toBe(AgentVisionGlyph);
    expect(glyph("read", { canvasInventory: true })).toBe(AgentInventoryGlyph);
    expect(glyph("read")).toBe(AgentDocumentGlyph);
    expect(glyph("create")).toBe(AgentCreateGlyph);
    expect(glyph("operate")).toBe(AgentEditGlyph);
    expect(glyph("other")).toBe(AgentOtherGlyph);
    expect(glyph("operate", { retry: true })).toBe(AgentRetryGlyph);
    const reasoning = renderToStaticMarkup(<AgentReasoningFeed items={[{ id: "r", role: "assistant", reasoning: true, text: "想一想" }]} theme={canvasThemes.light} />);
    expect(reasoning).toContain("agent-activity-glyph");
    expect(reasoning).toContain('aria-hidden="true"');
});

test("streaming replies show a caret that disappears when the stream ends", () => {
    const streaming = renderToStaticMarkup(<AgentChatMessage item={{ ...assistant("正在写"), streaming: true }} theme={canvasThemes.light} isStreaming />);
    expect(streaming).toContain("is-streaming");
    const done = renderToStaticMarkup(<AgentChatMessage item={assistant("写完了")} theme={canvasThemes.light} />);
    expect(done).not.toContain("is-streaming");
});

async function chatMotionCss() {
    const css = await Bun.file(new URL("../src/components/canvas/canvas-cloud-agent.css", import.meta.url)).text();
    const start = css.indexOf("/* agent-chat-motion:start");
    const end = css.indexOf("/* agent-chat-motion:end */");
    expect(start).toBeGreaterThan(-1);
    expect(end).toBeGreaterThan(start);
    return { css, motion: css.slice(start, end) };
}

test("motion contract: only transform/opacity, skin duration tokens, no transition: all", async () => {
    const { css, motion } = await chatMotionCss();
    expect(css).not.toMatch(/transition:\s*all/u);
    for (const [, body] of motion.matchAll(/@keyframes\s+[\w-]+\s*\{([\s\S]*?)\}\s*\}/gu)) {
        const properties = [...body.matchAll(/([a-z-]+)\s*:/gu)].map((match) => match[1]);
        expect(properties.every((property) => property === "opacity" || property === "transform")).toBe(true);
    }
    for (const [, value] of motion.matchAll(/transition:\s*([^;]+);/gu)) {
        if (value.trim() === "none") continue;
        for (const part of value.split(",")) expect(part.trim()).toMatch(/^(opacity|transform)\s+var\(--motion-/u);
    }
    for (const [, value] of motion.matchAll(/animation:\s*([^;]+);/gu)) {
        if (value.trim() === "none") continue;
        expect(value).toMatch(/var\(--motion-(state|instant)\)/u);
    }
    // 圆角跟随皮肤，不写死 999px / rounded-full。
    expect(motion).not.toMatch(/border-radius:\s*(999px|50%)/u);
});

test("motion contract: reduced motion turns every chat animation off", async () => {
    const { motion } = await chatMotionCss();
    const reduced = motion.slice(motion.indexOf("@media (prefers-reduced-motion: reduce)"));
    expect(reduced.length).toBeGreaterThan(0);
    for (const selector of ["[data-agent-enter]", ".agent-reply-option", ".agent-error-message", ".agent-error-diagnostics-body", ".agent-message-body.is-streaming", ".agent-message-node-link::before", ".agent-operation-list > *"]) {
        expect(reduced).toContain(selector);
    }
    expect(reduced).toMatch(/animation:\s*none/u);
    expect(reduced).toMatch(/transition:\s*none/u);
});

test("click, prefill and memo render-count behavior in a DOM", () => {
    const result = Bun.spawnSync([process.execPath, "test", "./test/fixtures/agent-chat-ux.harness.tsx"], { cwd: resolve(import.meta.dir, ".."), timeout: 30_000 });
    const output = new TextDecoder().decode(result.stdout) + new TextDecoder().decode(result.stderr);
    expect(result.exitCode, output).toBe(0);
    expect(output).toContain("0 fail");
}, 35_000);
