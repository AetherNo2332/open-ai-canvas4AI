import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { CanvasMediaVersionList } from "../src/components/canvas/canvas-media-versions-modal";
import type { Item } from "../src/lib/canvas/command-contract.generated";

test("version list leaves the current version selectable and blocks unfinished artifacts", () => {
    const base = { mode: "image", adoption: "pending", isCurrent: false, isPending: false, createdAt: "2026-10-24T00:00:00Z" };
    const items: Item[] = [
        { ...base, versionId: "pending", sequence: 3, status: "queued", isPending: true },
        { ...base, versionId: "failed", sequence: 2, status: "failed" },
        { ...base, versionId: "current", sequence: 1, status: "ready", isCurrent: true },
    ];
    const html = renderToStaticMarkup(<CanvasMediaVersionList items={items} previewId="current" onPreview={() => {}} onUse={() => {}} />);
    expect(html).toContain("生成中");
    expect(html).toContain("未完成");
    expect(html).toContain("保持此版本");
    const useButtons = (html.match(/<button[^>]*>[\s\S]*?<\/button>/g) || []).filter((button) => button.includes("版本 V"));
    expect(useButtons).toHaveLength(3);
    expect(useButtons[0]).toContain("disabled");
    expect(useButtons[1]).toContain("disabled");
    expect(useButtons[2]).not.toContain("disabled");
});
