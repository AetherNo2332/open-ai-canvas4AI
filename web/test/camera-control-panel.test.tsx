import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { CanvasNodeCameraPanel } from "@/components/canvas/canvas-node-camera-dialog";

type CameraOptions = { enabled: boolean; camera: string; lens: string; focalLength: number; aperture: number };

const render = (cameraControl?: CameraOptions, onConfirm?: () => void) => renderToStaticMarkup(createElement(CanvasNodeCameraPanel, { cameraControl, onClose() {}, onConfirm: onConfirm ?? (() => {}) }));

const buttonsOf = (html: string) => html.match(/<button\b[^>]*>[\s\S]*?<\/button>/g) ?? [];
const isDisabled = (button: string | undefined) => Boolean(button?.includes('disabled=""'));

test("camera panel keeps the four upstream columns with previous/next controls and a summary", () => {
    const html = render();
    expect(html).toContain("摄像机控制");
    expect(html).toContain("为当前图片生成设置镜头、焦距与光圈，滚轮可切换档位");
    for (const label of ["相机", "镜头", "焦距", "光圈"]) expect(html).toContain(label);
    expect(html.match(/title="上一项"/g)).toHaveLength(4);
    expect(html.match(/title="下一项"/g)).toHaveLength(4);
    expect(html).toContain("当前配置");
    expect(html).toContain("重置参数");
    expect(html).toContain("应用");
});

test("each parameter column exposes its own wheel-switch target", () => {
    const html = render();
    // 四列各自绑定滚轮切档，滑到卡片上时只切换本列参数。
    expect(html.match(/data-camera-column=/g)).toHaveLength(4);
    expect(html).toContain('data-camera-column="相机"');
    expect(html).toContain('data-camera-column="镜头"');
    expect(html).toContain('data-camera-column="焦距"');
    expect(html).toContain('data-camera-column="光圈"');
});

test("card columns stretch to the column width so corner badges cannot cover the centered labels", () => {
    const html = render();
    // 纵向 flex 的 align-items:center 会让包裹层收缩；包裹层必须带 w-full，卡片才有满宽可继承。
    expect(html).toContain('class="relative w-full"');
    expect(html.match(/class="relative flex h-\[clamp/g)).toHaveLength(4);
});

test("a new camera draft uses the declared ARRI and f/2.8 defaults consistently", () => {
    const html = render();
    expect(html).toContain("阿莱 Mini LF");
    expect(html).toContain("阿莱大师定焦");
    expect(html).toContain("50mm");
    expect(html).toContain("f/2.8");
    expect(html).toContain("阿莱 Mini LF · 阿莱大师定焦 · 50mm · f/2.8");
    expect(html).toContain('aria-checked="false"');
});

test("existing settings are displayed intact without writing during editing", () => {
    const options: CameraOptions = { enabled: true, camera: "sony_fx6", lens: "anamorphic_atlas", focalLength: 85, aperture: 1.8 };
    let writes = 0;
    const html = render(options, () => {
        writes++;
    });
    expect(html).toContain("索尼 FX6");
    expect(html).toContain("Atlas Orion 变形");
    expect(html).toContain("85mm");
    expect(html).toContain("f/1.8");
    expect(html).toContain('aria-checked="true"');
    expect(html).not.toContain('role="alert"');
    expect(writes).toBe(0);
    expect(options).toEqual({ enabled: true, camera: "sony_fx6", lens: "anamorphic_atlas", focalLength: 85, aperture: 1.8 });
});

test("unknown saved camera settings require an explicit correction instead of silently applying the first camera", () => {
    const html = render({ enabled: true, camera: "retired_camera", lens: "arri_signature_prime", focalLength: 50, aperture: 2.8 });
    expect(html).toContain('role="alert"');
    expect(html).toContain("不支持的相机");
    const buttons = buttonsOf(html);
    expect(isDisabled(buttons.find((button) => button.includes("应用")))).toBe(true);
    expect(isDisabled(buttons.find((button) => button.includes("复制提示词")))).toBe(true);
});

test("disabled camera direction keeps its saved choices and never offers an empty prompt to copy", () => {
    const html = render({ enabled: false, camera: "sony_fx6", lens: "anamorphic_atlas", focalLength: 85, aperture: 1.8 });
    const buttons = buttonsOf(html);
    expect(isDisabled(buttons.find((button) => button.includes("复制提示词")))).toBe(true);
    expect(isDisabled(buttons.find((button) => button.includes("应用")))).toBe(false);
    expect(html).toContain("索尼 FX6");
    expect(html).toContain("85mm");
    expect(html).toContain("f/1.8");
    expect(html).toContain('aria-checked="false"');
});
