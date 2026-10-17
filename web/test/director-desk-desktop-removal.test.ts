import { describe, expect, test } from "bun:test";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { relative, resolve } from "node:path";

// 剥离桌面端能力的契约(T2):
// 1) 桌面能力模块整体出界(Electron 文件读写/本地素材库/自动更新/MCP 宿主);
// 2) 被剥离能力以显式"云端不可用"提示替代,不留静默失败;
// 3) 应用运行面不得再引用这些模块。
const repoRoot = resolve(import.meta.dir, "../..");
const deskRoot = resolve(repoRoot, "director-desk");
const read = (path: string) => readFileSync(resolve(deskRoot, path), "utf8");

const REMOVED_PATHS = [
    "src/files/desktop-output.ts",
    "src/ui/project-save.ts",
    "src/ui/file-locations.ts",
    "src/ui/update-panel.ts",
    "src/ui/update-panel.css",
    "src/ui/ai-mcp.ts",
    "src/updates",
];

const REMOVED_MODULE_REFERENCES = [
    "files/desktop-output",
    "ui/project-save",
    "ui/file-locations",
    "ui/update-panel",
    "ui/ai-mcp",
    "../updates/",
];

/** 浏览器能力不得被误当桌面能力删除(历史误删项)。 */
const BROWSER_SAFE_PATHS = ["src/storage/local-library.ts"];

function sourceFiles(): string[] {
    const walk = (directory: string): string[] => readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
        const path = resolve(directory, entry.name);
        return entry.isDirectory() ? walk(path) : entry.isFile() && path.endsWith(".ts") ? [relative(deskRoot, path).replaceAll("\\", "/")] : [];
    });
    return walk(resolve(deskRoot, "src"));
}

describe("DirectorDesk 桌面端能力剥离", () => {
    test("桌面能力模块已出界", () => {
        for (const path of REMOVED_PATHS) {
            expect(existsSync(resolve(deskRoot, path))).toBe(false);
        }
    });

    test("浏览器能力(IndexedDB 本地库)保留", () => {
        for (const path of BROWSER_SAFE_PATHS) {
            expect(existsSync(resolve(deskRoot, path))).toBe(true);
        }
        expect(read("src/storage/local-library.ts")).toContain("indexedDB");
    });

    test("剥离能力有显式提示实现", () => {
        const removed = read("src/host/desktop-removed.ts");
        expect(removed).toContain("mountRemovedCapabilityNotice");
        expect(removed).toContain("export");
    });

    test("Electron 与 MCP 依赖不再声明", () => {
        const pkg = JSON.parse(read("package.json"));
        const declared = { ...(pkg.dependencies ?? {}), ...(pkg.devDependencies ?? {}) };
        for (const name of ["electron", "electron-builder", "electron-updater", "@modelcontextprotocol/sdk"]) {
            expect(declared[name]).toBeUndefined();
        }
    });

    test("应用源码不再引用被剥离的模块", () => {
        const offenders: string[] = [];
        for (const file of sourceFiles()) {
            if (file.startsWith("src/host/")) continue;
            const source = read(file);
            for (const reference of REMOVED_MODULE_REFERENCES) {
                if (source.includes(reference)) offenders.push(`${file} → ${reference}`);
            }
        }
        expect(offenders).toEqual([]);
    });

    test("AI 面板不再挂 MCP 宿主", () => {
        expect(read("src/ui/ai-panel.ts")).not.toContain("ai-mcp");
        expect(read("src/ui/ai-panel-view.ts")).not.toContain("ai-mcp");
    });

    test("入口不再挂载被剥离的桌面模块", () => {
        const main = read("src/main.ts");
        for (const symbol of ["mountUpdates", "mountFileLocations", "mountAIMcp"]) {
            expect(main).not.toContain(symbol);
        }
    });
});
