import { ArrowUpRight } from "lucide-react";
import { Link } from "react-router";
import restingBoy from "../../../../assets/home-resting-boy.png";
import { latestEditedCanvasId } from "@/lib/canvas/canvas-navigation";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";
import "./canvas-home.css";

export default function CreatePage() {
    const canvasProjects = useCanvasStore((state) => state.projects);
    const lastEditedCanvasId = latestEditedCanvasId(canvasProjects);
    const continueCanvasHref = lastEditedCanvasId ? `/canvas/${encodeURIComponent(lastEditedCanvasId)}` : "/canvas";

    return (
        <section className="canvas-home" aria-labelledby="canvas-home-title">
            <img className="canvas-home-character" src={restingBoy} alt="" aria-hidden="true" width={1536} height={1024} draggable={false} />
            <div className="canvas-home-inner">
                <div className="canvas-home-grid">
                    <div className="canvas-home-copy">
                        <h1 id="canvas-home-title">让想法<br />在画布上展开。</h1>
                        <p className="canvas-home-description">把故事、画面与灵感放在一起。<br />在一张画布上，构建你的下一部作品。</p>
                        <Link className="canvas-home-entry" to={continueCanvasHref}>
                            <span>从上一次离开的地方继续</span>
                            <ArrowUpRight aria-hidden="true" />
                        </Link>
                    </div>
                </div>
            </div>
        </section>
    );
}
