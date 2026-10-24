import { expect, test } from "bun:test";
import { sameCanvasJsonValue } from "../src/lib/canvas/canvas-json-value";

test("canvas JSON comparison ignores nested object field order and keeps JSON presence and array semantics", () => {
    expect(sameCanvasJsonValue({ first: 1, second: [{ a: true, b: null }] }, { second: [{ b: null, a: true }], first: 1 })).toBe(true);
    expect(sameCanvasJsonValue({ optional: undefined }, {})).toBe(true);
    expect(sameCanvasJsonValue([undefined], [null])).toBe(true);
    expect(sameCanvasJsonValue(undefined, null)).toBe(false);
    expect(sameCanvasJsonValue({ value: null }, {})).toBe(false);
    expect(sameCanvasJsonValue(["first", "second"], ["second", "first"])).toBe(false);
    expect(sameCanvasJsonValue({ value: 1 }, { value: "1" })).toBe(false);
    expect(sameCanvasJsonValue({ value: {} }, { value: [] })).toBe(false);
});
