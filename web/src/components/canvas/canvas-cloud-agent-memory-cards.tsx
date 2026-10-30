import { App, Button } from "antd";
import { BookMarked, Undo2 } from "lucide-react";
import { useState } from "react";

import type { canvasThemes } from "@/lib/canvas-theme";
import { undoAgentMemoryRevision } from "@/services/api/agent-memory";

type CanvasTheme = (typeof canvasThemes)[keyof typeof canvasThemes];

/** memory_updated 事件的展示数据：status 来自后端（applied / pending_review）。 */
export type CloudAgentMemoryUpdate = {
    revisionId: string;
    status: "applied" | "pending";
    summary: string;
    changes: Array<{ kind: "add" | "replace" | "remove"; section: string; before?: string; after?: string }>;
};

const SECTION_LABELS: Record<string, string> = { user: "关于用户", notes: "工作笔记" };
const CHANGE_LABELS: Record<string, string> = { add: "新增", replace: "改写", remove: "删除" };

/** 聊天里的记忆变更卡（上游 canvas-cloud-agent-memory-cards）：说明 Agent 记住或改了什么；已生效的修改可以一键撤销。 */
export function AgentMemoryUpdateCard({ update, theme }: { update: CloudAgentMemoryUpdate; theme: CanvasTheme }) {
    const { message } = App.useApp();
    const [state, setState] = useState<"idle" | "undoing" | "undone">("idle");
    const pending = update.status === "pending";
    const undo = async () => {
        if (!update.revisionId) return;
        setState("undoing");
        try {
            await undoAgentMemoryRevision(update.revisionId);
            setState("undone");
            message.success("已撤销这次记忆修改");
        } catch (error) {
            setState("idle");
            message.error(error instanceof Error ? error.message : "撤销失败");
        }
    };
    return (
        <div className="agent-status-message flex items-start gap-2 text-xs" role="status" data-agent-memory-update={update.status}>
            <span className="agent-timeline-marker relative" aria-hidden="true">
                <span className="relative grid size-5 place-items-center rounded-full" style={{ background: theme.node.fill, color: theme.accent.primary }}>
                    <BookMarked className="size-3" />
                </span>
            </span>
            <div className="min-w-0 flex-1 leading-5" style={{ color: theme.node.muted }}>
                <div className="flex flex-wrap items-center gap-x-2">
                    <span className="font-medium" style={{ color: theme.node.text }}>
                        {state === "undone" ? "已撤销这次记忆修改" : pending ? "Agent 想修改长期记忆，等你确认" : "长期记忆已更新"}
                    </span>
                    {update.summary ? <span className="opacity-70">{update.summary}</span> : null}
                </div>
                {update.changes.length ? (
                    <ul className="mt-1 space-y-0.5">
                        {update.changes.slice(0, 4).map((change, index) => (
                            <li key={`${change.kind}-${index}`} className={state === "undone" ? "line-through opacity-60" : undefined}>
                                <span className="opacity-70">
                                    {CHANGE_LABELS[change.kind] || change.kind} · {SECTION_LABELS[change.section] || change.section}：
                                </span>
                                {change.after || change.before}
                            </li>
                        ))}
                    </ul>
                ) : null}
                {pending ? <p className="mt-1 opacity-70">在「Agent 设置 → 长期记忆」里确认后才会生效。</p> : null}
            </div>
            {!pending && update.revisionId && state !== "undone" ? (
                <Button type="text" size="small" icon={<Undo2 className="size-3.5" />} loading={state === "undoing"} onClick={() => void undo()} aria-label="撤销这次记忆修改">
                    撤销
                </Button>
            ) : null}
        </div>
    );
}
