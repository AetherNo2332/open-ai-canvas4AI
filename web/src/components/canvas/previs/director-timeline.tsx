import { useRef, useState } from "react";
import { Pause, Play } from "lucide-react";
import { previsTimelineTotalDuration, resolveTimelineScrub, findShotAtTime } from "@/lib/canvas/previs/previs-timeline";
import { buildPrevisShotListItems } from "@/lib/canvas/previs/previs-shot-list";
import type { PrevisScene } from "@/types/previs";
import "./director-timeline.css";

export type DirectorTimelineProps = {
    scene: PrevisScene;
    /** 当前播放头（秒，整场时间轴）。 */
    time: number;
    playing: boolean;
    /** scrub/拖拽：目标时间（秒）。 */
    onScrub: (time: number) => void;
    onTogglePlay: () => void;
    /** 点击镜头段选中镜头。 */
    onSelectShot: (shotId: string) => void;
};

const percentOf = (value: number, total: number): string => {
    if (total <= 0) return "0%";
    const pct = (value / total) * 100;
    return `${pct % 1 === 0 ? pct : pct.toFixed(2)}%`;
};

const formatSeconds = (value: number): string => `${value.toFixed(1)}s`;

/**
 * DirectorDesk 原生时间轴（导演编辑器 scope，P1）：
 * 整场播放头 scrub + 播放/暂停 + 镜头段（按时长占比布局）点击切换。
 * 纯展示 + 回调；播放循环与引擎采样由 runtime 持有。
 */
export function DirectorTimeline({ scene, time, playing, onScrub, onTogglePlay, onSelectShot }: DirectorTimelineProps) {
    const trackRef = useRef<HTMLDivElement | null>(null);
    const [scrubbing, setScrubbing] = useState(false);
    const total = previsTimelineTotalDuration(scene);
    const clampedTime = Math.min(Math.max(0, time), total);
    const activeShotId = findShotAtTime(scene, clampedTime);

    const scrubFromPointer = (clientX: number): void => {
        const track = trackRef.current;
        if (!track || total <= 0) return;
        const rect = track.getBoundingClientRect();
        if (rect.width <= 0) return;
        onScrub(resolveTimelineScrub(scene, (clientX - rect.left) / rect.width));
    };

    const startScrubbing = (event: React.PointerEvent<HTMLDivElement>): void => {
        if (total <= 0) return;
        event.preventDefault();
        (event.target as HTMLElement).setPointerCapture?.(event.pointerId);
        setScrubbing(true);
        scrubFromPointer(event.clientX);
    };

    return (
        <div className="dtn-root">
            <div className="dtn-bar">
                <button type="button" className="dtn-play" onClick={onTogglePlay} aria-pressed={playing} aria-label={playing ? "暂停" : "播放"} disabled={total <= 0}>
                    {playing ? <Pause className="size-3.5" aria-hidden /> : <Play className="size-3.5" aria-hidden />}
                    <span>{playing ? "暂停" : "播放"}</span>
                </button>
                <span className="dtn-clock" aria-label="当前时间">
                    <strong>{formatSeconds(clampedTime)}</strong>
                    <small> / {formatSeconds(total)}</small>
                </span>
            </div>
            {total <= 0 ? (
                <div className="dtn-empty" role="status">
                    空时间轴
                </div>
            ) : (
                <div
                    ref={trackRef}
                    className={`dtn-track ${scrubbing ? "dtn-track--scrubbing" : ""}`}
                    role="slider"
                    aria-label="播放头"
                    aria-valuemin={0}
                    aria-valuemax={Math.round(total * 10) / 10}
                    aria-valuenow={Math.round(clampedTime * 10) / 10}
                    tabIndex={0}
                    onPointerDown={startScrubbing}
                    onPointerMove={(event) => {
                        if (scrubbing) scrubFromPointer(event.clientX);
                    }}
                    onPointerUp={() => setScrubbing(false)}
                    onPointerCancel={() => setScrubbing(false)}
                    onKeyDown={(event) => {
                        if (event.key === "ArrowLeft") {
                            event.preventDefault();
                            onScrub(Math.max(0, clampedTime - 0.5));
                        } else if (event.key === "ArrowRight") {
                            event.preventDefault();
                            onScrub(Math.min(total, clampedTime + 0.5));
                        } else if (event.key === "Home") {
                            event.preventDefault();
                            onScrub(0);
                        } else if (event.key === "End") {
                            event.preventDefault();
                            onScrub(total);
                        }
                    }}
                >
                    <div className="dtn-spans" style={{ width: "100%" }}>
                        {buildPrevisShotListItems(scene).map((item) => {
                            const shot = item.shot;
                            const width = Math.max(0, shot.duration);
                            return (
                                <button
                                    key={shot.id}
                                    type="button"
                                    className={`dtn-span ${activeShotId === shot.id ? "dtn-span--active" : ""}`}
                                    style={{ left: percentOf(item.startAt, total), width: percentOf(width, total) }}
                                    onClick={() => onSelectShot(shot.id)}
                                    aria-label={`镜头 ${shot.name}：${formatSeconds(width)}`}
                                >
                                    <span className="dtn-span__name">{shot.name}</span>
                                    <small className="dtn-span__dur">{formatSeconds(width)}</small>
                                </button>
                            );
                        })}
                    </div>
                    <div className="dtn-head" data-time={Math.round(clampedTime * 10) / 10} style={{ left: percentOf(clampedTime, total) }} aria-hidden>
                        <span className="dtn-head__flag">{formatSeconds(clampedTime)}</span>
                    </div>
                </div>
            )}
        </div>
    );
}
