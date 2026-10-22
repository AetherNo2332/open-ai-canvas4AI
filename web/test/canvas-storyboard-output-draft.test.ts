import { expect, test } from "bun:test";
import { applyCanvasReferenceGraphEdit, canvasReferenceIntentForConnection, draftStoryboardVideoOutput, type CanvasReferenceGraph } from "../src/lib/canvas/canvas-reference-edits";
import { attachNodeToStoryboardRow } from "../src/lib/canvas/canvas-project-domain";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type StoryboardRow } from "../src/types/canvas";

const node = (id: string, type: CanvasNodeType, metadata: Record<string, unknown> = {}, title = id): CanvasNodeData => ({ id, type, title, position: { x: 0, y: 0 }, width: 300, height: 200, metadata });
const image = (id: string) => node(id, CanvasNodeType.Image, { content: `https://example.test/${id}.png` });
const shot = (patch: Partial<StoryboardRow> = {}): StoryboardRow => ({ id: "shot", shotNumber: 3, durationSeconds: 8, plotDescription: "剧情描述", videoMotionPrompt: "镜头缓慢推进", characters: [], assetBindings: [{ nodeId: "asset", role: "style", priority: 60 }], ...patch }) as StoryboardRow;
const board = (row = shot(), metadata: Record<string, unknown> = {}) => node("board", CanvasNodeType.Script, { storyboard: { rows: [row], referenceNodeIds: ["global"], visibleColumns: [] }, ...metadata });
const rowLine = { fromNodeId: "board", toNodeId: "clip", fromHandleId: "row:shot" };
const bind = (graph: CanvasReferenceGraph, edge: Pick<CanvasConnection, "fromNodeId" | "toNodeId" | "fromHandleId" | "toHandleId"> = rowLine) => {
    const intent = canvasReferenceIntentForConnection(graph.nodes, edge, false, graph.connections)!;
    return draftStoryboardVideoOutput(graph, applyCanvasReferenceGraphEdit(graph, intent), intent);
};
const clipOf = (graph: CanvasReferenceGraph) => graph.nodes.find((item) => item.id === "clip")!;

test("connecting a storyboard row to a video carries the shot title, prompt and duration", () => {
    const next = bind({ nodes: [image("asset"), image("global"), board(), node("clip", CanvasNodeType.Video, {}, "我的视频")], connections: [] });
    const clip = clipOf(next);
    expect(clip.title).toBe("镜头 3 · 视频");
    expect(clip.metadata).toMatchObject({ prompt: "镜头缓慢推进", workflowKind: "shot", workflowTitle: "镜头 3 视频", shotIndex: 3, generationMode: "video", videoEditOperation: "text_to_video", seconds: "8" });
    expect(clip.metadata?.composerContent).toBe("参考资产：@图片1 @图片2\n镜头缓慢推进");
    // The contract the generation reads mirrors the drafted prompt and the row's declared inputs.
    expect(clip.metadata?.generationSpec?.prompt).toBe(clip.metadata?.composerContent);
    expect(clip.metadata?.generationSpec?.options.durationSeconds).toBe(8);
    expect(clip.metadata?.generationSpec?.referenceBindings.map((binding) => binding.nodeId)).toEqual(["asset", "global"]);
    expect(next.nodes.find((item) => item.id === "board")?.metadata?.storyboard?.rows[0].videoNodeId).toBe("clip");
});

test("the drafted video matches the storyboard's own connection attach", () => {
    const nodes = [image("asset"), image("global"), board(), node("clip", CanvasNodeType.Video)];
    const attached = attachNodeToStoryboardRow(nodes, rowLine).find((item) => item.id === "clip")!;
    const drafted = clipOf(bind({ nodes, connections: [] }));
    expect(drafted.title).toBe(attached.title);
    for (const key of ["prompt", "composerContent", "workflowKind", "workflowTitle", "shotIndex", "generationMode", "videoEditOperation", "seconds"] as const) expect(drafted.metadata?.[key]).toEqual(attached.metadata?.[key]);
});

