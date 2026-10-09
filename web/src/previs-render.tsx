import { createRoot } from "react-dom/client";
import { flushSync } from "react-dom";
import { useEffect, useRef, useState } from "react";
import { PrevisViewport, type PrevisViewportHandle } from "@/components/canvas/previs/previs-viewport";
import { preparePrevisRenderScene, type PrevisRenderState } from "@/lib/canvas/previs/previs-render";
import type { PrevisScene } from "@/types/previs";

type RendererAPI = { load: (scene: PrevisScene, shotId: string, duration: number) => void; state: () => PrevisRenderState; frame: (time: number) => Promise<string> };
declare global { interface Window { previsRenderer: RendererAPI } }
const noop = () => undefined;
const paint = () => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));

function Renderer() {
    const [scene, setScene] = useState<PrevisScene | null>(null);
    const [time, setTime] = useState(0);
    const [generation, setGeneration] = useState(0);
    const viewport = useRef<PrevisViewportHandle>(null);
    useEffect(() => {
        window.previsRenderer = {
            load: (input, shotId, duration) => flushSync(() => { setScene(preparePrevisRenderScene(input, shotId, duration)); setTime(0); setGeneration((value) => value + 1); }),
            state: () => viewport.current?.readRenderState() ?? { ready: false, error: "", pending: [] },
            frame: async (playhead) => {
                if (!Number.isFinite(playhead) || playhead < 0 || playhead > 60) throw new Error("invalid_frame_time");
                flushSync(() => setTime(playhead));
                await paint();
                const current = viewport.current;
                if (!current?.readRenderState().ready) throw new Error(current?.readRenderState().error || "viewport_not_ready");
                const blob = await current.capture("clay");
                return new Promise<string>((resolve, reject) => {
                    const reader = new FileReader();
                    reader.onerror = () => reject(new Error("frame_read_failed"));
                    reader.onload = () => resolve(String(reader.result).split(",", 2)[1]);
                    reader.readAsDataURL(blob);
                });
            },
        };
    }, []);
    if (!scene) return null;
    return <PrevisViewport key={generation} ref={viewport} scene={scene} selectedObjectId={null} selectedBone={null} transformMode="translate" renderMode="clay" playhead={time} playing={false} viewMode="camera" showNavigation={false} showModelLoadNotice={false} onSelectObject={noop} onSelectBone={noop} onObjectTransform={noop} onBoneTransform={noop} onActorRigReady={noop} />;
}

createRoot(document.getElementById("root")!).render(<Renderer />);
