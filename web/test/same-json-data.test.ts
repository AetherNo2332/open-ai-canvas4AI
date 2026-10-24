import { expect, test } from "bun:test";
import { sameJsonData } from "../src/lib/same-json-data";

test("compares JSON-shaped values structurally", () => {
    const history = [{ id: "c1", messages: [{ id: "m1", role: "user", text: "镜头一" }], run: { id: "r1", status: "succeeded", revision: 3 }, updatedAt: "2026-01-01" }];
    expect(sameJsonData(history, structuredClone(history))).toBe(true);
    expect(sameJsonData({ a: 1, b: [1, 2] }, { b: [1, 2], a: 1 })).toBe(true);
    expect(sameJsonData({ a: 1, gone: undefined }, { a: 1 })).toBe(true);
    expect(sameJsonData(0, -0)).toBe(true);
    expect(sameJsonData(NaN, NaN)).toBe(true);
    expect(sameJsonData(null, null)).toBe(true);
});

test("reports any content difference", () => {
    const base = { id: "c1", messages: [{ id: "m1", text: "a" }], run: null };
    expect(sameJsonData(base, { ...base, messages: [{ id: "m1", text: "b" }] })).toBe(false);
    expect(sameJsonData(base, { ...base, messages: [...base.messages, { id: "m2", text: "c" }] })).toBe(false);
    expect(sameJsonData(base, { ...base, run: { id: "r1" } })).toBe(false);
    expect(sameJsonData({ a: 1 }, { a: 1, b: 2 })).toBe(false);
    expect(sameJsonData({ a: null }, { a: undefined })).toBe(false);
    expect(sameJsonData([1, 2], { 0: 1, 1: 2, length: 2 })).toBe(false);
    expect(sameJsonData("1", 1)).toBe(false);
});

test("non-plain objects only match by reference", () => {
    const date = new Date(0);
    expect(sameJsonData(date, date)).toBe(true);
    expect(sameJsonData(new Date(0), new Date(1))).toBe(false);
    expect(sameJsonData(new Map(), new Map())).toBe(false);
    expect(sameJsonData({ at: new Date(0) }, { at: new Date(0) })).toBe(false);
});
