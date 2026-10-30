import type { CanvasNodeData } from "@/types/canvas";
import type { CanvasReferenceMigrationChoice } from "./canvas-reference-migration";

export type CanvasReferenceMigrationIssue = {
    key: string;
    ownerNodeId: string;
    sourceNodeId: string;
    location: string;
    fieldLabel: string;
    sourceTitle?: string;
    kind: "missing" | "self";
};

/** 为旧画布的手动修复提供定位信息；不改写引用，也不代替主机校验。 */
export function canvasReferenceMigrationIssues(nodes: CanvasNodeData[], choices: Record<string, CanvasReferenceMigrationChoice> = {}): CanvasReferenceMigrationIssue[] {
    const index = new Map(nodes.map((node) => [node.id, node]));
    const issues: CanvasReferenceMigrationIssue[] = [];
    for (const node of nodes) {
        const add = (sourceNodeId: string | undefined, location: string, fieldLabel: string, sourceTitle?: string) => {
            if (!sourceNodeId || (sourceNodeId !== node.id && index.has(sourceNodeId))) return;
            issues.push({ key: `${node.id}:${location}:${fieldLabel}:${sourceNodeId}`, ownerNodeId: node.id, sourceNodeId, location, fieldLabel, sourceTitle, kind: sourceNodeId === node.id ? "self" : "missing" });
        };
        const metadata = node.metadata;
        if (node.type === "script") {
            const storyboard = metadata?.storyboard;
            if (choices[`storyboard-context:${node.id}`] !== "connections") {
                storyboard?.referenceNodeIds?.forEach((id) => add(id, "整张分镜表", "共享素材"));
            }
            storyboard?.rows.forEach((row, rowIndex) => {
                const location = `第 ${rowIndex + 1} 行${row.shotNumber ? ` · 镜头 ${row.shotNumber}` : ""}`;
                if (choices[`storyboard-row:${node.id}:${row.id}`] !== "connections") {
                    row.assetBindings?.forEach((binding) => add(binding.nodeId, location, "镜头素材"));
                }
                // 出镜角色独立于镜头素材，即使选择沿用旧连线也不会被替换。
                row.characters?.forEach((character) => add(character.characterImageNodeId, location, "出镜角色", character.characterName));
            });
        } else if (node.type === "batch-table") {
            metadata?.batchTable?.rows.forEach((row, rowIndex) => {
                // 行级选择不代表每列都有替代连线，保留问题直到用户明确修复。
                const location = `第 ${rowIndex + 1} 行`;
                row.inputNodeIds?.forEach((id) => add(id, location, "参考图片"));
                row.textNodeIds?.forEach((id) => add(id, location, "参考文本"));
            });
        } else {
            const spec = metadata?.generationSpec;
            if (choices[`${spec ? "generation" : "node"}:${node.id}`] === "connections") continue;
            (spec?.referenceBindings ?? metadata?.inputBindings)?.forEach((binding) =>
                add(binding.nodeId, "节点素材设置", { "first-frame": "首帧图片", "last-frame": "尾帧图片", mask: "遮罩图片", "source-text": "参考文本", reference: "参考素材" }[binding.role]),
            );
        }
    }
    return [...new Map(issues.map((issue) => [issue.key, issue])).values()];
}
