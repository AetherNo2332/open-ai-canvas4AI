import { Button } from "antd";

/** 旧版画布提示条：不阻塞编辑，只给出一次性升级的入口（Agent 生成与素材关联需要先升级）。 */
export function CanvasReferenceMigrationBanner({ onOpen }: { onOpen: () => void }) {
    return (
        <div
            role="status"
            className="canvas-reference-migration-banner pointer-events-auto mx-auto mt-2 flex max-w-[min(720px,calc(100%-24px))] flex-wrap items-center gap-x-3 gap-y-1 rounded-[var(--card-radius)] border border-border/60 bg-[var(--site-surface)] px-3 py-2 text-sm text-foreground"
            data-canvas-no-zoom
        >
            <span className="min-w-0 flex-1 text-pretty">这是旧版画布：素材关联需要升级一次，升级后 Agent 才能在这里关联素材和生成。</span>
            <Button className="min-h-11" onClick={onOpen}>
                引用迁移
            </Button>
        </div>
    );
}
