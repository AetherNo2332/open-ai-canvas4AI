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

    test("02 wrapper injects its coordinator into the legacy workbench", () => {
        const source = wrapperSource();
        expect(source).toContain("saveController={saveController}");
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