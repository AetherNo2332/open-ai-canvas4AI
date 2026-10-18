import { afterAll, expect, test } from "bun:test";
import { createCanvasCommandTransport } from "../src/services/api/canvas-commands";
import type { Command } from "../src/lib/canvas/command-contract.generated";
import { apiBaseURL, apiClient, resetAPIHost, setAPIHost } from "../src/services/api/request";
const command: Command = { protocolVersion: 1, canvasId: "画布 /1", documentEpoch: "epoch", operationId: "op/1", clientId: "client", baseRevision: 1, operations: [] };
// bun test 单进程顺序执行:本文件会用 setAPIHost 覆盖全局 apiBaseURL,
// 文件结束后必须还原,否则后续文件(live2d-loader、canvas-remote-revision 等)
// 会把相对 /api 解析成这里残留的绝对 origin。
afterAll(() => {
    resetAPIHost();
});
test("command transport captures its host URL before another host is selected", async () => {
    const original = apiClient.request;
    const hosts: string[] = [];
    apiClient.request = (async (config: any) => {
        hosts.push(config.baseURL);
        return { data: { code: 0, data: {}, msg: "ok" }, status: 200 };
    }) as typeof apiClient.request;
    try {
        setAPIHost("http://127.0.0.1:41001");
        const first = createCanvasCommandTransport();
        setAPIHost("http://127.0.0.1:41002");
        await first.snapshot("canvas");
        expect(hosts).toEqual(["http://127.0.0.1:41001/api"]);
    } finally {
        apiClient.request = original;
        setAPIHost("http://127.0.0.1:41001");
    }
});
test("resetAPIHost restores the env/default base URL after setAPIHost", () => {
    resetAPIHost(); // 先归一:前面的测试文件可能残留过绝对 host
    expect(apiBaseURL).toBe("/api"); // 测试环境未设 VITE_CANVAS_BACKEND_URL
    setAPIHost("http://127.0.0.1:41099");
    expect(apiBaseURL).toBe("http://127.0.0.1:41099/api");
    resetAPIHost();
    expect(apiBaseURL).toBe("/api");
});
test("canvas command HTTP transport routes effects and recovers original task handles", async () => {
    const calls: any[] = [];
    const api = {
        get: async (url: string, config?: unknown) => {
            calls.push({ method: "get", url, config });
            return {};
        },
        post: async (url: string, body: unknown, config?: unknown) => {
            calls.push({ method: "post", url, body, config });
            return {};
        },
    };
    const t = createCanvasCommandTransport(api as any);
    const signal = new AbortController().signal;
    await t.snapshot(command.canvasId, signal);
    await t.events(command.canvasId, 7, signal);
    await t.execute(command, signal);
    await t.receipt(command, signal);
    const effect = { type: "task" as const, request: { targetNodeId: "a", task: { mode: "text", prompt: "生成" } } };
    await t.invoke!(command, effect, signal);
    await t.recover!(command, effect, signal);
    await t.invoke!(command, { type: "previs", request: { sceneId: "scene", shotId: "shot" } }, signal);
    await t.invoke!(command, { type: "undo" }, signal);
    await t.recover!(command, { type: "redo" }, signal);
    await t.invoke!(command, { type: "resolve_uncertainty", taskId: "task/1" }, signal);
    const base = "/canvas-runtime/canvases/" + encodeURIComponent(command.canvasId);
    expect(calls.map((c) => c.url)).toEqual([
        base + "/snapshot",
        base + "/events",
        base + "/commands",
        base + "/receipts/op%2F1",
        base + "/tasks",
        base + "/task-submissions/op%2F1",
        base + "/previs-tasks",
        base + "/undo",
        base + "/receipts/op%2F1",
        "/canvas-runtime/tasks/task%2F1/uncertainty/resolve",
    ]);
    expect(calls[1].config.params).toEqual({ after: 7, limit: 250 });
    expect(calls[3].config.params).toEqual({ clientId: "client", documentEpoch: "epoch" });
    expect(calls[4].body).toEqual({ ...effect.request, command });
    expect(calls[7].body).toEqual({ documentEpoch: "epoch", clientId: "client", operationId: "op/1" });
    expect(calls.every((c) => c.config.signal === signal)).toBe(true);
});
