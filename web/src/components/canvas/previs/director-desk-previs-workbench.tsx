import type { ComponentProps } from "react";
import { CanvasPrevisWorkbench } from "./canvas-previs-workbench";
import { useDirectorDeskPrevisRuntime } from "./use-director-desk-previs-runtime";
import "./director-desk-previs-workbench.css";

export type DirectorDeskPrevisWorkbenchProps = ComponentProps<typeof CanvasPrevisWorkbench>;

/**
 * Single previs entry point backed by the DirectorDesk browser runtime.
 * The existing workbench remains the product shell while the runtime seam is
 * migrated incrementally; Agent/save/approval callbacks stay untouched.
 */
export function DirectorDeskPrevisWorkbench(props: DirectorDeskPrevisWorkbenchProps) {
    const runtime = useDirectorDeskPrevisRuntime(props.open, props.scene);
    return (
        <>
            <div className="director-desk-runtime-host" aria-hidden="true">
                <div ref={runtime.stageRef} className="director-desk-runtime-stage" data-director-runtime="stage" />
                <div ref={runtime.shotRef} className="director-desk-runtime-stage" data-director-runtime="shot" />
            </div>
            <CanvasPrevisWorkbench {...props} />
        </>
    );
}
