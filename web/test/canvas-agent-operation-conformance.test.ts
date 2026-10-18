import { expect, test } from "bun:test";
import fixtures from "./fixtures/canvas-agent-operation-cases.json";
import { copyCanvasNodeGraph } from "@/lib/canvas/canvas-node-copy";
import { removeCanvasNodes } from "@/lib/canvas/canvas-project-domain";
import type { CanvasConnection, CanvasNodeData } from "@/types/canvas";

// 共享夹具用相对占位符表达“某个源节点的副本 id”，这样前后端各自的节点 id 生成规则
// 不同也能量到同一份行身份契约：@copy-node:<源节点id>:<后缀> → <副本节点id>-<后缀>。
const COPY_NODE_PLACEHOLDER = "@copy-node:";

function resolveCopiedPlaceholders(value: unknown, copiedNodeIds: ReadonlyMap<string, string>): unknown {
    if (typeof value === "string") {
        if (!value.startsWith(COPY_NODE_PLACEHOLDER)) return value;
        const [sourceNodeId, ...suffix] = value.slice(COPY_NODE_PLACEHOLDER.length).split(":");
        const copyNodeId = copiedNodeIds.get(sourceNodeId);
        if (!sourceNodeId || !suffix.length || !suffix.join(":") || !copyNodeId) throw new Error(`无法解析副本占位符：${value}`);
        return `${copyNodeId}-${suffix.join(":")}`;
    }
    if (Array.isArray(value)) return value.map((entry) => resolveCopiedPlaceholders(entry, copiedNodeIds));
    if (value && typeof value === "object") return Object.fromEntries(Object.entries(value as Record<string, unknown>).map(([key, entry]) => [key, resolveCopiedPlaceholders(entry, copiedNodeIds)]));
    return value;
}

for (const fixture of fixtures)
    test(`manual operation contract: ${fixture.name}`, () => {
        let nodes = structuredClone(fixture.input.nodes) as unknown as CanvasNodeData[];
        let connections = structuredClone(fixture.input.connections) as CanvasConnection[];
        const copiedNodeIds = new Map<string, string>();
        for (const op of fixture.ops) {
            if (op.type === "delete_node") {
                const removed = removeCanvasNodes(nodes, new Set([op.id]));
                nodes = removed.nodes;
                connections = connections.filter((edge) => !removed.removedIds.has(edge.fromNodeId) && !removed.removedIds.has(edge.toNodeId));
            } else {
                const sourceId = (op as { sourceNodeId: string }).sourceNodeId;
                const idMap = new Map(nodes.map((node) => [node.id, node.id === sourceId ? op.id : `${op.id}-${node.id}`]));
                const copied = copyCanvasNodeGraph(nodes, connections, sourceId, idMap, () => `copy-edge-${connections.length}`);
                for (const node of copied.nodes) if (node.metadata?.copiedFromNodeId) copiedNodeIds.set(node.metadata.copiedFromNodeId, node.id);
                nodes = [...nodes, ...copied.nodes];
                connections = [...connections, ...copied.connections];
            }
        }
        const expected = fixture.expected as {
            nodeCount: number;
            nodeAssertions: Array<{ id?: string; copiedFromNodeId?: string; absentNodeFields?: string[]; absentMetadata?: string[]; parentCopiedFromNodeId?: string; metadataReferenceAssertions?: Record<string, string>; [key: string]: unknown }>;
            connections: Array<{ fromNodeId?: string; toNodeId?: string; fromCopiedNodeId?: string; toCopiedNodeId?: string }>;
        };
        const findCopied = (id: string) => nodes.find((node) => node.metadata?.copiedFromNodeId === id)!;
        const atPath = (value: unknown, path: string) => path.split(".").reduce<unknown>((current, key) => (current && typeof current === "object" ? (current as Record<string, unknown>)[key] : undefined), value);
        expect(nodes).toHaveLength(expected.nodeCount);
        for (const assertion of expected.nodeAssertions) {
            const { id, copiedFromNodeId, absentNodeFields = [], absentMetadata = [], parentCopiedFromNodeId, metadataReferenceAssertions, ...fields } = assertion;
            const actual = id ? nodes.find((node) => node.id === id)! : findCopied(copiedFromNodeId!);
            expect(actual).toBeDefined();
            expect(JSON.parse(JSON.stringify(actual))).toMatchObject(resolveCopiedPlaceholders(fields, copiedNodeIds) as object);
            for (const field of absentNodeFields) expect((actual as unknown as Record<string, unknown>)[field]).toBeUndefined();
            for (const field of absentMetadata) expect(atPath(actual.metadata, field)).toBeUndefined();
            if (parentCopiedFromNodeId) expect(actual.parentId).toBe(findCopied(parentCopiedFromNodeId).id);
            for (const [field, source] of Object.entries(metadataReferenceAssertions || {})) expect(atPath(actual.metadata, field)).toBe(findCopied(source).id);
        }
        expect(connections.map(({ fromNodeId, toNodeId }) => ({ fromNodeId, toNodeId }))).toEqual(
            expected.connections.map((edge) => ({ fromNodeId: edge.fromNodeId || findCopied(edge.fromCopiedNodeId!).id, toNodeId: edge.toNodeId || findCopied(edge.toCopiedNodeId!).id })),
        );
    });
