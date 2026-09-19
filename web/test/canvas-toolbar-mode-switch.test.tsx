import { afterEach, describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { Hand, MousePointer2 } from "lucide-react";

import { FloatingDock } from "@/components/ui/aceternity/floating-dock";
import { CANVAS_MODE_TOOL_ID, defaultToolbarPrefs, migrateToolbarPrefs, resolveToolbarEntries, type ToolContext, type ToolbarHandlers } from "@/lib/canvas/tool-registry";

// bun 的测试环境没有 DOM，而 motion 的 <motion.div> 只按"window 是否存在"判断是否走浏览器分支：
// 一旦拿到别的用例临时装上的**部分** window（bun 在同一进程里并发跑各测试文件），它就会去调
// window.addEventListener 直接抛错。渲染断言因此依赖外部用例的时序，这里显式装一个够用的桩，
// 让这条断言自我闭环：跑单文件与跑全量结果一致。
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");

function installRenderingWindowStub() {
    const media = { matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} };
    Object.defineProperty(globalThis, "window", {
        configurable: true,
        value: {
            innerWidth: 1024,
            addEventListener() {},
            removeEventListener() {},
            matchMedia: () => media,
            location: { pathname: "/", origin: "http://localhost" },
            localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
        },
    });
}

function restoreRenderingWindowStub() {
    if (originalWindow) Object.defineProperty(globalThis, "window", originalWindow);
    else Object.defineProperty(globalThis, "window", { configurable: true, value: undefined });
}

afterEach(restoreRenderingWindowStub);

function createMainContext(canvasTool: "move" | "box-select" = "box-select"): ToolContext {
    return {
        selectedCount: 0,
        selectedNodeTypes: new Set(),
        selectedVideoCount: 0,
        canvasTool,
        workspaceMode: "professional",
        isProjectLinked: false,
        canUndo: false,
        canRedo: false,
        extractingVideoFrames: false,
        extractingAudio: false,
        trimmingVideo: false,
        mergingVideos: false,
        addPanelOpen: false,
        appearancePanelOpen: false,
        settingsPanelOpen: false,
        handlers: {} as ToolbarHandlers,
    };
}

describe("canvas toolbar mode switch", () => {
    test("renders grab and box-select as one dock switch", () => {
        const entries = resolveToolbarEntries("main", createMainContext("box-select"), null);
        const modeSwitch = entries.find((entry) => entry.kind === "switch" && entry.id === CANVAS_MODE_TOOL_ID);

        expect(modeSwitch?.kind).toBe("switch");
        if (modeSwitch?.kind !== "switch") return;
        expect(modeSwitch.value).toBe("box-select");
        expect(modeSwitch.options.map((option) => option.value)).toEqual(["box-select", "move"]);
        expect(entries.some((entry) => entry.id === "tool-move" || entry.id === "tool-box-select")).toBe(false);
    });

    test("reflects the active canvas tool on the switch", () => {
        const entries = resolveToolbarEntries("main", createMainContext("move"), null);
        const modeSwitch = entries.find((entry) => entry.kind === "switch");
        expect(modeSwitch?.kind === "switch" && modeSwitch.value).toBe("move");
    });

    test("migrates legacy separate tool prefs into the switch item", () => {
        const migrated = migrateToolbarPrefs("main", {
            order: ["tool-move", "tool-box-select", "tool-undo", "tool-add"],
            hidden: [],
        });
        expect(migrated.order[0]).toBe(CANVAS_MODE_TOOL_ID);
        expect(migrated.order).not.toContain("tool-move");
        expect(migrated.order).not.toContain("tool-box-select");
        expect(migrated.hidden).toEqual([]);
    });

    test("hides the switch only when both legacy tools were hidden", () => {
        const oneHidden = migrateToolbarPrefs("main", {
            order: ["tool-move", "tool-box-select", "tool-undo"],
            hidden: ["tool-move"],
        });
        expect(oneHidden.hidden).toEqual([]);

        const bothHidden = migrateToolbarPrefs("main", {
            order: ["tool-undo", "tool-move", "tool-box-select"],
            hidden: ["tool-move", "tool-box-select"],
        });
        expect(bothHidden.order[1]).toBe(CANVAS_MODE_TOOL_ID);
        expect(bothHidden.hidden).toEqual([CANVAS_MODE_TOOL_ID]);
    });

    test("leaves non-main toolbar prefs unchanged", () => {
        const prefs = { order: ["tool-move"], hidden: ["tool-box-select"] };
        expect(migrateToolbarPrefs("selection", prefs)).toEqual(prefs);
    });

    test("keeps the switch visible in default main toolbar prefs", () => {
        expect(defaultToolbarPrefs("main").order[0]).toBe(CANVAS_MODE_TOOL_ID);
        expect(defaultToolbarPrefs("main").hidden).toEqual([]);
    });

    test("hides rarely used arrange tools from the selection toolbar by default", () => {
        const prefs = defaultToolbarPrefs("selection");
        expect(prefs.hidden).toEqual(expect.arrayContaining([
            "selection-arrange-row",
            "selection-arrange-column",
            "selection-arrange-grid",
            "selection-arrange-flow",
            "selection-create-reference-group",
        ]));

        const entries = resolveToolbarEntries("selection", createMainContext(), null);
        expect(entries.some((entry) => entry.id === "selection-align-left")).toBe(true);
        expect(entries.some((entry) => entry.id === "selection-distribute-x")).toBe(true);
        expect(entries.some((entry) => entry.id === "selection-batch-connect")).toBe(true);
        expect(entries.some((entry) => entry.id === "selection-arrange-grid")).toBe(false);
        expect(entries.some((entry) => entry.id === "selection-create-reference-group")).toBe(false);
    });

    test("renders the pill switch with an active circular thumb", () => {
        installRenderingWindowStub();
        const html = renderToStaticMarkup(
            <FloatingDock
                items={[{
                    kind: "switch",
                    id: CANVAS_MODE_TOOL_ID,
                    label: "抓手 / 框选",
                    value: "box-select",
                    options: [
                        { id: "box-select", label: "区域选择", icon: <MousePointer2 />, value: "box-select" },
                        { id: "move", label: "抓手工具", icon: <Hand />, value: "move" },
                    ],
                    onChange: () => {},
                }]}
            />,
        );

        expect(html).toContain('role="radiogroup"');
        expect(html).toContain("aceternity-dock-switch-track");
        expect(html).toContain("aceternity-dock-switch-thumb");
        expect(html).toContain('aria-label="区域选择"');
        expect(html).toContain('aria-label="抓手工具"');
        expect(html).toContain('aria-checked="true"');
        expect(html).toContain('aria-checked="false"');
    });
});
