import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const FILES = [
    "src/components/canvas/previs/director-timeline.css",
    "src/components/canvas/previs/director-shot-list.css",
    "src/components/canvas/previs/director-inspector.css",
    "src/components/canvas/previs/director-previs-editor.css",
];

/**
 * 生产构建门禁:lightningcss minify 不容忍「空选择器」(规则体前没有选择器)。
 * 2026-10-13 真机 compose 测验曾捕获 director-timeline.css 全文件选择器缺失
 * (dev 模式容忍,生产 minify 直接失败)。此守卫防止同类回归。
 */
describe("previs CSS 选择器完整性", () => {
    for (const file of FILES) {
        test(file + " 的每个规则块都有选择器", () => {
            const css = readFileSync(resolve(import.meta.dir, "../", file), "utf8");
            const lines = css.split("\n");
            const offenders: string[] = [];
            for (let i = 0; i < lines.length; i++) {
                // 独立成行的裸 "{"(前面一行没有以 } 结尾的选择器文本也视为裸开放)。
                if (/^[ \t]*\{[ \t]*$/.test(lines[i])) {
                    offenders.push(`line ${i + 1} (prev: ${JSON.stringify(lines[i - 1] ?? "")})`);
                }
            }
            expect(offenders).toEqual([]);
        });
    }
});