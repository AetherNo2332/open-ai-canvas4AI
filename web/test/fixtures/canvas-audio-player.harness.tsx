import { afterAll, expect, mock, test } from "bun:test";
import { Window } from "happy-dom";
const dom = new Window({ url: "http://localhost/" });
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, HTMLElement: dom.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true })) Object.defineProperty(globalThis, key, { configurable: true, value });
const React = await import("react");
const { act } = React;
const { createRoot } = await import("react-dom/client");
let writes = 0;
const resets: string[] = [];
const snapshot = { nodeId: "audio", phase: "playing", currentTimeMs: 250, durationMs: 1000 };
mock.module("@/components/canvas/canvas-node-action-context", () => ({ useCanvasNodeActions: () => ({ updateMetadata: () => writes++ }) }));
mock.module("@/services/canvas-audio-playback", () => ({
    getCanvasAudioPlaybackSnapshot: () => snapshot,
    subscribeCanvasAudioNode: () => () => {},
    toggleCanvasAudio: async () => {},
    seekCanvasAudio: () => {},
    stopCanvasAudio: () => {},
    resetCanvasAudio: (id: string) => resets.push(id),
}));
const { CanvasAudioPlayer } = await import("@/components/canvas/canvas-audio-player");
const element = dom.document.createElement("div");
dom.document.body.append(element);
const root = createRoot(element as unknown as HTMLDivElement);
const theme: any = { node: { text: "black", muted: "gray", activeStroke: "red" }, accent: {} };
const node: any = { id: "audio", title: "Audio", metadata: { storageKey: "resource:first", content: "/first" } };
afterAll(async () => {
    await act(async () => root.unmount());
    await dom.happyDOM.abort();
});

test("decoded duration renders without sending an author metadata edit", async () => {
    await act(async () => root.render(<CanvasAudioPlayer node={node} theme={theme} />));
    expect(element.textContent).toContain("0:00/0:01");
    expect(element.querySelector("button")?.getAttribute("aria-label")).toBe("暂停Audio");
    expect(writes).toBe(0);
});

test("resource replacement releases old playback but a signed URL refresh does not", async () => {
    await act(async () => root.render(<CanvasAudioPlayer node={{ ...node, metadata: { ...node.metadata, content: "/new-signed-url" } }} theme={theme} />));
    expect(resets).toHaveLength(0);
    await act(async () => root.render(<CanvasAudioPlayer node={{ ...node, metadata: { storageKey: "resource:second", content: "/second" } }} theme={theme} />));
    expect(resets).toEqual(["audio"]);
});
