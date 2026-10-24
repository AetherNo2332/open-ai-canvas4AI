/**
 * Structural equality for JSON-shaped data: plain objects, arrays and
 * primitives. Properties holding `undefined` count as absent, as in JSON.
 * Any other object kind compares unequal unless it is the same reference, so
 * callers that use this to skip work fall back to doing the work.
 */
export function sameJsonData(left: unknown, right: unknown): boolean {
    if (Object.is(left, right)) return true;
    if (typeof left !== "object" || typeof right !== "object" || left === null || right === null) return left === right;
    if (Array.isArray(left) || Array.isArray(right)) {
        if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) return false;
        for (let index = 0; index < left.length; index++) if (!sameJsonData(left[index], right[index])) return false;
        return true;
    }
    const prototype = Object.getPrototypeOf(left);
    if ((prototype !== Object.prototype && prototype !== null) || Object.getPrototypeOf(right) !== prototype) return false;
    const leftRecord = left as Record<string, unknown>;
    const rightRecord = right as Record<string, unknown>;
    let leftKeys = 0;
    for (const key of Object.keys(leftRecord)) {
        if (leftRecord[key] === undefined) continue;
        leftKeys++;
        if (!Object.hasOwn(rightRecord, key) || !sameJsonData(leftRecord[key], rightRecord[key])) return false;
    }
    let rightKeys = 0;
    for (const key of Object.keys(rightRecord)) if (rightRecord[key] !== undefined) rightKeys++;
    return leftKeys === rightKeys;
}
