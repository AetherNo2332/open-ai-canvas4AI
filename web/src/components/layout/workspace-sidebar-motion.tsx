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
