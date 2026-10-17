import { useState } from "react";
import { ChevronDown, ChevronUp, Film, Plus, Trash2 } from "lucide-react";
import { shotSizeOptions, cameraMoveOptions } from "./previs-inspectors";
import { buildPrevisShotListItems } from "@/lib/canvas/previs/previs-shot-list";
import type { PrevisScene, PrevisShot } from "@/types/previs";
import "./director-shot-list.css";

export type DirectorShotListProps = {
    scene: PrevisScene;
    /** 选中镜头（活动镜头切换）。 */
    onSelectShot: (shotId: string) => void;
    onAddShot: () => void;
    onUpdateShot: (id: string, patch: Partial<PrevisShot>) => void;
    onRemoveShot: (id: string) => void;
    /** delta: -1 上移，1 下移。 */
    onMoveShot: (id: string, delta: -1 | 1) => void;
};

const formatSeconds = (value: number): string => {
    const rounded = Math.round(value * 10) / 10;
    return `${Number.isInteger(rounded) ? rounded : rounded.toFixed(1)}s`;
};

/**
 * DirectorDesk 原生镜头列表（导演编辑器 scope P1）：
 * 行内编辑名称/时长/景别/运镜，整场镜头的新增、删除、排序与激活。
 * 纯展示 + 回调：保存语义（草稿/CAS/flush）由持有保存协调器的容器负责。
 */
export function DirectorShotList({ scene, onSelectShot, onAddShot, onUpdateShot, onRemoveShot, onMoveShot }: DirectorShotListProps) {
    const items = buildPrevisShotListItems(scene);
    const active = items.find((item) => item.isActive);
    const [confirmingId, setConfirmingId] = useState<string | null>(null);

    const header = (
        <div className="dsl-header">
            <span className="dsl-header__label">
                镜头 <em>{items.length}</em>
            </span>
            <button type="button" className="dsl-add" onClick={onAddShot}>
                <Plus className="size-3.5" aria-hidden />
                新增镜头
            </button>
        </div>
    );

    if (!items.length) {
        return (
            <div className="dsl-root">
                {header}
                <div className="dsl-empty" role="status">
                    <Film className="dsl-empty__icon" aria-hidden />
                    <span>还没有镜头</span>
                </div>
            </div>
        );
    }

    return (
        <div className="dsl-root">
            {header}
            <ul className="dsl-list" aria-label="镜头列表">
                {items.map((item) => {
                    const shot = item.shot;
                    const confirming = confirmingId === shot.id;
                    return (
                        <li key={shot.id} className="dsl-row" data-active={item.isActive ? "true" : "false"} aria-current={item.isActive ? "true" : undefined}>
                            <button type="button" className="dsl-row__pick" onClick={() => onSelectShot(shot.id)} aria-label={`选中镜头 ${item.index + 1}：${shot.name}`}>
                                <span className="dsl-row__index">{item.index + 1}</span>
                                <small className="dsl-row__span">
                                    {item.startAt.toFixed(1)}–{item.endAt.toFixed(1)}s
                                </small>
                            </button>
                            <input
                                className="dsl-row__name"
                                type="text"
                                aria-label={`镜头 ${item.index + 1} 名称`}
                                defaultValue={shot.name}
                                key={`${shot.id}-${shot.name}`}
                                onBlur={(event) => {
                                    const value = event.target.value.trim();
                                    if (value && value !== shot.name) onUpdateShot(shot.id, { name: value });
                                }}
                                onKeyDown={(event) => {
                                    if (event.key === "Enter") (event.target as HTMLInputElement).blur();
                                }}
                            />
                            <input
                                className="dsl-row__dur"
                                type="number"
                                min={0.5}
                                max={120}
                                step={0.5}
                                aria-label={`镜头 ${item.index + 1} 时长（秒）`}
                                defaultValue={shot.duration}
                                key={`${shot.id}-d-${shot.duration}`}
                                onBlur={(event) => {
                                    const value = Number(event.target.value);
                                    if (Number.isFinite(value) && value !== shot.duration) onUpdateShot(shot.id, { duration: value });
                                }}
                                onKeyDown={(event) => {
                                    if (event.key === "Enter") (event.target as HTMLInputElement).blur();
                                }}
                            />
                            <small className="dsl-row__unit">{formatSeconds(shot.duration)}</small>
                            <select className="dsl-row__opt" aria-label={`镜头 ${item.index + 1} 景别`} value={shot.shotSize} onChange={(event) => onUpdateShot(shot.id, { shotSize: event.target.value as PrevisShot["shotSize"] })}>
                                {shotSizeOptions.map((option) => (
                                    <option key={option.value} value={option.value}>
                                        {option.label}
                                    </option>
                                ))}
                            </select>
                            <select className="dsl-row__opt" aria-label={`镜头 ${item.index + 1} 运镜`} value={shot.cameraMove} onChange={(event) => onUpdateShot(shot.id, { cameraMove: event.target.value as PrevisShot["cameraMove"] })}>
                                {cameraMoveOptions.map((option) => (
                                    <option key={option.value} value={option.value}>
                                        {option.label}
                                    </option>
                                ))}
                            </select>
                            <div className="dsl-row__actions">
                                <button type="button" className="dsl-icon-btn" aria-label="上移镜头" title="上移" disabled={item.index === 0} onClick={() => onMoveShot(shot.id, -1)}>
                                    <ChevronUp className="size-3.5" aria-hidden />
                                </button>
                                <button type="button" className="dsl-icon-btn" aria-label="下移镜头" title="下移" disabled={item.index === items.length - 1} onClick={() => onMoveShot(shot.id, 1)}>
                                    <ChevronDown className="size-3.5" aria-hidden />
                                </button>
                                <button
                                    type="button"
                                    className={`dsl-icon-btn ${confirming ? "dsl-icon-btn--confirm" : ""}`}
                                    aria-label={confirming ? "确认删除该镜头" : "删除镜头"}
                                    title={confirming ? "点击确认删除" : "删除"}
                                    disabled={items.length <= 1}
                                    onClick={() => {
                                        if (confirming) {
                                            setConfirmingId(null);
                                            onRemoveShot(shot.id);
                                        } else {
                                            setConfirmingId(shot.id);
                                        }
                                    }}
                                >
                                    <Trash2 className="size-3.5" aria-hidden />
                                    {confirming ? <span className="dsl-icon-btn__confirm">确认</span> : null}
                                </button>
                            </div>
                        </li>
                    );
                })}
            </ul>
            {active ? (
                <div className="dsl-prompt">
                    <label className="dsl-prompt__label" htmlFor="dsl-prompt-input">
                        镜头提示词
                    </label>
                    <textarea
                        id="dsl-prompt-input"
                        rows={3}
                        defaultValue={active.shot.prompt}
                        key={`${active.shot.id}-p`}
                        placeholder="镜头的运动与内容描述"
                        onBlur={(event) => {
                            if (event.target.value !== active.shot.prompt) onUpdateShot(active.shot.id, { prompt: event.target.value });
                        }}
                    />
                </div>
            ) : null}
        </div>
    );
}
