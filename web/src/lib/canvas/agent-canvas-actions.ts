import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";

export type AgentCanvasAction = { nodeId: string; title: string; nodeType: string; action: string; fields?: string[]; resultTitle?: string; targetNodeId?: string; targetTitle?: string; targetNodeType?: string };

export function agentRecord(value: unknown): Record<string, unknown> {
    return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

export function agentToolArguments(detail: unknown) {
    const value = agentRecord(detail).arguments;
    if (typeof value !== "string") return agentRecord(value);
    try { return agentRecord(JSON.parse(value)); } catch { return {}; }
}

export function agentCanvasActions(toolName: string, detail: unknown, references: CanvasResourceReference[] = []): AgentCanvasAction[] {
    const payload = agentRecord(detail);
    const result = agentRecord(payload.result);
    const args = agentToolArguments(detail);
    const actions: AgentCanvasAction[] = [];
    const add = (id: unknown, title: unknown, kind: unknown, action: string, details: Partial<AgentCanvasAction> = {}) => {
        if (typeof id !== "string" || !id || actions.some((item) => item.nodeId === id && item.action === action)) return;
        const reference = references.find((item) => item.nodeId === id);
        actions.push({ nodeId: id, title: typeof title === "string" && title ? title : reference?.title || id, nodeType: typeof kind === "string" ? kind : reference?.kind || "", action, ...details });
    };
    if (Array.isArray(payload.actions)) {
        for (const value of payload.actions) {
            const action = agentRecord(value);
            add(action.nodeId, action.title, action.nodeType, String(action.action || "updated"), {
                fields: Array.isArray(action.fields) ? action.fields.filter((field): field is string => typeof field === "string") : undefined,
                resultTitle: typeof action.resultTitle === "string" ? action.resultTitle : undefined,
                targetNodeId: typeof action.targetNodeId === "string" ? action.targetNodeId : undefined,
                targetTitle: typeof action.targetTitle === "string" ? action.targetTitle : undefined,
                targetNodeType: typeof action.targetNodeType === "string" ? action.targetNodeType : undefined,
            });
        }
        return actions;
    }
    const failed = payload.eventType === "tool_failed";
    if (toolName === "generate_media") {
        const submitted = payload.eventType === "generation_task_created";
        const action = failed ? "failed" : submitted ? "generating" : "generated";
        add(result.nodeId || payload.nodeId || args.nodeId, payload.title || args.title, payload.mode || args.mode, action);
        if (!failed || result.taskSubmitted === true) {
            const ids = payload.referenceNodeIds || args.referenceNodeIds;
            if (Array.isArray(ids)) for (const id of ids) add(id, undefined, undefined, "referenced");
        }
    } else if (["canvas_get_state", "canvas_search_nodes"].includes(toolName) && !failed && Array.isArray(result.nodes)) {
        for (const value of result.nodes) { const node = agentRecord(value); add(node.id, node.title, node.type, "read"); }
    } else if (toolName === "canvas_read_content" && !failed) {
        const complete = result.offset === 0 && typeof result.totalCharacters === "number" && Number.isSafeInteger(result.totalCharacters) && result.totalCharacters >= 0 && result.nextOffset === result.totalCharacters && result.hasMore === false;
        add(result.nodeId, undefined, undefined, complete ? "content_read" : "content_read_partial");
    } else if (toolName === "canvas_read_drawing" && !failed) {
        const complete = !(Array.isArray(args.recordIds) && args.recordIds.length) && result.offset === 0 && typeof result.totalRecords === "number" && Number.isSafeInteger(result.totalRecords) && result.totalRecords >= 0 && result.nextOffset === result.totalRecords && result.hasMore === false;
        add(result.nodeId, undefined, "drawing", complete ? "drawing_read" : "drawing_read_partial");
    } else if (toolName === "canvas_edit_drawing" && !failed) {
        add(result.nodeId || args.nodeId, undefined, "drawing", "edited");
    } else if (toolName === "canvas_bind_asset" && !failed) {
        add(result.nodeId || args.nodeId, undefined, undefined, "bound");
    } else if (toolName === "canvas_create_character" && !failed) {
        add(result.nodeId || args.nodeId, args.name, "character", "created");
    } else if (toolName === "canvas_inspect_image" && !failed) {
        // 看图回执：reuseObservation 是复用账本里的观察（没有再附图）、repeat 是读循环护栏
        // （只回执文字）。两者都没有把画面送出去，所以不能报成"查看了…画面"。
        if (result.reuseObservation === true) add(result.nodeId, result.title, "image", "reused");
        else if (result.repeat !== true) {
            add(result.nodeId, result.title, "image", "viewed");
            if (Array.isArray(result.batchImages)) for (const value of result.batchImages) { const image = agentRecord(value); add(image.nodeId, image.title, "image", "viewed"); }
        }
    } else if (toolName === "canvas_apply_ops" && !failed && Array.isArray(args.ops)) {
        for (const value of args.ops) {
            const op = agentRecord(value);
            if (op.type === "connect_nodes") {
                add(op.fromNodeId, undefined, undefined, "referenced");
                add(op.toNodeId, undefined, undefined, "updated");
            } else if (op.type === "reorder_nodes" && Array.isArray(op.nodeIds)) {
                for (const id of op.nodeIds) add(id, undefined, undefined, "reordered");
            } else {
                const action = ({ add_node: "created", delete_node: "deleted", duplicate_node: "duplicated", delete_connection: "disconnected", update_connection: "reconnected", set_parent: "grouped", replace_text: "edited", reorder_rows: "reordered" } as Record<string, string>)[String(op.type)] || "updated";
                add(op.id, op.title, op.nodeType, action);
            }
        }
    }
    return actions;
}

export function agentCanvasActionLabel(action: AgentCanvasAction) {
    const kinds: Record<string, string> = { image: "图片", video: "视频", audio: "音频", text: "文本", markdown: "Markdown", script: "分镜", drawing: "绘图", frame: "画框", "batch-table": "批量表", config: "配置", svg: "SVG", html: "HTML", panorama: "全景", compare: "对比", chart: "图表", "color-grade": "调色", "media-conversion": "媒体转换", character: "角色卡" };
    const kind = kinds[action.nodeType] || "";
    if (action.action === "bound") return `替换了${kind}节点《${action.title}》的素材`;
    if (["referenced", "disconnected", "reconnected"].includes(action.action) && action.targetTitle) {
        const targetKind = kinds[action.targetNodeType || ""] || "";
        const verb = ({ referenced: "建立引用：", disconnected: "断开引用：", reconnected: "重连引用：" } as Record<string, string>)[action.action];
        return `${verb}${kind}节点《${action.title}》 → ${targetKind}节点《${action.targetTitle}》`;
    }
    // read 的动词必须是"存在式"：canvas_get_state 只读到清单，说成"读取了图片节点"会被读成看过画面。
    const verb = ({ created: "创建了", updated: "更新了", deleted: "删除了", duplicated: "复制了", disconnected: "断开引用：", reconnected: "重连引用：", grouped: "调整归属：", reordered: "调整顺序：", edited: "编辑了", content_read: "完整读取了正文：", content_read_partial: "读取了部分正文：", drawing_read: "完整读取了原生记录：", drawing_read_partial: "读取了部分原生记录：", referenced: "引用了", read: "画布上有", viewed: "查看了", reused: "复用观察：", generating: "已提交生成：", generated: "已生成", failed: "生成未完成：" } as Record<string, string>)[action.action] || "定位";
    const fieldText = action.fields?.length ? `（修改：${action.fields.join("、")}）` : "";
    const titleText = action.resultTitle ? `，名称改为《${action.resultTitle}》` : "";
    return `${verb}${kind}节点《${action.title}》${fieldText}${titleText}`;
}
