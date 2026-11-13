import { expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join, relative, resolve, sep } from "node:path";

// 白名单按 git 风格正斜杠路径书写；Windows 的 relative() 返回反斜杠，比较前统一规范化。
const toPosix = (value: string) => value.split(sep).join("/");

function walkSourceFiles(dir: string): string[] {
    const files: string[] = [];
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) {
            files.push(...walkSourceFiles(path));
            continue;
        }
        if (/\.(ts|tsx)$/.test(entry.name)) files.push(path);
    }
    return files;
}

const webRoot = resolve(import.meta.dir, "..");
const srcRoot = resolve(webRoot, "src");
// The three source-contract checks inspect the same immutable checkout. Share
// one filesystem snapshot so Windows I/O under concurrent builds stays bounded.
const sourceFiles = walkSourceFiles(srcRoot);
const sourceText = new Map(sourceFiles.map((file) => [file, readFileSync(file, "utf8")]));

const axiosAllowed = new Set(["services/api/request.ts", "services/api/channel-transport.ts", "services/api/image-response.ts", "services/api/video-response.ts", "services/diagnostics/client-diagnostics.ts"]);

test("business API modules call http instead of axios or request(apiClient)", () => {
    const offenders = sourceFiles
        .filter((file) => file.startsWith(resolve(srcRoot, "services") + "/") || file.startsWith(resolve(srcRoot, "services") + "\\"))
        .filter((file) => {
            const rel = toPosix(relative(srcRoot, file));
            if (axiosAllowed.has(rel)) return false;
            const source = sourceText.get(file)!;
            return /from ["']axios["']/.test(source) || /request\s*(?:<[^>]+>)?\s*\(\s*apiClient\./.test(source) || /const api = apiClient/.test(source);
        })
        .map((file) => toPosix(relative(srcRoot, file)));
    expect(offenders).toEqual([]);
});

test("axios.create stays in the shared request client", () => {
    const offenders = sourceFiles
        .filter((file) => {
            if (toPosix(relative(srcRoot, file)) === "services/api/request.ts") return false;
            return sourceText.get(file)!.includes("axios.create(");
        })
        .map((file) => toPosix(relative(srcRoot, file)));
    expect(offenders).toEqual([]);
});

test("copied flush Modal padding lives only in AppModal", () => {
    const banned = ['styles={{ container: { padding: 0, overflow: "hidden" }, body: { padding: 0 } }}', "styles={{ container: { padding: 0 }, body: { padding: 0 } }}"];
    const offenders = sourceFiles
        .filter((file) => {
            if (toPosix(relative(srcRoot, file)) === "components/ui/product/app-modal/app-modal.tsx") return false;
            const source = sourceText.get(file)!;
            return banned.some((snippet) => source.includes(snippet));
        })
        .map((file) => toPosix(relative(srcRoot, file)));
    expect(offenders).toEqual([]);
});
