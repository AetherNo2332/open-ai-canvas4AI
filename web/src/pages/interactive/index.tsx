import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { ArrowLeft, Check, ChevronRight, Crosshair, GitBranch, Maximize, Minus, Pencil, Plus, RotateCcw, Trash2, X } from "lucide-react";

import { deriveEdges, type StoryNode, type StoryNodeKind, type StoryNodeStatus } from "./story-data";
import { getProject, saveProject } from "./storage";
import "./interactive.css";

const CARD_WIDTH = 288;
const MIN_SCALE = 0.35;
const MAX_SCALE = 1.6;

interface Viewport {
    x: number;
    y: number;
    scale: number;
}

type DragState = { mode: "pan"; startX: number; startY: number; originX: number; originY: number } | { mode: "node"; id: string; startX: number; startY: number; nodeX: number; nodeY: number };

const STATUS_CLASS: Record<StoryNodeStatus, string> = {
    已完成: "is-done",
    写作中: "is-writing",
    待续写: "is-todo",
};

function clamp(value: number, min: number, max: number) {
    return Math.min(max, Math.max(min, value));
}

function StoryBody({ body }: { body: string }) {
    return (
        <div className="story-node__body">
            {body.split("\n").map((line, index) => {
                const text = line.trim();
                if (!text) return null;
                if (text.startsWith("## ")) {
                    return (
                        <p key={index} className="story-node__scene">
                            {text.replace(/^##\s*/, "")}
                        </p>
                    );
                }
                return <p key={index}>{text}</p>;
            })}
        </div>
    );
}

export default function InteractiveStoryPage() {
    const { storyId } = useParams();
    const navigate = useNavigate();
    const [project] = useState(() => (storyId ? getProject(storyId) : null));
    const [nodes, setNodes] = useState<StoryNode[]>(() => project?.nodes ?? []);
    const [storyTitle, setStoryTitle] = useState(() => project?.title ?? "");
    const [selectedId, setSelectedId] = useState<string>(() => project?.nodes[0]?.id ?? "");
    const [editingId, setEditingId] = useState<string | null>(null);
    const [viewport, setViewport] = useState<Viewport>({ x: 72, y: 96, scale: 0.92 });
    const [heights, setHeights] = useState<Record<string, number>>({});
    const [savedAt, setSavedAt] = useState<number | null>(null);

    const surfaceRef = useRef<HTMLDivElement | null>(null);
    const nodeRefs = useRef(new Map<string, HTMLElement>());
    const dragRef = useRef<DragState | null>(null);
    const reservedIdsRef = useRef(new Set<string>((project?.nodes ?? []).map((node) => node.id)));
    const viewportRef = useRef(viewport);
    viewportRef.current = viewport;

    const nodeById = useMemo(() => new Map(nodes.map((node) => [node.id, node])), [nodes]);
    const edges = useMemo(() => deriveEdges(nodes), [nodes]);
    const editingNode = editingId ? (nodeById.get(editingId) ?? null) : null;

    const updateNode = useCallback((id: string, patch: Partial<StoryNode>) => {
        setNodes((prev) => prev.map((node) => (node.id === id ? { ...node, ...patch } : node)));
    }, []);

    // 节点落库后把 id 记进占位集合；只增不减，删掉的编号不会被新节点复用。
    useEffect(() => {
        nodes.forEach((node) => reservedIdsRef.current.add(node.id));
    }, [nodes]);

    // 项目不存在（手输错地址 / 已删除）时回到列表。
    useEffect(() => {
        if (!project) navigate("/interactive", { replace: true });
    }, [project, navigate]);

    // 编辑后 350ms 自动保存到 localStorage。
    useEffect(() => {
        if (!project) return;
        const timer = window.setTimeout(() => {
            saveProject({ ...project, title: storyTitle.trim() || "未命名互动剧", nodes, updatedAt: Date.now() });
            setSavedAt(Date.now());
        }, 350);
        return () => window.clearTimeout(timer);
    }, [nodes, storyTitle, project]);

    const addNode = (kind: StoryNodeKind) => {
        // 连续点击时 nodes 还是旧快照，用 reservedIdsRef 先占位，避免生成重复 id。
        const used = new Set<string>(nodes.map((node) => node.id));
        reservedIdsRef.current.forEach((id) => used.add(id));
        let serial = 1;
        while (used.has(`E${serial}`)) serial += 1;
        const id = `E${serial}`;
        reservedIdsRef.current.add(id);
        const surface = surfaceRef.current;
        const current = viewportRef.current;
        const width = surface?.clientWidth ?? 900;
        const height = surface?.clientHeight ?? 640;
        const x = Math.round((width / 2 - current.x) / current.scale - CARD_WIDTH / 2 + serial * 8);
        const y = Math.round((height / 2 - current.y) / current.scale - 100 + serial * 8);
        const node: StoryNode = {
            id,
            kind,
            title: kind === "branch" ? "新分支" : "新剧情",
            status: "待续写",
            body: "## 第 N 幕\n在这里写下这一场的内容。",
            options: [],
            x,
            y,
        };
        setNodes((prev) => [...prev, node]);
        setSelectedId(id);
        setEditingId(id);
    };

    const removeNode = (id: string) => {
        if (nodes.length <= 1) return;
        const node = nodeById.get(id);
        if (!node) return;
        // 删除前先把影响列清楚：哪些节点、哪几条选项会跟着消失，避免留下悬空边。
        const affected = nodes.filter((item) => item.id !== id && item.options.some((option) => option.to === id));
        const impact = affected.length
            ? `\n\n以下 ${affected.length} 个节点的选项会一起移除：\n${affected
                  .map(
                      (item) =>
                          `· ${item.id} ${item.title || "未命名"}：${item.options
                              .filter((option) => option.to === id)
                              .map((option) => `「${option.label}」`)
                              .join("、")}`,
                  )
                  .join("\n")}`
            : "\n\n没有其他节点的选项指向它，不会产生悬空连线。";
        const note = "\n\n资产、视频和制作画布不会被删除。";
        if (!window.confirm(`删除节点「${node.title || id}」？${impact}${note}`)) return;
        setNodes((prev) => prev.filter((item) => item.id !== id).map((item) => ({ ...item, options: item.options.filter((option) => option.to !== id) })));
        if (editingId === id) setEditingId(null);
        if (selectedId === id) setSelectedId("");
    };

    /** 用 ResizeObserver 记录每张卡片的真实高度，连线才能落在卡片垂直中点。 */
    useEffect(() => {
        const observer = new ResizeObserver((entries) => {
            setHeights((prev) => {
                let changed = false;
                const next = { ...prev };
                for (const entry of entries) {
                    const id = (entry.target as HTMLElement).dataset.nodeId;
                    if (!id) continue;
                    const height = Math.round(entry.contentRect.height);
                    if (next[id] !== height) {
                        next[id] = height;
                        changed = true;
                    }
                }
                return changed ? next : prev;
            });
        });
        for (const element of nodeRefs.current.values()) observer.observe(element);
        return () => observer.disconnect();
    }, [nodes]);

    const centerOn = useCallback(
        (id: string) => {
            const node = nodeById.get(id);
            const surface = surfaceRef.current;
            if (!node || !surface) return;
            const rect = surface.getBoundingClientRect();
            const { scale } = viewportRef.current;
            const height = heights[id] ?? 180;
            setViewport({
                scale,
                x: rect.width / 2 - (node.x + CARD_WIDTH / 2) * scale,
                y: rect.height / 2 - (node.y + height / 2) * scale,
            });
        },
        [heights, nodeById],
    );

    const fitView = useCallback(() => {
        const surface = surfaceRef.current;
        if (!surface || nodes.length === 0) return;
        const rect = surface.getBoundingClientRect();
        let minX = Infinity;
        let minY = Infinity;
        let maxX = -Infinity;
        let maxY = -Infinity;
        for (const node of nodes) {
            const height = heights[node.id] ?? 200;
            minX = Math.min(minX, node.x);
            minY = Math.min(minY, node.y);
            maxX = Math.max(maxX, node.x + CARD_WIDTH);
            maxY = Math.max(maxY, node.y + height);
        }
        const padding = 120;
        const scale = clamp(Math.min((rect.width - padding) / (maxX - minX), (rect.height - padding) / (maxY - minY)), MIN_SCALE, 1);
        setViewport({
            scale,
            x: rect.width / 2 - ((minX + maxX) / 2) * scale,
            y: rect.height / 2 - ((minY + maxY) / 2) * scale,
        });
    }, [heights, nodes]);

    useEffect(() => {
        const surface = surfaceRef.current;
        if (!surface) return;
        // 窄窗口（如手机/侧栏预览）里把首屏起点往左收，避免第一张卡片被裁掉。
        const rect = surface.getBoundingClientRect();
        const margin = 20;
        const maxX = Math.max(margin, rect.width - CARD_WIDTH * 0.92 - margin);
        setViewport({ scale: 0.92, x: Math.min(72, maxX), y: 96 });
    }, []);

    /** 滚轮缩放：以鼠标位置为锚点。React 的 onWheel 是 passive，这里挂原生监听。 */
    useEffect(() => {
        const surface = surfaceRef.current;
        if (!surface) return;
        const handleWheel = (event: WheelEvent) => {
            event.preventDefault();
            const rect = surface.getBoundingClientRect();
            const pointerX = event.clientX - rect.left;
            const pointerY = event.clientY - rect.top;
            const current = viewportRef.current;
            const factor = event.deltaY < 0 ? 1.08 : 0.92;
            const nextScale = clamp(current.scale * factor, MIN_SCALE, MAX_SCALE);
            const ratio = nextScale / current.scale;
            setViewport({
                scale: nextScale,
                x: pointerX - (pointerX - current.x) * ratio,
                y: pointerY - (pointerY - current.y) * ratio,
            });
        };
        surface.addEventListener("wheel", handleWheel, { passive: false });
        return () => surface.removeEventListener("wheel", handleWheel);
    }, []);

    const handleSurfacePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
        if (event.button !== 0) return;
        if (event.target !== event.currentTarget) return;
        dragRef.current = {
            mode: "pan",
            startX: event.clientX,
            startY: event.clientY,
            originX: viewport.x,
            originY: viewport.y,
        };
        event.currentTarget.setPointerCapture(event.pointerId);
    };

    const handleSurfacePointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
        const drag = dragRef.current;
        if (!drag || drag.mode !== "pan") return;
        setViewport((prev) => ({
            ...prev,
            x: drag.originX + (event.clientX - drag.startX),
            y: drag.originY + (event.clientY - drag.startY),
        }));
    };

    const endDrag = () => {
        dragRef.current = null;
    };

    const handleNodePointerDown = (event: React.PointerEvent<HTMLElement>, node: StoryNode) => {
        if (event.button !== 0) return;
        event.stopPropagation();
        setSelectedId(node.id);
        dragRef.current = {
            mode: "node",
            id: node.id,
            startX: event.clientX,
            startY: event.clientY,
            nodeX: node.x,
            nodeY: node.y,
        };
        event.currentTarget.setPointerCapture(event.pointerId);
    };

    const handleNodePointerMove = (event: React.PointerEvent<HTMLElement>, node: StoryNode) => {
        const drag = dragRef.current;
        if (!drag || drag.mode !== "node" || drag.id !== node.id) return;
        const scale = viewportRef.current.scale;
        updateNode(node.id, {
            x: Math.round(drag.nodeX + (event.clientX - drag.startX) / scale),
            y: Math.round(drag.nodeY + (event.clientY - drag.startY) / scale),
        });
    };

    const jumpTo = (id: string) => {
        setSelectedId(id);
        centerOn(id);
    };

    const selectedNode = nodeById.get(selectedId) ?? null;

    const nodePath = (fromId: string, toId: string) => {
        const from = nodeById.get(fromId);
        const to = nodeById.get(toId);
        if (!from || !to) return "";
        const fromX = from.x + CARD_WIDTH;
        const fromY = from.y + (heights[fromId] ?? 180) / 2;
        const toX = to.x;
        const toY = to.y + (heights[toId] ?? 180) / 2;
        const offset = Math.max(56, Math.abs(toX - fromX) * 0.45);
        return `M ${fromX} ${fromY} C ${fromX + offset} ${fromY}, ${toX - offset} ${toY}, ${toX} ${toY}`;
    };

    return (
        <div className="story-shell">
            <header className="story-topbar">
                <div className="story-topbar__left">
                    <Link to="/interactive" className="story-icon-button" title="返回互动剧列表">
                        <ArrowLeft className="size-4" />
                    </Link>
                    <div className="story-topbar__title">
                        <div className="story-topbar__eyebrow">
                            <GitBranch className="size-3.5" />
                            互动剧
                        </div>
                        <input className="story-title-input" value={storyTitle} placeholder="未命名互动剧" aria-label="互动剧名称" onChange={(event) => setStoryTitle(event.target.value)} />
                    </div>
                    <span className="story-topbar__meta">
                        {nodes.length} 个节点 · {edges.length} 条分支
                        <i className="story-topbar__dot" />
                        {savedAt ? `已保存 ${new Date(savedAt).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" })}` : "自动保存"}
                    </span>
                </div>
                <div className="story-topbar__right">
                    <button type="button" className="story-button is-primary" onClick={() => addNode("story")}>
                        <Plus className="size-3.5" />
                        添加剧情
                    </button>
                    <button type="button" className="story-button" onClick={() => addNode("branch")}>
                        <GitBranch className="size-3.5" />
                        添加分支
                    </button>
                    <button type="button" className="story-button is-optional" onClick={fitView}>
                        <Maximize className="size-3.5" />
                        适应画面
                    </button>
                    <button type="button" className="story-button is-optional" onClick={() => setViewport({ x: 72, y: 96, scale: 0.92 })}>
                        <RotateCcw className="size-3.5" />
                        重置视图
                    </button>
                    <div className="story-zoom">
                        <button type="button" onClick={() => setViewport((prev) => ({ ...prev, scale: clamp(prev.scale - 0.1, MIN_SCALE, MAX_SCALE) }))} title="缩小">
                            <Minus className="size-3.5" />
                        </button>
                        <span>{Math.round(viewport.scale * 100)}%</span>
                        <button type="button" onClick={() => setViewport((prev) => ({ ...prev, scale: clamp(prev.scale + 0.1, MIN_SCALE, MAX_SCALE) }))} title="放大">
                            <Plus className="size-3.5" />
                        </button>
                    </div>
                </div>
            </header>

            <div ref={surfaceRef} className="story-surface" onPointerDown={handleSurfacePointerDown} onPointerMove={handleSurfacePointerMove} onPointerUp={endDrag} onPointerCancel={endDrag}>
                <div
                    className="story-layer"
                    style={{
                        transform: `translate(${viewport.x}px, ${viewport.y}px) scale(${viewport.scale})`,
                    }}
                >
                    <svg className="story-edges" width={4200} height={4200} aria-hidden="true">
                        <defs>
                            <marker id="story-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                                <path d="M 0 1 L 7 4 L 0 7 z" fill="currentColor" />
                            </marker>
                        </defs>
                        {edges.map((edge) => {
                            const active = selectedId === edge.from || selectedId === edge.to;
                            return <path key={edge.optionId} d={nodePath(edge.from, edge.to)} className={active ? "story-edge is-active" : "story-edge"} markerEnd="url(#story-arrow)" />;
                        })}
                    </svg>

                    {nodes.map((node) => {
                        const active = selectedId === node.id;
                        return (
                            <article
                                key={node.id}
                                ref={(element) => {
                                    if (element) {
                                        element.dataset.nodeId = node.id;
                                        nodeRefs.current.set(node.id, element);
                                    } else {
                                        nodeRefs.current.delete(node.id);
                                    }
                                }}
                                data-node-id={node.id}
                                className={["story-node", node.kind === "branch" ? "story-node--branch" : "story-node--story", active ? "is-selected" : ""].filter(Boolean).join(" ")}
                                style={{ left: node.x, top: node.y, width: CARD_WIDTH }}
                                onPointerDown={(event) => handleNodePointerDown(event, node)}
                                onPointerMove={(event) => handleNodePointerMove(event, node)}
                                onPointerUp={endDrag}
                                onPointerCancel={endDrag}
                                onDoubleClick={() => setEditingId(node.id)}
                            >
                                <div className="story-node__head">
                                    <span className="story-node__kind">
                                        <i />
                                        {node.kind === "branch" ? "分支节点" : "剧情节点"}
                                    </span>
                                    <span className="story-node__id">{node.id}</span>
                                    <span className={`story-status ${STATUS_CLASS[node.status]}`}>
                                        {node.status === "已完成" ? <Check className="size-3" /> : null}
                                        {node.status}
                                    </span>
                                </div>

                                <h3 className="story-node__title">{node.title}</h3>
                                <StoryBody body={node.body} />

                                {node.options.length > 0 ? (
                                    <div className="story-options">
                                        {node.options.map((option) => (
                                            <button key={option.id} type="button" className="story-option" onPointerDown={(event) => event.stopPropagation()} onClick={() => jumpTo(option.to)} title={`跳转到 ${option.to}`}>
                                                <span className="story-option__label">{option.label}</span>
                                                <span className="story-option__to">
                                                    › {option.to}
                                                    <ChevronRight className="size-3" />
                                                </span>
                                            </button>
                                        ))}
                                    </div>
                                ) : null}

                                <footer className="story-node__foot">
                                    {node.isEnding ? <span className="story-node__ending">结局</span> : <span />}
                                    <button
                                        type="button"
                                        className="story-node__view"
                                        onPointerDown={(event) => event.stopPropagation()}
                                        onClick={() => {
                                            setSelectedId(node.id);
                                            setEditingId(node.id);
                                        }}
                                    >
                                        <Pencil className="size-3" />
                                        查看
                                    </button>
                                </footer>
                            </article>
                        );
                    })}
                </div>

                <div className="story-legend">
                    <span>
                        <i className="is-branch" />
                        分支节点
                    </span>
                    <span>
                        <i className="is-story" />
                        剧情节点
                    </span>
                    <span>
                        <i className="is-edge" />
                        选项连线
                    </span>
                </div>

                <div className="story-hint">拖拽空白处平移 · 滚轮缩放 · 拖动节点移动 · 双击节点编辑</div>
            </div>

            {editingNode ? (
                <aside className="story-inspector">
                    <div className="story-inspector__head">
                        <div>
                            <span className="story-inspector__eyebrow">{editingNode.id} · 节点编辑</span>
                            <h2>{editingNode.title || "未命名节点"}</h2>
                        </div>
                        <button type="button" className="story-icon-button" onClick={() => setEditingId(null)} title="关闭">
                            <X className="size-4" />
                        </button>
                    </div>

                    <label className="story-field">
                        <span>标题</span>
                        <input value={editingNode.title} onChange={(event) => updateNode(editingNode.id, { title: event.target.value })} />
                    </label>

                    <div className="story-field-row">
                        <label className="story-field">
                            <span>类型</span>
                            <select value={editingNode.kind} onChange={(event) => updateNode(editingNode.id, { kind: event.target.value as StoryNode["kind"] })}>
                                <option value="story">剧情节点</option>
                                <option value="branch">分支节点</option>
                            </select>
                        </label>
                        <label className="story-field">
                            <span>状态</span>
                            <select value={editingNode.status} onChange={(event) => updateNode(editingNode.id, { status: event.target.value as StoryNodeStatus })}>
                                <option value="已完成">已完成</option>
                                <option value="写作中">写作中</option>
                                <option value="待续写">待续写</option>
                            </select>
                        </label>
                    </div>

                    <label className="story-check">
                        <input type="checkbox" checked={Boolean(editingNode.isEnding)} onChange={(event) => updateNode(editingNode.id, { isEnding: event.target.checked })} />
                        <span>标记为结局节点</span>
                    </label>

                    <label className="story-field">
                        <span>正文（用 ## 开头表示幕标题）</span>
                        <textarea rows={7} value={editingNode.body} onChange={(event) => updateNode(editingNode.id, { body: event.target.value })} />
                    </label>

                    <div className="story-field">
                        <span>选项分支</span>
                        <div className="story-option-editor">
                            {editingNode.options.map((option) => (
                                <div key={option.id} className="story-option-editor__row">
                                    <input
                                        value={option.label}
                                        onChange={(event) =>
                                            updateNode(editingNode.id, {
                                                options: editingNode.options.map((item) => (item.id === option.id ? { ...item, label: event.target.value } : item)),
                                            })
                                        }
                                    />
                                    <select
                                        value={option.to}
                                        onChange={(event) =>
                                            updateNode(editingNode.id, {
                                                options: editingNode.options.map((item) => (item.id === option.id ? { ...item, to: event.target.value } : item)),
                                            })
                                        }
                                    >
                                        {nodes
                                            .filter((node) => node.id !== editingNode.id)
                                            .map((node) => (
                                                <option key={node.id} value={node.id}>
                                                    {node.id} · {node.title}
                                                </option>
                                            ))}
                                    </select>
                                    <button
                                        type="button"
                                        className="story-icon-button is-danger"
                                        title="删除选项"
                                        onClick={() =>
                                            updateNode(editingNode.id, {
                                                options: editingNode.options.filter((item) => item.id !== option.id),
                                            })
                                        }
                                    >
                                        <Trash2 className="size-3.5" />
                                    </button>
                                </div>
                            ))}
                            {nodes.length > 1 ? (
                                <button
                                    type="button"
                                    className="story-button is-wide"
                                    onClick={() => {
                                        const target = nodes.find((node) => node.id !== editingNode.id);
                                        if (!target) return;
                                        updateNode(editingNode.id, {
                                            options: [
                                                ...editingNode.options,
                                                {
                                                    id: `${editingNode.id}-${Date.now().toString(36)}`,
                                                    label: "新选项",
                                                    to: target.id,
                                                },
                                            ],
                                        });
                                    }}
                                >
                                    <Plus className="size-3.5" />
                                    添加选项
                                </button>
                            ) : null}
                        </div>
                    </div>

                    <div className="story-inspector__foot">
                        <button type="button" className="story-button" onClick={() => centerOn(editingNode.id)}>
                            <Crosshair className="size-3.5" />
                            定位节点
                        </button>
                        <button type="button" className="story-button is-danger" disabled={nodes.length <= 1} onClick={() => removeNode(editingNode.id)}>
                            <Trash2 className="size-3.5" />
                            删除节点
                        </button>
                        <button type="button" className="story-button is-primary" onClick={() => setEditingId(null)}>
                            完成
                        </button>
                    </div>
                </aside>
            ) : null}

            <div className="story-fab">
                <button type="button" className="story-fab__button is-primary" onClick={() => addNode("story")}>
                    <Plus className="size-4" />
                    剧情
                </button>
                <button type="button" className="story-fab__button" onClick={() => addNode("branch")}>
                    <GitBranch className="size-4" />
                    分支
                </button>
            </div>

            {selectedNode ? (
                <div className="story-selection">
                    已选中 <strong>{selectedNode.id}</strong> · {selectedNode.title}
                </div>
            ) : null}
        </div>
    );
}
