import { describe, expect, it } from "bun:test";
import { AGENT_PANEL_MIN_CANVAS_WIDTH, AGENT_PANEL_WIDTH, agentPanelIsCompact, agentPanelWidth } from "@/lib/canvas/agent-panel-layout";
import { agentErrorPresentation, agentSubmissionErrorTitle } from "@/lib/canvas/agent-error-presentation";
import { ApiError } from "@/services/api/request";

describe("Agent window layout", () => {
    it("docks at a fixed width when the viewport is wide enough", () => {
        expect(agentPanelWidth({ width: 1280, height: 800 })).toBe(AGENT_PANEL_WIDTH);
        expect(agentPanelIsCompact({ width: 1280, height: 800 })).toBe(false);
    });
    it("shrinks with the viewport only below the dockable threshold", () => {
        const threshold = AGENT_PANEL_WIDTH + AGENT_PANEL_MIN_CANVAS_WIDTH;
        expect(agentPanelIsCompact({ width: threshold, height: 800 })).toBe(false);
        expect(agentPanelIsCompact({ width: threshold - 1, height: 800 })).toBe(true);
        expect(agentPanelWidth({ width: 700, height: 800 })).toBe(700);
    });
    it("never returns a width wider than the viewport", () => {
        expect(agentPanelWidth({ width: 320, height: 600 })).toBe(320);
        expect(agentPanelWidth({ width: 200, height: 600 })).toBe(200);
    });
});

describe("Agent error semantics", () => {
    it("uses HTTP status rather than matching error wording", () => {
        expect(agentErrorPresentation(new ApiError("Not found", { status: 404 })).title).toBe("Agent 接口或资源不存在");
        expect(agentErrorPresentation(new Error("HTTP 404"))).toEqual({ title: "Agent 执行失败", text: "HTTP 404" });
    });
    it("does not falsely claim that a disabled model channel is still enabled", () => {
        const result = agentErrorPresentation(new ApiError("系统渠道不存在或已停用", { status: 404 }));
        expect(result.text).toBe("系统渠道不存在或已停用");
        expect(JSON.stringify(result)).not.toContain("没有被停用");
    });
    it("preserves authentication, quota, and model errors", () => {
        for (const status of [401, 403, 429, 500]) {
            expect(agentErrorPresentation(new ApiError("真实业务错误", { status }), "请求失败")).toEqual({ title: "请求失败", text: "真实业务错误" });
        }
    });
    it("reports unimplemented endpoints without retry promises", () => {
        expect(agentErrorPresentation(new ApiError("Not implemented", { status: 501 })).title).toBe("当前 Agent 能力尚未开放");
    });
    it("distinguishes confirmed server failures from missing responses", () => {
        expect(agentSubmissionErrorTitle(new ApiError("系统处理失败", { status: 500 }), false)).toBe("服务端已返回错误；重试将核对原请求，不重复创建");
        expect(agentSubmissionErrorTitle(new ApiError("参数错误", { status: 400 }), false)).toBe("请求已被服务端拒绝");
        expect(agentSubmissionErrorTitle(new TypeError("network failed"), false)).toBe("未收到服务端确认；重试将核对原请求，不重复创建");
        expect(agentSubmissionErrorTitle(undefined, true)).toBe("运行已接收，但本地提交记录清理失败");
    });
});
