import { describe, expect, test } from "bun:test";
import { execSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

// DirectorDesk 0.4.11 上游源码入库的来源与许可固化契约。
// Ruling(T1): 只入库运行所需的上游内容(src/skills/public/index.html/构建配置/LICENSE),
// 上游 docs/(28M)、examples/、scripts/、tests/、desktop/(Electron 壳) 不入库。
const repoRoot = resolve(import.meta.dir, "../..");
const deskRoot = resolve(repoRoot, "director-desk");
const read = (path: string) => readFileSync(resolve(deskRoot, path), "utf8");

describe("DirectorDesk 上游入库来源固化", () => {
    test("NOTICE 记录上游仓库、SHA、版本与许可", () => {
        const notice = read("NOTICE.md");
        expect(notice).toContain("mangfufu/director-desk");
        expect(notice).toContain("f3afacf");
        expect(notice).toContain("0.4.11");
        expect(notice).toContain("MIT");
    });

    test("package.json 版本与上游发布一致", () => {
        expect(JSON.parse(read("package.json")).version).toBe("0.4.11");
    });

    test("运行所需的入口与 UI 模块已入库", () => {
        for (const path of ["src/main.ts", "src/ui/timeline.ts", "index.html", "vite.config.mjs", "tsconfig.json", "LICENSE", "README.md"]) {
            expect(existsSync(resolve(deskRoot, path))).toBe(true);
        }
        // 动画库数据文件是运行数据，必须随包入库。
        expect(existsSync(resolve(deskRoot, "src/animation/library/humanoid-v1.json"))).toBe(true);
        // AI 内置技能随包入库（保留 AI 接入的素材面）。
        expect(existsSync(resolve(deskRoot, "skills/director-desk"))).toBe(true);
    });

    test("桌面端与上游开发目录不入库", () => {
        for (const path of ["desktop", "docs", "examples", "scripts", "tests", ".git", "release"]) {
            expect(existsSync(resolve(deskRoot, path))).toBe(false);
        }
    });

    test("依赖与构建产物由 git 忽略(可原地安装/构建而不入库)", () => {
        // check-ignore --no-index：CI 干净 checkout 里这些目录不存在，
        // 但忽略规则仍然生效，用 --no-index 走纯规则匹配。
        for (const path of ["director-desk/node_modules", "director-desk/dist"]) {
            const ignored = execSync(`git check-ignore -q --no-index ${path} && echo yes || echo no`, { cwd: repoRoot, encoding: "utf8" }).trim();
            expect(ignored).toBe("yes");
        }
    });
});
