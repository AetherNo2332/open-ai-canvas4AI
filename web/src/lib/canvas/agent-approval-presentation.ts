import type { AgentApprovalPreview, AgentApprovalPreviewItem, AgentApprovalPreviewOperation } from "@/services/api/agent";

type RecordValue = Record<string, unknown>;

export type AgentApprovalPresentation = AgentApprovalPreview & { source: "server" | "fallback" };

function record(value: unknown): RecordValue | null {
    return value && typeof value === "object" && !Array.isArray(value) ? value as RecordValue : null;
}

function text(value: unknown, max = 240): string {
    if (typeof value !== "string") return "";
    const compact = value.replace(/[\u0000-\u001f\u007f]/gu, " ").trim();
    return compact.length > max ? `${compact.slice(0, max)}…` : compact;
}

function approvalArguments(value: unknown): RecordValue | null {
    if (typeof value !== "string") return record(value);
    try {
        return record(JSON.parse(value));
    } catch {
        return null;
    }
}

function shortReference(value: unknown): string {
    const id = text(value, 80);
    if (!id) return "未知目标";
    return id.length <= 16 ? id : `${id.slice(0, 8)}…${id.slice(-4)}`;
}

function nodeTypeLabel(value: unknown): string {
    const type = text(value, 32).toLowerCase();
    return ({ text: "文本", markdown: "Markdown", image: "图片", video: "视频", audio: "音频", frame: "画框", script: "脚本" } as Record<string, string>)[type] || (type ? "节点" : "");
}

function fieldLabel(key: string, nodeType: string): string {
    if (key === "title") return "节点名称";
    if (key === "content") return ["image", "video", "audio"].includes(nodeType.toLowerCase()) ? "下一版提示词" : "正文";
    return key === "composerContent" ? "下一版提示词" : "已配置字段";
}

function operation(value: unknown): AgentApprovalPreviewOperation | null {
    return typeof value === "string" && Object.hasOwn(AGENT_APPROVAL_OPERATION_LABELS, value) ? value as AgentApprovalPreviewOperation : null;
}

export const AGENT_APPROVAL_OPERATION_LABELS: Record<AgentApprovalPreviewOperation, string> = {
    add_node: "新增", update_node: "修改", delete_node: "删除", duplicate_node: "复制", connect_nodes: "连线", delete_connection: "断开连线", update_connection: "重连", set_parent: "分组", replace_text: "编辑正文", reorder_nodes: "调整顺序", reorder_rows: "调整行序", arrange_nodes: "整理", generate_media: "生成", create_storyboard: "创建分镜", edit_storyboard: "修改分镜", edit_drawing: "修改绘图", bind_asset: "绑定素材", canvas_undo: "撤销", canvas_redo: "重做", create_character: "创建角色卡", plan_step: "计划",
};

function normalizeServerPreview(value: unknown): AgentApprovalPresentation | null {
    const source = record(value);
    if (!source || !Array.isArray(source.items)) return null;
    const rawItems = source.items;
    const items: AgentApprovalPreviewItem[] = [];
    for (const raw of rawItems) {
        const item = record(raw);
        const op = operation(item?.operation);
        const summary = text(item?.summary);
        if (!item || !op || !summary) continue;
        items.push({
            operation: op,
            nodeId: text(item.nodeId, 120) || undefined,
            nodeTitle: text(item.nodeTitle, 120) || undefined,
            resultTitle: text(item.resultTitle, 120) || undefined,
            nodeType: text(item.nodeType, 32) || undefined,
            nodeTypeLabel: text(item.nodeTypeLabel, 48) || undefined,
            targetNodeId: text(item.targetNodeId, 120) || undefined,
            targetNodeTitle: text(item.targetNodeTitle, 120) || undefined,
            targetNodeType: text(item.targetNodeType, 32) || undefined,
            fields: Array.isArray(item.fields) ? item.fields.map((field) => text(field, 64)).filter(Boolean).slice(0, 12) : undefined,
            details: Array.isArray(item.details) ? item.details.map((detail) => text(detail, 180)).filter(Boolean).slice(0, 12) : undefined,
            summary,
        });
    }
    const title = text(source.title, 120);
    const description = text(source.description, 500);
    if (!title || !description || !items.length) return null;
    return { kind: text(source.kind, 64) || "agent_approval", title, description, items, source: "server" };
}

