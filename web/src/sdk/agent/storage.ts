/**
 * 会话本地状态的存储边界。键按“用户 + 范围 + 会话”分区，切换账号、业务或会话不会读到别人的草稿。
 * 浏览器里由接线层注入带用户 scope 的存储；SDK 核心不直接访问浏览器存储。
 */
import type { SessionView } from "./types";

export type AgentStorage = {
    get(key: string): string | undefined;
    set(key: string, value: string): void;
    delete(key: string): void;
};

export function agentStorageKey(userId: string, session: Pick<SessionView, "id" | "scope">, field: "draft"): string {
    const { application, spaceType, spaceId } = session.scope;
    return ["agent", userId, application, spaceType, spaceId, session.id, field].map(encodeURIComponent).join(":");
}

export function createMemoryStorage(): AgentStorage {
    const values = new Map<string, string>();
    return {
        get: (key) => values.get(key),
        set: (key, value) => void values.set(key, value),
        delete: (key) => void values.delete(key),
    };
}
