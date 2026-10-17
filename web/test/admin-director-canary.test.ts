import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

function source(path: string) {
    return readFileSync(resolve(import.meta.dir, path), "utf8");
}

/**
 * P5 灰度分群:canary 模式必须支持按用户分组(白名单 + 稳定比例)。
 * 服务端为唯一真源(internal/platform/feature_availability.go 的
 * ResolveDirectorDeskPrevisMode,Go 测试覆盖分配语义);这里固定前端合同:
 * 类型、管理端 payload/解析/比较、面板 UI 都要带上分组字段,
 * 防止管理端 PATCH 往返把这些字段冲掉或让运维无法配置分群。
 */

describe("DirectorDesk canary grouping contracts", () => {
    test("01 FeatureAvailability type carries the canary targeting fields", () => {
        const store = source("../src/stores/use-user-store.ts");
        expect(store).toContain("directorDeskPrevisCanaryUsers");
        expect(store).toContain("directorDeskPrevisCanaryBasisPoints");
    });

    test("02 admin API payload keeps the canary fields across round-trips", () => {
        const auth = source("../src/services/api/auth.ts");
        const payload = source("../src/pages/admin/components/feature-availability-panel.tsx");
        // PATCH 的 Pick 必须包含分组字段(否则管理端保存会丢失分群配置)。
        expect(auth).toMatch(/Pick<FeatureAvailability,[^]*"directorDeskPrevisCanaryUsers"/);
        expect(auth).toMatch(/"directorDeskPrevisCanaryBasisPoints"/);
        // 面板 payload/解析/比较三处一致。
        expect(payload).toContain("directorDeskPrevisCanaryUsers: features.directorDeskPrevisCanaryUsers");
        expect(payload).toContain("directorDeskPrevisCanaryBasisPoints: features.directorDeskPrevisCanaryBasisPoints");
        expect(payload).toMatch(/directorDeskPrevisCanaryUsers[^]*left\.directorDeskPrevisCanaryUsers/);
    });

    test("03 admin panel exposes the canary group controls (allowlist + ratio)", () => {
        const panel = source("../src/pages/admin/components/feature-availability-panel.tsx");
        expect(panel).toContain("灰度分组");
        expect(panel).toContain("内部用户(逗号分隔)");
        expect(panel).toContain("比例");
        // 分组只在 canary 模式下可操作
        expect(panel).toContain('aria-label="设置灰度分组"');
        expect(panel).toContain("保存灰度分组");
    });
});