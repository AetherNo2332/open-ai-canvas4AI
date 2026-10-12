import { expect, test } from "bun:test";

import { DEFAULT_CLASSIC_SKIN } from "../src/lib/skin-themes";

// 默认皮肤必须符合 swiss-design：直角、实心按钮、无阴影、无悬停抬升、150ms 动效。
// 服务端的内置预设由 backend/internal/app/appearance_swiss_preset_test.go 守护。
test("default skin follows the swiss structural constraints", () => {
    const { tokens } = DEFAULT_CLASSIC_SKIN;
    const components = tokens.components;

    for (const key of ["buttonRadius", "inputRadius", "cardRadius", "overlayRadius", "menuRadius", "checkboxRadius"] as const) {
        expect(components[key]).toBe(0);
    }
    expect(components.shadowStyle).toBe("none");
    expect(components.hoverLift).toBe(0);
    expect(components.motionFast).toBe(150);
    expect(components.motionNormal).toBe(150);
    expect(tokens.buttons.light.mode).toBe("solid");
    expect(tokens.buttons.dark.mode).toBe("solid");
});

test("default skin keeps one accent per mode and paper surfaces", () => {
    expect(DEFAULT_CLASSIC_SKIN.name).toBe("瑞士纸感");
    expect(DEFAULT_CLASSIC_SKIN.tokens.light.primary).toBe("#c8102e");
    expect(DEFAULT_CLASSIC_SKIN.tokens.dark.primary).toBe("#e05163");
    // 开关开启态跟随主色，避免出现第二个强调色相。
    expect(DEFAULT_CLASSIC_SKIN.tokens.light.switchChecked).toBe(DEFAULT_CLASSIC_SKIN.tokens.light.primary);
    expect(DEFAULT_CLASSIC_SKIN.tokens.dark.switchChecked).toBe(DEFAULT_CLASSIC_SKIN.tokens.dark.primary);
});
