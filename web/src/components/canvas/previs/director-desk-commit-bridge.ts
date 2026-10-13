import type { PrevisScene } from "@/types/previs";

export type DirectorDeskSceneCommit = (scene: PrevisScene) => void;

/**
 * Keeps runtime edits on the workbench's canonical save path. The fallback is
 * used only during mount/unmount gaps before the coordinator registers.
 */
export function createDirectorDeskCommitBridge(fallback: DirectorDeskSceneCommit) {
    let registered: DirectorDeskSceneCommit | null = null;
    return {
        commit(scene: PrevisScene) {
            (registered || fallback)(scene);
        },
        register(commit: DirectorDeskSceneCommit) {
            registered = commit;
        },
        clear() {
            registered = null;
        },
    };
}
