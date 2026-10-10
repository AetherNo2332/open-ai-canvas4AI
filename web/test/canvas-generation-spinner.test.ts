import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const nodeContent = readFileSync(resolve(import.meta.dir, "../src/components/canvas/canvas-node-content.tsx"), "utf8");
const homeSkin = readFileSync(resolve(import.meta.dir, "../src/styles/home-skin.css"), "utf8");

test("视频生成节点的加载指示器在工作区皮肤下保持圆形", () => {
    expect(nodeContent).toContain("canvas-generation-spinner size-10 animate-spin rounded-full border-2");
    expect(homeSkin).toMatch(/\.app-user-workspace\.app-user-workspace\s+\.canvas-generation-spinner\s*\{\s*border-radius:\s*50%\s*!important;\s*\}/s);
});
