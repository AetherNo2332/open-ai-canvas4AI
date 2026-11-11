import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { discoverTests, buildTestGroups, runGroups, isolatedGroups } from "./run-tests.mjs";

test("each discovered test belongs to exactly one group, original process boundaries retained", () => {
    const files = [...isolatedGroups.flatMap((g) => g.files), "src/new.test.ts", "test/a.test.tsx"];
    const groups = buildTestGroups(files);
    assert.equal(groups.length, 3);
    assert.deepEqual(groups[0].files, isolatedGroups[0].files);
    assert.deepEqual(groups[1].files, isolatedGroups[1].files);
    const scheduled = groups.flatMap((g) => g.files);
    assert.equal(scheduled.length, new Set(scheduled).size);
    assert.deepEqual([...scheduled].sort(), [...files].sort());
});
test("new tests discovered, ignored dependencies and fixtures excluded", () => {
    const root = mkdtempSync(path.join(tmpdir(), "web-scheduler-"));
    for (const file of ["src/new.test.ts", "test/another.spec.tsx", "node_modules/pkg/x.test.ts", "test/fixtures/x.harness.mjs"]) {
        mkdirSync(path.dirname(path.join(root, file)), { recursive: true });
        writeFileSync(path.join(root, file), "");
    }
    assert.deepEqual(discoverTests(root), ["src/new.test.ts", "test/another.spec.tsx"]);
});
test("child failure propagates and stops execution; command batches never repeat files", () => {
    const seen = [];
    const groups = [
        { id: "first", files: ["a.test.ts"] },
        { id: "second", files: ["b.test.ts"] },
    ];
    assert.equal(
        runGroups(groups, {
            root: ".",
            spawn: (cmd, args) => {
                seen.push(args);
                return { status: 7 };
            },
        }),
        7,
    );
    assert.equal(seen.length, 1);
    assert.equal(runGroups(groups, { root: ".", spawn: () => ({ status: null, error: new Error("cannot start") }) }), 1);
});
test("missing isolation test is an error rather than silent loss", () => assert.throws(() => buildTestGroups(["test/new.test.ts"])));
