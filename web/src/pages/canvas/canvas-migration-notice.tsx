import type { ReactNode } from "react";
import { Button } from "antd";

export type CanvasMigrationNoticeItem = {
    key: string;
    title: string;
    description: string;
    action: string;
    onAction: () => void;
    icon: ReactNode;
};

/**
 * 画布格式升级的统一入口。旧引用与旧分镜可能同时待处理，集中成一张卡并
 * 固定在顶栏下方，避免多个横幅与顶部视图切换控件互相覆盖。
 */
export function CanvasMigrationNotice({ items }: { items: CanvasMigrationNoticeItem[] }) {
    if (!items.length) return null;
    return (
        <div className="pointer-events-none absolute inset-x-0 top-[var(--canvas-topbar-offset)] z-[var(--z-panel-floating)] flex justify-center px-3">
            <div role="status" className="pointer-events-auto w-[min(720px,100%)] overflow-hidden rounded-[var(--card-radius)] border border-border/60 bg-[var(--site-surface)] text-foreground shadow-[var(--shadow-lg)]" data-canvas-no-zoom>
                <div className="flex items-center justify-between gap-3 border-b border-border/50 px-4 py-2.5">
                    <p className="text-sm font-semibold">画布升级</p>
                    <span className="shrink-0 text-[var(--fs-tiny)] text-foreground/50">{items.length} 项待处理</span>
                </div>
                <ul className="divide-y divide-border/50">
                    {items.map((item) => (
                        <li key={item.key} className="flex flex-wrap items-start gap-3 px-4 py-3 sm:flex-nowrap">
                            <span className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-[var(--r-md)] bg-foreground/[0.06] text-foreground/70">{item.icon}</span>
                            <div className="min-w-0 flex-1">
                                <p className="text-sm font-medium">{item.title}</p>
                                <p className="mt-0.5 text-[var(--fs-label)] leading-5 text-foreground/65 text-pretty">{item.description}</p>
                            </div>
                            <Button className="min-h-9 shrink-0" onClick={item.onAction}>
                                {item.action}
                            </Button>
                        </li>
                    ))}
                </ul>
            </div>
        </div>
    );
}
