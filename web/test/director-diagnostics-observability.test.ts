import { describe, expect, test } from "bun:test";
import { PREVIS_DIAGNOSTIC_CODES, formatPrevisDiagnosticCode, isPrevisDiagnosticCode, projectPrevisDiagnostic } from "../src/lib/canvas/previs/previs-diagnostics";
import { DIRECTOR_DESK_VERSION } from "../src/integrations/director-desk";

/**
 * P5 观测:结构化日志关联字段(计划要求 runtimeVersion 等)。
 * 通过既有白名单诊断通道扩展——不新建平行日志。
 */
describe("P5 观测 — 运行时初始化诊断与关联字段", () => {
    test("01 运行时初始化失败 code 在白名单内,error 级 + 固定文案 + 版本进签名", () => {
        expect(isPrevisDiagnosticCode("PREVIS_RUNTIME_INIT_FAILED")).toBe(true);
        expect(PREVIS_DIAGNOSTIC_CODES).toContain("PREVIS_RUNTIME_INIT_FAILED");
        const event = projectPrevisDiagnostic("PREVIS_RUNTIME_INIT_FAILED", { sceneId: "scene-1", runtimeVersion: DIRECTOR_DESK_VERSION });
        expect(event).not.toBeNull();
        expect(event!.level).toBe("error");
        expect(event!.fields.sceneId).toBe("scene-1");
        expect(event!.fields.runtimeVersion).toBe("0.4.11");
        // 版本必须参与去重签名:不同引擎版本的失败不能互相吞掉。
        expect(formatPrevisDiagnosticCode(event!)).toContain("runtime=0.4.11");
    });

    test("02 runtimeVersion 只接受 semver 形态,自由文本/超长/空白一律拒绝", () => {
        for (const version of ["0.4.11", "1.0.0", "12.4.11-canary.1"]) {
            const event = projectPrevisDiagnostic("PREVIS_RUNTIME_INIT_FAILED", { runtimeVersion: version });
            expect(event?.fields.runtimeVersion).toBe(version);
        }
        for (const bad of ["", "0.4", "0.4.x", "0.4.11 ", "0.4.11\n", "evil/../../etc", "a".repeat(65), 42, null]) {
            const event = projectPrevisDiagnostic("PREVIS_RUNTIME_INIT_FAILED", { runtimeVersion: bad });
            expect(event?.fields.runtimeVersion).toBeUndefined();
        }
    });

    test("03 requestId 走 safe-id 校验(commit 关联),非法形态被拒绝", () => {
        const ok = projectPrevisDiagnostic("PREVIS_DIRECTOR_COMMIT_CONFLICT", { requestId: "director-preview:scene-1:shot-1:0" });
        expect(ok?.fields.requestId).toBe("director-preview:scene-1:shot-1:0");
        for (const bad of ["", "a b", "http://x/y", "a\nb", "id".repeat(50)]) {
            const event = projectPrevisDiagnostic("PREVIS_DIRECTOR_COMMIT_CONFLICT", { requestId: bad });
            expect(event?.fields.requestId).toBeUndefined();
        }
    });
});