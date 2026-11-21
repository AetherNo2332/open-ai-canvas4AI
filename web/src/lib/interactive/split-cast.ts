import { normalizeSplitName, type SplitCast, type SplitRow } from "./split-types";

/** 汇总角色与场景名称（不是 assetId），按首次出现顺序去重，口径同后端 NormalizeName。 */
export function collectCast(rows: SplitRow[]): SplitCast {
    const characters: string[] = [];
    const scenes: string[] = [];
    const seenCharacters = new Set<string>();
    const seenScenes = new Set<string>();
    for (const row of rows) {
        for (const name of row.characters) {
            const key = normalizeSplitName(name);
            if (!key || seenCharacters.has(key)) continue;
            seenCharacters.add(key);
            characters.push(name);
        }
        for (const name of row.scenes) {
            const key = normalizeSplitName(name);
            if (!key || seenScenes.has(key)) continue;
            seenScenes.add(key);
            scenes.push(name);
        }
    }
    return { characters, scenes };
}
