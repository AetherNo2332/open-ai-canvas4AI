import { useLayoutEffect, useRef, useState, type CSSProperties } from "react";
import { useReducedMotion } from "motion/react";
import type { CanvasTheme } from "@/lib/canvas-theme";
import { computeSubagentOffsets } from "@/lib/canvas/agent-subagent-layout";
import "./canvas-agent-subagent-list.css";

export type AgentSubagentAvatarItem = { id: string; name: string; avatarUrl: string; state: "running" | "waiting" | "done" | "failed" };

export function AgentSubagentList({ items, theme }: { items: AgentSubagentAvatarItem[]; theme: CanvasTheme }) {
    const reducedMotion = useReducedMotion();
    const [hovered, setHovered] = useState(-1);
    const [focused, setFocused] = useState(-1);
    const [widths, setWidths] = useState<number[]>([]);
    const names = useRef<Array<HTMLSpanElement | null>>([]);
    const row = useRef<HTMLDivElement>(null);
    const [geometry, setGeometry] = useState({ size: 44, gap: 8 });
    const expanded = hovered >= 0 ? hovered : focused;
    useLayoutEffect(() => {
        const measure = () => {
            setWidths(items.map((_, index) => names.current[index]?.scrollWidth || 0));
            const button = row.current?.querySelector("button");
            if (button && row.current) setGeometry({ size: button.offsetWidth, gap: Number.parseFloat(getComputedStyle(row.current).gap) || 0 });
        };
        measure();
        const observer = typeof ResizeObserver !== "undefined" ? new ResizeObserver(measure) : null;
        names.current.slice(0, items.length).forEach(name => { if (name) observer?.observe(name); });
        return () => observer?.disconnect();
    }, [items]);
    if (items.length === 0) return null;
    const offsets = computeSubagentOffsets(items.length, geometry.size, geometry.gap, reducedMotion ? -1 : expanded, index => widths[index] || 0);
    const baseWidth = items.length * (geometry.size + geometry.gap) - geometry.gap;
    const extraWidth = !reducedMotion && expanded >= 0 ? (widths[expanded] || 0) + geometry.gap : 0;
    return <div className="agent-subagent-list" aria-label="活动子智能体" style={{ "--agent-subagent-surface": theme.node.panel, "--agent-subagent-text": theme.node.text } as CSSProperties}>
        <div ref={row} className="agent-subagent-row" style={{ minWidth: baseWidth + extraWidth }}>
            {items.map((item, index) => <div key={item.id} className="agent-subagent-item" data-state={item.state} data-expanded={expanded === index} style={{ transform: `translateX(${offsets[index]}px)` }}>
                <button type="button" className="agent-subagent-avatar grid place-items-center rounded-full" aria-label={item.name} title={`${item.name} · ${stateLabel[item.state]}`} onMouseEnter={() => setHovered(index)} onMouseLeave={() => setHovered(-1)} onFocus={() => setFocused(index)} onBlur={() => setFocused(-1)}>
                    <img src={item.avatarUrl} className="rounded-full object-cover" alt="" />
                </button>
                <span ref={element => { names.current[index] = element; }} className="agent-subagent-name" aria-hidden="true">{item.name}</span>
            </div>)}
        </div>
        {reducedMotion && expanded >= 0 ? <span className="agent-subagent-reduced-name">{items[expanded]?.name}</span> : null}
    </div>;
}

const stateLabel = { running: "运行中", waiting: "等待中", done: "已完成", failed: "失败" };
