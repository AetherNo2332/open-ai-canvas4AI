import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { CanvasNodeContent } from "../src/components/canvas/canvas-node-content";
import { CanvasNodeContextMenu } from "../src/components/canvas/canvas-context-menu";
import { resolveAddNodeMenuCommands } from "../src/lib/canvas/tool-registry";
import { canvasThemes } from "../src/lib/canvas-theme";
import { CanvasNodeType } from "../src/types/canvas";

test("all canvas creation contexts omit previs while ordinary node commands remain usable", () => {
    for (const workspaceMode of ["simple", "professional"] as const) {
        for (const isProjectLinked of [false, true]) {
            const invoked: string[] = [];
            const handlers = new Proxy({}, { get: (_target, name) => () => invoked.push(String(name)) });
            const context = { workspaceMode, isProjectLinked, enabledPluginIds: new Set<string>(), handlers } as any;
            const commands = resolveAddNodeMenuCommands(context);
            expect(commands.some((command) => command.id === "previs")).toBe(false);
            expect(commands.some((command) => /导演台|预演台/.test(command.label))).toBe(false);
            commands.find((command) => command.id === "text")!.run(context);
            commands.find((command) => command.id === "image")!.run(context);
            expect(invoked).toEqual(["onAddText", "onAddImage"]);
        }
    }
});

test("image context menu retains ordinary media actions without a director conversion entry", () => {
    const noop = () => {};
    const callbacks = new Proxy({}, { get: () => noop });
    const props = {
        ...Object.fromEntries(
            [
                "onClose",
                "onAddNode",
                "onAddFolder",
                "onChooseStyle",
                "onUpload",
                "onOpenAssets",
                "onOpenProjectCharacters",
                "onUndo",
                "onRedo",
                "onPaste",
                "onCopyNode",
                "onCreateGenerationCopy",
                "onDuplicate",
                "onDelete",
                "onSaveAsset",
                "onViewMedia",
                "onEditText",
                "onOpenDrawing",
                "onGenerateImage",
                "onCopyContent",
                "onCopyMediaUrl",
                "onUploadToArkPrivateAsset",
                "onSetAssetCategory",
                "onToggleFrame",
                "onSendToAgent",
            ].map((name) => [name, callbacks[name]]),
        ),
        menu: { type: "node", nodeId: "image", x: 20, y: 20 },
        node: { id: "image", type: CanvasNodeType.Image, title: "Saved image", position: { x: 0, y: 0 }, width: 300, height: 200, metadata: { content: "https://example.test/image.png" } },
        canUndo: false,
        canRedo: false,
        canPaste: false,
    } as any;
    const html = renderToStaticMarkup(<CanvasNodeContextMenu {...props} />);
    expect(html).not.toContain("3D 导演预演台");
    expect(html).toContain("发送到 Agent");
    expect(html).toContain("创建生成副本");
});

test("historical previs metadata does not replace ordinary text/media content with a legacy editor", () => {
    for (const type of [CanvasNodeType.Text, CanvasNodeType.Video]) {
        const node = {
            id: "legacy",
            type,
            title: "Historical result",
            position: { x: 0, y: 0 },
            width: 300,
            height: 200,
            metadata: { content: type === CanvasNodeType.Text ? "Saved document body" : "https://example.test/video.mp4", previsSceneId: "saved-scene", previsShotId: "saved-shot" },
        };
        const before = structuredClone(node);
        let customCalls = 0;
        const props = {
            node,
            theme: canvasThemes.dark,
            renderLOD: "full",
            isEditingContent: false,
            textareaRef: { current: null },
            isBatchRoot: false,
            batchCount: 0,
            batchExpanded: false,
            batchOpening: false,
            batchRecovering: false,
            renderNodeContent: () => {
                customCalls++;
                return <div>Legacy editor</div>;
            },
        } as any;
        const html = renderToStaticMarkup(<CanvasNodeContent {...props} />);
        expect(customCalls).toBe(0);
        expect(html).not.toContain("Legacy editor");
        if (type === CanvasNodeType.Text) expect(html).toContain("Saved document body");
        expect(node).toEqual(before);
    }
});
