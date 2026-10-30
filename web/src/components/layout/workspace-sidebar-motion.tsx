import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import type { ReactNode } from "react";
import { Link } from "react-router";

export const WorkspaceMotionLink = motion.create(Link);

/** Keep closing content visible for its exit, but immediately remove keyboard access. */
export function WorkspaceSidebarDisclosure({ open, children }: { open: boolean; children: ReactNode }) {
    return (
        <div inert={!open}>
            <AnimatePresence initial={false}>
                {open ? (
                    <motion.div key="content" initial={{ height: 0, opacity: 0 }} animate={{ height: "auto", opacity: 1 }} exit={{ height: 0, opacity: 0 }} style={{ overflow: "hidden" }}>
                        {children}
                    </motion.div>
                ) : null}
            </AnimatePresence>
        </div>
    );
}

export function WorkspaceSidebarLabel({ children, identity }: { children: ReactNode; identity: string }) {
    const reducedMotion = useReducedMotion();
    return (
        <AnimatePresence initial={false} mode="wait">
            <motion.span key={identity} className="app-workspace-nav-title truncate" initial={{ opacity: 0, x: reducedMotion ? 0 : -4 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: reducedMotion ? 0 : -4 }}>
                {children}
            </motion.span>
        </AnimatePresence>
    );
}

/** 侧栏展开/收起的过渡：时长来自当前皮肤的常规过渡（ms），减少动态效果时直接跳到终态。 */
export function workspaceSidebarTransition(reducedMotion: boolean | null, motionNormalMs: number) {
    return { duration: reducedMotion ? 0 : Math.max(0, motionNormalMs) / 1000, ease: [0.2, 0.8, 0.2, 1] as const };
}