function canvasFallback(args: RecordValue | null): AgentApprovalPresentation {
    const rawOps = Array.isArray(args?.ops) ? args.ops : [];
    const items: AgentApprovalPreviewItem[] = [];
    for (const raw of rawOps) {
        const op = record(raw);
        const kind = operation(op?.type);
        if (!op || !kind || kind === "generate_media") continue;
        const type = text(op.nodeType, 32).toLowerCase();
        const label = nodeTypeLabel(type);
        if (kind === "add_node") {
            const title = text(op.title, 120) || `未命名${label || "节点"}`;
            items.push({ operation: kind, nodeTitle: title, nodeType: type || undefined, nodeTypeLabel: label || undefined, fields: ["节点名称", label === "文本" || label === "Markdown" ? "正文" : "初始内容"], summary: `新增${label || "节点"}《${title}》` });
            continue;
        }
        if (kind === "update_node") {
            const patch = record(op.patch);
            const id = shortReference(op.id);
            const fields = patch ? Object.keys(patch).map((key) => fieldLabel(key, type)).filter(Boolean) : [];
            const resultTitle = text(patch?.title, 120);
            const target = `目标节点（引用 ${id}）`;
            items.push({ operation: kind, nodeTitle: target, nodeType: type || undefined, nodeTypeLabel: label || undefined, resultTitle: resultTitle || undefined, fields: fields.length ? [...new Set(fields)] : ["已配置字段"], summary: `修改${label || "节点"}《${target}》的${fields.length ? [...new Set(fields)].join("、") : "已配置字段"}${resultTitle ? `，名称改为《${resultTitle}》` : ""}` });
            continue;
        }
        if (kind !== "connect_nodes") {
            const target = `目标（引用 ${shortReference(kind === "duplicate_node" ? op.sourceNodeId : op.id)}）`;
            const count = Array.isArray(op.nodeIds) ? op.nodeIds.length : Array.isArray(op.rowIds) ? op.rowIds.length : 0;
            const summary = ({ delete_node: `删除节点《${target}》`, duplicate_node: `复制节点《${target}》`, delete_connection: `断开连线《${target}》`, update_connection: `重连《${target}》`, set_parent: op.parentId ? `调整节点《${target}》的归属` : `让节点《${target}》脱离分组`, replace_text: `精确替换节点《${target}》的正文片段`, reorder_nodes: `调整 ${count} 个节点的顺序`, reorder_rows: `调整《${target}》的 ${count} 行顺序` } as Record<string, string>)[kind] || `${AGENT_APPROVAL_OPERATION_LABELS[kind]}《${target}》`;
            items.push({ operation: kind, nodeTitle: target, summary });
            continue;
        }
        const from = shortReference(op.fromNodeId);
        const to = shortReference(op.toNodeId);
        items.push({ operation: kind, nodeTitle: `来源（引用 ${from}）`, targetNodeTitle: `目标（引用 ${to}）`, summary: `建立来源（引用 ${from}）→目标（引用 ${to}）的引用连线` });
    }
    const counts = items.reduce<Record<string, number>>((out, item) => { out[item.operation] = (out[item.operation] || 0) + 1; return out; }, {});
    const parts = [counts.add_node ? `新增 ${counts.add_node} 个节点` : "", counts.update_node ? `修改 ${counts.update_node} 个节点` : "", counts.connect_nodes ? `建立 ${counts.connect_nodes} 条引用连线` : "", ...Object.entries(counts).filter(([kind]) => !["add_node", "update_node", "connect_nodes"].includes(kind)).map(([kind, count]) => `${AGENT_APPROVAL_OPERATION_LABELS[kind as AgentApprovalPreviewOperation]} ${count} 项`)].filter(Boolean);
    return { source: "fallback", kind: "canvas_mutation", title: "确认画布修改", description: parts.length ? `Agent 准备${parts.join("，")}。历史审批缺少画布快照，以下目标使用安全引用标识；无法确认具体目标时不会代替当前画布。` : "审批参数不完整，无法确认具体修改目标，请重新读取画布后再申请审批。", items, };
}

