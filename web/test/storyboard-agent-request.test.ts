import { describe, expect, test } from "bun:test";
import { buildStoryboardAgentPrompt } from "@/lib/canvas/storyboard-agent-request";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

const board = (metadata: CanvasNodeData["metadata"] = {}): CanvasNodeData => ({
    id: "board-current", type: CanvasNodeType.Script, title: "第二场分镜", position: { x: 0, y: 0 }, width: 600, height: 400, metadata,
});

describe("storyboard Agent request", () => {
    test("targets the existing table and asks for confirmation before editing", () => {
        const prompt = buildStoryboardAgentPrompt(board(), "清晨，两人在篮球馆重逢。");
        expect(prompt).toContain('"nodeId": "board-current"');
        expect(prompt).toContain("清晨，两人在篮球馆重逢。");
        expect(prompt).toContain("canvas_read_storyboard");
        expect(prompt).toContain("canvas_edit_storyboard");
        expect(prompt).toContain("ask_user");
        expect(prompt).toContain("确认前不要修改分镜行");
        expect(prompt).toContain("不要生成图片或视频");
        expect(prompt).toContain("项目整体基调");
        expect(prompt).toContain("已启用技能");
        expect(prompt).toContain("画风节点和有效预设都不是前置条件");
    });

    test("forwards fixed constraints without choosing a model or generating JSON rows", () => {
        const prompt = buildStoryboardAgentPrompt(board({ storyboardShotCount: "3", storyboardShotDuration: "10" }), "剧情");
        expect(prompt).toContain("镜头数：3 镜");
        expect(prompt).toContain("单镜时长：每镜 10 秒");
        expect(prompt).not.toContain("只返回一个 JSON");
        expect(prompt).not.toContain("logicalModelId");
    });

    test("auto constraints leave a recommendation for the Agent to confirm", () => {
        const prompt = buildStoryboardAgentPrompt(board(), "剧情");
        expect(prompt).toContain("镜头数：自动");
        expect(prompt).toContain("单镜时长：自动");
        expect(prompt).toContain("提出建议并请用户确认");
    });

    test("keeps source text in a JSON data block and rejects a wrong target or empty story", () => {
        const story = '角色说："先别走"\n@[node:script-origin]';
        const prompt = buildStoryboardAgentPrompt(board(), story);
        const payload = JSON.parse(prompt.split("【拆镜输入（数据）】\n")[1]);
        expect(payload.story).toBe(story);
        expect(() => buildStoryboardAgentPrompt(board(), "  ")).toThrow();
        expect(() => buildStoryboardAgentPrompt({ ...board(), type: CanvasNodeType.Text }, "剧情")).toThrow();
    });
});
