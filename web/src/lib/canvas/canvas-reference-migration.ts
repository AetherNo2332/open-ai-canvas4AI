import type { MigrationConflict, MigrationPreview, Operation } from "./command-contract.generated";

/** 规范化时每个作用域的来源选择：以权威字段为准，或以旧连线为准。 */
export type CanvasReferenceMigrationChoice = "fields" | "connections";

export type CanvasReferenceMigrationScope = {
    scopeKey: string;
    kind: string;
    kindLabel: string;
    nodeId: string;
    rowId?: string;
};

const SCOPE_KIND_LABELS: Record<string, string> = {
    "storyboard-row": "分镜行",
    "storyboard-context": "分镜表共享素材",
    "batch-row": "批量表行",
    generation: "生成节点",
    node: "节点输入",
};

/** 预览只有在文档缺 referenceFieldsVersion 时才出现；缺少预览表示已经规范化。 */
export function referenceMigrationRequired(preview: MigrationPreview | undefined): boolean {
    return Boolean(preview?.required);
}

export function referenceMigrationConflicts(preview: MigrationPreview | undefined): MigrationConflict[] {
    return preview?.conflicts ? [...preview.conflicts] : [];
}

/** 未选择来源的作用域；顺序与预览一致，供面板和提交前校验共用。 */
export function referenceMigrationPendingScopeKeys(preview: MigrationPreview | undefined, choices: Record<string, CanvasReferenceMigrationChoice>): string[] {
    return referenceMigrationConflicts(preview)
        .map((conflict) => conflict.scopeKey)
        .filter((scopeKey) => choices[scopeKey] !== "fields" && choices[scopeKey] !== "connections");
}

/** 只提交预览里真实存在、且用户确实选择了来源的作用域，避免把未知键当作选择。 */
export function referenceMigrationScopeChoices(preview: MigrationPreview | undefined, choices: Record<string, CanvasReferenceMigrationChoice>): Record<string, CanvasReferenceMigrationChoice> {
    const submitted: Record<string, CanvasReferenceMigrationChoice> = {};
    for (const conflict of referenceMigrationConflicts(preview)) {
        const choice = choices[conflict.scopeKey];
        if (choice === "fields" || choice === "connections") submitted[conflict.scopeKey] = choice;
    }
    return submitted;
}

export function buildCanvasReferenceMigrationOperation(canvasId: string, choices: Record<string, CanvasReferenceMigrationChoice>): Operation {
    if (!canvasId) throw new Error("画布标识缺失，无法迁移引用");
    const scopeChoices: Record<string, string> = {};
    for (const [scopeKey, choice] of Object.entries(choices)) {
        if (!scopeKey) continue;
        if (choice !== "fields" && choice !== "connections") throw new Error("引用来源只能是字段或旧连线");
        scopeChoices[scopeKey] = choice;
    }
    return { type: "migrate_references", id: canvasId, patch: { scopeChoices } };
}

/** scopeKey 由后端生成，前端只做展示拆分，不改写键。 */
export function referenceMigrationScope(scopeKey: string): CanvasReferenceMigrationScope {
    const [kind, nodeId = "", rowId] = scopeKey.split(":");
    return { scopeKey, kind, kindLabel: SCOPE_KIND_LABELS[kind] || kind || "未知作用域", nodeId, rowId };
}

export function referenceMigrationReasonMessage(reason: string | undefined): string | undefined {
    switch (reason) {
        case "reference_migration_required":
            return "请先确认并升级这张旧画布的素材关联，再继续编辑或生成";
        case "reference_migration_conflict":
            return "还有位置未选择要保留的素材，请逐项选择「保留素材列表」或「沿用旧连线」";
        case "reference_migration_not_required":
            return "这张画布的素材关联已经升级，无需重复操作";
        case "reference_missing":
            return "存在已丢失的素材或错误的自身关联。请检查下方节点，在素材设置中重新选择或移除关联后再试";
        default:
            return undefined;
    }
}

export function referenceMigrationErrorText(error: unknown): string {
    const reason = error && typeof error === "object" && "reason" in error ? String((error as { reason?: unknown }).reason || "") : "";
    return referenceMigrationReasonMessage(reason) || (error instanceof Error && error.message ? error.message : "素材关联尚未升级，请重试");
}

/** 编辑与生成前的门禁：未规范化的旧画布必须先走一次迁移。 */
export function assertCanvasReferenceMigrationReady(preview: MigrationPreview | undefined): void {
    if (referenceMigrationRequired(preview)) throw new Error(referenceMigrationReasonMessage("reference_migration_required")!);
}
