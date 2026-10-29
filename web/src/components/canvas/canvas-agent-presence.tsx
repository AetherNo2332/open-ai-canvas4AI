import { memo, useEffect, useState } from "react";
import { subscribeCanvasRealtime } from "@/services/canvas-realtime";
import type { AgentCanvasPresence } from "@/services/canvas-realtime-client";
import type { CanvasNodeData } from "@/types/canvas";
import "./canvas-agent-presence.css";

type Marker = { id: string; x: number; y: number; width: number; height: number; editing: boolean };

/** Editing wins over reading when several Agent runs touch the same node. */
export function agentPresenceMarkers(agents: readonly AgentCanvasPresence[], nodes: readonly CanvasNodeData[]): Marker[] {
    const phases = new Map<string, boolean>();
    for (const agent of agents) for (const id of agent.nodeIds) phases.set(id, phases.get(id) === true || agent.phase === "editing");
    if (!phases.size) return [];
    return nodes.flatMap((node) => {
        const editing = phases.get(node.id);
        return editing === undefined || !(node.width > 0 && node.height > 0) ? [] : [{ id: node.id, x: node.position.x, y: node.position.y, width: node.width, height: node.height, editing }];
    });
}

/** Pure world-space layer; it sits inside the canvas world layer so pan/zoom need no per-frame work. */
export const CanvasAgentPresenceMarkers = memo(function CanvasAgentPresenceMarkers({ agents, nodes }: { agents: readonly AgentCanvasPresence[]; nodes: readonly CanvasNodeData[] }) {
    const markers = agentPresenceMarkers(agents, nodes);
    if (!markers.length) return null;
    return (
        <div className="canvas-agent-presence" aria-hidden="true">
            {markers.map((marker) => (
                <div
                    key={marker.id}
                    className="canvas-agent-presence-node"
                    data-agent-node-id={marker.id}
                    data-agent-editing={marker.editing ? "true" : "false"}
                    style={{ transform: `translate(${marker.x}px, ${marker.y}px)`, width: marker.width, height: marker.height }}
                >
                    <span className="canvas-agent-presence-label">{marker.editing ? "Agent 正在编辑" : "Agent 正在读取"}</span>
                </div>
            ))}
        </div>
    );
});

/** Subscribes to the shared realtime connection; renders nothing while disconnected. */
export function CanvasAgentPresence({ canvasId, enabled, nodes }: { canvasId: string; enabled: boolean; nodes: readonly CanvasNodeData[] }) {
    const [agents, setAgents] = useState<readonly AgentCanvasPresence[]>([]);
    useEffect(() => {
        if (!enabled) return;
        const unsubscribe = subscribeCanvasRealtime(canvasId, (snapshot, event) => {
            if (event !== "agent" && event !== "connection") return;
            const next = snapshot.connected ? snapshot.agents ?? [] : [];
            setAgents((current) => (current.length === 0 && next.length === 0 ? current : next));
        });
        return () => {
            unsubscribe();
            setAgents([]);
        };
    }, [canvasId, enabled]);
    return enabled ? <CanvasAgentPresenceMarkers agents={agents} nodes={nodes} /> : null;
}
