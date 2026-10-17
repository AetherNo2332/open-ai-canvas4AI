import { ArrowUpRight } from "lucide-react";
import { useRef, useState, type MouseEvent, type PointerEvent } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { App } from "antd";
import { motion, useMotionTemplate, useMotionValue, useReducedMotion } from "motion/react";
import { HoverBorderGradient } from "@/components/ui/aceternity/hover-border-gradient";
import { aceternityMotion } from "@/lib/aceternity-motion";
import restingBoy from "../../../../assets/home-resting-boy.png";
import { latestEditedCanvasId } from "@/lib/canvas/canvas-navigation";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";
import { useUserStore } from "@/stores/use-user-store";
import { listRemoteCanvasProjectsPage } from "@/services/api/user-data";
import "./canvas-home.css";

const riseVariants = {
    hidden: { opacity: 0, y: 24 },
    show: { opacity: 1, y: 0, transition: { duration: aceternityMotion.duration.panel, ease: aceternityMotion.easing.enter } },
} as const;

const fadeVariants = {
    hidden: { opacity: 0 },
    show: { opacity: 1, transition: { duration: 0.5, ease: aceternityMotion.easing.enter } },
} as const;

export default function CreatePage() {
    const { message } = App.useApp();
    const navigate = useNavigate();
    const openingRef = useRef(false);
    const reducedMotion = useReducedMotion();
    const glowX = useMotionValue(0);
    const glowY = useMotionValue(0);
    const glowMask = useMotionTemplate`radial-gradient(320px circle at ${glowX}px ${glowY}px, black, transparent 78%)`;
    const [pointerInside, setPointerInside] = useState(false);
    const userId = useUserStore((state) => state.user?.id);
    const sessionHydrated = useUserStore((state) => state.hydrated);
    const localProjects = useCanvasStore((state) => state.projects);
    const latestCanvasQuery = useQuery({
        queryKey: ["home-latest-canvas", userId],
        queryFn: ({ signal }) => listRemoteCanvasProjectsPage({ page: 1, pageSize: 1, sort: "updated", signal }),
        enabled: Boolean(userId) && sessionHydrated,
    });
    const canvasProjects = userId ? latestCanvasQuery.data?.projects || [] : localProjects;
    const lastEditedCanvasId = latestEditedCanvasId(canvasProjects);
    const continueCanvasHref = lastEditedCanvasId ? `/canvas/${encodeURIComponent(lastEditedCanvasId)}` : "/canvas";

    const trackPointer = (event: PointerEvent<HTMLElement>) => {
        const bounds = event.currentTarget.getBoundingClientRect();
        glowX.set(event.clientX - bounds.left);
        glowY.set(event.clientY - bounds.top);
    };

    const continueCanvas = async (event: MouseEvent<HTMLAnchorElement>) => {
        if (!userId || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
        event.preventDefault();
        if (openingRef.current) return;
        openingRef.current = true;
        try {
            const result = await latestCanvasQuery.refetch();
            if (useUserStore.getState().user?.id !== userId) return;
            if (result.isError) {
                message.error("读取最近画布失败，请重试");
                return;
            }
            const id = latestEditedCanvasId(result.data?.projects || []);
            navigate(id ? `/canvas/${encodeURIComponent(id)}` : "/canvas");
        } finally {
            openingRef.current = false;
        }
    };

    // 首次挂载播一次入场分展；reduced-motion 直接跳到最终状态，不播任何过渡。
    const entranceState = reducedMotion ? "show" : "hidden";

    return (
        <section
            className="canvas-home"
            aria-labelledby="canvas-home-title"
            onPointerEnter={(event) => {
                if (reducedMotion) return;
                trackPointer(event);
                setPointerInside(true);
            }}
            onPointerMove={reducedMotion ? undefined : trackPointer}
            onPointerLeave={() => setPointerInside(false)}
        >
            {/* 指针跟随光斑：只动 opacity/transform，颜色由皮肤 token 派生，reduced-motion 不渲染。 */}
            {reducedMotion ? null : (
                <motion.span
                    aria-hidden
                    className="canvas-home-glow"
                    style={{
                        backgroundColor: "color-mix(in srgb, var(--primary) 10%, transparent)",
                        maskImage: glowMask,
                        WebkitMaskImage: glowMask,
                        opacity: pointerInside ? 1 : 0,
                    }}
                />
            )}
            <motion.img
                className="canvas-home-character"
                src={restingBoy}
                alt=""
                aria-hidden="true"
                width={1536}
                height={1024}
                draggable={false}
                variants={fadeVariants}
                initial={entranceState}
                animate="show"
                transition={{ delay: 0.25 }}
            />
            <div className="canvas-home-inner">
                <motion.div
                    className="canvas-home-grid"
                    variants={{ hidden: {}, show: { transition: { staggerChildren: 0.08, delayChildren: 0.05 } } }}
                    initial={entranceState}
                    animate="show"
                >
                    <div className="canvas-home-copy">
                        <motion.h1 id="canvas-home-title" variants={riseVariants}>让想法<br />在画布上展开。</motion.h1>
                        <motion.p className="canvas-home-description" variants={riseVariants}>把故事、画面与灵感放在一起。<br />在一张画布上，构建你的下一部作品。</motion.p>
                        <motion.div className="canvas-home-entry-motion" variants={riseVariants}>
                            <HoverBorderGradient
                                as="div"
                                duration={0.9}
                                idleRotate={false}
                                containerClassName="canvas-home-entry-shell rounded-[var(--button-radius)]"
                                className="p-0"
                            >
                                <Link className="canvas-home-entry" to={continueCanvasHref} onClick={continueCanvas}>
                                    <span>从上一次离开的地方继续</span>
                                    <ArrowUpRight aria-hidden="true" />
                                </Link>
                            </HoverBorderGradient>
                        </motion.div>
                    </div>
                </motion.div>
            </div>
        </section>
    );
}
