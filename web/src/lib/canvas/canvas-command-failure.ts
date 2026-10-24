const versionConflicts = new Set(["revision_conflict", "entity_version_conflict", "document_epoch_mismatch", "history_conflict", "projection_conflict"]);
export function classifyCanvasCommandFailure(status: number | undefined, reason: string | undefined): "conflict" | "rejected" | "unconfirmed" {
    if (status !== undefined && (status >= 500 || [408, 425, 429].includes(status))) return "unconfirmed";
    if (reason && versionConflicts.has(reason)) return "conflict";
    return status !== undefined && status >= 400 && status < 500 ? "rejected" : "unconfirmed";
}

export function historicalCanvasRejection(reason: string | undefined) {
    return !!reason && ["invalid_operation", "invalid_reference", "reference_missing", "permission_denied", "node_locked", "edit_proof_invalid", "operation_id_reused"].includes(reason);
}
