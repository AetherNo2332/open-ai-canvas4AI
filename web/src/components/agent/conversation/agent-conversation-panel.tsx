import { useEffect, useMemo, useReducer, useState } from "react";
import { App, Button, Input } from "antd";
import { Bot, Send } from "lucide-react";

import { AppDrawer } from "@/components/ui/product/app-drawer/app-drawer";
import { createAgentClient, createSessionController, type AgentStorage, type Scope, type SessionView } from "@/sdk/agent";
import { webTransport } from "@/sdk/creation-platform/http-transport";
import { useUserStore } from "@/stores/use-user-store";

import "./agent-conversation.css";

// 草稿与游标是小数据，按“用户 + 范围 + 会话”的键放在 localStorage；切换账号或业务不会读到别人的草稿。
const browserStorage: AgentStorage = {
    get: (key) => localStorage.getItem(key) ?? undefined,
    set: (key, value) => localStorage.setItem(key, value),
    delete: (key) => localStorage.removeItem(key),
};

const ACTOR_LABEL: Record<string, string> = { user: "我", supervisor: "总控", executor: "Agent", system: "系统" };

export type AgentConversationPanelProps = {
    open: boolean;
    onClose: () => void;
    title: string;
    definitionId: string;
    scope: Scope;
};

/**
 * 共用会话面板：打开时在当前业务空间取（或创建）一个会话并显示消息。只负责呈现与调用 SDK 控制器；
 * 工具执行、预算与上下文都在服务端。关闭面板只结束本地订阅，不取消服务端工作。
 */
export function AgentConversationPanel({ open, onClose, title, definitionId, scope }: AgentConversationPanelProps) {
    const { message } = App.useApp();
    const userId = useUserStore((state) => state.user?.id ?? "");
    const [, rerender] = useReducer((n: number) => n + 1, 0);
    const [session, setSession] = useState<SessionView>();
    const [sending, setSending] = useState(false);
    const client = useMemo(() => createAgentClient({ transport: webTransport }), []);
    const controller = useMemo(() => createSessionController({ client, userId, storage: browserStorage, onChange: rerender }), [client, userId]);
    const [draft, setDraft] = useState("");
    const { application, spaceType, spaceId } = scope;

    useEffect(() => {
        if (!open || !userId) return;
        let cancelled = false;
        const target: Scope = { application, spaceType, spaceId };
        void (async () => {
            try {
                const page = await client.listSessions({ scope: target, limit: 1 });
                const existing = page.sessions.find((item) => item.status === "active" && item.definitionId === definitionId);
                const next = existing ?? (await client.createSession({ definitionId, scope: target, idempotencyKey: `panel:${userId}:${application}:${spaceId}` }));
                if (cancelled) return;
                setSession(next);
                setDraft(controller.draft(next));
                await controller.open(next);
            } catch (error) {
                if (!cancelled) message.error(error instanceof Error ? error.message : "无法打开 Agent 会话");
            }
        })();
        return () => {
            cancelled = true;
        };
    }, [open, userId, client, controller, definitionId, application, spaceType, spaceId, message]);

    const state = session ? controller.state(session.id) : undefined;

    async function send() {
        const text = draft.trim();
        if (!session || !text) return;
        setSending(true);
        try {
            await controller.send(session.id, text);
            setDraft("");
        } catch (error) {
            message.error(error instanceof Error ? error.message : "发送失败");
        } finally {
            setSending(false);
        }
    }

    return (
        <AppDrawer open={open} onClose={onClose} title={title} size={420} flush>
            <section className="agent-session-panel" aria-label={title}>
                <ol className="agent-session-panel-messages" aria-live="polite">
                    {state?.messages.length ? (
                        state.messages
                            .filter((item) => item.kind === "message")
                            .map((item) => (
                                <li key={item.sequence} className="agent-session-panel-message" data-actor={item.actor}>
                                    <span className="agent-session-panel-actor">{ACTOR_LABEL[item.actor] ?? item.actor}</span>
                                    <p>{item.text}</p>
                                </li>
                            ))
                    ) : (
                        <li className="agent-session-panel-empty">
                            <Bot className="size-5" aria-hidden />
                            <span>{state?.loading ? "正在加载会话…" : "这个会话只属于当前业务空间。告诉 Agent 你想做什么。"}</span>
                        </li>
                    )}
                </ol>
                {state?.error ? (
                    <p className="agent-session-panel-error" role="alert">
                        {state.error}
                    </p>
                ) : null}
                <div className="agent-session-panel-input">
                    <Input.TextArea
                        value={draft}
                        autoSize={{ minRows: 2, maxRows: 6 }}
                        placeholder="输入消息，Enter 发送，Shift+Enter 换行"
                        disabled={!session}
                        aria-label="消息内容"
                        onChange={(event) => {
                            setDraft(event.target.value);
                            if (session) controller.setDraft(session, event.target.value);
                        }}
                        onPressEnter={(event) => {
                            if (!event.shiftKey) {
                                event.preventDefault();
                                void send();
                            }
                        }}
                    />
                    <Button type="primary" icon={<Send className="size-4" />} loading={sending} disabled={!session || !draft.trim()} onClick={() => void send()} aria-label="发送">
                        发送
                    </Button>
                </div>
            </section>
        </AppDrawer>
    );
}
