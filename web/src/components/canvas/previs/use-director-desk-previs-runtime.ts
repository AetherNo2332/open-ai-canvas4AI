import { useEffect, useRef, useState, type RefObject } from "react";
import { createDirectorRuntime, disposeDirectorRuntime, type DirectorRuntime } from "@/integrations/director-desk";
import { previsSceneToDirectorDocument } from "@/integrations/director-desk/previs-adapter";
import type { PrevisScene } from "@/types/previs";

export type DirectorDeskPrevisRuntimeState = {
    stageRef: RefObject<HTMLDivElement | null>;
    shotRef: RefObject<HTMLDivElement | null>;
    runtime: DirectorRuntime | null;
    status: "idle" | "ready" | "error";
    error: unknown;
};

/** Owns the DirectorDesk browser runtime for exactly one workbench session. */
export function useDirectorDeskPrevisRuntime(open: boolean, scene: PrevisScene | null): DirectorDeskPrevisRuntimeState {
    const stageRef = useRef<HTMLDivElement>(null);
    const shotRef = useRef<HTMLDivElement>(null);
    const runtimeRef = useRef<DirectorRuntime | null>(null);
    const [status, setStatus] = useState<DirectorDeskPrevisRuntimeState["status"]>("idle");
    const [error, setError] = useState<unknown>(null);

    useEffect(() => {
        if (!open || !scene || !stageRef.current || !shotRef.current) return;
        if (runtimeRef.current) return;
        try {
            runtimeRef.current = createDirectorRuntime({ stage: stageRef.current, shot: shotRef.current });
            const document = previsSceneToDirectorDocument(scene);
            runtimeRef.current.scene.userData.directorDocument = document;
            runtimeRef.current.syncDocument(document);
            runtimeRef.current.render();
            setStatus("ready");
        } catch (cause) {
            setError(cause);
            setStatus("error");
        }
        return () => {
            if (!runtimeRef.current) return;
            disposeDirectorRuntime(runtimeRef.current);
            runtimeRef.current = null;
            setStatus("idle");
        };
    }, [open, Boolean(scene)]);

    useEffect(() => {
        if (!runtimeRef.current || !scene) return;
        const document = previsSceneToDirectorDocument(scene);
        runtimeRef.current.scene.userData.directorDocument = document;
        runtimeRef.current.syncDocument(document);
        runtimeRef.current.render();
    }, [scene]);

    return { stageRef, shotRef, runtime: runtimeRef.current, status, error };
}
