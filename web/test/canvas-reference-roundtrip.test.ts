import { expect, test } from "bun:test";

import parity from "../../backend/internal/canvas/references/testdata/reference-projection-parity.json";
import { addReferenceConnections, applyConnectionToReferenceFields, FIRST_FRAME_REFERENCE_MESSAGE, projectReferenceConnections, removeReferenceConnection, SHARED_REFERENCE_MESSAGE } from "../src/lib/canvas/canvas-reference-fields";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData } from "../src/types/canvas";

const node = (id: string, type: CanvasNodeType, metadata: Record<string, unknown> = {}): CanvasNodeData => ({ id, type, title: id, position: { x: 0, y: 0 }, width: 300, height: 200, metadata });
const binding = { id: "node:img", nodeId: "img", mediaType: "image" as const, role: "reference" as const, order: 0, resolution: "latest" as const };
const declared = () => [node("img", CanvasNodeType.Image), node("target", CanvasNodeType.Image, { inputBindings: [binding] })];
const sequence = () => {
    let next = 0;
    return () => `edge-${++next}`;
};

test("Agent 写入的 inputBindings 投影为可见连线", () => {
    const connections = projectReferenceConnections(declared(), [], 1);
    expect(connections).toEqual([{ id: expect.any(String), fromNodeId: "img", toNodeId: "target", managedBy: "reference-fields-v1", referenceOwnerNodeId: "target", referenceBindingId: "node:img" }]);
    expect(projectReferenceConnections(declared(), connections, 1)).toBe(connections);
});

test("删除投影连线后 inputBindings 为空数组", () => {
    const nodes = declared();
    const [line] = projectReferenceConnections(nodes, [], 1);
    const next = applyConnectionToReferenceFields(nodes, { kind: "remove", connection: line }, 1);
    expect(next.find((item) => item.id === "target")?.metadata?.inputBindings).toEqual([]);
});

test("缺少 referenceFieldsVersion 的文档不改字段也不画连线", () => {
    const nodes = declared();
    const free: CanvasConnection = { id: "free", fromNodeId: "img", toNodeId: "target" };
    expect(projectReferenceConnections(nodes, [], undefined)).toEqual([]);
    expect(applyConnectionToReferenceFields(nodes, { kind: "remove", connection: free }, undefined)).toBe(nodes);
    expect(applyConnectionToReferenceFields(nodes, { kind: "add", connection: { id: "x", fromNodeId: "target", toNodeId: "img" } }, undefined)).toBe(nodes);
});

test("用户连线写入目标节点的生成合同并换成受管连线", () => {
    const graph = addReferenceConnections({ nodes: [node("img", CanvasNodeType.Image), node("target", CanvasNodeType.Image)], connections: [] }, [{ id: "drag", fromNodeId: "img", toNodeId: "target", toAnchorRatio: 0.3 }], 1);
    expect(graph.nodes[1].metadata?.generationSpec?.referenceBindings.map((item) => [item.nodeId, item.role, item.mediaType])).toEqual([["img", "reference", "image"]]);
    expect(graph.connections).toEqual([{ id: "drag", fromNodeId: "img", toNodeId: "target", toAnchorRatio: 0.3, managedBy: "reference-fields-v1", referenceOwnerNodeId: "target", referenceBindingId: "node:img" }]);
});

test("后端不接受的引用来源保持自由连线", () => {
    const graph = addReferenceConnections({ nodes: [node("sketch", CanvasNodeType.Drawing), node("target", CanvasNodeType.Image)], connections: [] }, [{ id: "drag", fromNodeId: "sketch", toNodeId: "target" }], 1);
    expect(graph.nodes[1].metadata?.generationSpec).toBeUndefined();
    expect(graph.connections).toEqual([{ id: "drag", fromNodeId: "sketch", toNodeId: "target" }]);
});

test("删除受管连线同时删除字段与重复的自由连线", () => {
    const nodes = declared();
    const connections: CanvasConnection[] = [...projectReferenceConnections(nodes, [], 1), { id: "twin", fromNodeId: "img", toNodeId: "target" }];
    const next = removeReferenceConnection({ nodes, connections }, connections[0].id, 1);
    expect(next.connections).toEqual([]);
    expect(next.nodes[1].metadata?.inputBindings).toEqual([]);
});

