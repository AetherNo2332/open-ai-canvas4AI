import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { SnapshotJson } from "../src/pages/admin/components/snapshot-json";
import { SnapshotReferences } from "../src/pages/admin/components/snapshot-references";

test("snapshot references render escaped worker and task IDs without invented routes", () => {
    const html = renderToStaticMarkup(<SnapshotReferences references={{ workerIds: ["worker-one"], workers: ["worker-one"], runs: [{ runId: "run-<42>" }] }} />);
    expect(html).toContain("worker-one");
    expect(html).toContain("run-&lt;42&gt;");
    expect(html).not.toContain("href=");
});

test("schema differences escape markup and keep field values readable", () => {
    const html = renderToStaticMarkup(<SnapshotJson value={{ description: "<script>alert('x')</script>", minimum: 0, required: ["id"] }} />);
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
    expect(html).toContain("minimum");
    expect(html).toContain("required");
});
test("long values are bounded and explicitly direct operators to export", () => {
    const html = renderToStaticMarkup(<SnapshotJson value={"x".repeat(50000)} limit={100} />);
    expect(html.length).toBeLessThan(500);
    expect(html).toContain("导出可查看全文");
});
