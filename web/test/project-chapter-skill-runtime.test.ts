import { expect, test } from "bun:test";

test("技能选择器占满表单宽度", async () => {
    const source = await Bun.file(new URL("../src/components/skills/skill-runtime-picker.tsx", import.meta.url)).text();
    expect(source).toContain('style={{ width: "100%" }}');
});

test("画布分镜通过当前 Agent 会话使用已启用技能", async () => {
    const source = await Bun.file(new URL("../src/pages/canvas/use-canvas-storyboard.ts", import.meta.url)).text();
    const request = await Bun.file(new URL("../src/lib/canvas/storyboard-agent-request.ts", import.meta.url)).text();
    expect(request).toContain("适用的已启用技能");
    expect(source).not.toContain("skillRuntime.prepare");
    expect(source).not.toContain("generateScriptRows");
});
