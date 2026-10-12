import { describe, expect, test } from "bun:test";

const source = (path: string) => Bun.file(new URL(`../src/${path}`, import.meta.url)).text();

// `--creation-*` 里只有这几个是颜色角色，其余（例如 --creation-media-pending-max-width）是尺寸。
const COLOR_TOKEN = /^\s*--creation-(?:accent|bg|border|border-strong|faint|muted|shadow|surface|surface-hover|surface-muted|surface-raised|surface-selected|text|history-[a-z-]+):/;

describe("creation surfaces follow the site skin", () => {
    test("creation colour tokens derive from skin tokens instead of literal colours", async () => {
        const css = await source("styles/globals.css");
        const lines = css.split("\n").filter((line) => COLOR_TOKEN.test(line));

        expect(lines.length).toBeGreaterThan(0);
        const literals = lines.filter((line) => /#[0-9a-fA-F]{3,8}/.test(line) || /rgba?\(/.test(line));
        expect(literals).toEqual([]);
    });
});
