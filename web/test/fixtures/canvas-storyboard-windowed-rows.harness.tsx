import { afterAll, expect, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({ window: dom, document: dom.document, navigator: dom.navigator, Element: dom.Element, Node: dom.Node, HTMLElement: dom.HTMLElement, MutationObserver: dom.MutationObserver, IS_REACT_ACT_ENVIRONMENT: true })) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { StoryboardWindowedRows } = await import("../../src/components/canvas/canvas-script-node-parts");
const { STORYBOARD_ROW_HEIGHT, storyboardMountedRowRanges, storyboardVisibleRowWindow } = await import("../../src/lib/canvas/canvas-storyboard-layout");

afterAll(() => {
    dom.happyDOM.abort();
    for (const [key, value] of originals) {
        if (value) Object.defineProperty(globalThis, key, value);
        else Reflect.deleteProperty(globalThis, key);
    }
});

const ROW_COUNT = 200;
const VIEWPORT = 10 * STORYBOARD_ROW_HEIGHT;

function Rows({ scrollTop, editingIndex }: { scrollTop: number; editingIndex: number }) {
    const window = storyboardVisibleRowWindow(scrollTop, VIEWPORT, STORYBOARD_ROW_HEIGHT, ROW_COUNT);
    const ranges = storyboardMountedRowRanges(window, ROW_COUNT, editingIndex);
    return (
        <div data-list>
            <StoryboardWindowedRows
                rowCount={ROW_COUNT}
                ranges={ranges}
                rowHeight={STORYBOARD_ROW_HEIGHT}
                renderRow={(index) => (
                    <div key={`row-${index}`} data-row={index} style={{ height: STORYBOARD_ROW_HEIGHT }}>
                        <textarea aria-label={`row ${index}`} />
                    </div>
                )}
            />
        </div>
    );
}

test("窗口移动、编辑行滚出与滚回时保留同一个行 DOM 与焦点", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    const textarea = (index: number) => host.querySelector<HTMLTextAreaElement>(`[data-row="${index}"] textarea`);
    try {
        await act(async () => root.render(<Rows scrollTop={0} editingIndex={-1} />));
        const editing = textarea(3)!;
        const neighbour = textarea(8)!;
        editing.focus();
        expect(document.activeElement).toBe(editing);

        // Window shifts by one row: rows that stay in the window must not remount.
        await act(async () => root.render(<Rows scrollTop={STORYBOARD_ROW_HEIGHT} editingIndex={3} />));
        expect(textarea(3)).toBe(editing);
        expect(textarea(8)).toBe(neighbour);
        expect(document.activeElement).toBe(editing);

        // The edited row scrolls out and stays mounted on its own.
        await act(async () => root.render(<Rows scrollTop={100 * STORYBOARD_ROW_HEIGHT} editingIndex={3} />));
        expect(host.querySelectorAll("[data-row]").length).toBe(1 + 18);
        expect(textarea(3)).toBe(editing);
        expect(document.activeElement).toBe(editing);

        // Scrolling further keeps it; scrolling back merges it into the window without remounting.
        await act(async () => root.render(<Rows scrollTop={120 * STORYBOARD_ROW_HEIGHT} editingIndex={3} />));
        expect(textarea(3)).toBe(editing);
        await act(async () => root.render(<Rows scrollTop={0} editingIndex={3} />));
        expect(textarea(3)).toBe(editing);
        expect(document.activeElement).toBe(editing);
        const total = [...host.querySelector("[data-list]")!.children].reduce((sum, child) => sum + Number.parseFloat((child as HTMLElement).style.height || "0"), 0);
        expect(total).toBe(ROW_COUNT * STORYBOARD_ROW_HEIGHT);
    } finally {
        await act(async () => root.unmount());
        host.remove();
    }
});