test("分镜行连线删除对应 assetBindings", () => {
    const board = node("board", CanvasNodeType.Script, { storyboard: { referenceNodeIds: [], visibleColumns: [], rows: [{ id: "shot", shotNumber: 1, characters: [], assetBindings: [{ nodeId: "img", role: "style", priority: 60 }] }] } });
    const nodes = [node("img", CanvasNodeType.Image), board];
    const [line] = projectReferenceConnections(nodes, [], 1);
    expect(line).toMatchObject({ fromNodeId: "img", toNodeId: "board", toHandleId: "row:shot", storyboardRowId: "shot" });
    const next = removeReferenceConnection({ nodes, connections: [line] }, line.id, 1);
    expect(next.nodes[1].metadata?.storyboard?.rows[0].assetBindings).toEqual([]);
    expect(next.connections).toEqual([]);
});

type ParityCase = { name: string; owners: string[]; document: { nodes: CanvasNodeData[]; connections: CanvasConnection[] }; lines: CanvasConnection[] };
for (const item of parity as unknown as ParityCase[]) {
    test(`前端投影与后端 Reconcile 一致：${item.name}`, () => {
        const owners = new Set(item.owners);
        const lines = projectReferenceConnections(item.document.nodes, item.document.connections, 1, sequence()).filter((edge) => edge.managedBy === "reference-fields-v1" && owners.has(edge.referenceOwnerNodeId || ""));
        expect(lines).toEqual(item.lines);
    });
}

const storyboard = (rowPatch: Record<string, unknown>, extra: Record<string, unknown> = {}) =>
    node("board", CanvasNodeType.Script, { ...extra, storyboard: { referenceNodeIds: [], visibleColumns: [], ...((extra.storyboard as object) || {}), rows: [{ id: "shot", shotNumber: 1, characters: [], assetBindings: [], ...rowPatch }] } });

test("分镜表级共享引用画出的行连线拒绝删除且图不变", () => {
    const nodes = [node("img", CanvasNodeType.Image), storyboard({}, { storyboard: { referenceNodeIds: ["img"] } })];
    const connections = projectReferenceConnections(nodes, [], 1);
    const rowLine = connections.find((edge) => edge.toHandleId === "row:shot")!;
    const before = structuredClone({ nodes, connections });
    expect(() => removeReferenceConnection({ nodes, connections }, rowLine.id, 1)).toThrow(SHARED_REFERENCE_MESSAGE);
    expect({ nodes, connections }).toEqual(before);
});

test("按 characterAssetId 匹配的出镜角色连线拒绝删除（按 fork 语义由人物字段维护）", () => {
    const hero = node("hero", CanvasNodeType.Text, { workflowKind: "character", characterAssetId: "hero-asset" });
    const nodes = [hero, storyboard({ characters: [{ characterName: "主角", characterAssetId: "hero-asset" }] })];
    const connections = projectReferenceConnections(nodes, [], 1);
    const line = connections.find((edge) => edge.fromNodeId === "hero")!;
    expect(line).toMatchObject({ toHandleId: "row:shot" });
    expect(() => removeReferenceConnection({ nodes, connections }, line.id, 1)).toThrow(SHARED_REFERENCE_MESSAGE);
});

test("关键帧模式下的首帧连线拒绝删除", () => {
    const nodes = [node("frame", CanvasNodeType.Image), node("clip", CanvasNodeType.Video), storyboard({ imageNodeId: "frame", videoNodeId: "clip" }, { storyboardVideoInputMode: "keyframe" })];
    const connections: CanvasConnection[] = [...projectReferenceConnections(nodes, [], 1), { id: "first", fromNodeId: "frame", toNodeId: "clip" }];
    expect(() => removeReferenceConnection({ nodes, connections }, "first", 1)).toThrow(FIRST_FRAME_REFERENCE_MESSAGE);
});

test("同一文档两次投影得到相同的连线 id", () => {
    const first = projectReferenceConnections(declared(), [], 1);
    const second = projectReferenceConnections(declared(), [], 1);
    expect(first.map((edge) => edge.id)).toEqual(second.map((edge) => edge.id));
    expect(first[0].id).toMatch(/^ref-[0-9a-f]{16}$/);
});
