import { directorDocumentToPrevisScene } from "./previs-adapter";
import type { DirectorProject } from "./previs-runtime-types";
import type { PrevisSaveCoordinator } from "@/lib/canvas/previs/previs-save";
import type { PrevisSaveController } from "@/components/canvas/previs/use-previs-save-coordinator";
import type { PrevisScene } from "@/types/previs";

export type DirectorCommitStatus = "saved" | "dirty" | "saving" | "error";

export type DirectorCommitProgress = {
    revision: number;
    confirmedRevision: number;
    status: DirectorCommitStatus;
};

export type DirectorCommitTransport = {
    commit: (scene: PrevisScene) => boolean;
    snapshot: () => DirectorCommitProgress;
    onStatus: (handler: (status: DirectorCommitStatus) => void) => () => void;
};

export type DirectorCommitConflictReason = "translation" | "rejected" | "save-failed";

export type DirectorCommitRecord = {
    scene?: PrevisScene;
    revision: number;
    status: "pending" | "succeeded" | "conflict";
    conflictReason?: DirectorCommitConflictReason;
};

export type DirectorEditBridgeOptions = {
    expectedSceneId: string;
    onCommitSucceeded?: (record: DirectorCommitRecord) => void;
    onCommitConflict?: (record: DirectorCommitRecord) => void;
};

export type DirectorEditBridge = {
    /** Convert one runtime document commit and drive it into the save coordinator. */
    commitDocument: (document: DirectorProject) => DirectorCommitRecord;
    getRecord: () => DirectorCommitRecord | null;
    dispose: () => void;
};

function toTransport(commit: (scene: PrevisScene) => boolean, snapshot: () => DirectorCommitProgress, onStatus: DirectorCommitTransport["onStatus"]): DirectorCommitTransport {
    return { commit, snapshot, onStatus };
}

/** Adapt the raw previs save coordinator to the bridge commit transport (unit-test seam). */
export function createDirectorCommitTransport(coordinator: PrevisSaveCoordinator): DirectorCommitTransport {
    return toTransport(
        (scene) => coordinator.edit(scene),
        () => {
            const snapshot = coordinator.getSnapshot();
            return { revision: snapshot.revision, confirmedRevision: snapshot.confirmedRevision, status: snapshot.status };
        },
        (handler) => coordinator.onStatusChange(handler),
    );
}

/** Adapt the workbench-level save controller (stable callbacks) to the same transport. */
export function createDirectorControllerTransport(controller: PrevisSaveController): DirectorCommitTransport {
    return toTransport(
        (scene) => controller.commitScene(scene),
        () => {
            const progress = controller.currentSnapshot();
            return { revision: progress.revision, confirmedRevision: progress.confirmedRevision, status: progress.status };
        },
        (handler) => controller.onSaveStatus((progress) => handler(progress.status)),
    );
}

/** No commit revision was allocated (translation conflict or transport rejection). */
const NO_REVISION = -1;

/**
 * Bridges DirectorDesk runtime commits into the existing previs save
 * coordinator. Every commit is converted to a PrevisScene, pushed through
 * `edit` (local draft + debounced CAS flush), and the coordinator status
 * decides the commit outcome: saved/drain-confirmed is success, an error
 * status is a conflict the host can retry by committing again. Host
 * callbacks are isolated so they cannot break the commit path.
 */
export function createDirectorEditBridge(transport: DirectorCommitTransport, options: DirectorEditBridgeOptions): DirectorEditBridge {
    const { expectedSceneId, onCommitSucceeded, onCommitConflict } = options;

    let current: DirectorCommitRecord | null = null;
    let unsubscribe: (() => void) | null = null;
    let disposed = false;

    const detach = () => {
        if (unsubscribe) {
            unsubscribe();
            unsubscribe = null;
        }
    };

    const notifyConflict = (record: DirectorCommitRecord) => {
        try {
            onCommitConflict?.(record);
        } catch {
            // A host callback must never break the commit path.
        }
    };

    const notifySucceeded = (record: DirectorCommitRecord) => {
        try {
            onCommitSucceeded?.(record);
        } catch {
            // A host callback must never break the commit path.
        }
    };

    const attach = (record: DirectorCommitRecord) => {
        detach();
        unsubscribe = transport.onStatus((status) => {
            if (disposed || current !== record) return;

            if (status === "error") {
                record.status = "conflict";
                record.conflictReason = "save-failed";
                detach();
                notifyConflict(record);
                return;
            }
            if (status !== "saved") return;
            if (transport.snapshot().confirmedRevision < record.revision) return;

            record.status = "succeeded";
            detach();
            notifySucceeded(record);
        });
    };

    const commitDocument = (document: DirectorProject): DirectorCommitRecord => {
        if (disposed) {
            return { revision: NO_REVISION, status: "conflict", conflictReason: "rejected" };
        }

        let scene: PrevisScene;
        try {
            scene = directorDocumentToPrevisScene(document);
        } catch {
            const record: DirectorCommitRecord = { revision: NO_REVISION, status: "conflict", conflictReason: "translation" };
            notifyConflict(record);
            return record;
        }

        if (scene.id !== expectedSceneId) {
            const record: DirectorCommitRecord = { revision: NO_REVISION, status: "conflict", conflictReason: "rejected" };
            notifyConflict(record);
            return record;
        }

        if (!transport.commit(scene)) {
            const record: DirectorCommitRecord = { revision: NO_REVISION, status: "conflict", conflictReason: "rejected" };
            notifyConflict(record);
            return record;
        }

        current = { scene, revision: transport.snapshot().revision, status: "pending" };
        attach(current);
        return current;
    };

    const getRecord = () => current;

    const dispose = () => {
        if (disposed) return;
        disposed = true;
        detach();
        current = null;
    };

    return { commitDocument, getRecord, dispose };
}