import { hostBridge } from "./runtime.ts";
import { removedCapabilityMessage } from "./desktop-removed.ts";

/**
 * 云端 AI 通道的挂载点。
 *
 * 桌面版通过 Electron 桥 + 本机 MCP 宿主提供 AI;云端改为影策 Agent 通道
 * (`desk.ai.send` / `host.ai.event` / `host.ai.state`,见 host/contract.ts)。
 * 本任务(T2)负责挂载与状态呈现,实际传输在 T8 接入影策 /api/agent。
 */
export type HostAIState = { ready: boolean; text: string };

export function mountHostAIChannel(panel: HTMLElement, status: (message: string) => void): (state?: unknown) => void {
    const state = panel.querySelector<HTMLElement>("#ai-host-status");
    const bridge = hostBridge();
    const render = (next?: unknown) => {
        const value: HostAIState = next && typeof next === 'object' && 'text' in (next as HostAIState)
            ? next as HostAIState
            : { ready: true, text: '云端 AI 通道已连接' };
        if (state) state.textContent = value.text;
        status(value.text);
    };
    if (!bridge) {
        render({ ready: false, text: "云端 AI 通道未就绪" });
        return render;
    }
    bridge.on("host.ai.state", payload => render(payload));
    render({ ready: true, text: "云端 AI 通道已连接" });
    return render;
}

export function removedMCPMessage(): string {
    return removedCapabilityMessage("mcp");
}
