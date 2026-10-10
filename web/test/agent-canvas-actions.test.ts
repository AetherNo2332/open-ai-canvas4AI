import { expect, test } from "bun:test";
import { agentCanvasActions, agentCanvasActionLabel } from "@/lib/canvas/agent-canvas-actions";

test("persisted node actions retain human-readable titles without a live canvas", () => {
    const detail = JSON.parse(JSON.stringify({ actions: [
        { action: "created", nodeId: "video", title: "满月动画", nodeType: "video" },
        { action: "referenced", nodeId: "image", title: "满月照片", nodeType: "image" },
    ] }));
    expect(agentCanvasActions("canvas_apply_ops", detail).map(agentCanvasActionLabel)).toEqual(["创建了视频节点《满月动画》", "引用了图片节点《满月照片》"]);
});

test("server-authored canvas actions explain updated fields, renamed nodes, and both connection endpoints", () => {
    const detail = JSON.parse(JSON.stringify({ actions: [
        {
            action: "updated",
            nodeId: "video",
            title: "旧名",
            nodeType: "video",
            fields: ["节点名称", "下一版提示词"],
            resultTitle: "新名",
        },
        {
            action: "referenced",
            nodeId: "image",
            title: "参考图",
            nodeType: "image",
            targetNodeId: "video",
            targetTitle: "目标视频",
            targetNodeType: "video",
        },
    ] }));
    expect(agentCanvasActions("canvas_apply_ops", detail).map(agentCanvasActionLabel)).toEqual([
        "更新了视频节点《旧名》（修改：节点名称、下一版提示词），名称改为《新名》",
        "建立引用：图片节点《参考图》 → 视频节点《目标视频》",
    ]);
});

test("legacy generation failures retain node name and never claim creation or reference success", () => {
    const detail = { eventType: "tool_failed", arguments: JSON.stringify({ nodeId: "video", title: "满月动画", mode: "video", referenceNodeIds: ["image"] }), result: { phase: "admission", taskSubmitted: false } };
    expect(agentCanvasActions("generate_media", detail).map(agentCanvasActionLabel)).toEqual(["生成未完成：视频节点《满月动画》"]);
});

test("malformed historical arguments do not break the conversation", () => {
    expect(agentCanvasActions("generate_media", { arguments: "{" })).toEqual([]);
});

test("structural operation receipts name the committed action and connection endpoints", () => {
    for (const [action, verb] of Object.entries({ deleted: "删除了", duplicated: "复制了", grouped: "调整归属：", reordered: "调整顺序：", edited: "编辑了" })) {
        expect(agentCanvasActionLabel({ nodeId: "n", title: "剧本", nodeType: "text", action })).toBe(`${verb}文本节点《剧本》`);
    }
    for (const [action, verb] of Object.entries({ disconnected: "断开引用：", reconnected: "重连引用：" })) {
        expect(agentCanvasActionLabel({ nodeId: "n", title: "参考", nodeType: "image", action, targetTitle: "镜头", targetNodeType: "video" })).toBe(`${verb}图片节点《参考》 → 视频节点《镜头》`);
    }
    const actions = agentCanvasActions("canvas_apply_ops", { arguments: { ops: [{ type: "delete_node", id: "old" }, { type: "duplicate_node", id: "copy", sourceNodeId: "old" }, { type: "replace_text", id: "text", match: "private", replacement: "private" }, { type: "set_parent", id: "copy", parentId: "" }, { type: "reorder_nodes", nodeIds: ["copy", "text"] }] } });
    expect(actions.map((item) => item.action)).toEqual(["deleted", "duplicated", "edited", "grouped", "reordered", "reordered"]);
    expect(JSON.stringify(actions)).not.toContain("private");
});

test("content read receipts distinguish full content from the last page alone", () => {
    const result = { nodeId: "text", offset: 0, nextOffset: 12, totalCharacters: 12, hasMore: false, content: "private text" };
    expect(agentCanvasActions("canvas_read_content", { result })[0].action).toBe("content_read");
    expect(agentCanvasActions("canvas_read_content", { result: { ...result, offset: 6 } })[0].action).toBe("content_read_partial");
    expect(agentCanvasActionLabel({ nodeId: "text", title: "剧本", nodeType: "text", action: "content_read_partial" })).toBe("读取了部分正文：文本节点《剧本》");
    expect(JSON.stringify(agentCanvasActions("canvas_read_content", { result }))).not.toContain("private");
});

test("native drawing receipts report record reads and committed edits", () => {
    expect(agentCanvasActions("canvas_read_drawing", { result: { nodeId: "drawing", offset: 0, nextOffset: 2, totalRecords: 2, hasMore: false } })[0].action).toBe("drawing_read");
    expect(agentCanvasActions("canvas_read_drawing", { result: { nodeId: "drawing", offset: 1, nextOffset: 2, totalRecords: 2, hasMore: false } })[0].action).toBe("drawing_read_partial");
    expect(agentCanvasActions("canvas_edit_drawing", { arguments: { nodeId: "drawing", operations: [] } })[0].action).toBe("edited");
});

test("asset binding and character creation retain target identity without leaking payload content", () => {
    const bound = agentCanvasActions("canvas_bind_asset", { arguments: { nodeId: "image", assetId: "private-asset" }, result: { nodeId: "image" } });
    expect(bound[0].action).toBe("bound");
    expect(JSON.stringify(bound)).not.toContain("private");
    expect(agentCanvasActionLabel({ ...bound[0], title: "镜头", nodeType: "image" })).toBe("替换了图片节点《镜头》的素材");
    const character = agentCanvasActions("canvas_create_character", { arguments: { nodeId: "card", name: "女主", definition: { appearance: "private" } }, result: { nodeId: "card" } });
    expect(character[0]).toMatchObject({ nodeId: "card", title: "女主", nodeType: "character", action: "created" });
    expect(JSON.stringify(character)).not.toContain("private");
});
