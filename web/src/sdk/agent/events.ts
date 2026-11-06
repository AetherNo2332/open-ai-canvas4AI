/**
 * 消息合并：按会话序号去重并保持顺序。重复投递、断线重读与刷新恢复都只依赖序号，不依赖到达顺序。
 */
import type { SessionMessage } from "./types";

export function mergeMessages(current: readonly SessionMessage[], incoming: readonly SessionMessage[], sessionId: string): SessionMessage[] {
    const bySequence = new Map<number, SessionMessage>();
    for (const message of current) bySequence.set(message.sequence, message);
    for (const message of incoming) if (message.sessionId === sessionId) bySequence.set(message.sequence, message);
    return [...bySequence.values()].sort((a, b) => a.sequence - b.sequence);
}

export function lastSequence(messages: readonly SessionMessage[], fallback = 0): number {
    return messages.reduce((max, message) => Math.max(max, message.sequence), fallback);
}
