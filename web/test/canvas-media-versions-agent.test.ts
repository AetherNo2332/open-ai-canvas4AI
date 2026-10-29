import { expect, test } from "bun:test";

import fixture from "../../backend/internal/canvas/mediaversions/testdata/agent-writeback.json";
import { adoptMediaVersion, currentMediaVersion, hasMediaVersionHistory, listMediaVersions, mediaVersionAdoptBlock } from "../src/lib/canvas/canvas-media-versions";
import type { CanvasNodeData } from "../src/types/canvas";

// 夹具由 Go 侧 mediaversions.Finish 写出（WRITE_AGENT_FIXTURE=1 go test ./internal/canvas/mediaversions）：
// Agent 原位生成写回的节点，浏览器用上游函数读取时必须看到同一份版本历史。
const before = fixture.before as unknown as CanvasNodeData;
const after = fixture.after as unknown as CanvasNodeData;

test("浏览器看到 Agent 写回的新版本并认定为当前版本", () => {
    expect(listMediaVersions(before)).toHaveLength(0);
    const versions = listMediaVersions(after);
    expect(versions.map((version) => version.number)).toEqual([1, 2]);
    expect(currentMediaVersion(after)?.id).toBe(fixture.outcome.VersionID);
    expect(currentMediaVersion(after)?.generation?.taskId).toBe("agent-task");
    expect(versions[0].media.storageKey).toBe(before.metadata?.storageKey);
    expect(hasMediaVersionHistory(after)).toBe(true);
    expect(mediaVersionAdoptBlock(after)).toBeNull();
});

test("浏览器可以把 Agent 写回的节点切回旧版本", () => {
    const old = listMediaVersions(after)[0];
    const adopted = adoptMediaVersion(after, old.id);
    expect(adopted?.metadata?.storageKey).toBe(before.metadata?.storageKey);
    expect(adopted?.metadata?.currentMediaVersionId).toBe(old.id);
    expect(adopted?.metadata?.taskId).toBeUndefined();
});
