import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { ModelCapabilityEditor } from "../src/components/model-capability-editor";

// 回归：文本模型编辑器曾丢失 `references.maxImages` 输入（6141bd68 重写时被
// 识图开关组替代），而后端 model-preflight 按 `text.references.maxImages`
// 拒绝超限图片，管理员只能看着"最多支持 0 张"。两个字段必须在所有分区同时可编辑。
describe("文本模型能力编辑器", () => {
    test("图片引用卡片同时提供最大图片引用与识图批次封顶", () => {
        for (const section of ["references", "all"] as const) {
            const editor = renderToStaticMarkup(
                <ModelCapabilityEditor
                    capability="text"
                    section={section}
                    value={{ version: 1, text: { visionSupported: true, references: { promptMaxChars: 32000, maxImages: 10, maxImageBytes: 0, maxVideos: 0, maxVideoBytes: 0 } } }}
                />,
            );
            expect(editor).toContain("最大图片引用");
            expect(editor).toContain("识图批次安全封顶");
        }
    });

    test("最大图片引用按配置值回显，不受识图批次封顶影响", () => {
        const editor = renderToStaticMarkup(
            <ModelCapabilityEditor
                capability="text"
                section="references"
                value={{ version: 1, text: { visionMaxBatchImages: 4, references: { promptMaxChars: 32000, maxImages: 10, maxImageBytes: 0, maxVideos: 0, maxVideoBytes: 0 } } }}
            />,
        );
        expect(editor).toMatch(/最大图片引用[^]*?value="10"/);
        expect(editor).toMatch(/识图批次安全封顶[^]*?value="4"/);
    });
});
