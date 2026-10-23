import { useCallback, useEffect, useRef, useState } from "react";
import {
    cancelAgentQueuedMessage,
    getAgentMessageQueue,
    getAgentUISettings,
    updateAgentMessageQueue,
    updateAgentQueuedMessage,
    updateAgentUISettings,
    type AgentMessageQueue,
    type AgentQueuedMessage,
    type AgentSendMode,
    type AgentUISettings,
} from "@/services/api/agent";
import { getActiveUserScope } from "@/lib/user-scope";

// Presentation state only. The server owns ordering, dispatch, question gates
// and permission checks; cached snapshots never schedule a message locally.
export function useAgentMessageQueue(userScope: string, canvasId: string, conversationId?: string) {
    const [settings, setSettings] = useState<{ scope: string; value: AgentUISettings }>();
    const [queueState, setQueueState] = useState<{ key: string; value: AgentMessageQueue }>();
    const [settingsError, setSettingsError] = useState("");
    const [queueError, setQueueError] = useState("");
    const [savingMode, setSavingMode] = useState(false);
    const [acting, setActing] = useState(false);
    const key = `${userScope}:${canvasId}:${conversationId || ""}`;
    const account = useRef({ scope: userScope });
    const current = useRef({ key });
    if (account.current.scope !== userScope) account.current = { scope: userScope };
    if (current.current.key !== key) current.current = { key };
    const queueRequest = useRef(0);
    const settingsRequest = useRef(0);
    const actionRef = useRef<object | null>(null);
    const modeActionRef = useRef<object | null>(null);
    const settingsRef = useRef(settings);
    settingsRef.current = settings;

    const reloadSettings = useCallback(
        async (signal?: AbortSignal) => {
            const scope = account.current;
            const request = ++settingsRequest.current;
            try {
                const value = await getAgentUISettings(signal);
                if (account.current === scope && request === settingsRequest.current && !signal?.aborted && getActiveUserScope() === userScope) {
                    const next = { scope: userScope, value };
                    settingsRef.current = next;
                    setSettings(next);
                    setSettingsError("");
                }
                return value.defaultSendMode;
            } catch (cause) {
                if (account.current === scope && !signal?.aborted) setSettingsError("发送方式加载失败，请重试");
                throw cause;
            }
        },
        [userScope],
    );

    const refresh = useCallback(
        async (signal?: AbortSignal) => {
            if (!conversationId) return;
            const scope = current.current;
            const request = ++queueRequest.current;
            try {
                const value = await getAgentMessageQueue(conversationId, signal);
                if (current.current === scope && request === queueRequest.current && !signal?.aborted && getActiveUserScope() === userScope) {
                    setQueueState({ key, value });
                    setQueueError("");
                }
            } catch {
                if (current.current === scope && !signal?.aborted) setQueueError("待发消息加载失败，请重试");
            }
        },
        [conversationId, key, userScope],
    );

    useEffect(() => {
        const controller = new AbortController();
        setSettingsError("");
        setSavingMode(false);
        modeActionRef.current = null;
        const reload = () => {
            if (!modeActionRef.current) void reloadSettings(controller.signal).catch(() => {});
        };
        reload();
        window.addEventListener("focus", reload);
        return () => {
            controller.abort();
            window.removeEventListener("focus", reload);
        };
    }, [reloadSettings]);
    useEffect(() => {
        const controller = new AbortController();
        setQueueError("");
        setActing(false);
        actionRef.current = null;
        const reload = () => void refresh(controller.signal);
        reload();
        const timer = window.setInterval(reload, 4_000);
        window.addEventListener("focus", reload);
        window.addEventListener("online", reload);
        return () => {
            controller.abort();
            clearInterval(timer);
            window.removeEventListener("focus", reload);
            window.removeEventListener("online", reload);
        };
    }, [refresh]);

    const changeMode = async (mode: AgentSendMode) => {
        if (modeActionRef.current) return;
        const scope = account.current,
            action = {};
        const existing = settingsRef.current;
        if (existing?.scope !== userScope) return;
        modeActionRef.current = action;
        setSavingMode(true);
        settingsRequest.current++;
        try {
            const value = await updateAgentUISettings({ ...existing.value, defaultSendMode: mode });
            if (account.current === scope && getActiveUserScope() === userScope) {
                const next = { scope: userScope, value };
                settingsRef.current = next;
                setSettings(next);
                setSettingsError("");
            }
        } catch {
            if (account.current === scope) {
                setSettingsError("发送方式保存失败，请重试");
                await reloadSettings().catch(() => {});
            }
        } finally {
            if (modeActionRef.current === action) {
                modeActionRef.current = null;
                if (account.current === scope) setSavingMode(false);
            }
        }
    };
    const perform = async (operation: () => Promise<unknown>) => {
        if (!conversationId || actionRef.current) return false;
        const scope = current.current,
            action = {};
        actionRef.current = action;
        setActing(true);
        try {
            await operation();
            if (current.current !== scope || getActiveUserScope() !== userScope) return false;
            await refresh();
            return true;
        } catch {
            if (current.current === scope) {
                await refresh();
                setQueueError("操作未完成，请刷新后重试");
            }
            return false;
        } finally {
            if (actionRef.current === action) {
                actionRef.current = null;
                if (current.current === scope) setActing(false);
            }
        }
    };
    const queue = queueState?.key === key ? queueState.value : undefined;
    return {
        queue,
        queueError,
        settingsError,
        savingMode,
        acting,
        refresh,
        mode: settings?.scope === userScope ? settings.value.defaultSendMode : undefined,
        getMode: async () => (settingsRef.current?.scope === userScope ? settingsRef.current.value.defaultSendMode : reloadSettings()),
        changeMode,
        retry: async () => {
            await Promise.allSettled([refresh(), reloadSettings()]);
        },
        edit: (item: AgentQueuedMessage, prompt: string) => perform(() => updateAgentQueuedMessage(conversationId!, item.id, { revision: item.revision, prompt })),
        promote: (item: AgentQueuedMessage) => perform(() => updateAgentQueuedMessage(conversationId!, item.id, { revision: item.revision, action: "interject" })),
        remove: (item: AgentQueuedMessage) => perform(() => cancelAgentQueuedMessage(conversationId!, item.id, item.revision)),
        resume: () => (queue ? perform(() => updateAgentMessageQueue(conversationId!, { revision: queue.revision, action: "resume" })) : Promise.resolve(false)),
    };
}
