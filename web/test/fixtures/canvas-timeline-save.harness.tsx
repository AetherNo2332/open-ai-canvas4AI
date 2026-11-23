import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";
import type { CanvasNodeData } from "@/types/canvas";
import type { TimelineProject } from "@/types/timeline";

const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    localStorage: dom.localStorage,
    HTMLElement: dom.HTMLElement,
    HTMLBodyElement: dom.HTMLBodyElement,
    HTMLHtmlElement: dom.HTMLHtmlElement,
    SVGElement: dom.SVGElement,
    Element: dom.Element,
    Node: dom.Node,
    Event: dom.Event,
    ShadowRoot: dom.ShadowRoot,
    MutationObserver: dom.MutationObserver,
    ResizeObserver: dom.ResizeObserver,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
})) {
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}

mock.module("../../src/components/canvas/canvas-timeline-preview", () => ({ CanvasTimelinePreview: () => null }));
mock.module("file-saver", () => ({ saveAs: () => undefined }));
const { act, createRef } = await import("react");
const { createRoot } = await import("react-dom/client");
const { App } = await import("antd");
const { CanvasTimelineDialog } = await import("../../src/components/canvas/canvas-timeline-dialog");
const { CanvasNodeType } = await import("@/types/canvas");

afterAll(() => dom.happyDOM.abort());

test("an asset arriving during the first save is included in the second save", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const node: CanvasNodeData = { id: "video-1", title: "First video", type: CanvasNodeType.Video, position: { x: 0, y: 0 }, width: 160, height: 90, metadata: { content: "/first.mp4" } };
    const addNode = createRef<((node: CanvasNodeData) => void) | null>();
    const onClose = mock(() => undefined);
    const snapshots: TimelineProject[] = [];
    let finishSave!: () => void;
    const firstSave = new Promise<void>((resolve) => (finishSave = resolve));
    const saveButton = () => Array.from(document.querySelectorAll("button")).find((button) => button.textContent?.trim().replace(/\s/g, "") === "保存")!;
    try {
        await act(async () =>
            root.render(
                <App>
                    <CanvasTimelineDialog
                        open
                        node={node}
                        nodes={[node]}
                        timeline={null}
                        addNodeToTimelineRef={addNode}
                        onClose={onClose}
                        onSaveSubtitles={() => undefined}
                        onSave={async (project) => {
                            snapshots.push(project);
                            if (snapshots.length === 1) await firstSave;
                        }}
                    />
                </App>,
            ),
        );
        await act(async () => saveButton().click());
        expect(snapshots[0]?.clips.map((clip) => clip.nodeId)).toEqual([node.id]);
        await act(async () => addNode.current?.({ ...node, id: "video-2", title: "Second video" }));
        await act(async () => finishSave());
        expect(onClose).not.toHaveBeenCalled();
        await act(async () => saveButton().click());
        expect(snapshots[1]?.clips.map((clip) => clip.nodeId)).toContain("video-2");
        expect(onClose).toHaveBeenCalledTimes(1);
    } finally {
        await act(async () => root.unmount());
        host.remove();
    }
});
