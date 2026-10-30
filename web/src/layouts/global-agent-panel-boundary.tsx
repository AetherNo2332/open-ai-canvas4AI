import { Component, type ReactNode } from "react";

import { markAgentPanelUnavailable } from "@/stores/use-global-agent-store";

/**
 * Agent 代码块加载失败或面板崩溃时只隐藏面板，不让路由错误页替换整个工作区。
 * 同时结束等待中的投递，之后的投递直接返回 false，画布页不会被一条无人消费的请求卡住。
 */
export class GlobalAgentPanelBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
    state = { failed: false };
    static getDerivedStateFromError() {
        return { failed: true };
    }
    componentDidCatch(error: unknown) {
        console.error("Agent 面板加载失败，已隐藏面板", error);
        markAgentPanelUnavailable();
    }
    render() {
        return this.state.failed ? null : this.props.children;
    }
}
