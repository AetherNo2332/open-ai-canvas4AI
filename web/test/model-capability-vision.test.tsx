import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { ModelCapabilityEditor } from "../src/components/model-capability-editor";
import { defaultModelCapabilityConfig } from "../src/lib/model-capabilities";

function textConfig(patch: Record<string, unknown>, maxImages: number) {
    const base = defaultModelCapabilityConfig().text!;
    return { version: 1 as const, text: { ...base, ...patch, references: { ...base.references, maxImages } } };
}

describe("文本模型识图配置", () => {
    for (const section of ["references", undefined] as const) {
        test(`${section ?? "完整"}视图显示识图开关与两个批次封顶`, () => {
            const html = renderToStaticMarkup(<ModelCapabilityEditor capability="text" section={section} value={textConfig({ visionSupported: true, visionMaxBatchImages: 3, visionMaxBatchCost: 12 }, 4)} />);
            expect(html).toContain("支持 Agent 识图");
            expect(html).toContain("识图批次安全封顶（张，0=自动）");
            expect(html).toContain("识图批次成本封顶（0=自动）");
            expect(html).toContain('value="3"');
            expect(html).toContain('value="12"');
        });
    }

    test("未声明 visionSupported 时按最大参考图片数推断，与后端 textVisionSupported 一致", () => {
        const on = renderToStaticMarkup(<ModelCapabilityEditor capability="text" section="references" value={textConfig({}, 4)} />);
        const off = renderToStaticMarkup(<ModelCapabilityEditor capability="text" section="references" value={textConfig({}, 0)} />);
        expect(on).toContain('aria-checked="true"');
        expect(off).toContain('aria-checked="false"');
    });

    test("明确关闭识图时即使允许图片引用也显示关闭，批次封顶不可编辑", () => {
        const html = renderToStaticMarkup(<ModelCapabilityEditor capability="text" section="references" value={textConfig({ visionSupported: false }, 4)} />);
        expect(html).toContain('aria-checked="false"');
        const caps = html.match(/<input[^>]*value="0"[^>]*>/g) ?? [];
        expect(caps.filter((input) => input.includes("disabled")).length).toBeGreaterThanOrEqual(2);
    });
});
