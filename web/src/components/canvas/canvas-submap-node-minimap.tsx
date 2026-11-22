import { useEffect, useMemo, useRef } from "react";

import { SUBMAP_COLLAPSED_HEIGHT, SUBMAP_COLLAPSED_WIDTH, drawSubmapMinimap, submapMinimapTransform } from "@/lib/canvas/canvas-submap-minimap";
import type { CanvasTheme } from "@/lib/canvas-theme";
import type { CanvasNodeData } from "@/types/canvas";

/** 折叠态小地图画布：把 submap 内部节点按世界坐标等比画进固定小窗。 */
export function SubmapNodeMinimap({ nodes, theme }: { nodes: CanvasNodeData[]; theme: CanvasTheme }) {
    const canvasRef = useRef<HTMLCanvasElement>(null);
    const transform = useMemo(() => submapMinimapTransform(nodes, SUBMAP_COLLAPSED_WIDTH, SUBMAP_COLLAPSED_HEIGHT), [nodes]);

    useEffect(() => {
        drawSubmapMinimap(canvasRef.current, nodes, transform, theme);
    }, [nodes, theme, transform]);

    return <canvas ref={canvasRef} width={SUBMAP_COLLAPSED_WIDTH} height={SUBMAP_COLLAPSED_HEIGHT} className="pointer-events-none absolute inset-0 block size-full" aria-hidden="true" />;
}
