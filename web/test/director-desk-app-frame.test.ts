import { describe, expect, test } from "bun:test";
import { moduleGroupSource } from "./helpers/module-group-source";

/**
 * DirectorDeskAppFrame 框架契约：iframe 沙箱、关闭/保存状态、加载失败回退。
 */

const frameSource = () => moduleGroupSource("components/canvas/previs/director-desk-app-frame.tsx");
const cssSource = () => require("fs").readFileSync("web/src/components/canvas/previs/director-desk-app-frame.css", "utf-8");

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
        expect(source).toContain("aria-label=\"关闭预演台\"");
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

    test("04 frame returns null on load failure (triggers onFallback)", () => {
        const source = frameSource();
        expect(source).toContain('status === "error"');
        expect(source).toContain("onFallback?.()");
    });

    test("05 CSS uses semantic tokens only, no hardcoded colors, and includes :focus-visible", () => {
        const css = cssSource();
        // 只用 CSS 变量，不用硬编码十六进制或 rgb。
        expect(css).not.toContain("#");
        expect(css).not.toContain("rgb(");
        // 使用皮肤 token。
        expect(css).toContain("var(--site-canvas)");
        expect(css).toContain("var(--site-text)");
        expect(css).toContain("var(--site-border)");
        expect(css).toContain("var(--button-radius)");
        expect(css).toContain("var(--overlay-radius)");
        // 关键焦点样式。
        expect(css).toContain(":focus-visible");
    });

    test("06 frame bridge receives readDocument, onSceneChange, onSaveRequest", () => {
        const source = frameSource();
        expect(source).toContain("readDocument: () => readDocumentRef.current?.() ?? null");
        expect(source).toContain("commitDocument: (document) => onSceneChangeRef.current(document)");
        expect(source).toContain("requestSave: () => onSaveRequestRef.current()");
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
