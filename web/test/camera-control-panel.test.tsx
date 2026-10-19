import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { CanvasNodeCameraPanel } from "@/components/canvas/canvas-node-camera-dialog";

const render = (cameraControl?: { enabled: boolean; camera: string; lens: string; focalLength: number; aperture: number }) => renderToStaticMarkup(createElement(CanvasNodeCameraPanel, { cameraControl, onClose() {}, onConfirm() {} }));

test("camera settings have directly selectable equipment and accessible focal/aperture controls", () => {
    const html = render();
    expect(html).toContain('aria-label="摄影机机身"');
    expect(html).toContain('aria-label="摄影镜头"');
    expect(html).toContain('aria-label="焦距"');
    expect(html).toContain('aria-label="光圈"');
    expect(html.match(/role="combobox"/g)).toHaveLength(2);
    expect(html.match(/role="slider"/g)).toHaveLength(2);
    expect(html).toContain("应用到当前生成");
});

test("a new camera draft uses the declared ARRI and f/2.8 defaults consistently", () => {
    const html = render();
    expect(html).toContain("ARRI Alexa Mini LF");
    expect(html).toContain("f/2.8");
    expect(html).toContain('aria-checked="false"');
});

test("existing settings are displayed intact without writing during editing", () => {
    const options = { enabled: true, camera: "sony_fx6", lens: "cooke_s7i", focalLength: 85, aperture: 1.8 };
    let writes = 0;
    const html = renderToStaticMarkup(
        createElement(CanvasNodeCameraPanel, {
            cameraControl: options,
            onClose() {},
            onConfirm() {
                writes++;
            },
        }),
    );
    expect(html).toContain("Sony FX6");
    expect(html).toContain("85");
    expect(html).toContain("f/1.8");
    expect(html).toContain('aria-checked="true"');
    expect(writes).toBe(0);
    expect(options).toEqual({ enabled: true, camera: "sony_fx6", lens: "cooke_s7i", focalLength: 85, aperture: 1.8 });
});

test("unknown saved camera settings require an explicit correction instead of silently selecting the first camera", () => {
    const html = render({ enabled: true, camera: "retired_camera", lens: "arri_signature_prime", focalLength: 50, aperture: 2.8 });
    expect(html).toContain('role="alert"');
    expect(html).toContain("不支持的相机配置");
    const apply = html.match(/<button\b[^>]*>[\s\S]*?<\/button>/g)?.find((button) => button.includes("应用到当前生成"));
    expect(apply).toContain("disabled");
});

test("disabled camera direction keeps its saved choices and never offers an empty prompt to copy", () => {
    const html = render({ enabled: false, camera: "sony_fx6", lens: "cooke_s7i", focalLength: 85, aperture: 1.8 });
    const buttons = html.match(/<button\b[^>]*>[\s\S]*?<\/button>/g) || [];
    expect(buttons.find((button) => button.includes("复制提示词"))).toContain("disabled");
    expect(buttons.find((button) => button.includes("应用到当前生成"))).not.toContain("disabled");
    expect(html).toContain("85mm");
    expect(html).toContain("f/1.8");
});
