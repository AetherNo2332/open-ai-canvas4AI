import { describe, expect, test } from "bun:test";
import { moduleGroupSource } from "./helpers/module-group-source";

/**
 * 源码约束：DirectorDesk 包装器必须把 runtime 提交 routed 进保存协调器。
 * 行为基线由 director-edit-bridge.test.ts 覆盖；这里固定接线合同，防止
 * 未来回退成「直接 onChange 镜像」（绕过草稿/CAS/关闭 flush）。
 */

const wrapperSource = () => moduleGroupSource("components/canvas/previs/director-desk-previs-workbench.tsx");
const legacySource = () => moduleGroupSource("components/canvas/previs/canvas-previs-workbench.tsx");

describe("DirectorDesk edit commit wiring", () => {
    test("01 wrapper owns the coordinator and routes runtime commits through the edit bridge", () => {
        const source = wrapperSource();

        // 包装器自己持有保存协调器（canary/authoritative 模式的唯一写入者）。
        expect(source).toContain("usePrevisSaveCoordinator(");

        // 每个 runtime session 创建一个编辑桥，并接入 hook 级 controller 的提交传输。
        expect(source).toContain("createDirectorEditBridge(");
        expect(source).toContain("createDirectorControllerTransport(");
        expect(source).toContain("expectedSceneId: scene.id");

        // runtime 文档变更必须走桥的 commitDocument；不允许出现第二条
        // 「文档 → 直接 onChange」的提交路径（legacy 降级镜像除外）。
        expect(source.match(/bridge\.commitDocument\(document\)/gu)).toHaveLength(1);
        const directMirrorCount = source.match(/onChange\(directorDocumentToPrevisScene\(document\)\)/gu)?.length ?? 0;
        expect(directMirrorCount).toBeLessThanOrEqual(1);

        // 桥的生命周期跟随会话：mode/scene id 变化时 dispose。
        expect(source).toContain("editBridgeRef.current?.dispose()");

        // 冲突要留下结构化诊断（白名单 code），不能静默。
        expect(source).toContain("PREVIS_DIRECTOR_COMMIT_CONFLICT");
    });

    test("02 wrapper injects its coordinator into the legacy workbench only on fallback", () => {
        const source = wrapperSource();
        // app 模式下 legacy 回退（appFailed）时注入 saveController。
        expect(source).toContain("appFailed");
        expect(source).toContain("saveController={saveController}");
        // 默认路径（非 legacy、app 挂载）不注入 coordinator 给 CanvasPrevisWorkbench。
        expect(source).toContain("if (!usingApp || !scene) return <CanvasPrevisWorkbench {...props} />");
    });

    test("03 legacy workbench honors an injected controller and stays single-writer", () => {
        const source = legacySource();

        // 可选注入：存在外部协调器时本地 hook 必须 inert（sceneId 置 null）。
        expect(source).toContain("injectedSaveController ? null :");
        expect(source).toContain("injectedSaveController ?? localSaveController");

        // 旧工作台不再自己创建协调器去覆盖注入者：创建点只有一个。
        expect(source.match(/usePrevisSaveCoordinator\(/gu)).toHaveLength(1);
    });

    test("03b wrapper exposes undo/redo keyboard bindings to the edit bridge", () => {
        const source = wrapperSource();
        // Ctrl/Cmd+Z (Shift=redo) 走编辑桥；输入态不劫持。
        expect(source).toContain("keydown");
        expect(source).toContain("bridge.undo()");
        expect(source).toContain("bridge.redo()");
    });

    test("04 workbench commit entry reports the coordinator decision", () => {
        const source = legacySource();
        const commitCalls = source.match(/commitScene\((current|next)\)/gu) ?? [];
        expect(commitCalls.length).toBeGreaterThanOrEqual(2);
        // 提交入口必须来自 coordinator（canonical 路径），不直接写项目状态绕过它。
        expect(source).toContain("saveControllerRef.current?.commitScene");
    });
});

const editorSource = () => moduleGroupSource("components/canvas/previs/director-previs-editor.tsx");

describe("P1 editor composition wiring", () => {
    test("05 wrapper mounts the DirectorDeskAppFrame instead of the legacy workbench (fallback only on legacy/error)", () => {
        const source = wrapperSource();
        // 非 legacy 会话的默认渲染内容是原版应用壳（iframe）。
        expect(source).toContain("<DirectorDeskAppFrame");
        expect(source).toContain("sceneId={scene.id}");
        expect(source).toContain("scene={scene}");
        expect(source).toContain("saveController={saveController}");
        // 旧工作台只在 legacy 策略 / 未打开 / 无场景 / app 加载失败时出现。
        expect(source).toContain("if (!usingApp || !scene) return <CanvasPrevisWorkbench {...props} />");
        expect(source).toContain("appFailed");
        expect(source).toContain("onFallback={() => setAppFailed(true)}");
        // legacy 工作台渲染点 ≤ 2（legacy 分支 + 故障回退），不得与 app 壳并存。
        const legacyRenderCount = source.match(/<CanvasPrevisWorkbench/gu)?.length ?? 0;
        expect(legacyRenderCount).toBeLessThanOrEqual(2);
        // 关闭入口与保存请求接到 app 壳。
        expect(source).toContain("onClose={props.onClose}");
        expect(source).toContain("onSaveRequest={");
    });

    test("06 editor is the single writer for native mode and owns close/pagehide semantics", () => {
        const source = editorSource();
        // 唯一提交路径：commitScene（协调器）+ store 镜像，没有第二条写者。
        const commitCalls = source.match(/commitScene\((touched|next)\)/gu)?.length ?? 0;
        expect(commitCalls).toBe(1);
        expect(source).toContain("onChangeRef.current(touched)");
        // 关闭走 prepareClose 决策；失败且无草稿时落诊断。
        expect(source).toContain("prepareClose()");
        expect(source).toContain("resolvePrevisCloseOutcome(");
        expect(source).toContain("PREVIS_CLOSE_BLOCKED");
        // 离开页面语义与 legacy 一致。
        expect(source).toContain('window.addEventListener("pagehide"');
        expect(source).toContain('window.addEventListener("beforeunload"');
        expect(source).toContain("shouldBlockPrevisUnload(");
        // 不使用静态 Modal.confirm（lint 亦禁止）。
        expect(source).not.toContain("modal.confirm(");
        // 时间轴与引擎播放循环接线。
        expect(source).toContain("runtime?.setPlayhead(");
        expect(source).toContain("runtime?.play()");
        expect(source).toContain("runtime?.pause()");
    });
});