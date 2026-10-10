import { expect, test } from "bun:test";
import fixtures from "./fixtures/canvas-agent-operation-cases.json";
import { copyCanvasNodeGraph } from "@/lib/canvas/canvas-node-copy";
import { removeCanvasNodes } from "@/lib/canvas/canvas-project-domain";
import type { CanvasConnection, CanvasNodeData } from "@/types/canvas";

for (const fixture of fixtures)
    test(`manual operation contract: ${fixture.name}`, () => {
        let nodes = structuredClone(fixture.input.nodes) as unknown as CanvasNodeData[];
        let connections = structuredClone(fixture.input.connections) as CanvasConnection[];
        for (const op of fixture.ops) {
            if (op.type === "delete_node") {
                const removed = removeCanvasNodes(nodes, new Set([op.id]));
                nodes = removed.nodes;
                connections = connections.filter((edge) => !removed.removedIds.has(edge.fromNodeId) && !removed.removedIds.has(edge.toNodeId));
            } else {
                const sourceId = (op as { sourceNodeId: string }).sourceNodeId;
                const idMap = new Map(nodes.map((node) => [node.id, node.id === sourceId ? op.id : `${op.id}-${node.id}`]));
                const copied = copyCanvasNodeGraph(nodes, connections, sourceId, idMap, () => `copy-edge-${connections.length}`);
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
            expect(JSON.parse(JSON.stringify(actual))).toMatchObject(fields);
            for (const field of absentNodeFields) expect((actual as unknown as Record<string, unknown>)[field]).toBeUndefined();
            for (const field of absentMetadata) expect(atPath(actual.metadata, field)).toBeUndefined();
            if (parentCopiedFromNodeId) expect(actual.parentId).toBe(findCopied(parentCopiedFromNodeId).id);
            for (const [field, source] of Object.entries(metadataReferenceAssertions || {})) expect(atPath(actual.metadata, field)).toBe(findCopied(source).id);
        }
        expect(connections.map(({ fromNodeId, toNodeId }) => ({ fromNodeId, toNodeId }))).toEqual(
            expected.connections.map((edge) => ({ fromNodeId: edge.fromNodeId || findCopied(edge.fromCopiedNodeId!).id, toNodeId: edge.toNodeId || findCopied(edge.toCopiedNodeId!).id })),
        );
    });
