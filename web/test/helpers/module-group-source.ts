// 源码约束测试的读取辅助：大文件按职责拆分后，约束应覆盖原文件及其拆分出的同组模块。
//
// 用法：moduleGroupSource("pages/assets/index.tsx") 返回原文件与同组模块拼接后的文本。
// 只在「源码断言」类测试里使用；行为测试请直接 import 模块。

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/** 原文件（相对 src）到拆分出的同组模块。新增拆分时在这里登记。 */
export const SPLIT_MODULE_GROUPS: Record<string, readonly string[]> = {
    "components/canvas/previs/canvas-previs-workbench.tsx": ["components/canvas/previs/previs-inspectors.tsx"],
    "components/canvas/previs/previs-viewport.tsx": ["components/canvas/previs/previs-viewport-capture.ts", "components/canvas/previs/previs-viewport-rig.ts"],
};

const srcRoot = resolve(import.meta.dir, "../../src");

/** 读取 src 下的文件；若它被拆分过，一并读取同组模块。 */
export function moduleGroupSource(relativePath: string): string {
    const normalized = relativePath.replace(/^(\.\.\/)*src\//, "").replace(/^\.\//, "");
    const files = [normalized, ...(SPLIT_MODULE_GROUPS[normalized] ?? [])];
    return files.map((file) => readFileSync(resolve(srcRoot, file), "utf8")).join("\n");
}
