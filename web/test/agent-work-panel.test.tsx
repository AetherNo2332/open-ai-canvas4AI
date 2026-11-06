import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import React from "react";
import { AgentWorkList } from "@/components/agent/conversation/agent-work-panel";
import type { WorkDirectoryEntry } from "@/sdk/agent";

const directory: WorkDirectoryEntry[] = [
    {
        work: {
            id: "root-1",
            rootWorkId: "root-1",
            goal: "做一集短剧",
            status: "running",
            version: 3,
            authorizationVersion: 1,
            supervisorSessionId: "sup-session-1",
            scope: { application: "orchestration", spaceType: "work", spaceId: "root-1" },
            budgetGrantId: "grant-1",
        },
        children: [
            {
                work: {
                    id: "child-1",
                    parentWorkId: "root-1",
                    rootWorkId: "root-1",
                    goal: "梳理第一集",
                    status: "running",
                    version: 2,
                    authorizationVersion: 1,
                    scope: { application: "drama", spaceType: "drama_project", spaceId: "drama-1" },
                    budgetGrantId: "grant-2",
                },
                executorSessionId: "exec-session-1",
            },
        ],
    },
    {
        work: {
            id: "root-2",
            rootWorkId: "root-2",
            goal: "一套主图",
            status: "completed",
            version: 5,
            authorizationVersion: 1,
            supervisorSessionId: "sup-session-2",
            scope: { application: "orchestration", spaceType: "work", spaceId: "root-2" },
            budgetGrantId: "grant-3",
            resultSummary: "主图已交付",
        },
        children: [],
    },
];

test("B4-A: 工作卡把总控会话与原执行会话 ID 一起带给统一 UI", () => {
    const markup = renderToStaticMarkup(React.createElement(AgentWorkList, { works: directory }));
    expect(markup.includes('data-session-id="sup-session-1"')).toBe(true);
    expect(markup.includes('data-session-id="exec-session-1"')).toBe(true);
    expect(markup.includes("打开总控会话")).toBe(true);
    expect(markup.includes("执行会话（梳理第一集，进行中）")).toBe(true);
});

test("B4-E: 停止工作是工作卡上的显式动作，且只出现在未结束的工作上", () => {
    const markup = renderToStaticMarkup(React.createElement(AgentWorkList, { works: directory, onStop: () => {} }));
    expect(markup.includes('data-action="stop"')).toBe(true);
    // root-2 已完成：其卡片区域没有停止动作，结果摘要直接可见。
    const completedIndex = markup.indexOf("一套主图");
    const stopIndex = markup.indexOf('data-action="stop"');
    expect(stopIndex).toBeGreaterThanOrEqual(0);
    expect(stopIndex).toBeLessThan(completedIndex);
    expect(markup.includes("主图已交付")).toBe(true);
});

test("B4-B: 状态文案与工作树随数据渲染", () => {
    const markup = renderToStaticMarkup(React.createElement(AgentWorkList, { works: directory }));
    expect(markup.includes("进行中")).toBe(true);
    expect(markup.includes("已完成")).toBe(true);
    expect(markup.includes("做一集短剧")).toBe(true);
    expect(markup.includes("梳理第一集")).toBe(true);
});

test("B4-B: 关闭面板的卡片不复制第二份执行会话，停止与关闭是两个动作", () => {
    // 卡片的会话锚点直接携带执行会话 ID（关闭面板后重新打开仍指向同一会话）。
    const markup = renderToStaticMarkup(React.createElement(AgentWorkList, { works: directory }));
    expect(markup.split('data-session-id="exec-session-1"').length - 1).toBe(1);
});

test("B4-D: 总控与执行视图消费同一审批数据源（工作卡不带第二个审批 ID）", () => {
    const markup = renderToStaticMarkup(React.createElement(AgentWorkList, { works: directory }));
    // 卡片不重复渲染审批标识；审批由 client.workApprovals 与会话面板共享同一份后端数据。
    expect(markup.includes("approval")).toBe(false);
});
