import type { CSSProperties } from "react";
import { Aperture, BookOpen, Compass, Feather, Film, FlaskConical, Focus, Gem, Layers, Lightbulb, Orbit, PenTool, ScanSearch, Telescope, WandSparkles, Waypoints } from "lucide-react";
import type { CanvasTheme } from "@/lib/canvas-theme";
import "./canvas-agent-subagent-list.css";

export type AgentSubagentAvatarItem = { id: string; name: string; state: "running" | "waiting" | "done" | "failed"; writeGranted?: boolean };

const icons = [Compass, Feather, ScanSearch, Aperture, BookOpen, Film, Lightbulb, Layers, PenTool, Telescope, FlaskConical, Focus, Orbit, Waypoints, Gem, WandSparkles];

export function AgentSubagentList({ items, theme, onSelect }: { items: AgentSubagentAvatarItem[]; theme: CanvasTheme; onSelect: (id: string) => void }) {
    if (items.length === 0) return null;
    return (
        <section className="agent-subagent-list" aria-label="协作代理" style={{ "--agent-subagent-surface": theme.node.panel, "--agent-subagent-text": theme.node.text } as CSSProperties}>
            <div className="agent-subagent-heading">
                <span>
                    协作代理 <span className="agent-subagent-count">{items.length}</span>
                </span>
                <span>点击查看会话</span>
            </div>
            <div className="agent-subagent-row" data-canvas-wheel-scroll>
                {items.map((item, index) => {
                    const Icon = icons[index % icons.length];
                    return (
                        <div key={item.id} className="agent-subagent-item" data-state={item.state}>
                            <button
                                type="button"
                                className="agent-subagent-button"
                                data-subagent-id={item.id}
                                aria-label={`查看${item.name}的会话 · ${stateLabel[item.state]}${item.writeGranted ? " · 可写入" : ""}`}
                                title={`${item.name} · ${stateLabel[item.state]}${item.writeGranted ? " · 可写入画布" : ""}`}
                                onClick={() => onSelect(item.id)}
                            >
                                <span className="agent-subagent-avatar">
                                    <Icon size={18} strokeWidth={1.6} aria-hidden="true" />
                                    {index >= icons.length ? <span className="agent-subagent-number">{index + 1}</span> : null}
                                    <span className="agent-subagent-status" aria-hidden="true" />
                                </span>
                                <span className="agent-subagent-name">
                                    {item.name}
                                    {item.writeGranted ? (
                                        <span className="agent-subagent-write" title="该子代理拥有画布写入权限">
                                            写
                                        </span>
                                    ) : null}
                                </span>
                            </button>
                        </div>
                    );
                })}
            </div>
        </section>
    );
}

const stateLabel = { running: "运行中", waiting: "等待中", done: "已完成", failed: "失败" };
