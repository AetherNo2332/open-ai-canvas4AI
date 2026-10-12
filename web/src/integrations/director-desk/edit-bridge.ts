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
    /** 撤销上一次提交：把目标文档的场景走回同一条提交路径（草稿/CAS/冲突语义一致）。 */
    undo: () => boolean;
    redo: () => boolean;
    canUndo: () => boolean;
    canRedo: () => boolean;
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

/** 文档快照栈深度：与上游 DirectorDesk SceneSession 的 undo 上限一致。 */
const UNDO_DEPTH = 35;

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

    // 文档快照历史（桥内持有）：undoStack 是当前文档之前的版本，redoStack 是
    // 被撤销的版本。present 记录当前已提交文档与其内容指纹（去重）。
    let undoStack: DirectorProject[] = [];
    let redoStack: DirectorProject[] = [];
    let present: { json: string; document: DirectorProject } | null = null;

    const pushHistory = (nextDocument: DirectorProject, nextJson: string): void => {
        if (present) {
            undoStack.push(present.document);
            if (undoStack.length > UNDO_DEPTH) undoStack.shift();
        }
        redoStack = [];
        present = { json: nextJson, document: structuredClone(nextDocument) };
    };

    const restoreHistory = (direction: "undo" | "redo"): boolean => {
        if (disposed) return false;
        const target = direction === "undo"
            ? (undoStack.length ? undoStack[undoStack.length - 1] : null)
            : (redoStack.length ? redoStack[redoStack.length - 1] : null);
        if (!target || !present) return false;

        let scene: PrevisScene;
        try {
            scene = directorDocumentToPrevisScene(target);
        } catch {
            // 已提交过的文档必然可转换；防御性拒绝，历史保持不变。
            return false;
        }
        if (scene.id !== expectedSceneId) return false;

        // 关键：恢复 = 把目标场景再提交一次，走与正常提交完全相同的路径。
        // 被拒绝（如场景切换）时不移动任何历史指针。
        if (!transport.commit(scene)) return false;

        if (direction === "undo") {
            undoStack.pop();
            redoStack.push(present.document);
        } else {
            redoStack.pop();
            undoStack.push(present.document);
        }
        if (undoStack.length > UNDO_DEPTH) undoStack.shift();
        if (redoStack.length > UNDO_DEPTH) redoStack.shift();

        const json = JSON.stringify(target);
        present = { json, document: structuredClone(target) };

        const record: DirectorCommitRecord = { scene, revision: transport.snapshot().revision, status: "pending" };
        current = record;
        attach(record);
        return true;
    };

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

        // 历史只在内容变化时前进：相同内容的重复提交（提交回执/无操作改动）
        // 不产生新的 undo 项。
        const json = JSON.stringify(document);
        if (!present || present.json !== json) pushHistory(document, json);

        current = { scene, revision: transport.snapshot().revision, status: "pending" };
        attach(current);
        return current;
    };

    const getRecord = () => current;

    const undo = (): boolean => restoreHistory("undo");
    const redo = (): boolean => restoreHistory("redo");
    const canUndo = (): boolean => !disposed && undoStack.length > 0;
    const canRedo = (): boolean => !disposed && redoStack.length > 0;

    const dispose = () => {
        if (disposed) return;
        disposed = true;
        detach();
        current = null;
        undoStack = [];
        redoStack = [];
        present = null;
    };

    return { commitDocument, getRecord, undo, redo, canUndo, canRedo, dispose };
}