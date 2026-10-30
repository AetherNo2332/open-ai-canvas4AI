import { ArrowRight } from "lucide-react";
import type { CSSProperties, SyntheticEvent } from "react";

import { extractAgentReplyOptions, type AgentReplyOption, type AgentReplyOptions } from "@/lib/canvas/agent-reply-options";
import { requestAgentPrefill, type AgentPrefillOutcome } from "@/stores/use-global-agent-store";

type AgentReplyOptionCardsProps = {
    options: readonly AgentReplyOption[];
    onSelect: (option: AgentReplyOption) => void;
    disabled?: boolean;
    /** 选项组的可读名称，读屏时作为分组标题。 */
    label?: string;
};

// 选项可能出现在可拖拽的面板里，按下时不要把事件交给面板或画布的拖拽逻辑。
const stop = (event: SyntheticEvent) => event.stopPropagation();

/**
 * 只有对话停在 Agent 的最后一条回复上（之后没有用户发言）时，这条回复末尾的编号选项
 * 才变成可点击卡片；更早的回复保持原文，便于回看。调用方只在本轮已停下时启用。
 */
export function agentReplyOptionsTarget(messages: readonly { id: string; role: string; text: string; reasoning?: boolean }[]): { id: string; options: AgentReplyOptions } | null {
    for (let index = messages.length - 1; index >= 0; index -= 1) {
        const message = messages[index];
        if (message.role === "user") return null;
        if (message.role !== "assistant" || message.reasoning) continue;
        const options = extractAgentReplyOptions(message.text);
        return options ? { id: message.id, options } : null;
    }
    return null;
}

/**
 * 选项卡片走与画布页相同的全局投递路径：面板取走后按外部发送语义直接提交，
 * 已有待消费请求或没有画布时返回 false，重复点击不会重复提交。
 */
export function sendAgentReplyOption({ canvasId, userScope, text }: { canvasId: string; userScope: string; text: string }): Promise<AgentPrefillOutcome> {
    if (!canvasId || !text.trim()) return Promise.resolve(false);
    return requestAgentPrefill({ canvasId, userScope, text, autoSubmit: true });
}

/**
 * 可点击的回复选项卡片。点一下就把该选项作为用户回复发出，不用再手动输入编号。
 * `--agent-option-index` 只用于入场错峰，动效与时长由样式表按皮肤 token 决定。
 */
export function AgentReplyOptionCards({ options, onSelect, disabled = false, label = "可选的下一步" }: AgentReplyOptionCardsProps) {
    return (
        <div className="agent-reply-options" role="group" aria-label={label}>
            {options.map((option, index) => (
                <button
                    key={`${option.marker}-${option.text}`}
                    type="button"
                    className="agent-reply-option"
                    style={{ "--agent-option-index": index } as CSSProperties}
                    disabled={disabled}
                    title={option.text}
                    onMouseDown={stop}
                    onPointerDown={stop}
                    onClick={(event) => {
                        stop(event);
                        onSelect(option);
                    }}
                >
                    <span className="agent-reply-option-marker" aria-hidden="true">
                        {option.marker}
                    </span>
                    <span className="agent-reply-option-copy">
                        <span className="agent-reply-option-title">{option.title}</span>
                        {option.detail ? <span className="agent-reply-option-detail">{option.detail}</span> : null}
                    </span>
                    <ArrowRight className="agent-reply-option-arrow" aria-hidden="true" />
                </button>
            ))}
        </div>
    );
}
