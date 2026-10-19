import { expect, test } from "bun:test";
import { assetOriginLabel, assetSourceLabel, assetProjectLabel } from "@/lib/asset-origin";

test("free canvas origin is named without inventing a business project", () => {
    const asset = { metadata: { source: "generation-task", canvasId: "c", canvasName: "自由画布 7" } };
    expect(assetOriginLabel(asset)).toBe("画布：自由画布 7");
    expect(assetSourceLabel(asset)).toBe("生成任务");
    expect(assetProjectLabel(asset)).toBe("未关联业务项目");
});
test("canvas and business project names are both visible", () => {
    expect(assetOriginLabel({ metadata: { canvasName: "镜头画布", projectIds: ["p"], projectName: "剧集一" } })).toBe("项目：剧集一 · 画布：镜头画布");
});
test("user source labels are preserved and unknown origins are explicit", () => {
    expect(assetSourceLabel({ source: "用户填写的来源", metadata: { source: "generation-task" } })).toBe("用户填写的来源");
    expect(assetOriginLabel({})).toBe("个人素材");
    expect(assetSourceLabel({})).toBe("未标注来源");
});
