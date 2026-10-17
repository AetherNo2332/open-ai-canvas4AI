import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

/**
 * 导演台启动序列里 mountAI 抛错会中断 main.ts 后续的 mountSettings/renderPanels,
 * 表现为资产列表等面板为空。T2 加的「云端接入」文案用了 `#ai-host > p`,
 * 而模板里 <p> 在 .ai-host-heading 内,不是直接子元素 → querySelector 返回 null 后抛错。
 */
const deskSrc = resolve(import.meta.dir, "../../director-desk/src");
const read = (path: string) => readFileSync(resolve(deskSrc, path), "utf8");

describe("导演台 AI 面板挂载不得中断启动", () => {
    test("01 云端接入文案直接写在模板里,挂载代码不再按结构选择器改写", () => {
        const panel = read("ui/ai-panel.ts");
        expect(panel).not.toContain("#ai-host > p");
        const view = read("ui/ai-panel-view.ts");
        expect(view).toContain("云端 AI 由影策 Agent 接入；技能随工作台内置。");
    });

    test("02 T2 新增的 host 挂载点只按 id 查找,且对缺失元素容错", () => {
        const channel = read("host/ai-channel.ts");
        expect(channel).toContain('querySelector<HTMLElement>("#ai-host-status")');
        // 不得对查询结果做非空断言后直接写属性。
        expect(channel).not.toMatch(/querySelector[^;]*\)!\./u);
        const view = read("ui/ai-panel-view.ts");
        expect(view).toContain('id="ai-host-status"');
    });
});
