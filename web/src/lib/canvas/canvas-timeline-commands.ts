import type { TimelineOperation } from "./command-contract.generated";
type Entity = Record<string, any>;
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
export function canvasTimelineCommands(before: Entity | undefined, after: Entity | undefined, fields: { track: Record<string, unknown>; clip: Record<string, unknown>; media?: Record<string, unknown> } | undefined): TimelineOperation[] {
    if (same(before, after)) return [];
    if (!after) return [{ type: "clear" }];
    if (!fields) throw new Error("canvas_timeline_capabilities_missing");
    const ops: TimelineOperation[] = [];
    const deletedTracks = new Set<string>((before?.tracks || []).filter((track: Entity) => !(after.tracks || []).some((next: Entity) => next.id === track.id)).map((track: Entity) => track.id));
    for (const key of new Set([...Object.keys(before || {}), ...Object.keys(after)]))
        if (!["version", "tracks", "clips", "durationMs", "updatedAt"].includes(key) && !same(before?.[key], after[key])) throw new Error("canvas_editor_unmapped:timeline:" + key);
    for (const kind of ["track", "clip"] as const) {
        const key = kind + "s",
            old: Entity[] = before?.[key] || [],
            next: Entity[] = after[key] || [];
        const byId = new Map(old.map((item) => [item.id, item])),
            ids = new Set(next.map((item) => item.id));
        for (const item of old) if (!ids.has(item.id) && (kind !== "clip" || !deletedTracks.has(item.trackId))) ops.push({ type: "delete_" + kind, id: item.id });
        for (const item of next) {
            const previous = byId.get(item.id),
                patch: Entity = {};
            for (const field of new Set([...Object.keys(previous || {}), ...Object.keys(item)])) {
                if (field === "id" || same(previous?.[field], item[field])) continue;
                if (!Object.hasOwn(fields[kind], field)) throw new Error("canvas_editor_unmapped:timeline:" + item.id + ":" + field);
                if (field === "directMedia" && item[field]) {
                    const media: Entity = {};
                    for (const name of new Set([...Object.keys(previous?.directMedia || {}), ...Object.keys(item.directMedia)])) {
                        if (same(previous?.directMedia?.[name], item.directMedia[name])) continue;
                        if (!fields.media || !Object.hasOwn(fields.media, name)) throw new Error("canvas_editor_unmapped:timeline:media:" + name);
                        media[name] = structuredClone(item.directMedia[name] ?? null);
                    }
                    patch[field] = media;
                } else patch[field] = structuredClone(item[field] ?? null);
            }
            if (Object.keys(patch).length) ops.push({ type: "upsert_" + kind, id: item.id, patch });
        }
        const natural = old
            .map((item) => item.id)
            .filter((id) => ids.has(id))
            .concat(next.filter((item) => !byId.has(item.id)).map((item) => item.id));
        if (
            !same(
                natural,
                next.map((item) => item.id),
            )
        )
            ops.push(kind === "track" ? { type: "reorder_tracks", trackIds: next.map((item) => item.id) } : { type: "reorder_clips", clipIds: next.map((item) => item.id) });
    }
    return ops;
}
