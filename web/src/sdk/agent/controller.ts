/**
 * 会话控制器：每个会话一份独立状态。每个异步回调都捕获发起时的会话 ID，结果只写回那个会话；
 * 切换当前会话只改变 active，不取消服务端工作，也不把迟到的响应写进新打开的会话。
 */
import type { AgentClient } from "./client";
import { lastSequence, mergeMessages } from "./events";
import { agentStorageKey, type AgentStorage } from "./storage";
import type { MessageReceipt, SessionMessage, SessionView } from "./types";

export type SessionState = { session?: SessionView; messages: SessionMessage[]; loading: boolean; error?: string };

export type SessionControllerOptions = { client: AgentClient; userId: string; storage: AgentStorage; onChange?: (sessionId: string) => void };

export function createSessionController(options: SessionControllerOptions) {
    const { client, userId, storage } = options;
    const states = new Map<string, SessionState>();
    let activeId: string | undefined;

    const stateOf = (sessionId: string): SessionState => {
        let state = states.get(sessionId);
        if (!state) {
            state = { messages: [], loading: false };
            states.set(sessionId, state);
        }
        return state;
    };

    const changed = (sessionId: string) => options.onChange?.(sessionId);

    async function load(session: SessionView) {
        const target = session.id; // captured: the response belongs to this Session only
        const state = stateOf(target);
        state.loading = true;
        changed(target);
        try {
            // Resume only after messages this controller holds; a fresh page holds none
            // and reads the full history, so a reload never hides earlier messages.
            const page = await client.messages(target, lastSequence(state.messages));
            const next = stateOf(target);
            next.messages = mergeMessages(next.messages, page.messages, target);
            next.error = undefined;
        } catch (error) {
            stateOf(target).error = error instanceof Error ? error.message : "加载失败";
        } finally {
            stateOf(target).loading = false;
            changed(target);
        }
    }

    return {
        /** 打开会话并加载它的增量消息；返回的 Promise 只影响该会话。 */
        open(session: SessionView) {
            activeId = session.id;
            stateOf(session.id).session = session;
            changed(session.id);
            return load(session);
        },

        active: () => (activeId ? stateOf(activeId).session : undefined),

        state: (sessionId: string): Readonly<SessionState> => stateOf(sessionId),

        async refresh(sessionId: string) {
            const session = stateOf(sessionId).session;
            if (session) await load(session);
        },

        async send(sessionId: string, text: string): Promise<MessageReceipt> {
            const session = stateOf(sessionId).session;
            const receipt = await client.sendMessage(sessionId, { text });
            if (session) storage.delete(agentStorageKey(userId, session, "draft"));
            if (session) await load(session);
            return receipt;
        },

        draft: (session: Pick<SessionView, "id" | "scope">) => storage.get(agentStorageKey(userId, session, "draft")) ?? "",

        setDraft(session: Pick<SessionView, "id" | "scope">, text: string) {
            const key = agentStorageKey(userId, session, "draft");
            if (text) storage.set(key, text);
            else storage.delete(key);
        },
    };
}

export type SessionController = ReturnType<typeof createSessionController>;
