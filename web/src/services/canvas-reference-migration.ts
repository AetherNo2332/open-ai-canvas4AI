import { nanoid } from "nanoid";
import type { Snapshot } from "@/lib/canvas/command-contract.generated";
import { buildCanvasReferenceMigrationOperation, referenceMigrationPendingScopeKeys, referenceMigrationScopeChoices, referenceMigrationRequired, type CanvasReferenceMigrationChoice } from "@/lib/canvas/canvas-reference-migration";
import type { CommandTransport } from "./canvas-command-session";

export async function migrateCanvasReferences(options: { snapshot: Snapshot; choices: Record<string, CanvasReferenceMigrationChoice>; transport: CommandTransport; save: () => Promise<void>; refresh: () => Promise<unknown> }): Promise<Snapshot> {
    const { snapshot, choices, transport, save, refresh } = options;
    await save();
    const current = await transport.snapshot(snapshot.canvasId);
    if (!referenceMigrationRequired(current.referenceMigration)) {
        await refresh();
        return current;
    }
    // 本地保存或其他窗口可能改变引用来源，旧选择不能批准新冲突。
    if (JSON.stringify(current.referenceMigration?.conflicts) !== JSON.stringify(snapshot.referenceMigration?.conflicts)) {
        throw new Error("引用来源已变化，请重新核对迁移选项");
    }
    if (referenceMigrationPendingScopeKeys(current.referenceMigration, choices).length) {
        throw new Error("还有作用域没有选择引用来源");
    }
    await transport.execute({
        protocolVersion: 1,
        canvasId: current.canvasId,
        documentEpoch: current.documentEpoch,
        baseRevision: current.revision,
        expectedEntityVersions: current.entityVersions,
        clientId: "web-reference-migration",
        operationId: nanoid(),
        operations: [buildCanvasReferenceMigrationOperation(current.canvasId, referenceMigrationScopeChoices(current.referenceMigration, choices))],
    });
    await refresh();
    const migrated = await transport.snapshot(current.canvasId);
    if (referenceMigrationRequired(migrated.referenceMigration)) throw new Error("引用规范化尚未完成，请重试");
    return migrated;
}