test("the plot description is the prompt when the row has no motion prompt", () => {
    const clip = clipOf(bind({ nodes: [image("asset"), image("global"), board(shot({ videoMotionPrompt: "" })), node("clip", CanvasNodeType.Video)], connections: [] }));
    expect(clip.metadata?.prompt).toBe("剧情描述");
});

test("binding the same video again keeps a prompt edited since", () => {
    const first = bind({ nodes: [image("asset"), image("global"), board(), node("clip", CanvasNodeType.Video)], connections: [] });
    const edited = { ...first, nodes: first.nodes.map((item) => (item.id === "clip" ? { ...item, title: "改过的标题", metadata: { ...item.metadata, composerContent: "用户改写" } } : item)) };
    const again = bind(edited);
    expect(clipOf(again).title).toBe("改过的标题");
    expect(clipOf(again).metadata?.composerContent).toBe("用户改写");
});

test("the row's inputs replace the video's own and their lines are redrawn", () => {
    // The video was a free-standing generation node with its own reference before becoming the shot output.
    const graph = { nodes: [image("asset"), image("global"), image("old"), board(), node("clip", CanvasNodeType.Video)], connections: [{ id: "free", fromNodeId: "old", toNodeId: "clip" }] };
    const next = bind(graph);
    expect(clipOf(next).metadata?.generationSpec?.referenceBindings.map((binding) => binding.nodeId)).toEqual(["asset", "global"]);
    const clipLines = next.connections.filter((edge) => edge.toNodeId === "clip");
    expect(clipLines.map((edge) => [edge.fromNodeId, edge.managedBy, edge.referenceOwnerNodeId])).toEqual([
        ["board", "reference-fields-v1", "board"],
        ["asset", "reference-fields-v1", "clip"],
        ["global", "reference-fields-v1", "clip"],
    ]);
});

test("keyframe rows bind the shot image as the first frame", () => {
    const row = shot({ imageNodeId: "frame" });
    const clip = clipOf(bind({ nodes: [image("asset"), image("global"), image("frame"), board(row, { storyboardVideoInputMode: "keyframe" }), node("clip", CanvasNodeType.Video)], connections: [] }));
    expect(clip.metadata?.videoStartFrameNodeId).toBe("frame");
    expect(clip.metadata?.generationSpec?.referenceBindings.find((binding) => binding.role === "first-frame")?.nodeId).toBe("frame");
});

test("image outputs and other references are not drafted", () => {
    const graph = { nodes: [image("asset"), image("global"), board(), node("still", CanvasNodeType.Image, {}, "静帧")], connections: [] };
    const intent = canvasReferenceIntentForConnection(graph.nodes, { fromNodeId: "board", toNodeId: "still", fromHandleId: "row:shot" }, false, [])!;
    const bound = applyCanvasReferenceGraphEdit(graph, intent);
    expect(draftStoryboardVideoOutput(graph, bound, intent)).toBe(bound);
    expect(bound.nodes.find((item) => item.id === "still")?.title).toBe("静帧");
    const reference = canvasReferenceIntentForConnection(graph.nodes, { fromNodeId: "asset", toNodeId: "board", toHandleId: "row:shot" }, false, [])!;
    const referenced = applyCanvasReferenceGraphEdit(graph, reference);
    expect(draftStoryboardVideoOutput(graph, referenced, reference)).toBe(referenced);
});

// The controller turns this error into a warning rather than leaving the drag line stuck.
test("an ambiguous character rejects the bind before the host would", () => {
    const row = shot({ characters: [{ characterName: "主角", characterAssetId: "hero" }] as StoryboardRow["characters"] });
    const heroes = [node("v1", CanvasNodeType.Text, { workflowKind: "character", characterAssetId: "hero" }), node("v2", CanvasNodeType.Text, { workflowKind: "character", characterAssetId: "hero" })];
    expect(() => bind({ nodes: [image("asset"), image("global"), ...heroes, board(row), node("clip", CanvasNodeType.Video)], connections: [] })).toThrow("人物素材匹配多个节点");
});
