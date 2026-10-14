import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { moduleGroupSource } from "./helpers/module-group-source";

/**
 * DirectorDeskAppFrame 框架契约：iframe 沙箱、关闭/保存状态、加载失败回退。
 */

const frameSource = () => moduleGroupSource("components/canvas/previs/director-desk-app-frame.tsx");
const cssSource = () => readFileSync(resolve(import.meta.dir, "../src/components/canvas/previs/director-desk-app-frame.css"), "utf8");
const repoRoot = resolve(import.meta.dir, "../..");

describe("DirectorDeskAppFrame 层级与 Agent 入口", () => {
    test("L1 全屏外壳 portal 到 body,使用预演台层级(位于画布导航之上、Agent 之下)", () => {
        const source = frameSource();
        expect(source).toContain("createPortal(");
        expect(source).toContain("document.body");
        expect(source).toContain("data-canvas-no-zoom");
        const css = cssSource();
        const root = css.slice(css.indexOf(".director-desk-app-frame {"), css.indexOf("}", css.indexOf(".director-desk-app-frame {")));
        expect(root).toContain("position: fixed");
        expect(root).toContain("inset: 0");
        expect(root).toContain("z-index: var(--z-workbench)");
    });

    test("L2 顶栏提供 Agent 入口(仅在宿主提供 onOpenAgent 时出现)", () => {
        const source = frameSource();
        expect(source).toContain("onOpenAgent?: () => void");
        expect(source).toMatch(/\{onOpenAgent \? \(/u);
        expect(source).toContain('aria-label="打开 Agent 助手"');
    });

    test("L3 画布页挂载 Agent 面板;预演台打开时改为浮层并隐藏悬浮按钮,入口交给导演台顶栏", () => {
        const project = moduleGroupSource("pages/canvas/project.tsx");
        expect(project).toContain("<CanvasCloudAgentPanel");
        expect(project).toContain("floating={Boolean(previsNodeId)}");
        expect(project).toContain("launcher={!previsNodeId}");
        expect(project).toContain("onOpenAgent={openAgent}");
        const panel = moduleGroupSource("components/canvas/canvas-cloud-agent-panel.tsx");
        expect(panel).toContain("launcher?: boolean");
        expect(panel).toContain("{!open && launcher ? <AgentLauncher");
        const wrapper = moduleGroupSource("components/canvas/previs/director-desk-previs-workbench.tsx");
        expect(wrapper).toContain("onOpenAgent={props.onOpenAgent}");
    });
});

describe("DirectorDeskAppFrame composition", () => {
    test("01 frame requires scene, saveController, and onFallback props", () => {
        const source = frameSource();
        // 关闭、保存状态、fallback 都是必需的。
        expect(source).toContain("scene: PrevisScene");
        expect(source).toContain("saveController: PrevisSaveController");
        expect(source).toContain("onFallback?: () => void");
        // usePrevisCloseHandler 復用逻辑。
        expect(source).toContain("usePrevisCloseHandler");
        expect(source).toContain("describePrevisSaveStatus");
    });

    test("02 frame renders header with close button, save status, and title", () => {
        const source = frameSource();
        expect(source).toContain("director-desk-app-frame-header");
        expect(source).toContain("director-desk-app-frame-close");
        expect(source).toContain("director-desk-app-frame-save-status");
        expect(source).toContain("closeHandler");
        expect(source).toContain('aria-label="关闭预演台"');
    });

    test("03 frame shows notice on unsaved state and confirm dialog before exit", () => {
        const source = frameSource();
        // 错误/警告提示。
        expect(source).toContain("director-desk-app-frame-notice");
        // 二次确认对话框。
        expect(source).toContain("exitConfirm");
        expect(source).toContain("director-desk-app-frame-confirm");
        expect(source).toContain("confirmExit");
        expect(source).toContain("cancelExit");
    });

    test("04 frame triggers onFallback from an effect on load failure, never during render", () => {
        const source = frameSource();
        expect(source).toContain('status === "error"');
        // 回退必须在 effect 里通知父组件，渲染期 setState 会触发 React 警告/循环。
        expect(source).toMatch(/useEffect\(\(\) => \{\s*if \(status === "error"\) onFallbackRef\.current\?\.\(\);/u);
        expect(source).not.toMatch(/if \(status === "error"\) \{\s*onFallback\?\.\(\);\s*return null;/u);
    });

    test("05 CSS uses semantic tokens only, no hardcoded colors, and includes :focus-visible", () => {
        const css = cssSource();
        // 只用 CSS 变量，不用硬编码十六进制或 rgb，也不给 token 写色值/圆角回退。
        expect(css).not.toContain("#");
        expect(css).not.toContain("rgb(");
        expect(css).not.toMatch(/var\(--[a-z0-9-]+,\s*[^v)]/u);
        // 时长引用动效 token，不写死秒数；禁止 transition: all。
        expect(css).not.toMatch(/transition:[^;]*\d+(\.\d+)?m?s/u);
        expect(css).not.toContain("transition: all");
        // 使用皮肤 token。
        expect(css).toContain("var(--site-canvas)");
        expect(css).toContain("var(--site-text)");
        expect(css).toContain("var(--site-border)");
        expect(css).toContain("var(--button-radius)");
        expect(css).toContain("var(--overlay-radius)");
        // 关键焦点样式。
        expect(css).toContain(":focus-visible");
    });

    test("06 embedded app theme contract includes a light skin", () => {
        const source = readFileSync(resolve(repoRoot, "director-desk/src/theme.ts"), "utf8");
        const css = readFileSync(resolve(repoRoot, "director-desk/src/theme.css"), "utf8");
        expect(source).toContain("applyDirectorDeskAppearance");
        expect(css).toContain("html[data-desk-themed]");
        expect(css).toContain("var(--desk-text)");
        expect(css).not.toMatch(/#[0-9a-f]{3,8}\b/i);
    });

    test("07 frame bridge receives readDocument, onSceneChange, onSaveRequest", () => {
        const source = frameSource();
        expect(source).toContain("readDocument: () => readDocumentRef.current?.() ?? null");
        expect(source).toContain("commitDocument: (document) => onSceneChangeRef.current(document)");
        expect(source).toContain("requestSave: () => onSaveRequestRef.current()");
    });

    test("08 frame passes the active light/dark theme into the embedded app", () => {
        const source = frameSource();
        expect(source).toContain('theme?: "light" | "dark"');
        expect(source).toContain("readAppearance: () => appearanceRef.current");
        expect(source).toContain("pushAppearance(appearance)");
    });
});

describe("usePrevisCloseHandler hook", () => {
    test("01 hook exports PrevisCloseOutcome type and options", () => {
        const hookSource = moduleGroupSource("hooks/use-previs-close-handler.ts");
        expect(hookSource).toContain("export type PrevisCloseOutcome");
        expect(hookSource).toContain("export type UsePrevisCloseHandlerOptions");
        expect(hookSource).toContain("export type UsePrevisCloseHandlerResult");
    });

    test("02 hook implements prepareClose decision logic and pagehide/beforeunload", () => {
        const hookSource = moduleGroupSource("hooks/use-previs-close-handler.ts");
        expect(hookSource).toContain("resolvePrevisCloseOutcome");
        expect(hookSource).toContain("shouldBlockPrevisUnload");
        expect(hookSource).toContain('window.addEventListener("pagehide"');
        expect(hookSource).toContain('window.addEventListener("beforeunload"');
    });
});
