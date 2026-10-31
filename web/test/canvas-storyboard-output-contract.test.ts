import { describe, expect, test } from "bun:test";

import { stripProjectStyleExecutionBlocks } from "../src/lib/canvas/canvas-project-domain";

describe("stripProjectStyleExecutionBlocks", () => {
    test("removes repeated legacy style suffixes while preserving the original story", () => {
        const prompt = "@图片1 冬天\n\n【项目画风执行规范】\n旧风格规范\n\n【项目画风执行规范】\n重复旧风格规范";

        expect(stripProjectStyleExecutionBlocks(prompt)).toBe("@图片1 冬天");
    });
});
