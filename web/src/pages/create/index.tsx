import { ArrowUpRight } from "lucide-react";
import { useRef, type MouseEvent } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { App } from "antd";
import restingBoy from "../../../../assets/home-resting-boy.png";
import { latestEditedCanvasId } from "@/lib/canvas/canvas-navigation";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";
import { useUserStore } from "@/stores/use-user-store";
import { listRemoteCanvasProjectsPage } from "@/services/api/user-data";
import "./canvas-home.css";

export default function CreatePage() {
    const { message } = App.useApp();
    const navigate = useNavigate();
    const openingRef = useRef(false);
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

    return (
        <section className="canvas-home" aria-labelledby="canvas-home-title">
            <img className="canvas-home-character" src={restingBoy} alt="" aria-hidden="true" width={1536} height={1024} draggable={false} />
            <div className="canvas-home-inner">
                <div className="canvas-home-grid">
                    <div className="canvas-home-copy">
                        <h1 id="canvas-home-title">让想法<br />在画布上展开。</h1>
                        <p className="canvas-home-description">把故事、画面与灵感放在一起。<br />在一张画布上，构建你的下一部作品。</p>
                        <Link className="canvas-home-entry" to={continueCanvasHref} onClick={continueCanvas}>
                            <span>从上一次离开的地方继续</span>
                            <ArrowUpRight aria-hidden="true" />
                        </Link>
                    </div>
                </div>
            </div>
        </section>
    );
}
