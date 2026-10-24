import { lazy, Suspense, type ComponentProps } from "react";
import type { VideoPlayer } from "@/components/video-player";

// Vidstack starts a requestAnimationFrame loop that never stops as soon as its
// module is evaluated, so a statically imported player keeps every canvas page
// producing frames while idle. Load it only when a video actually plays.
const DeferredVideoPlayer = lazy(() => import("@/components/video-player").then((module) => ({ default: module.VideoPlayer })));

export function LazyVideoPlayer(props: ComponentProps<typeof VideoPlayer>) {
    return (
        <Suspense fallback={null}>
            <DeferredVideoPlayer {...props} />
        </Suspense>
    );
}
