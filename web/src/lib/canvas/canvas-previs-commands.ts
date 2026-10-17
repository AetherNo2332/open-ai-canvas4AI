import type { Operation, PrevisEntityOperation } from "./command-contract.generated";
type Entity = Record<string, any>;
export type PrevisEditorFields = Record<"settings" | "object" | "camera" | "light" | "shot", Record<string, unknown>>;
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
function patch(before: unknown, after: unknown): unknown {
    if (before && after && typeof before === "object" && typeof after === "object" && !Array.isArray(before) && !Array.isArray(after)) {
        return Object.fromEntries([...new Set([...Object.keys(before), ...Object.keys(after)])].filter((key) => !same((before as Entity)[key], (after as Entity)[key])).map((key) => [key, patch((before as Entity)[key], (after as Entity)[key])]));
    }
    return structuredClone(after ?? null);
}
function fieldsPatch(before: Entity | undefined, after: Entity, fields: Record<string, unknown>, ignored: string[]): Entity {
    const result: Entity = {};
    for (const key of new Set([...Object.keys(before || {}), ...Object.keys(after)])) {
        if (ignored.includes(key) || same(before?.[key], after[key])) continue;
        if (!Object.hasOwn(fields, key)) throw new Error("canvas_editor_unmapped:previs:" + after.id + ":" + key);
        result[key] = patch(before?.[key], after[key]);
    }
    return result;
}
export function canvasPrevisCommands(canvasId: string, before: Entity[] = [], after: Entity[] = [], fields?: PrevisEditorFields): Operation[] {
    if (same(before, after)) return [];
    if (!fields) throw new Error("canvas_previs_capabilities_missing");
    const operations: Operation[] = [],
        previous = new Map(before.map((scene) => [scene.id, scene])),
        nextIds = new Set(after.map((scene) => scene.id));
    for (const scene of before) if (!nextIds.has(scene.id)) operations.push({ type: "delete_previs_scene", id: scene.id });
    for (const scene of after) {
        const old = previous.get(scene.id);
        if (same(old, scene)) continue;
        const edits: PrevisEntityOperation[] = [];
        const settings = fieldsPatch(old, scene, fields.settings, ["id", "version", "createdAt", "updatedAt", "objects", "cameras", "lights", "shots"]);
        if (Object.keys(settings).length) edits.push({ type: "settings", patch: settings });
        for (const kind of ["object", "camera", "light", "shot"] as const) {
            const key = kind + "s",
                items: Entity[] = scene[key] || [],
                existing: Entity[] = old?.[key] || [],
                byId = new Map(existing.map((item) => [item.id, item])),
                ids = new Set(items.map((item) => item.id));
            for (const item of existing) if (!ids.has(item.id)) edits.push({ type: "delete_" + kind, id: item.id });
            for (const item of items) {
                const update = fieldsPatch(byId.get(item.id), item, fields[kind], ["id"]);
                if (Object.keys(update).length) edits.push({ type: "upsert_" + kind, id: item.id, patch: update });
            }
            const natural = existing
                .map((item) => item.id)
                .filter((id) => ids.has(id))
                .concat(items.filter((item) => !byId.has(item.id)).map((item) => item.id));
            if (
                !same(
                    natural,
                    items.map((item) => item.id),
                )
            )
                edits.push({ type: "reorder_" + kind, ids: items.map((item) => item.id) });
        }
        if (edits.length) operations.push({ type: "edit_previs_scene", id: scene.id, ...(!old ? { action: "initialize" } : {}), previsEntityOperations: edits });
    }
    const natural = before
        .map((scene) => scene.id)
        .filter((id) => nextIds.has(id))
        .concat(after.filter((scene) => !previous.has(scene.id)).map((scene) => scene.id));
    if (
        !same(
            natural,
            after.map((scene) => scene.id),
        )
    )
        operations.push({ type: "edit_previs_scene", id: canvasId, action: "reorder", previsEntityOperations: [{ type: "reorder_scenes", ids: after.map((scene) => scene.id) }] });
    return operations;
}
