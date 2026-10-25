import { expect, test } from "bun:test";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { Window } from "happy-dom";

import type { InsertAssetPayload } from "../src/components/canvas/asset-picker-modal";
import { resolveAddNodeMenuCommands, type AddNodeMenuContext } from "../src/lib/canvas/tool-registry";
import { useCanvasCharacterEntry } from "../src/pages/canvas/use-canvas-character-entry";
import type { Position } from "../src/types/canvas";

for (const workspaceMode of ["simple", "professional"] as const) {
    for (const isProjectLinked of [false, true]) {
        test(`${workspaceMode} ${isProjectLinked ? "project" : "free"} canvas exposes one usable character command`, () => {
            const invoked: string[] = [];
            const context: AddNodeMenuContext = {
                workspaceMode,
                isProjectLinked,
                enabledPluginIds: new Set(),
                handlers: new Proxy({} as AddNodeMenuContext["handlers"], {
                    get: (_target, name) => () => invoked.push(String(name)),
                }),
            };
            const commands = resolveAddNodeMenuCommands(context).filter((command) => command.id === "project-character");
            expect(commands).toHaveLength(1);
            commands[0].run(context);
            expect(invoked).toEqual(["onOpenProjectCharacters"]);
        });
    }
}

async function withCharacterEntry(
    options: { projectId?: string; shortDramaEnabled: boolean },
    run: (entry: () => ReturnType<typeof useCanvasCharacterEntry>, host: HTMLElement, projectOpens: unknown[][], inserts: unknown[][]) => Promise<void>,
) {
    const dom = new Window({ url: "https://canvas.test/" });
    const globals = {
        window: dom,
        document: dom.document,
        navigator: dom.navigator,
        HTMLElement: dom.HTMLElement,
        Element: dom.Element,
        Node: dom.Node,
        Event: dom.Event,
        IS_REACT_ACT_ENVIRONMENT: true,
    };
    const descriptors = new Map(Object.keys(globals).map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)]));
    for (const [key, value] of Object.entries(globals)) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
    const projectOpens: unknown[][] = [];
    const inserts: unknown[][] = [];
    let entry: ReturnType<typeof useCanvasCharacterEntry>;
    function Harness() {
        entry = useCanvasCharacterEntry({
            ...options,
            openProjectAssets: (...args) => { projectOpens.push(args); },
            handleProjectAssetsInsert: async (...args) => { inserts.push(args); return []; },
        });
        return <output>{entry.characterLibraryOpen ? "account library open" : "account library closed"}</output>;
    }
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    try {
        await act(async () => { root.render(<Harness />); });
        await run(() => entry!, host, projectOpens, inserts);
    } finally {
        await act(async () => { root.unmount(); });
        await dom.happyDOM.close();
        for (const [key, descriptor] of descriptors) {
            if (descriptor) Object.defineProperty(globalThis, key, descriptor);
            else Reflect.deleteProperty(globalThis, key);
        }
    }
}

for (const options of [
    { projectId: undefined, shortDramaEnabled: true },
    { projectId: "linked-project", shortDramaEnabled: false },
]) {
    test(`${options.projectId ? "disabled short drama" : "free canvas"} routes character entry to the account library`, async () => {
        await withCharacterEntry(options, async (entry, host, projectOpens) => {
            expect(host.textContent).toBe("account library closed");
            await act(async () => { entry().openCharacters({ x: 120, y: 240 }); });
            expect(host.textContent).toBe("account library open");
            expect(projectOpens).toEqual([]);
            await act(async () => { entry().closeCharacterLibrary(); });
            expect(host.textContent).toBe("account library closed");
        });
    });
}

test("enabled linked project delegates character entry and its position to project assets", async () => {
    await withCharacterEntry({ projectId: "linked-project", shortDramaEnabled: true }, async (entry, host, projectOpens) => {
        const position: Position = { x: 120, y: 240 };
        await act(async () => { entry().openCharacters(position); });
        expect(projectOpens).toEqual([["character", position]]);
        expect(host.textContent).toBe("account library closed");
    });
});

test("account insert preserves payload identity and right-click position, then clears that position", async () => {
    await withCharacterEntry({ shortDramaEnabled: true }, async (entry, _host, _projectOpens, inserts) => {
        const position: Position = { x: 120, y: 240 };
        const payloads: InsertAssetPayload[] = [{ kind: "character", title: "林夏", assetId: "asset-1", versionId: "version-1", prompt: "米色风衣", aliases: ["夏夏"], definition: { appearance: "短发" }, visualStatus: "ready", voiceStatus: "pending" }];
        await act(async () => { entry().openCharacters(position); });
        await act(async () => { await entry().insertLibraryCharacters(payloads); });
        expect(inserts).toEqual([[payloads, position]]);
        expect(inserts[0][0]).toBe(payloads);
        expect(inserts[0][1]).toBe(position);
        await act(async () => { await entry().insertLibraryCharacters(payloads); });
        expect(inserts[1]).toEqual([payloads, undefined]);
    });
});

test("closing the account library discards its old insertion position", async () => {
    await withCharacterEntry({ shortDramaEnabled: true }, async (entry, _host, _projectOpens, inserts) => {
        const payloads: InsertAssetPayload[] = [{ kind: "character", title: "林夏", assetId: "asset-1", versionId: "version-1", prompt: "米色风衣", aliases: ["夏夏"], definition: { appearance: "短发" }, visualStatus: "ready", voiceStatus: "pending" }];
        await act(async () => { entry().openCharacters({ x: 120, y: 240 }); });
        await act(async () => { entry().closeCharacterLibrary(); });
        await act(async () => { entry().openCharacters(); });
        await act(async () => { await entry().insertLibraryCharacters(payloads); });
        expect(inserts).toEqual([[payloads, undefined]]);
    });
});
