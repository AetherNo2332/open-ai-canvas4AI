import { expect, mock, test } from "bun:test";
import { Window } from "happy-dom";

const dom = new Window({ url: "http://localhost/" });
Object.assign(globalThis, { window: dom, document: dom.document, navigator: dom.navigator, HTMLElement: dom.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true });

let previews: Array<{ revision: number; required: boolean; conflicts: Array<{ scopeKey: string; fieldNodeIds: string[]; edgeNodeIds: string[] }> }> = [];
const migrateCalls: Array<{ canvasId: string; revision: number; choices: Record<string, string> }> = [];
let migrateFailure: Error | undefined;
const refreshes: string[] = [];
mock.module("@/services/api/user-data", () => ({
    getCanvasReferenceMigration: async () => previews.shift() ?? { revision: 0, required: false, conflicts: [] },
    migrateCanvasReferences: async (canvasId: string, revision: number, choices: Record<string, string>) => {
        migrateCalls.push({ canvasId, revision, choices });
        if (migrateFailure) throw migrateFailure;
        return { project: { id: canvasId, revision: revision + 1 } };
    },
}));
mock.module("@/services/user-data-sync", () => ({ refreshCanvasAfterAgent: async (id: string) => void refreshes.push(id) }));

const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { useCanvasReferenceMigration } = await import("@/pages/canvas/use-canvas-reference-migration");

let hook: ReturnType<typeof useCanvasReferenceMigration>;
function Probe(props: { enabled: boolean; legacy: boolean }) {
    hook = useCanvasReferenceMigration("canvas", props);
    return null;
}
const settle = () => new Promise((resolve) => setTimeout(resolve, 10));
async function render(props: { enabled: boolean; legacy: boolean }) {
    const root = createRoot(document.createElement("div"));
    await act(async () => {
        root.render(<Probe {...props} />);
        await settle();
    });
    return root;
}

test("declarative canvases never request a preview", async () => {
    previews = [{ revision: 3, required: true, conflicts: [] }];
    const root = await render({ enabled: true, legacy: false });
    expect(hook.required).toBe(false);
    expect(previews.length).toBe(1);
    await act(async () => root.unmount());
});

test("a legacy canvas migrates with only the offered choices and refreshes from the server", async () => {
    previews = [{ revision: 3, required: true, conflicts: [{ scopeKey: "storyboard-row:board:shot", fieldNodeIds: ["a"], edgeNodeIds: ["b"] }] }];
    migrateCalls.length = refreshes.length = 0;
    const root = await render({ enabled: true, legacy: true });
    expect(hook.required).toBe(true);
    await act(async () => {
        await hook.apply({ "storyboard-row:board:shot": "connections", "unknown:scope": "fields" });
    });
    expect(migrateCalls).toEqual([{ canvasId: "canvas", revision: 3, choices: { "storyboard-row:board:shot": "connections" } }]);
    expect(refreshes).toEqual(["canvas"]);
    expect(hook.required).toBe(false);
    await act(async () => root.unmount());
});

test("a failed migration shows the reason and re-reads the preview", async () => {
    previews = [
        { revision: 3, required: true, conflicts: [] },
        { revision: 4, required: true, conflicts: [] },
    ];
    migrateFailure = Object.assign(new Error("conflict"), { reason: "reference_migration_conflict" });
    const root = await render({ enabled: true, legacy: true });
    await act(async () => {
        await hook.apply({});
        await settle();
    });
    expect(hook.error).toContain("还有位置未选择");
    expect(hook.required).toBe(true);
    expect(previews.length).toBe(0);
    migrateFailure = undefined;
    await act(async () => root.unmount());
});
