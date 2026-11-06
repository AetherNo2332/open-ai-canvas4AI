import { useMemo, useState } from "react";
import { App, Drawer, Tag } from "antd";

// AgentWorkList 是工作树卡片的纯展示：数据与回调都来自调用方，便于真实页面与测试共用。
export function AgentWorkList({ works, onOpenSession, onStop }: { works: WorkDirectoryEntry[]; onOpenSession?: (sessionId: string, scope: Scope, title: string) => void; onStop?: (workId: string, expectedVersion: number) => void }) {
    return (
        <>
            {works.map((entry) => {
                const live = entry.work.status === "running" || entry.work.status === "waiting_user" || entry.work.status === "paused";
                return (
                    <div key={entry.work.id} className="agent-work-card" data-work-id={entry.work.id} style={{ border: "1px solid var(--color-border-secondary, #eee)", borderRadius: 8, padding: 12, marginBottom: 12 }}>
                        <div style={{ display: "flex", justifyContent: "space-between", gap: 8 }}>
                            <strong style={{ wordBreak: "break-all" }}>{entry.work.goal}</strong>
                            <Tag color={statusColor(entry.work.status)}>{workStatusLabels[entry.work.status] ?? entry.work.status}</Tag>
                        </div>
                        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 8 }}>
                            {entry.work.supervisorSessionId ? (
                                <a data-session-id={entry.work.supervisorSessionId} onClick={() => onOpenSession?.(entry.work.supervisorSessionId!, entry.work.scope, `总控：${entry.work.goal}`)}>
                                    打开总控会话
                                </a>
                            ) : null}
                            {(entry.children ?? []).map((child) =>
                                child.executorSessionId ? (
                                    <a key={child.work.id} data-session-id={child.executorSessionId} onClick={() => onOpenSession?.(child.executorSessionId!, child.work.scope, `执行：${child.work.goal}`)}>
                                        执行会话（{child.work.goal}，{workStatusLabels[child.work.status] ?? child.work.status}）
                                    </a>
                                ) : (
                                    <span key={child.work.id}>子工作（{workStatusLabels[child.work.status] ?? child.work.status}）</span>
                                ),
                            )}
                        </div>
                        {entry.work.resultSummary ? <p style={{ marginTop: 8, marginBottom: 0, color: "var(--color-text-secondary, #666)" }}>{entry.work.resultSummary}</p> : null}
                        {live && onStop ? (
                            <a data-action="stop" style={{ color: "var(--color-danger, #d4380d)", display: "inline-block", marginTop: 8 }} onClick={() => onStop(entry.work.id, entry.work.version)}>
                                停止工作
                            </a>
                        ) : null}
                    </div>
                );
            })}
        </>
    );
}
import { createAgentClient, type Scope, type WorkDirectoryEntry } from "@/sdk/agent";
import { webTransport } from "@/sdk/creation-platform/http-transport";
import { AgentConversationPanel } from "@/components/agent/conversation/agent-conversation-panel";
import { useUserStore } from "@/stores/use-user-store";

const workStatusLabels: Record<string, string> = {
    running: "进行中",
    waiting_user: "等待用户",
    paused: "已暂停",
    completed: "已完成",
    failed: "已失败",
    cancelled: "已停止",
    pending: "排队中",
};

function statusColor(status: string): string {
    if (status === "completed") return "green";
    if (status === "failed" || status === "cancelled") return "red";
    if (status === "waiting_user") return "gold";
    return "blue";
}

export type AgentWorkPanelProps = {
    open: boolean;
    onClose: () => void;
    /** 会话面板直接复用同一 SDK；测试注入假传输。 */
    transport?: typeof webTransport;
};

/**
 * 总控工作台：列出当前用户的根工作树、派工与执行会话。工作卡直达原
 * ExecutorSessionID；关闭面板只结束本地订阅，不停止服务端工作——停止工作
 * 是工作卡上的显式动作。
 */
export function AgentWorkPanel({ open, onClose, transport }: AgentWorkPanelProps) {
    const { message } = App.useApp();
    const userId = useUserStore((state) => state.user?.id ?? "");
    const client = useMemo(() => createAgentClient({ transport: transport ?? webTransport }), [transport]);
    const [works, setWorks] = useState<WorkDirectoryEntry[]>();
    const [loading, setLoading] = useState(false);
    // [会话, 标题]：打开哪一个原会话（总控或执行），面板只呈现服务端状态。
    const [chat, setChat] = useState<{ sessionId: string; title: string; scope: Scope }>();

    async function refresh() {
        setLoading(true);
        try {
            const page = await client.works(20);
            setWorks(page.works);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "无法读取工作目录");
        } finally {
            setLoading(false);
        }
    }

    function visible(): boolean {
        if (!open) {
            return false;
        }
        if (userId && works === undefined && !loading) {
            void refresh();
        }
        return true;
    }

    async function stopDirect(workId: string, expectedVersion: number) {
        try {
            await client.stopWork(workId, expectedVersion, "用户停止");
            message.success("工作已停止");
            await refresh();
        } catch (error) {
            message.error(error instanceof Error ? error.message : "停止工作失败");
        }
    }

    const active = visible();

    return (
        <>
            <Drawer open={active} onClose={onClose} title="总控工作台" width={460} loading={loading && works === undefined}>
                <AgentWorkList works={works ?? []} onOpenSession={(sessionId, scope, title) => setChat({ sessionId, scope, title })} onStop={(workId, expectedVersion) => void stopDirect(workId, expectedVersion)} />
                {works !== undefined && works.length === 0 ? <p style={{ color: "var(--color-text-secondary, #666)" }}>还没有总控工作。发起一条目标后，这里会显示工作树与执行会话。</p> : null}
            </Drawer>
            <AgentConversationPanel
                open={chat !== undefined}
                onClose={() => setChat(undefined)}
                title={chat?.title ?? ""}
                definitionId={chat?.sessionId ? "" : "drama.agent"}
                scope={chat?.scope ?? { application: "drama", spaceType: "drama_project", spaceId: "" }}
                sessionId={chat?.sessionId}
            />
        </>
    );
}