function mediaFallback(args: RecordValue | null, payload: RecordValue | null): AgentApprovalPresentation {
    const mode = text(args?.mode, 32).toLowerCase();
    const type = mode === "video" ? "视频" : mode === "image" ? "图片" : mode === "audio" ? "音频" : "媒体";
    const references = Array.isArray(args?.referenceNodeIds) ? args.referenceNodeIds.filter((id) => typeof id === "string") : [];
    const title = text(args?.title, 120) || `未命名${type}`;
    const details = [text(payload?.modelName) ? `模型：${text(payload?.modelName)}` : text(args?.channelModelKey) || text(args?.logicalModelId) ? `模型：${text(args?.channelModelKey) || text(args?.logicalModelId)}` : "", references.length ? `引用 ${references.length} 个画布资产，并建立连线` : "不引用画布媒体资产", typeof args?.durationSeconds === "number" && args.durationSeconds > 0 ? `时长：${args.durationSeconds} 秒` : "", text(args?.size) ? `画幅：${text(args?.size, 40)}` : "", text(args?.quality) ? `质量：${text(args?.quality, 40)}` : "", typeof args?.videoGenerateAudio === "boolean" ? `音频：${args.videoGenerateAudio ? "开启" : "关闭"}` : ""].filter(Boolean);
    return { source: "fallback", kind: "media_generation", title: `确认生成${type}`, description: `${type}草稿节点和引用连线已创建，尚未提交生成。确认规格后批准才会提交收费任务；拒绝则保留草稿，结果自动回写画布。`, items: [{ operation: "generate_media", nodeTitle: title, nodeType: mode || undefined, nodeTypeLabel: type, details, summary: `生成${type}《${title}》` }] };
}

export function agentApprovalPresentation(detail: unknown): AgentApprovalPresentation {
    const payload = record(detail);
    const serverPreview = normalizeServerPreview(payload?.preview);
    if (serverPreview) return serverPreview;
    const call = record(payload?.call);
    const fn = record(call?.function);
    const name = text(fn?.name) || text(payload?.toolName);
    const args = approvalArguments(fn?.arguments ?? payload?.arguments);
    if (name === "canvas_apply_ops") return canvasFallback(args);
    if (name === "canvas_edit_drawing") return { source: "fallback", kind: "canvas_mutation", title: "确认绘图修改", description: "Agent 准备修改绘图原生记录；批准后才会写入画布。", items: [{ operation: "edit_drawing", nodeTitle: `目标（引用 ${shortReference(args?.nodeId)}）`, summary: `修改绘图的 ${Array.isArray(args?.operations) ? args.operations.length : 0} 个原生记录` }] };
    if (name === "canvas_bind_asset") return { source: "fallback", kind: "canvas_mutation", title: "确认替换素材", description: "Agent 准备使用素材库内容替换节点；批准后才会写入画布。", items: [{ operation: "bind_asset", nodeTitle: `目标（引用 ${shortReference(args?.nodeId)}）`, summary: "替换目标节点的素材内容" }] };
    if (name === "canvas_undo" || name === "canvas_redo") return { source: "fallback", kind: "canvas_history", title: `${name === "canvas_undo" ? "撤销" : "重做"}画布操作`, description: "批准后恢复当前运行的画布历史快照。", items: [{ operation: name, summary: `${name === "canvas_undo" ? "撤销" : "重做"}本次运行的最新画布操作` }] };
    if (name === "canvas_create_character") return { source: "fallback", kind: "canvas_mutation", title: "确认创建角色卡", description: "批准后创建角色库记录并放置画布节点。", items: [{ operation: "create_character", nodeTitle: text(args?.name, 120) || "角色卡", summary: `创建角色卡《${text(args?.name, 120) || "角色卡"}》` }] };
    if (name === "generate_media") return mediaFallback(args, payload);
    return { source: "fallback", kind: "agent_approval", title: "确认执行操作", description: "Agent 请求执行一项操作，但审批参数无法识别；请重新读取画布后再申请审批。", items: [] };
}
