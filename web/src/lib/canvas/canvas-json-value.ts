import { canonicalize } from "json-canonicalize";

/** Host JSON maps and editor objects may enumerate the same fields differently.
 * Compare their JSON values while retaining array order and explicit nulls. */
export function sameCanvasJsonValue(left: unknown, right: unknown): boolean {
    return Object.is(left, right) || canonicalize(left) === canonicalize(right);
}
